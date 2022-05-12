package testutil

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	t      *testing.T
	cmd    *exec.Cmd
	stdin  io.Writer
	stdout io.Reader
	prompt string
}

type Output struct {
	sh     *DebugShell
	stdout []byte
}

func (o *Output) OutEqual(s string) *Output {
	o.sh.t.Log("stdout:\n" + string(o.stdout))
	if s != string(o.stdout) {
		o.sh.t.Fatalf("unexpected stdout\nwanted:\n%s\ngot:\n%s", s, o.stdout)
	}
	return o
}

func (o *Output) OutContains(s string) *Output {
	o.sh.t.Log("stdout:\n" + string(o.stdout))
	if !strings.Contains(string(o.stdout), s) {
		o.sh.t.Fatalf("unexpected stdout\nmust include:\n%s\ngot:\n%s", s, o.stdout)
	}
	return o
}

func (o *Output) OutNotContains(s string) *Output {
	o.sh.t.Log("stdout:\n" + string(o.stdout))
	if strings.Contains(string(o.stdout), s) {
		o.sh.t.Fatalf("unexpected stdout\nmust NOT include:\n%s\ngot:\n%s", s, o.stdout)
	}
	return o
}

func NewDebugShell(t *testing.T, buildCtx string, options ...string) *DebugShell {
	buildgCmd := getBuildgBinary(t)
	prompt := identity.NewID()
	cmd := exec.Command(buildgCmd, append(append([]string{"debug"}, options...), buildCtx)...)
	cmd.Env = append(os.Environ(), "BUILDG_PS1"+"="+prompt)
	stdinP, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutP, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// TODO: enable to dump stderr if needed
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	sh := &DebugShell{t, cmd, stdinP, stdoutP, prompt}
	if _, err := sh.readUntilPrompt(); err != nil {
		t.Fatal(err)
	}
	return sh
}

func (sh *DebugShell) Do(args string) *Output {
	if _, err := sh.stdin.Write([]byte(args + "\n")); err != nil {
		return &Output{sh, nil}
	}
	stdout, err := sh.readUntilPrompt()
	if err != nil {
		sh.t.Fatalf("failed to read stdout: %v", err)
	}
	return &Output{sh, stdout}
}

func (sh *DebugShell) readUntilPrompt() (out []byte, retErr error) {
	buf := make([]byte, 4096)
	for {
		n, err := sh.stdout.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				return out, nil
			}
			return out, err
		}
		if i := strings.LastIndex(string(out), sh.prompt); i >= 0 {
			out = out[:i]
			return out, nil
		}
	}
}

func (sh *DebugShell) Wait() error {
	return sh.cmd.Wait()
}

func (sh *DebugShell) Close() error {
	sh.Do("exit")
	sh.cmd.Process.Kill()
	return sh.cmd.Wait()
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
