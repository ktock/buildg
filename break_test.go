package main

import (
	"fmt"
	"testing"

	"github.com/ktock/buildg/pkg/testutil"
)

func TestBreakpoint(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo -n a > /a
RUN echo -n b > /b
RUN echo -n c > /c
RUN echo -n d > /d`, testutil.Mirror("busybox:1.32.0"))
	fmt.Printf(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close()
	sh.Do("b 3")     // breakpoint 0
	sh.Do("break 4") // breakpoint 1
	sh.Do("breakpoints").OutContains("line: Dockerfile:3").OutContains("line: Dockerfile:4")
	sh.Do("c").OutContains("reached line: Dockerfile:3")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutEqual("b")
	sh.Do(execNoTTY("cat /c")).OutContains("process execution failed")
	sh.Do(execNoTTY("cat /d")).OutContains("process execution failed")

	sh.Do("clear 1")
	sh.Do("b 5")
	sh.Do("breakpoints").OutContains("line: Dockerfile:3").OutNotContains("line: Dockerfile:4").OutContains("line: Dockerfile:5")
	sh.Do("c").OutContains("reached line: Dockerfile:5")
	sh.Do(execNoTTY("cat /d")).OutEqual("d")

	sh.Do("clearall")
	sh.Do("breakpoints").OutEqual("")
	sh.Do("c")

	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestNext(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo -n a > /a
RUN echo -n b > /b`, testutil.Mirror("busybox:1.32.0"))
	fmt.Printf(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close()
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutContains("process execution failed")
	sh.Do("n")

	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutEqual("b")
	sh.Do("n")

	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestOnFail(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo -n a > /a
RUN cat /dummy`, testutil.Mirror("busybox:1.32.0"))
	fmt.Printf(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close()
	sh.Do("breakpoints").OutContains("on-fail")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do("c").OutContains("Breakpoint[on-fail]")

	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /dummy")).OutContains("process execution failed")
	sh.Do("c")

	if err := sh.Wait(); err == nil {
		t.Fatal(fmt.Errorf("must fail"))
	}
}

func TestExit(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo -n a > /a
RUN echo -n b > /b`, testutil.Mirror("busybox:1.32.0"))
	fmt.Printf(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close()
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutContains("process execution failed")
	sh.Do("exit")

	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
}
