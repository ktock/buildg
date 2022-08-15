package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/ktock/buildg/pkg/testutil"
	"golang.org/x/crypto/ssh/agent"
)

func TestExec(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo -n a > /a`, testutil.Mirror("busybox:1.32.0"))
	fmt.Println(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx, testutil.WithOptions("--image="+testutil.Mirror("ubuntu:22.04")))
	defer sh.Close(t)
	sh.Do("next")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("--image cat /etc/os-release")).OutContains(`NAME="Ubuntu"`)
	sh.Do(execNoTTY("--image cat /debugroot/a")).OutEqual("a")
	sh.Do(execNoTTY("--image --mountroot=/testdebugroot/rootdir/ cat /testdebugroot/rootdir/a")).OutEqual("a")
	sh.Do(execNoTTY("--init-state cat /a")).OutContains("process execution failed")
	sh.Do(execNoTTY("-e MSG=hello -e MSG2=world /bin/sh -c \"echo -n $MSG $MSG2\"")).OutEqual("hello world")
	sh.Do(execNoTTY("--workdir /tmp /bin/sh -c \"echo -n $(pwd)\"")).OutEqual("/tmp")
	sh.Do("c")

	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestExecNonRun(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s AS dev
RUN echo -n hi > /a

FROM %s
COPY --from=dev /a /b
RUN cat /b
`, testutil.Mirror("busybox:1.32.0"), testutil.Mirror("busybox:1.32.0"))
	fmt.Println(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx, testutil.WithOptions("--image="+testutil.Mirror("ubuntu:22.04")))
	defer sh.Close(t)
	sh.Do(execNoTTY("cat /a")).OutContains("process execution failed")
	sh.Do("next")
	sh.Do(execNoTTY("cat /a")).OutEqual("hi")
	sh.Do("next")
	sh.Do(execNoTTY("cat /a")).OutContains("process execution failed")
	sh.Do(execNoTTY("cat /b")).OutEqual("hi")
	sh.Do(execNoTTY("--image cat /etc/os-release")).OutContains(`NAME="Ubuntu"`)
	sh.Do(execNoTTY("--image cat /debugroot/b")).OutEqual("hi")
	sh.Do(execNoTTY("--image --mountroot=/testdebugroot/rootdir/ cat /testdebugroot/rootdir/b")).OutEqual("hi")
	sh.Do(execNoTTY("--init-state cat /a")).OutContains("one rootfs mount must be specified")
	sh.Do(execNoTTY("-e MSG=hello -e MSG2=world /bin/sh -c \"echo -n $MSG $MSG2\"")).OutEqual("hello world")
	sh.Do(execNoTTY("--workdir /tmp /bin/sh -c \"echo -n $(pwd)\"")).OutEqual("/tmp")
	sh.Do("c")
	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestExecQuotes(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo foo`, testutil.Mirror("busybox:1.32.0"))
	fmt.Println(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close(t)
	sh.Do("next")
	sh.Do(execNoTTY(`echo -n "hello world"`)).OutEqual("hello world")
	sh.Do(execNoTTY(`sh -c "echo -n \"hello world\""`)).OutEqual("hello world")
	sh.Do(execNoTTY(`sh -c 'echo -n "hello world"'`)).OutEqual("hello world")
	sh.Do(execNoTTY(`sh -c "echo -n 'hello world'"`)).OutEqual("hello world")
	sh.Do("c")

	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestExecSecrets(t *testing.T) {
	t.Parallel()

	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, fmt.Sprintf(`FROM %s
RUN --mount=type=secret,id=testsecret,target=/root/secret [ "$(cat /root/secret)" = 'test-secret' ]`,
		testutil.Mirror("busybox:1.32.0")))
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

	// test secret from file
	sh := testutil.NewDebugShell(t, tmpCtx, testutil.WithOptions("--secret=id=testsecret,src="+tmpSec.Name()))
	defer sh.Close(t)
	sh.Do("next")
	sh.Do(execNoTTY("cat /root/secret")).OutEqual("test-secret")
	sh.Do("c")
	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}

	// test secret from env
	sh2 := testutil.NewDebugShell(t, tmpCtx,
		testutil.WithOptions("--secret=id=testsecret,env=TEST_SECRET"),
		testutil.WithEnv("TEST_SECRET=test-secret"),
	)
	defer sh2.Close(t)
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /root/secret")).OutEqual("test-secret")
	sh2.Do("c")
	if err := sh2.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestExecSSH(t *testing.T) {
	t.Parallel()

	// test ssh from socket
	tests := []struct {
		name          string
		buildgOptions func(sockPath string) []testutil.DebugShellOption
		mountOption   string
	}{
		{
			name: "default",
			buildgOptions: func(sockPath string) []testutil.DebugShellOption {
				return []testutil.DebugShellOption{testutil.WithOptions("--ssh=default=" + sockPath)}
			},
			mountOption: "type=ssh",
		},
		{
			name: "default-env",
			buildgOptions: func(sockPath string) []testutil.DebugShellOption {
				return []testutil.DebugShellOption{
					testutil.WithEnv("SSH_AUTH_SOCK=" + sockPath),
					testutil.WithOptions("--ssh=default"),
				}
			},
			mountOption: "type=ssh",
		},
		{
			name: "id",
			buildgOptions: func(sockPath string) []testutil.DebugShellOption {
				return []testutil.DebugShellOption{testutil.WithOptions("--ssh=mysecret=" + sockPath)}
			},
			mountOption: "type=ssh,id=mysecret",
		},
		{
			name: "id-env",
			buildgOptions: func(sockPath string) []testutil.DebugShellOption {
				return []testutil.DebugShellOption{
					testutil.WithOptions("--ssh=mysecret"),
					testutil.WithEnv("SSH_AUTH_SOCK=" + sockPath),
				}
			},
			mountOption: "type=ssh,id=mysecret",
		},
		{
			name: "id-env2",
			buildgOptions: func(sockPath string) []testutil.DebugShellOption {
				return []testutil.DebugShellOption{
					testutil.WithEnv("SSH_AUTH_SOCK=" + sockPath),
					testutil.WithOptions("--ssh=mysecret"),
					testutil.WithOptions("--ssh=mysecret2"),
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
			sh := testutil.NewDebugShell(t, tmpCtx, tt.buildgOptions(sockPath)...)
			defer sh.Close(t)
			sh.Do("b 3")
			sh.Do("c").OutContains("reached line: Dockerfile:3")
			sh.Do(execNoTTY(`ssh-add -l | grep 2048 | grep RSA`)).OutContains("2048").OutContains("(RSA)")
			sh.Do(execNoTTY(`/bin/sh -c "ssh-keygen -f /tmp/key -N '' && ssh-add -k /tmp/key 2>&1"`)).OutContains("agent refused operation")
			sh.Do("c")
			if err := sh.Wait(); err != nil {
				t.Fatal(err)
			}
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
	sh2 := testutil.NewDebugShell(t, tmpCtx, testutil.WithOptions("--ssh=testsecret="+tmpSec.Name()))
	defer sh2.Close(t)
	sh2.Do("b 3")
	sh2.Do("c")
	sh2.Do(execNoTTY(`ssh-add -l | grep 2048 | grep RSA`)).OutContains("2048").OutContains("(RSA)")
	sh2.Do("c")
	if err := sh2.Wait(); err != nil {
		t.Fatal(err)
	}
}

func execNoTTY(args string) string {
	return "exec -i=false -t=false " + args
}
