package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ktock/buildg/pkg/testutil"
)

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

	sh := testutil.NewDebugShell(t, tmpCtx, testutil.WithRootDir(tmpRoot))
	defer sh.Close(t)
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

	sh2 := testutil.NewDebugShell(t, tmpCtx, testutil.WithRootDir(tmpRoot))
	defer sh2.Close(t)
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
	shOK := testutil.NewDebugShell(t, tmpOKCtx, testutil.WithRootDir(tmpRoot))
	defer shOK.Close(t)
	shOK.Do("next")
	shOK.Do(execNoTTY("cat /a")).OutEqual(a)
	shOK.Do("next")
	shOK.Do(execNoTTY("cat /b")).OutEqual(b)
	shOK.Do("next")
	shOK.Do(execNoTTY("cat /a")).OutEqual(a2)
	shOK.Do(execNoTTY("cat /b")).OutEqual(b2)
	okOut := nonEmpty(t, shOK.Do(execNoTTY("cat /ok")).Out())
	shOK.Do("next")
	if err := shOK.Wait(); err != nil {
		t.Fatal(err)
	}

	if _, err := testutil.BuildgCmd(t, []string{"prune"}, testutil.WithGlobalOptions("--root="+tmpRoot)).Output(); err != nil {
		t.Fatal(err)
	}
	duOut, err := testutil.BuildgCmd(t, []string{"du"}, testutil.WithGlobalOptions("--root="+tmpRoot)).Output()
	if err != nil {
		t.Fatal(err)
	}
	zeroOut := "Total:\t0 B"
	if !strings.Contains(string(duOut), zeroOut) {
		t.Fatalf("du must contain %q; got %q", zeroOut, string(duOut))
	}
	shPrune := testutil.NewDebugShell(t, tmpOKCtx, testutil.WithRootDir(tmpRoot))
	defer shPrune.Close(t)
	shPrune.Do("next")
	shPrune.Do(execNoTTY("cat /a")).OutNotEqual(a)
	shPrune.Do("next")
	shPrune.Do(execNoTTY("cat /b")).OutNotEqual(b)
	shPrune.Do("next")
	shPrune.Do(execNoTTY("cat /a")).OutNotEqual(a2)
	shPrune.Do(execNoTTY("cat /b")).OutNotEqual(b2)
	shPrune.Do(execNoTTY("cat /ok")).OutNotEqual(okOut)
	shPrune.Do("next")
	if err := shPrune.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestNoCacheReuse(t *testing.T) {
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

	sh := testutil.NewDebugShell(t, tmpCtx,
		testutil.WithRootDir(tmpRoot),
		testutil.WithOptions("--cache-reuse=false"))
	defer sh.Close(t)
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
		testutil.WithRootDir(tmpRoot),
		testutil.WithOptions("--cache-reuse=false"))
	defer sh2.Close(t)
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /a")).OutNotEqual(a)
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /b")).OutNotEqual(b)
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /a")).OutNotEqual(a2)
	sh2.Do(execNoTTY("cat /b")).OutNotEqual(b2)
	sh2.Do("next")
	if err := sh2.Wait(); err == nil {
		t.Fatal(fmt.Errorf("must fail"))
	}
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

	sh := testutil.NewDebugShell(t, tmpCtx, testutil.WithRootDir(tmpRoot))
	defer sh.Close(t)
	sh.Do("next")
	a := nonEmpty(t, sh.Do(execNoTTY("cat /a")).Out())
	sh.Do("next")
	b := nonEmpty(t, sh.Do(execNoTTY("cat /b")).Out())
	sh.Do("next")
	b2 := nonEmpty(t, sh.Do(execNoTTY("cat /b")).Out())
	sh.Do("next")
	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("wanted %q; got %q", a, b)
	}
	if b != b2 {
		t.Fatalf("wanted %q; got %q", b, b2)
	}

	sh2 := testutil.NewDebugShell(t, tmpCtx, testutil.WithRootDir(tmpRoot))
	defer sh2.Close(t)
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /a")).OutEqual(a)
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /b")).OutEqual(b)
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /b")).OutEqual(b2)
	sh2.Do("next")
	if err := sh2.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestCacheReuseDebugImage(t *testing.T) {
	t.Parallel()
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, fmt.Sprintf(`FROM %s
RUN date > /a
`, testutil.Mirror("busybox:1.32.0")))
	defer doneTmpCtx()

	tmpRoot, err := os.MkdirTemp(os.Getenv(testutil.BuildgTestTmpDirEnv), "buildg-test-tmproot")
	if err != nil {
		t.Fatal(err)
		return
	}
	defer os.RemoveAll(tmpRoot)

	sh := testutil.NewDebugShell(t, tmpCtx,
		testutil.WithRootDir(tmpRoot),
		testutil.WithOptions("--image="+testutil.Mirror("ubuntu:22.04")))
	defer sh.Close(t)
	sh.Do("next")
	a := nonEmpty(t, sh.Do(execNoTTY("cat /a")).Out())
	a2 := nonEmpty(t, sh.Do(execNoTTY("--image cat /debugroot/a")).Out())
	sh.Do("c")
	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}

	sh2 := testutil.NewDebugShell(t, tmpCtx,
		testutil.WithRootDir(tmpRoot),
		testutil.WithOptions("--image="+testutil.Mirror("ubuntu:22.04")))
	defer sh2.Close(t)
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /a")).OutEqual(a)
	sh2.Do(execNoTTY("--image cat /debugroot/a")).OutEqual(a2)
	sh2.Do("c")
	if err := sh2.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestCacheReuseBindMount(t *testing.T) {
	t.Parallel()

	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, fmt.Sprintf(`FROM %s
RUN --mount=type=bind,target=/root/mnt cat /root/mnt/data/hi > /a && date > /b
`, testutil.Mirror("busybox:1.32.0")))
	defer doneTmpCtx()
	if err := os.Mkdir(filepath.Join(tmpCtx, "data"), 0755); err != nil {
		t.Fatal(err)
		return
	}
	tmpData, err := os.Create(filepath.Join(tmpCtx, "data", "hi"))
	if err != nil {
		t.Fatal(err)
	}
	sampleStr := time.Now().String()
	if _, err := tmpData.Write([]byte(sampleStr)); err != nil {
		t.Fatal(err)
	}
	if err := tmpData.Close(); err != nil {
		t.Fatal(err)
	}

	tmpRoot, err := os.MkdirTemp(os.Getenv(testutil.BuildgTestTmpDirEnv), "buildg-test-tmproot")
	if err != nil {
		t.Fatal(err)
		return
	}
	defer os.RemoveAll(tmpRoot)

	sh := testutil.NewDebugShell(t, tmpCtx, testutil.WithRootDir(tmpRoot))
	defer sh.Close(t)
	sh.Do("next")
	sh.Do(execNoTTY("cat /a")).OutEqual(sampleStr)
	b := nonEmpty(t, sh.Do(execNoTTY("cat /b")).Out())
	sh.Do("c")
	if err := sh.Wait(); err != nil {
		t.Fatal(err)
	}

	sh2 := testutil.NewDebugShell(t, tmpCtx, testutil.WithRootDir(tmpRoot))
	defer sh2.Close(t)
	sh2.Do("next")
	sh2.Do(execNoTTY("cat /a")).OutEqual(sampleStr)
	sh2.Do(execNoTTY("cat /b")).OutEqual(b)
	sh2.Do("c")
	if err := sh2.Wait(); err != nil {
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

	tmpRoot, err := os.MkdirTemp(os.Getenv(testutil.BuildgTestTmpDirEnv), "buildg-test-tmproot")
	if err != nil {
		t.Fatal(err)
		return
	}
	defer os.RemoveAll(tmpRoot)

	sh := testutil.NewDebugShell(t, tmpCtx, testutil.WithRootDir(tmpRoot))
	defer sh.Close(t)
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

	sh2 := testutil.NewDebugShell(t, tmpCtx, testutil.WithRootDir(tmpRoot))
	defer sh2.Close(t)
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
