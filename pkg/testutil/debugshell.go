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
}

type Output struct {
	sh     *DebugShell
	stdout []byte
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
	opts []string
	env  []string
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

func NewDebugShell(t *testing.T, buildCtx string, opts ...DebugShellOption) *DebugShell {
	gotOpts := options{}
	for _, o := range opts {
		o(&gotOpts)
	}

	buildgCmd := getBuildgBinary(t)
	prompt := identity.NewID()
	args := append(append([]string{"debug"}, gotOpts.opts...), buildCtx)
	t.Logf("executing %q with args %+v", buildgCmd, args)
	cmd := exec.Command(buildgCmd, args...)
	cmd.Env = append(append(os.Environ(), "BUILDG_PS1"+"="+prompt), gotOpts.env...)
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
		t:          t,
		cmd:        cmd,
		stdin:      stdinP,
		stdout:     stdout,
		stderr:     stderr,
		stdoutDump: stdoutDump,
		prompt:     prompt,
		waitDoneCh: make(chan struct{}),
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

func (sh *DebugShell) Close() error {
	sh.Do("exit")
	sh.cmd.Process.Kill()
	return sh.wait()
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

func ExecNoTTY(args string) string {
	return "exec -i=false -t=false " + args
}
