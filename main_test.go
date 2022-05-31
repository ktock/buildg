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
RUN date > /a
RUN date > /b
RUN date > /
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
	a := nonEmpty(t, sh.Do(execNoTTY("cat /a")).Out())
	sh.Do("next")
	b := nonEmpty(t, sh.Do(execNoTTY("cat /b")).Out())
	sh.Do("next")
	a2 := nonEmpty(t, sh.Do(execNoTTY("cat /a")).Out())
	b2 := nonEmpty(t, sh.Do(execNoTTY("cat /b")).Out())
	sh.Do("next")
	if err := sh.Wait(); err == nil {
		t.Fatal(fmt.Errorf("must fail"))
	}

	sh2 := testutil.NewDebugShell(t, tmpCtx,
		testutil.WithGlobalOptions("--root="+tmpRoot),
		testutil.WithOptions("--cache-reuse"))
	defer sh2.Close()
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /a")).OutEqual(a)
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /b")).OutEqual(b)
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /a")).OutEqual(a2)
	sh2.Do(execNoTTY("cat /b")).OutEqual(b2)
	sh2.Do("next")
	if err := sh2.Wait(); err == nil {
		t.Fatal(fmt.Errorf("must fail"))
	}

	tmpOKCtx, doneTmpOKCtx := testutil.NewTempContext(t, fmt.Sprintf(`FROM %s
RUN date > /a
RUN date > /b
RUN date > /ok
`, testutil.Mirror("busybox:1.32.0")))
	defer doneTmpOKCtx()
	shOK := testutil.NewDebugShell(t, tmpOKCtx,
		testutil.WithGlobalOptions("--root="+tmpRoot),
		testutil.WithOptions("--cache-reuse"))
	defer shOK.Close()
	shOK.Do("next")
	shOK.Do(execNoTTY("cat /a")).OutEqual(a)
	shOK.Do("next")
	shOK.Do(execNoTTY("cat /b")).OutEqual(b)
	shOK.Do("next")
	shOK.Do(execNoTTY("cat /a")).OutEqual(a2)
	shOK.Do(execNoTTY("cat /b")).OutEqual(b2)
	nonEmpty(t, shOK.Do(execNoTTY("cat /ok")).Out())
	shOK.Do("next")
	if err := shOK.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestCacheMultiReuse(t *testing.T) {
	t.Parallel()
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, fmt.Sprintf(`FROM %s AS base
RUN date > /a
RUN date > /b

FROM %s
COPY --from=base /a /a2
COPY --from=base /b /b2
RUN date > /ok
`, testutil.Mirror("busybox:1.32.0"), testutil.Mirror("busybox:1.32.0")))
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
	a := nonEmpty(t, sh.Do(execNoTTY("cat /a")).Out())
	sh.Do("next")
	b := nonEmpty(t, sh.Do(execNoTTY("cat /b")).Out())
	sh.Do("next")
	sh.Do("next")
	sh.Do("next")
	sh.Do(execNoTTY("cat /a2")).OutEqual(a)
	sh.Do(execNoTTY("cat /b2")).OutEqual(b)
	nonEmpty(t, sh.Do(execNoTTY("cat /ok")).Out())
	sh.Do("next")
	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}

	sh2 := testutil.NewDebugShell(t, tmpCtx,
		testutil.WithGlobalOptions("--root="+tmpRoot),
		testutil.WithOptions("--cache-reuse"))
	defer sh2.Close()
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /a")).OutEqual(a)
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /b")).OutEqual(b)
	sh2.Do("next")
	sh2.Do("next")
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /a2")).OutEqual(a)
	sh2.Do(execNoTTY("cat /b2")).OutEqual(b)
	nonEmpty(t, sh2.Do(execNoTTY("cat /ok")).Out())
	sh2.Do("next")
	if err := sh2.Wait(); err != nil {
		t.Fatal(err)
	}
}

func nonEmpty(t *testing.T, s string) string {
	if s == "" {
		t.Fatal("must not empty")
	}
	return s
}
