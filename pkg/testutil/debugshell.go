package testutil

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/buildkit/identity"
)

const buildgPathEnv = "TEST_BUILDG_PATH"
const BuildgTestTmpDirEnv = "TEST_BUILDG_TMP_DIR"

func getBuildgBinary(t *testing.T) string {
	buildgCmd := "buildg"
	if c := os.Getenv(buildgPathEnv); c != "" {
		buildgCmd = c
	}
	p, err := exec.LookPath(buildgCmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("using buildg command %q", buildgCmd)
	return p
}

func Mirror(ref string) string {
	return fmt.Sprintf("ghcr.io/stargz-containers/%s-org", ref)
}

type DebugShell struct {
	t          *testing.T
	cmd        *exec.Cmd
	stdin      io.Writer
	stdout     *bytes.Buffer
	stderr     *bytes.Buffer
	stdoutDump *bytes.Buffer
	prompt     string

	waitMu     sync.Mutex
	waitDone   bool
	waitErr    error
	waitDoneCh chan struct{}

	rootDir        string
	cleanupRootDir func() error
}

type Output struct {
	sh     *DebugShell
	stdout []byte
}

func (o *Output) Out() string {
	o.sh.t.Log("stdout:\n" + string(o.stdout))
	return string(o.stdout)
}

func (o *Output) OutNotEqual(s string) *Output {
	o.sh.t.Log("stdout:\n" + string(o.stdout))
	if s == string(o.stdout) {
		o.sh.fatal(fmt.Sprintf("unexpected stdout\nmust not be:\n%s\ngot:\n%s", s, o.stdout))
	}
	return o
}

func (o *Output) OutEqual(s string) *Output {
	o.sh.t.Log("stdout:\n" + string(o.stdout))
	if s != string(o.stdout) {
		o.sh.fatal(fmt.Sprintf("unexpected stdout\nwanted:\n%s\ngot:\n%s", s, o.stdout))
	}
	return o
}

func (o *Output) OutContains(s string) *Output {
	o.sh.t.Log("stdout:\n" + string(o.stdout))
	if !strings.Contains(string(o.stdout), s) {
		o.sh.fatal(fmt.Sprintf("unexpected stdout\nmust include:\n%s\ngot:\n%s", s, o.stdout))
	}
	return o
}

func (o *Output) OutNotContains(s string) *Output {
	o.sh.t.Log("stdout:\n" + string(o.stdout))
	if strings.Contains(string(o.stdout), s) {
		o.sh.fatal(fmt.Sprintf("unexpected stdout\nmust NOT include:\n%s\ngot:\n%s", s, o.stdout))
	}
	return o
}

type options struct {
	opts    []string
	env     []string
	rootDir string
}

type DebugShellOption func(*options)

func WithOptions(opts ...string) DebugShellOption {
	return func(o *options) {
		o.opts = append(o.opts, opts...)
	}
}

func WithEnv(env ...string) DebugShellOption {
	return func(o *options) {
		o.env = env
	}
}

func WithRootDir(rootDir string) DebugShellOption {
	return func(o *options) {
		o.rootDir = rootDir
	}
}

type buildgCmdOptions struct {
	env        []string
	globalOpts []string
}

type BuildgCmdOption func(*buildgCmdOptions)

func WithGlobalOptions(globalOpts ...string) BuildgCmdOption {
	return func(o *buildgCmdOptions) {
		o.globalOpts = append(o.globalOpts, globalOpts...)
	}
}

func WithBuildgCmdEnv(env ...string) BuildgCmdOption {
	return func(o *buildgCmdOptions) {
		o.env = env
	}
}

func BuildgCmd(t *testing.T, args []string, opts ...BuildgCmdOption) *exec.Cmd {
	gotOpts := buildgCmdOptions{}
	for _, o := range opts {
		o(&gotOpts)
	}

	buildgCmd := getBuildgBinary(t)
	cmd := exec.Command(buildgCmd, append(append(gotOpts.globalOpts, "--debug"), args...)...)
	cmd.Env = append(os.Environ(), gotOpts.env...)
	return cmd
}

func NewDebugShell(t *testing.T, buildCtx string, opts ...DebugShellOption) *DebugShell {
	gotOpts := options{}
	for _, o := range opts {
		o(&gotOpts)
	}

	var cleanupRootDir func() error
	rootDir := gotOpts.rootDir
	if rootDir == "" {
		tmpRoot, err := os.MkdirTemp(os.Getenv(BuildgTestTmpDirEnv), "buildg-test-tmproot")
		if err != nil {
			t.Fatal(err)
		}
		rootDir = tmpRoot
		cleanupRootDir = func() error { return os.RemoveAll(tmpRoot) }
	}
	cmd := BuildgCmd(t, append(append([]string{"debug"}, gotOpts.opts...), buildCtx),
		WithBuildgCmdEnv(gotOpts.env...),
		WithGlobalOptions("--root="+rootDir),
	)
	t.Logf("executing %q with args %+v", cmd.Path, cmd.Args)
	prompt := identity.NewID()
	cmd.Env = append(cmd.Env, "BUILDG_PS1"+"="+prompt)
	stdinP, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := new(bytes.Buffer)
	stdoutDump := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.Stdout = io.MultiWriter(stdout, stdoutDump)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	sh := &DebugShell{
		t:              t,
		cmd:            cmd,
		stdin:          stdinP,
		stdout:         stdout,
		stderr:         stderr,
		stdoutDump:     stdoutDump,
		prompt:         prompt,
		waitDoneCh:     make(chan struct{}),
		rootDir:        rootDir,
		cleanupRootDir: cleanupRootDir,
	}
	go func() {
		err := cmd.Wait()
		sh.waitMu.Lock()
		sh.waitDone, sh.waitErr = true, err
		sh.waitMu.Unlock()
		close(sh.waitDoneCh)
	}()
	if _, err := sh.readUntilPrompt(); err != nil {
		sh.fatal(err.Error())
	}
	return sh
}

func (sh *DebugShell) wait() error {
	<-sh.waitDoneCh
	sh.waitMu.Lock()
	defer sh.waitMu.Unlock()
	return sh.waitErr
}

func (sh *DebugShell) Do(args string) *Output {
	if _, err := sh.stdin.Write([]byte(args + "\n")); err != nil {
		return &Output{sh, nil}
	}
	stdout, err := sh.readUntilPrompt()
	if err != nil && err != io.EOF {
		sh.fatal(fmt.Sprintf("failed to read stdout: %v", err))
	}
	return &Output{sh, stdout}
}

func (sh *DebugShell) readUntilPrompt() (out []byte, retErr error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		<-ticker.C
		got, err := io.ReadAll(sh.stdout)
		out = append(out, got...)
		if err != nil {
			return out, err
		}
		if i := strings.LastIndex(string(out), sh.prompt); i >= 0 {
			out = out[:i]
			return out, nil
		}
		sh.waitMu.Lock()
		isDone := sh.waitDone
		sh.waitMu.Unlock()
		if isDone {
			return out, nil
		}
	}
}

func (sh *DebugShell) fatal(mes string) {
	sh.dump()
	sh.t.Fatal(mes)
}

func (sh *DebugShell) dump() {
	sh.t.Log("stdout log:\n" + sh.stdoutDump.String())
	sh.t.Log("stderr log:\n" + sh.stderr.String())
}

func (sh *DebugShell) Wait() error {
	return sh.wait()
}

func (sh *DebugShell) Close(t *testing.T) error {
	sh.Do("exit")
	sh.cmd.Process.Kill()
	retErr := sh.wait()
	if _, err := BuildgCmd(t, []string{"prune"}, WithGlobalOptions("--root="+sh.rootDir)).Output(); err != nil {
		retErr = err
	}
	if sh.cleanupRootDir != nil {
		if err := sh.cleanupRootDir(); err != nil {
			retErr = err
		}
	}
	return retErr
}

func NewTempContext(t *testing.T, dt string) (p string, done func() error) {
	tmpCtx, err := os.MkdirTemp("", "buildg-test-tmpctx")
	if err != nil {
		t.Fatal(err)
		return
	}
	t.Logf("temporary context: %q", tmpCtx)
	if err := os.WriteFile(filepath.Join(tmpCtx, "Dockerfile"), []byte(dt), 0600); err != nil {
		os.RemoveAll(tmpCtx)
		t.Fatal(err)
		return
	}
	return tmpCtx, func() error { return os.RemoveAll(tmpCtx) }
}
