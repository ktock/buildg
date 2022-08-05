package main

import (
	"fmt"
	"testing"

	"github.com/ktock/buildg/pkg/testutil"
)

func TestList(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo a > /a
RUN echo b > /b
RUN echo c > /c`, testutil.Mirror("busybox:1.32.0"))
	fmt.Println(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close(t)
	sh.Do("ls").OutEqual(fmt.Sprintf(`Filename: "Dockerfile"
 =>   1| FROM %s
      2| RUN echo a > /a
      3| RUN echo b > /b
      4| RUN echo c > /c
`, testutil.Mirror("busybox:1.32.0")))
	sh.Do("next")
	sh.Do("ls").OutEqual(fmt.Sprintf(`Filename: "Dockerfile"
      1| FROM %s
 =>   2| RUN echo a > /a
      3| RUN echo b > /b
      4| RUN echo c > /c
`, testutil.Mirror("busybox:1.32.0")))
	sh.Do("break 3")
	sh.Do("ls").OutEqual(fmt.Sprintf(`Filename: "Dockerfile"
      1| FROM %s
 =>   2| RUN echo a > /a
*     3| RUN echo b > /b
      4| RUN echo c > /c
`, testutil.Mirror("busybox:1.32.0")))
	sh.Do("break 4")
	sh.Do("ls").OutEqual(fmt.Sprintf(`Filename: "Dockerfile"
      1| FROM %s
 =>   2| RUN echo a > /a
*     3| RUN echo b > /b
*     4| RUN echo c > /c
`, testutil.Mirror("busybox:1.32.0")))
	sh.Do("clearall")
	sh.Do("ls").OutEqual(fmt.Sprintf(`Filename: "Dockerfile"
      1| FROM %s
 =>   2| RUN echo a > /a
      3| RUN echo b > /b
      4| RUN echo c > /c
`, testutil.Mirror("busybox:1.32.0")))
}

func TestListRange(t *testing.T) {
	t.Parallel()
	dt := fmt.Sprintf(`FROM %s
RUN echo a
RUN echo b
RUN echo c
RUN echo d
RUN echo e
RUN echo f
RUN echo g
RUN echo h
RUN echo i
RUN echo j
RUN echo k
RUN echo l
`, testutil.Mirror("busybox:1.32.0"))
	fmt.Println(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close(t)
	sh.Do("ls").OutEqual(fmt.Sprintf(`Filename: "Dockerfile"
 =>   1| FROM %s
      2| RUN echo a
      3| RUN echo b
      4| RUN echo c
`, testutil.Mirror("busybox:1.32.0")))
	sh.Do("ls --all").OutEqual(fmt.Sprintf(`Filename: "Dockerfile"
 =>   1| FROM %s
      2| RUN echo a
      3| RUN echo b
      4| RUN echo c
      5| RUN echo d
      6| RUN echo e
      7| RUN echo f
      8| RUN echo g
      9| RUN echo h
     10| RUN echo i
     11| RUN echo j
     12| RUN echo k
     13| RUN echo l
`, testutil.Mirror("busybox:1.32.0")))
	sh.Do("b 7")
	sh.Do("c")
	sh.Do("ls").OutEqual(`Filename: "Dockerfile"
      4| RUN echo c
      5| RUN echo d
      6| RUN echo e
*=>   7| RUN echo f
      8| RUN echo g
      9| RUN echo h
     10| RUN echo i
`)
	sh.Do("ls -A 2 -B 4").OutEqual(`Filename: "Dockerfile"
      3| RUN echo b
      4| RUN echo c
      5| RUN echo d
      6| RUN echo e
*=>   7| RUN echo f
      8| RUN echo g
      9| RUN echo h
`)
	sh.Do("ls --range 2").OutEqual(`Filename: "Dockerfile"
      5| RUN echo d
      6| RUN echo e
*=>   7| RUN echo f
      8| RUN echo g
      9| RUN echo h
`)
}
