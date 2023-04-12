package dap

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-dap"
	"github.com/ktock/buildg/pkg/testutil"
	"golang.org/x/crypto/ssh/agent"
)

func TestContinueExit(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo a > /a
RUN echo b > /b
RUN echo c > /c`, testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{}, true)
	c.continueAndExit(t)
}

func TestInitialBreakpoints(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo a > /a
RUN echo b > /b
RUN echo c > /c
RUN echo d > /d
RUN echo e > /e`, testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{
		{
			Line: 2,
		},
		{
			Line: 4,
		},
	}, true)
	c.continueAndStop(t, 2)
	c.continueAndStop(t, 4)
	c.continueAndExit(t)
}

func TestInitialBreakpointsStopOnEntryFalse(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo a > /a
RUN echo b > /b
RUN echo c > /c
RUN echo d > /d
RUN echo e > /e`, testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: false,
	}, []dap.SourceBreakpoint{
		{
			Line: 2,
		},
		{
			Line: 4,
		},
	}, true)
	c.testCurrentLine(t, 2)
	c.continueAndStop(t, 4)
	c.continueAndExit(t)
}

func TestSetBreakpoints(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo a > /a
RUN echo b > /b
RUN echo c > /c
RUN echo d > /d
RUN echo e > /e`, testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{}, true)
	c.setBreakpoints(t, []dap.SourceBreakpoint{
		{
			Line: 2,
		},
		{
			Line: 4,
		},
	})
	c.continueAndStop(t, 2)
	c.continueAndStop(t, 4)
	c.continueAndExit(t)
}

func TestSetBreakpointsChange(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo a > /a
RUN echo b > /b
RUN echo c > /c
RUN echo d > /d
RUN echo e > /e`, testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{}, true)
	c.setBreakpoints(t, []dap.SourceBreakpoint{
		{
			Line: 2,
		},
		{
			Line: 4,
		},
	})
	c.continueAndStop(t, 2)
	c.setBreakpoints(t, []dap.SourceBreakpoint{
		{
			Line: 3,
		},
		{
			Line: 5,
		},
	})
	c.continueAndStop(t, 3)
	c.continueAndStop(t, 5)
	c.continueAndExit(t)
}

func TestNext(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo a > /a
RUN echo b > /b
RUN echo c > /c
RUN echo d > /d
RUN echo e > /e`, testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{
		{
			Line: 3,
		},
	}, true)
	c.continueAndStop(t, 3)
	c.nextAndStop(t, 4)
	c.nextAndStop(t, 5)
	c.nextAndStop(t, 6)
	c.nextAndExit(t)
}

func TestNoDebug(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo a > /a
RUN echo b > /b
RUN echo c > /c
RUN echo d > /d
RUN echo e > /e`, testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: false,
		NoDebug:     true,
	}, []dap.SourceBreakpoint{}, false)
	if e, ok := c.receive(t).(*dap.ThreadEvent); !ok || e.Event.Event != "thread" || e.Body.Reason != "exited" {
		t.Fatalf("thread event (exited) must be returned")
	}
	if _, ok := c.receive(t).(*dap.TerminatedEvent); !ok {
		t.Fatalf("terminated event must be returned")
	}
	if _, ok := c.receive(t).(*dap.ExitedEvent); !ok {
		t.Fatalf("exited event must be returned")
	}
}

func TestMultipleLineHits(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s AS dev-a
RUN echo a > /a

FROM %s AS dev-b
RUN echo b > /b

FROM scratch
COPY --from=dev-a /a /a
COPY --from=dev-b /b /b`, testutil.Mirror("busybox:1.32.0"), testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{
		{
			Line: 4,
		},
	}, true)
	c.testCurrentLine(t, 4)
	c.continueAndExit(t)
}

func TestBreakpointVariables(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
ARG FOO=bar
RUN echo a > /a
RUN echo b > /b`, testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{
		{
			Line: 3,
		},
	}, true)
	c.continueAndStop(t, 3)
	c.send(&dap.ThreadsRequest{
		Request: c.newRequest("threads"),
	})
	if r, ok := c.receive(t).(*dap.ThreadsResponse); !ok || len(r.Body.Threads) != 1 || r.Body.Threads[0].Id != 1 {
		t.Fatalf("threads response must be returned")
	}
	c.send(&dap.StackTraceRequest{
		Request: c.newRequest("stackTrace"),
		Arguments: dap.StackTraceArguments{
			ThreadId: 1,
		},
	})
	if r, ok := c.receive(t).(*dap.StackTraceResponse); !ok || len(r.Body.StackFrames) != 1 || r.Body.StackFrames[0].Line != 3 {
		t.Fatalf("stackTrace response must be returned")
	}
	c.send(&dap.ScopesRequest{
		Request: c.newRequest("scopes"),
		Arguments: dap.ScopesArguments{
			FrameId: 0,
		},
	})
	if r, ok := c.receive(t).(*dap.ScopesResponse); !ok || len(r.Body.Scopes) != 1 || r.Body.Scopes[0].Name != "Environment Variables" || r.Body.Scopes[0].VariablesReference != 1 {
		t.Fatalf("scopes response must be returned")
	}
	c.send(&dap.VariablesRequest{
		Request: c.newRequest("variables"),
		Arguments: dap.VariablesArguments{
			VariablesReference: 1,
		},
	})
	r, ok := c.receive(t).(*dap.VariablesResponse)
	if !ok || len(r.Body.Variables) == 0 {
		t.Fatalf("variables response must be returned")
	}
	var found bool
	for _, kv := range r.Body.Variables {
		if kv.Name == "FOO" && kv.Value == "bar" {
			found = true
		}
	}
	if !found {
		t.Fatalf("failed to get variable")
	}
	c.continueAndExit(t)
}

func TestTarget(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s AS dev-1
RUN echo a > /a
RUN echo b > /b

FROM %s AS dev-2
RUN echo c > /c
COPY --from=dev-1 /a /a
COPY --from=dev-1 /b /b
RUN echo d > /d

FROM %s AS dev-3
RUN echo e > /e
RUN echo f > /f`, testutil.Mirror("busybox:1.32.0"), testutil.Mirror("busybox:1.32.0"), testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
		Target:      "dev-2",
	}, []dap.SourceBreakpoint{
		{
			Line: 3,
		},
		{
			Line: 9,
		},
		{
			Line: 12, // will be skipped
		},
	}, true)
	c.continueAndStop(t, 3)
	c.continueAndStop(t, 9)
	c.continueAndExit(t)
}

func TestExecSimple(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo -n a > /a
RUN echo -n b > /b
RUN echo -n c > /c`, testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
		Image:       testutil.Mirror("ubuntu:22.04"),
	}, []dap.SourceBreakpoint{
		{
			Line: 2,
		},
		{
			Line: 3,
		},
	}, true)
	c.continueAndStop(t, 2)
	if out := c.execContainer(t, "cat /a"); out != "a" {
		t.Fatalf("wanted: \"a\"; got: %q", out)
	}
	if out := c.execContainer(t, "cat /b"); out != "" {
		t.Fatalf("wanted: \"\"; got: %q", out)
	}
	if out := c.execContainer(t, "--image cat /debugroot/a"); out != "a" {
		t.Fatalf("wanted: \"a\"; got: %q", out)
	}
	if out := c.execContainer(t, "--image cat /etc/os-release"); !strings.Contains(out, `NAME="Ubuntu"`) {
		t.Fatalf("wanted: \"NAME=\"Ubuntu\"\"; got: %q", out)
	}
	if out := c.execContainer(t, "--image --mountroot=/testdebugroot/rootdir/ cat /testdebugroot/rootdir/a"); out != "a" {
		t.Fatalf("wanted: \"a\"; got: %q", out)
	}
	if out := c.execContainer(t, "--init-state cat /a"); out != "" {
		t.Fatalf("wanted: \"\"; got: %q", out)
	}
	if out := c.execContainer(t, `-e MSG=hello -e MSG2=world /bin/sh -c "echo -n $MSG $MSG2"`); out != "hello world" {
		t.Fatalf("wanted: \"hello world\"; got: %q", out)
	}
	if out := c.execContainer(t, `--workdir /tmp /bin/sh -c "echo -n $(pwd)"`); out != "/tmp" {
		t.Fatalf("wanted: \"/tmp\"; got: %q", out)
	}
	c.continueAndStop(t, 3)
	if out := c.execContainer(t, "cat /a"); out != "a" {
		t.Fatalf("wanted: \"a\"; got: %q", out)
	}
	if out := c.execContainer(t, "cat /b"); out != "b" {
		t.Fatalf("wanted: \"b\"; got: %q", out)
	}
	c.continueAndExit(t)
}

func TestExecNonRun(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s AS dev
RUN echo -n hi > /a

FROM %s
COPY --from=dev /a /b
RUN cat /b
`, testutil.Mirror("busybox:1.32.0"), testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
		Image:       testutil.Mirror("ubuntu:22.04"),
	}, []dap.SourceBreakpoint{
		{
			Line: 2,
		},
		{
			Line: 5,
		},
	}, true)
	if out := c.execContainer(t, "cat /a"); out != "" {
		t.Fatalf("wanted: \"\"; got: %q", out)
	}
	c.continueAndStop(t, 2)
	if out := c.execContainer(t, "cat /a"); out != "hi" {
		t.Fatalf("wanted: \"hi\"; got: %q", out)
	}
	if out := c.execContainer(t, "cat /b"); out != "" {
		t.Fatalf("wanted: \"\"; got: %q", out)
	}
	c.continueAndStop(t, 5)
	if out := c.execContainer(t, "cat /a"); out != "" {
		t.Fatalf("wanted: \"\"; got: %q", out)
	}
	if out := c.execContainer(t, "cat /b"); out != "hi" {
		t.Fatalf("wanted: \"hi\"; got: %q", out)
	}

	if out := c.execContainer(t, "--image cat /debugroot/b"); out != "hi" {
		t.Fatalf("wanted: \"hi\"; got: %q", out)
	}
	if out := c.execContainer(t, "--image cat /etc/os-release"); !strings.Contains(out, `NAME="Ubuntu"`) {
		t.Fatalf("wanted: \"NAME=\"Ubuntu\"\"; got: %q", out)
	}
	if out := c.execContainer(t, "--image --mountroot=/testdebugroot/rootdir/ cat /testdebugroot/rootdir/b"); out != "hi" {
		t.Fatalf("wanted: \"hi\"; got: %q", out)
	}
	if out := c.execContainer(t, `-e MSG=hello -e MSG2=world /bin/sh -c "echo -n $MSG $MSG2"`); out != "hello world" {
		t.Fatalf("wanted: \"hello world\"; got: %q", out)
	}
	if out := c.execContainer(t, `--workdir /tmp /bin/sh -c "echo -n $(pwd)"`); out != "/tmp" {
		t.Fatalf("wanted: \"/tmp\"; got: %q", out)
	}
	c.continueAndExit(t)
}

func TestExecSecrets(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN --mount=type=secret,id=testsecret,target=/root/secret [ "$(cat /root/secret)" = 'test-secret' ]`,
		testutil.Mirror("busybox:1.32.0"))
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	tmpSec, err := os.CreateTemp("", "testexecsecret")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpSec.Name())
	if _, err := tmpSec.Write([]byte("test-secret")); err != nil {
		t.Fatal(err)
	}
	if err := tmpSec.Close(); err != nil {
		t.Fatal(err)
	}

	// secrets from path
	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
		Secrets:     []string{"id=testsecret,src=" + tmpSec.Name()},
	}, []dap.SourceBreakpoint{}, true)
	c.nextAndStop(t, 2)
	if out := c.execContainer(t, "cat /root/secret"); out != "test-secret" {
		t.Fatalf("wanted: \"test-secret\"; got: %q", out)
	}
	c.continueAndExit(t)
	s.Close()

	// secrets from env
	s2 := testutil.NewDAPServer(t, testutil.WithDAPServerEnv("TEST_SECRET=test-secret"))
	defer s2.Close()
	c2 := newDAPClient(t, s2.Conn())
	c2.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
		Secrets:     []string{"id=testsecret,env=TEST_SECRET"},
	}, []dap.SourceBreakpoint{}, true)
	c2.nextAndStop(t, 2)
	if out := c2.execContainer(t, "cat /root/secret"); out != "test-secret" {
		t.Fatalf("wanted: \"test-secret\"; got: %q", out)
	}
	c2.continueAndExit(t)
}

func TestExecSSH(t *testing.T) {
	t.Parallel()
	// test ssh from socket
	tests := []struct {
		name          string
		launchConfig  func(sockPath string) LaunchConfig
		buildgOptions func(sockPath string) []testutil.DAPServerOption
		mountOption   string
	}{
		{
			name: "default",
			launchConfig: func(sockPath string) LaunchConfig {
				return LaunchConfig{
					SSH: []string{"default=" + sockPath},
				}
			},
			mountOption: "type=ssh",
		},
		{
			name: "default-env",
			launchConfig: func(sockPath string) LaunchConfig {
				return LaunchConfig{
					SSH: []string{"default"},
				}
			},
			buildgOptions: func(sockPath string) []testutil.DAPServerOption {
				return []testutil.DAPServerOption{
					testutil.WithDAPServerEnv("SSH_AUTH_SOCK=" + sockPath),
				}
			},
			mountOption: "type=ssh",
		},
		{
			name: "id",
			launchConfig: func(sockPath string) LaunchConfig {
				return LaunchConfig{
					SSH: []string{"mysecret=" + sockPath},
				}
			},
			mountOption: "type=ssh,id=mysecret",
		},
		{
			name: "id-env",
			launchConfig: func(sockPath string) LaunchConfig {
				return LaunchConfig{
					SSH: []string{"mysecret"},
				}
			},
			buildgOptions: func(sockPath string) []testutil.DAPServerOption {
				return []testutil.DAPServerOption{
					testutil.WithDAPServerEnv("SSH_AUTH_SOCK=" + sockPath),
				}
			},
			mountOption: "type=ssh,id=mysecret",
		},
		{
			name: "id-env2",
			launchConfig: func(sockPath string) LaunchConfig {
				return LaunchConfig{
					SSH: []string{"mysecret", "mysecret2"},
				}
			},
			buildgOptions: func(sockPath string) []testutil.DAPServerOption {
				return []testutil.DAPServerOption{
					testutil.WithDAPServerEnv("SSH_AUTH_SOCK=" + sockPath),
				}
			},
			mountOption: "type=ssh,id=mysecret2",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := agent.NewKeyring()
			k, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Add(agent.AddedKey{PrivateKey: k}); err != nil {
				t.Fatal(err)
			}
			tmpSock, err := os.MkdirTemp("", "sshsockroot")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpSock)
			sockPath := filepath.Join(tmpSock, "ssh_auth_sock")
			l, err := net.Listen("unix", sockPath)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			go func() {
				for {
					c, err := l.Accept()
					if err != nil {
						return
					}
					go agent.ServeAgent(a, c)
				}
			}()
			tmpCtx, doneTmpCtx := testutil.NewTempContext(t, fmt.Sprintf(`FROM %s
RUN apk add openssh
RUN --mount=%s ssh-add -l | grep 2048 | grep RSA`,
				testutil.Mirror("alpine:3.15.3"), tt.mountOption))
			defer doneTmpCtx()

			var bOpts []testutil.DAPServerOption
			if tt.buildgOptions != nil {
				bOpts = tt.buildgOptions(sockPath)
			}
			s := testutil.NewDAPServer(t, bOpts...)
			defer s.Close()
			c := newDAPClient(t, s.Conn())
			lCfg := tt.launchConfig(sockPath)
			lCfg.Program = filepath.Join(tmpCtx, "Dockerfile")
			lCfg.StopOnEntry = true
			c.start(t, lCfg, []dap.SourceBreakpoint{
				{
					Line: 3,
				},
			}, true)
			c.continueAndStop(t, 3)
			if out := c.execContainer(t, `ssh-add -l | grep 2048 | grep RSA`); !strings.Contains(out, "2048") || !strings.Contains(out, "(RSA)") {
				t.Fatalf("wanted: \"2048\" and \"(RSA)\"; got: %q", out)
			}
			if out := c.execContainer(t, `/bin/sh -c "ssh-keygen -f /tmp/key -N '' && ssh-add -k /tmp/key 2>&1"`); !strings.Contains(out, "agent refused operation") {
				t.Fatalf("wanted: \"agent refused operation\"; got: %q", out)
			}
			c.continueAndExit(t)
		})
	}

	// test ssh from file
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, fmt.Sprintf(`FROM %s
RUN apk add openssh
RUN --mount=type=ssh,id=testsecret ssh-add -l | grep 2048 | grep RSA`,
		testutil.Mirror("alpine:3.15.3")))
	defer doneTmpCtx()
	tmpSec, err := os.CreateTemp("", "testexecsecret")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpSec.Name())
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpSec.Write(pem.EncodeToMemory(
		&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(k),
		},
	)); err != nil {
		t.Fatal(err)
	}
	if err := tmpSec.Close(); err != nil {
		t.Fatal(err)
	}

	s := testutil.NewDAPServer(t)
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
		SSH:         []string{"testsecret=" + tmpSec.Name()},
	}, []dap.SourceBreakpoint{
		{
			Line: 3,
		},
	}, true)
	c.continueAndStop(t, 3)
	if out := c.execContainer(t, `ssh-add -l | grep 2048 | grep RSA`); !strings.Contains(out, "2048") || !strings.Contains(out, "(RSA)") {
		t.Fatalf("wanted: \"2048\" and \"(RSA)\"; got: %q", out)
	}
	c.continueAndExit(t)
}

func TestCacheReuse(t *testing.T) {
	t.Parallel()
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, fmt.Sprintf(`FROM %s
RUN date > /a
RUN date > /b
RUN date > /
`, testutil.Mirror("busybox:1.32.0")))
	defer doneTmpCtx()

	tmpRoot, err := os.MkdirTemp(os.Getenv(testutil.BuildgTestTmpDirEnv), "buildg-test-tmproot")
	if err != nil {
		t.Fatal(err)
		return
	}
	defer os.RemoveAll(tmpRoot)

	s := testutil.NewDAPServer(t, testutil.WithDAPServerGlobalOptions("--root="+tmpRoot))
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{}, true)
	c.nextAndStop(t, 2)
	a := nonEmpty(t, c.execContainer(t, "cat /a"))
	c.nextAndStop(t, 3)
	b := nonEmpty(t, c.execContainer(t, "cat /b"))
	c.nextAndStop(t, 4)
	a2 := nonEmpty(t, c.execContainer(t, "cat /a"))
	b2 := nonEmpty(t, c.execContainer(t, "cat /b"))
	c.nextAndExit(t)
	s.Close()

	s2 := testutil.NewDAPServer(t, testutil.WithDAPServerGlobalOptions("--root="+tmpRoot))
	defer s2.Close()
	c2 := newDAPClient(t, s2.Conn())
	c2.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{}, true)
	c2.nextAndStop(t, 2)
	if out := c2.execContainer(t, "cat /a"); out != a {
		t.Fatalf("want %q; got %q", a, out)
	}
	c2.nextAndStop(t, 3)
	if out := c2.execContainer(t, "cat /b"); out != b {
		t.Fatalf("want %q; got %q", b, out)
	}
	c2.nextAndStop(t, 4)
	if out := c2.execContainer(t, "cat /a"); out != a2 {
		t.Fatalf("want %q; got %q", a2, out)
	}
	if out := c2.execContainer(t, "cat /b"); out != b2 {
		t.Fatalf("want %q; got %q", b2, out)
	}
	c2.nextAndExit(t)
	s2.Close()

	if _, err := testutil.BuildgCmd(t, []string{"dap", "prune"}, testutil.WithGlobalOptions("--root="+tmpRoot)).Output(); err != nil {
		t.Fatal(err)
	}
	duOut, err := testutil.BuildgCmd(t, []string{"dap", "du"}, testutil.WithGlobalOptions("--root="+tmpRoot)).Output()
	if err != nil {
		t.Fatal(err)
	}
	zeroOut := "Total:\t0 B"
	if !strings.Contains(string(duOut), zeroOut) {
		t.Fatalf("du must contain %q; got %q", zeroOut, string(duOut))
	}

	sp := testutil.NewDAPServer(t, testutil.WithDAPServerGlobalOptions("--root="+tmpRoot))
	defer sp.Close()
	cp := newDAPClient(t, sp.Conn())
	cp.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{}, true)
	cp.nextAndStop(t, 2)
	if out := cp.execContainer(t, "cat /a"); out == a {
		t.Fatalf("shouldn't be %q; got %q", a, out)
	}
	cp.nextAndStop(t, 3)
	if out := cp.execContainer(t, "cat /b"); out == b {
		t.Fatalf("shouldn't be %q; got %q", b, out)
	}
	cp.nextAndStop(t, 4)
	if out := cp.execContainer(t, "cat /a"); out == a2 {
		t.Fatalf("shouldn't be %q; got %q", a2, out)
	}
	if out := cp.execContainer(t, "cat /b"); out == b2 {
		t.Fatalf("shouldn't be %q; got %q", b2, out)
	}
	cp.nextAndExit(t)
}

func TestCacheReuseNonRun(t *testing.T) {
	t.Parallel()
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, fmt.Sprintf(`FROM %s AS dev
RUN date > /a

FROM %s
COPY --from=dev /a /b
RUN cat /b
`, testutil.Mirror("busybox:1.32.0"), testutil.Mirror("busybox:1.32.0")))
	defer doneTmpCtx()

	tmpRoot, err := os.MkdirTemp(os.Getenv(testutil.BuildgTestTmpDirEnv), "buildg-test-tmproot")
	if err != nil {
		t.Fatal(err)
		return
	}
	defer os.RemoveAll(tmpRoot)

	s := testutil.NewDAPServer(t, testutil.WithDAPServerGlobalOptions("--root="+tmpRoot))
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{}, true)
	c.nextAndStop(t, 2)
	a := nonEmpty(t, c.execContainer(t, "cat /a"))
	c.nextAndStop(t, 5)
	b := nonEmpty(t, c.execContainer(t, "cat /b"))
	c.nextAndStop(t, 6)
	b2 := nonEmpty(t, c.execContainer(t, "cat /b"))
	c.nextAndExit(t)
	s.Close()
	if a != b {
		t.Fatalf("wanted %q; got %q", a, b)
	}
	if b != b2 {
		t.Fatalf("wanted %q; got %q", b, b2)
	}

	s2 := testutil.NewDAPServer(t, testutil.WithDAPServerGlobalOptions("--root="+tmpRoot))
	defer s2.Close()
	c2 := newDAPClient(t, s2.Conn())
	c2.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{}, true)
	c2.nextAndStop(t, 2)
	if out := c2.execContainer(t, "cat /a"); out != a {
		t.Fatalf("want %q; got %q", a, out)
	}
	c2.nextAndStop(t, 5)
	if out := c2.execContainer(t, "cat /b"); out != b {
		t.Fatalf("want %q; got %q", b, out)
	}
	c2.nextAndStop(t, 6)
	if out := c2.execContainer(t, "cat /b"); out != b2 {
		t.Fatalf("want %q; got %q", b2, out)
	}
	c2.nextAndExit(t)
	s2.Close()
}

func TestDisconnect(t *testing.T) {
	t.Parallel()
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, fmt.Sprintf(`FROM %s
RUN date > /a
RUN date > /b
RUN date > /
`, testutil.Mirror("alpine:3.15.3")))
	defer doneTmpCtx()

	tmpRoot, err := os.MkdirTemp(os.Getenv(testutil.BuildgTestTmpDirEnv), "buildg-test-tmproot")
	if err != nil {
		t.Fatal(err)
		return
	}
	defer os.RemoveAll(tmpRoot)

	// Test normal disconnection
	s := testutil.NewDAPServer(t, testutil.WithDAPServerGlobalOptions("--root="+tmpRoot))
	defer s.Close()
	c := newDAPClient(t, s.Conn())
	c.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{}, true)
	c.nextAndStop(t, 2)
	nonEmpty(t, c.execContainer(t, "cat /a"))
	c.nextAndStop(t, 3)
	nonEmpty(t, c.execContainer(t, "cat /b"))
	c.nextAndStop(t, 4)
	nonEmpty(t, c.execContainer(t, "cat /a"))
	nonEmpty(t, c.execContainer(t, "cat /b"))
	c.disconnect(t)
	doneCh := make(chan struct{})
	go func() {
		s.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("disconnect takes too long")
	}

	// Test disconnection under the case where running container and active I/O exist
	s2 := testutil.NewDAPServer(t, testutil.WithDAPServerGlobalOptions("--root="+tmpRoot))
	defer s2.Close()
	c2 := newDAPClient(t, s2.Conn())
	c2.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{}, true)
	c2.nextAndStop(t, 2)
	cmd := c2.execContainerCmd(t, `/bin/sh -c "echo started; sleep 1000000"`)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start io client: %v", err)
	}
	doneCh = make(chan struct{})
	errCh := make(chan error)
	go func() {
		scanner := bufio.NewScanner(io.TeeReader(pr, os.Stdout))
		for scanner.Scan() {
			txt := scanner.Text()
			if txt == "started" {
				close(doneCh)
				return
			}
		}
		if err := scanner.Err(); err != nil {
			errCh <- err
		}
	}()
	select {
	case <-doneCh:
	case err := <-errCh:
		t.Fatalf("failed to wait for container output: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatalf("io timeout during waiting for container startup string")
	}
	c2.disconnect(t)
	doneCh = make(chan struct{})
	go func() {
		s2.Wait()
		cmd.Process.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("disconnect takes too long")
	}

	// Test disconnection under the corner case where I/O attacher starts too late after disconnection
	s3 := testutil.NewDAPServer(t, testutil.WithDAPServerGlobalOptions("--root="+tmpRoot))
	defer s3.Close()
	c3 := newDAPClient(t, s3.Conn())
	c3.start(t, LaunchConfig{
		Program:     filepath.Join(tmpCtx, "Dockerfile"),
		StopOnEntry: true,
	}, []dap.SourceBreakpoint{}, true)
	c3.nextAndStop(t, 2)
	cmd = c3.execContainerCmd(t, `/bin/sh -c "echo started; sleep 1000000"`)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	c3.disconnect(t) // disconnect before attaching
	s3.Wait()        // wait for server ending
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start io client: %v", err)
	}
	doneCh = make(chan struct{})
	go func() {
		cmd.Process.Wait() // this should not hang
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("disconnect takes too long")
	}
}

func newDAPClient(_ *testing.T, conn net.Conn) *dapClient {
	c := &dapClient{
		conn:  conn,
		mesCh: make(chan dap.Message),
		errCh: make(chan error),
	}
	go c.listen()
	return c
}

type dapClient struct {
	conn   net.Conn
	sendMu sync.Mutex
	seq    int
	mesCh  chan dap.Message
	errCh  chan error
}

func (c *dapClient) setBreakpoints(t *testing.T, breakpoints []dap.SourceBreakpoint) {
	c.send(&dap.SetBreakpointsRequest{
		Request: c.newRequest("setBreakpoints"),
		Arguments: dap.SetBreakpointsArguments{
			Source: dap.Source{
				Name: "Dockerfile",
			},
			Breakpoints: breakpoints,
		},
	})
	if _, ok := c.receive(t).(*dap.SetBreakpointsResponse); !ok {
		t.Fatalf("setBreakpoints response must be returned")
	}
}

func (c *dapClient) disconnect(t *testing.T) {
	c.send(&dap.DisconnectRequest{
		Request: c.newRequest("disconnect"),
	})
	if e, ok := c.receive(t).(*dap.ThreadEvent); !ok || e.Event.Event != "thread" || e.Body.Reason != "exited" {
		t.Fatalf("thread event (exited) must be returned")
	}
	if _, ok := c.receive(t).(*dap.TerminatedEvent); !ok {
		t.Fatalf("terminated event must be returned")
	}
	if _, ok := c.receive(t).(*dap.ExitedEvent); !ok {
		t.Fatalf("exited event must be returned")
	}
	if _, ok := c.receive(t).(*dap.DisconnectResponse); !ok {
		t.Fatalf("disconnect response must be returned")
	}
}

// https://microsoft.github.io/debug-adapter-protocol/overview#debug-session-end
func (c *dapClient) continueAndExit(t *testing.T) {
	c.send(&dap.ContinueRequest{
		Request: c.newRequest("continue"),
		Arguments: dap.ContinueArguments{
			ThreadId: 1,
		},
	})
	if _, ok := c.receive(t).(*dap.ContinueResponse); !ok {
		t.Fatalf("continue response must be returned")
	}
	if e, ok := c.receive(t).(*dap.ThreadEvent); !ok || e.Event.Event != "thread" || e.Body.Reason != "exited" {
		t.Fatalf("thread event (exited) must be returned")
	}
	if _, ok := c.receive(t).(*dap.TerminatedEvent); !ok {
		t.Fatalf("terminated event must be returned")
	}
	if _, ok := c.receive(t).(*dap.ExitedEvent); !ok {
		t.Fatalf("exited event must be returned")
	}
}

func (c *dapClient) continueAndStop(t *testing.T, line int) {
	c.send(&dap.ContinueRequest{
		Request: c.newRequest("continue"),
		Arguments: dap.ContinueArguments{
			ThreadId: 1,
		},
	})
	if _, ok := c.receive(t).(*dap.ContinueResponse); !ok {
		t.Fatalf("continue response must be returned")
	}
	if _, ok := c.receive(t).(*dap.StoppedEvent); !ok {
		t.Fatalf("stopped event must be returned")
	}
	c.testCurrentLine(t, line)
}

func (c *dapClient) nextAndExit(t *testing.T) {
	c.send(&dap.NextRequest{
		Request: c.newRequest("next"),
		Arguments: dap.NextArguments{
			ThreadId: 1,
		},
	})
	if _, ok := c.receive(t).(*dap.NextResponse); !ok {
		t.Fatalf("next response must be returned")
	}
	if e, ok := c.receive(t).(*dap.ThreadEvent); !ok || e.Event.Event != "thread" || e.Body.Reason != "exited" {
		t.Fatalf("thread event (exited) must be returned")
	}
	if _, ok := c.receive(t).(*dap.TerminatedEvent); !ok {
		t.Fatalf("terminated event must be returned")
	}
	if _, ok := c.receive(t).(*dap.ExitedEvent); !ok {
		t.Fatalf("exited event must be returned")
	}
}

func (c *dapClient) nextAndStop(t *testing.T, line int) {
	c.send(&dap.NextRequest{
		Request: c.newRequest("next"),
		Arguments: dap.NextArguments{
			ThreadId: 1,
		},
	})
	if _, ok := c.receive(t).(*dap.NextResponse); !ok {
		t.Fatalf("next response must be returned")
	}
	if _, ok := c.receive(t).(*dap.StoppedEvent); !ok {
		t.Fatalf("stopped event must be returned")
	}
	c.testCurrentLine(t, line)
}

func (c *dapClient) testCurrentLine(t *testing.T, line int) {
	c.send(&dap.ThreadsRequest{
		Request: c.newRequest("threads"),
	})
	if r, ok := c.receive(t).(*dap.ThreadsResponse); !ok || len(r.Body.Threads) != 1 || r.Body.Threads[0].Id != 1 {
		t.Fatalf("threads response must be returned")
	}
	c.send(&dap.StackTraceRequest{
		Request: c.newRequest("stackTrace"),
		Arguments: dap.StackTraceArguments{
			ThreadId: 1,
		},
	})
	if r, ok := c.receive(t).(*dap.StackTraceResponse); !ok || len(r.Body.StackFrames) != 1 || r.Body.StackFrames[0].Line != line {
		t.Fatalf("stackTrace response (want line: %d) must be returned", line)
	}
}

// https://microsoft.github.io/debug-adapter-protocol/overview#configuring-breakpoint-and-exception-behavior
func (c *dapClient) start(t *testing.T, launchCfg LaunchConfig, initialBreakpoints []dap.SourceBreakpoint, wait bool) {
	c.send(&dap.InitializeRequest{
		Request: c.newRequest("initialize"),
		Arguments: dap.InitializeRequestArguments{
			AdapterID:                    "dockerfile",
			LinesStartAt1:                true,
			ColumnsStartAt1:              true,
			PathFormat:                   "path",
			SupportsRunInTerminalRequest: true,
		},
	})
	if _, ok := c.receive(t).(*dap.InitializeResponse); !ok {
		t.Fatalf("initialize response must be returned")
	}
	if _, ok := c.receive(t).(*dap.InitializedEvent); !ok {
		t.Fatalf("initialized event must be returned")
	} // TODO: check capabilities

	c.send(&dap.SetBreakpointsRequest{
		Request: c.newRequest("setBreakpoints"),
		Arguments: dap.SetBreakpointsArguments{
			Source: dap.Source{
				Name: "Dockerfile",
			},
			Breakpoints: initialBreakpoints,
		},
	})
	if _, ok := c.receive(t).(*dap.SetBreakpointsResponse); !ok {
		t.Fatalf("setBreakpoints response must be returned")
	}

	// TODO: check breakpoints

	// setExceptionBreakpoints request is optional

	c.send(&dap.ConfigurationDoneRequest{
		Request: c.newRequest("configurationDone"),
	})
	if _, ok := c.receive(t).(*dap.ConfigurationDoneResponse); !ok {
		t.Fatalf("configurationDone response must be returned")
	}

	launchArgument, err := json.Marshal(launchCfg)
	if err != nil {
		t.Fatalf("failed to marshal launch config: %v", err)
	}
	c.send(&dap.LaunchRequest{
		Request:   c.newRequest("launch"),
		Arguments: json.RawMessage(launchArgument),
	})
	if e, ok := c.receive(t).(*dap.ThreadEvent); !ok || e.Event.Event != "thread" || e.Body.Reason != "started" {
		t.Fatalf("thread event (stardted) must be returned") // TODO: do we need this (undocumented in DAP)
	}
	if _, ok := c.receive(t).(*dap.LaunchResponse); !ok {
		t.Fatalf("launch response must be returned")
	}
	if wait {
		if _, ok := c.receive(t).(*dap.StoppedEvent); !ok {
			t.Fatalf("stopped event must be returned")
		}
	}
}

func (c *dapClient) execContainer(t *testing.T, args string) string {
	c.send(&dap.EvaluateRequest{
		Request: c.newRequest("evaluate"),
		Arguments: dap.EvaluateArguments{
			Expression: "exec -t=false -i=false " + args,
			Context:    "repl",
		},
	})
	if _, ok := c.receive(t).(*dap.EvaluateResponse); !ok {
		t.Fatalf("evaluate response must be returned")
	}
	runInTerminalReq, ok := c.receive(t).(*dap.RunInTerminalRequest)
	if !ok {
		t.Fatalf("runInTerminal request must be returned")
	}
	if k := runInTerminalReq.Arguments.Kind; k != "integrated" {
		t.Fatalf("wants: \"integrated\", got: %q", k)
	}
	if args := runInTerminalReq.Arguments.Args; len(args) < 2 {
		t.Fatalf("not enough arguments: %q", args)
	}
	if cwd := runInTerminalReq.Arguments.Cwd; cwd == "" {
		t.Fatalf("cwd must be provided")
	}
	execCmd := exec.Command(runInTerminalReq.Arguments.Args[0], runInTerminalReq.Arguments.Args[1:]...)
	execCmd.Stderr = os.Stderr
	out, err := execCmd.Output()
	if err != nil {
		t.Fatalf("failed to run command: %v", err)
	}
	return string(out)
}

func (c *dapClient) execContainerCmd(t *testing.T, args string) *exec.Cmd {
	c.send(&dap.EvaluateRequest{
		Request: c.newRequest("evaluate"),
		Arguments: dap.EvaluateArguments{
			Expression: "exec -t=false -i=false " + args,
			Context:    "repl",
		},
	})
	if _, ok := c.receive(t).(*dap.EvaluateResponse); !ok {
		t.Fatalf("evaluate response must be returned")
	}
	runInTerminalReq, ok := c.receive(t).(*dap.RunInTerminalRequest)
	if !ok {
		t.Fatalf("runInTerminal request must be returned")
	}
	if k := runInTerminalReq.Arguments.Kind; k != "integrated" {
		t.Fatalf("wants: \"integrated\", got: %q", k)
	}
	if args := runInTerminalReq.Arguments.Args; len(args) < 2 {
		t.Fatalf("not enough arguments: %q", args)
	}
	return exec.Command(runInTerminalReq.Arguments.Args[0], runInTerminalReq.Arguments.Args[1:]...)
}

func (c *dapClient) send(message dap.Message) {
	c.sendMu.Lock()
	dap.WriteProtocolMessage(c.conn, message)
	fmt.Printf("message sent %+v\n", message)
	c.sendMu.Unlock()
}

func (c *dapClient) receive(t *testing.T) dap.Message {
	select {
	case mes := <-c.mesCh:
		return mes
	case err := <-c.errCh:
		t.Fatalf("failed to receive message: %v", err)
	}
	return nil
}

func (c *dapClient) listen() {
	defer close(c.mesCh)
	r := bufio.NewReader(c.conn)
	for {
		req, err := dap.ReadProtocolMessage(r)
		if err != nil {
			if err == io.EOF {
				return
			}
			c.errCh <- err
		}
		if _, ok := req.(*dap.OutputEvent); ok {
			fmt.Printf("output message received %+v\n", req)
			continue
		}
		fmt.Printf("message received %+v\n", req)
		c.mesCh <- req
	}
}

func (c *dapClient) newRequest(command string) dap.Request {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.seq++
	return dap.Request{
		ProtocolMessage: dap.ProtocolMessage{
			Seq:  c.seq,
			Type: "request",
		},
		Command: command,
	}
}

func nonEmpty(t *testing.T, s string) string {
	if s == "" {
		t.Fatal("must not empty")
	}
	return s
}
