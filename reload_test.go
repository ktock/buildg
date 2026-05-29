package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ktock/buildg/pkg/testutil"
)

func TestReload(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo -n a > /a
RUN echo -n b > /b
RUN echo -n c > /c`, testutil.Mirror("busybox:1.32.0"))
	fmt.Println(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close(t)
	sh.Do("n")
	sh.Do("n")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutEqual("b")
	sh.Do(execNoTTY("cat /c")).OutContains("process execution failed")

	sh.Do("reload")
	sh.Do(execNoTTY("cat /a")).OutContains("process execution failed")
	sh.Do(execNoTTY("cat /b")).OutContains("process execution failed")
	sh.Do(execNoTTY("cat /c")).OutContains("process execution failed")
	sh.Do("b 3") // breakpoint 0
	sh.Do("c")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutEqual("b")
	sh.Do(execNoTTY("cat /c")).OutContains("process execution failed")

	sh.Do("reload")
	sh.Do("breakpoints").OutContains("line: Dockerfile:3")
	sh.Do("c")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutEqual("b")
	sh.Do(execNoTTY("cat /c")).OutContains("process execution failed")

	dt = fmt.Sprintf(`FROM %s
RUN echo -n a2 > /a2
RUN echo -n b2 > /b2
RUN echo -n c2 > /c2`, testutil.Mirror("busybox:1.32.0"))
	if err := os.WriteFile(filepath.Join(tmpCtx, "Dockerfile"), []byte(dt), 0600); err != nil {
		t.Fatal(err)
		return
	}
	sh.Do("reload")
	sh.Do("breakpoints").OutContains("line: Dockerfile:3")
	sh.Do("c")
	sh.Do(execNoTTY("cat /a2")).OutEqual("a2")
	sh.Do(execNoTTY("cat /b2")).OutEqual("b2")
	sh.Do("c")

	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
}
