package main

import (
	"fmt"
	"testing"

	"github.com/ktock/buildg/pkg/testutil"
)

func TestExec(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo -n a > /a`, testutil.Mirror("busybox:1.32.0"))
	fmt.Printf(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx, "--image="+testutil.Mirror("ubuntu:20.04"))
	defer sh.Close()
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("--image cat /etc/os-release")).OutContains(`NAME="Ubuntu"`)
	sh.Do(execNoTTY("--image cat /debugroot/a")).OutEqual("a")
	sh.Do(execNoTTY("--image --mountroot=/testdebugroot/rootdir/ cat /testdebugroot/rootdir/a")).OutEqual("a")
	sh.Do(execNoTTY("--init-state cat /a")).OutContains("process execution failed")
	sh.Do("c")

	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestExecQuotes(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo foo`, testutil.Mirror("busybox:1.32.0"))
	fmt.Printf(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close()
	sh.Do(execNoTTY(`echo -n "hello world"`)).OutEqual("hello world")
	sh.Do(execNoTTY(`sh -c "echo -n \"hello world\""`)).OutEqual("hello world")
	sh.Do(execNoTTY(`sh -c 'echo -n "hello world"'`)).OutEqual("hello world")
	sh.Do(execNoTTY(`sh -c "echo -n 'hello world'"`)).OutEqual("hello world")
	sh.Do("c")

	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
}

func execNoTTY(args string) string {
	return "exec -i=false -t=false " + args
}
