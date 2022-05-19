package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/ktock/buildg/pkg/testutil"
)

const buildgTestTmpDirEnv = "TEST_BUILDG_TMP_DIR"

func TestCacheReuse(t *testing.T) {
	t.Parallel()
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, fmt.Sprintf(`FROM %s
RUN echo -n a > /a
RUN echo -n b > /b
RUN echo fail > /
`, testutil.Mirror("busybox:1.32.0")))
	defer doneTmpCtx()

	tmpRoot, err := os.MkdirTemp(os.Getenv(buildgTestTmpDirEnv), "buildg-test-tmproot")
	if err != nil {
		t.Fatal(err)
		return
	}
	defer os.RemoveAll(tmpRoot)

	sh := testutil.NewDebugShell(t, tmpCtx,
		testutil.WithGlobalOptions("--root="+tmpRoot),
		testutil.WithOptions("--cache-reuse"))
	defer sh.Close()
	sh.Do("next")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do("next")
	sh.Do(execNoTTY("cat /b")).OutEqual("b")
	sh.Do("next")
	sh.Do(execNoTTY("cat /a")).OutEqual("a")
	sh.Do(execNoTTY("cat /b")).OutEqual("b")
	sh.Do("next")
	if err := sh.Wait(); err == nil {
		t.Fatal(fmt.Errorf("must fail"))
	}

	sh2 := testutil.NewDebugShell(t, tmpCtx,
		testutil.WithGlobalOptions("--root="+tmpRoot),
		testutil.WithOptions("--cache-reuse"))
	defer sh2.Close()
	sh2.Do(execNoTTY("cat /a")).OutEqual("a")
	sh2.Do(execNoTTY("cat /b")).OutEqual("b")
	sh2.Do("next")
	if err := sh2.Wait(); err == nil {
		t.Fatal(fmt.Errorf("must fail"))
	}

	tmpOKCtx, doneTmpOKCtx := testutil.NewTempContext(t, fmt.Sprintf(`FROM %s
RUN echo -n a > /a
RUN echo -n b > /b
RUN echo -n ok > /ok
`, testutil.Mirror("busybox:1.32.0")))
	defer doneTmpOKCtx()
	shOK := testutil.NewDebugShell(t, tmpOKCtx,
		testutil.WithGlobalOptions("--root="+tmpRoot),
		testutil.WithOptions("--cache-reuse"))
	defer shOK.Close()
	shOK.Do(execNoTTY("cat /a")).OutEqual("a")
	shOK.Do(execNoTTY("cat /b")).OutEqual("b")
	shOK.Do(execNoTTY("cat /ok")).OutEqual("ok")
	shOK.Do("next")
	if err := shOK.Wait(); err != nil {
		t.Fatal(err)
	}
}
