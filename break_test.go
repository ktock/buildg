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
	fmt.Println(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close(t)
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
	sh.Do("breakpoints").OutNotContains("line: Dockerfile:3").OutNotContains("line: Dockerfile:4").OutNotContains("line: Dockerfile:5")
	sh.Do("c")

	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestBreakpointContinue(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo -n a > /a
RUN echo -n b > /b
RUN echo -n c > /c
RUN echo -n d > /d
RUN echo -n e > /e`, testutil.Mirror("busybox:1.32.0"))
	fmt.Println(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close(t)
	sh.Do("b 2")     // breakpoint 0
	sh.Do("b 3")     // breakpoint 1
	sh.Do("break 5") // breakpoint 2
	sh.Do("breakpoints").OutContains("line: Dockerfile:2").OutContains("line: Dockerfile:3").OutContains("line: Dockerfile:5")
	sh.Do("c 1").OutContains("reached line: Dockerfile:3")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutEqual("b")
	sh.Do(execNoTTY("cat /c")).OutContains("process execution failed")
	sh.Do(execNoTTY("cat /d")).OutContains("process execution failed")
	sh.Do(execNoTTY("cat /e")).OutContains("process execution failed")
	sh.Do("c")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutEqual("b")
	sh.Do(execNoTTY("cat /c")).OutEqual("c")
	sh.Do(execNoTTY("cat /d")).OutEqual("d")
	sh.Do(execNoTTY("cat /e")).OutContains("process execution failed")
	sh.Do("n")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutEqual("b")
	sh.Do(execNoTTY("cat /c")).OutEqual("c")
	sh.Do(execNoTTY("cat /d")).OutEqual("d")
	sh.Do(execNoTTY("cat /e")).OutEqual("e")
	sh.Do("c")
	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestBreakpointNonExec(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s AS base
RUN echo -n a > /a

FROM %s
RUN echo -n b > /b
COPY --from=base /a /
RUN echo -n c > /c`, testutil.Mirror("busybox:1.32.0"), testutil.Mirror("alpine:3.15.3"))
	fmt.Println(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close(t)
	sh.Do("break 6")
	sh.Do("breakpoints").OutContains("line: Dockerfile:6")

	sh.Do("c").OutContains("reached line: Dockerfile:6")
	sh.Do("next")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutEqual("b")
	sh.Do(execNoTTY("cat /c")).OutEqual("c")

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
	fmt.Println(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close(t)
	sh.Do("n")

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
	fmt.Println(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close(t)
	sh.Do("breakpoints").OutContains("on-fail")

	sh.Do("next")
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
	fmt.Println(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close(t)
	sh.Do("n")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutContains("process execution failed")
	sh.Do("exit")

	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
}
