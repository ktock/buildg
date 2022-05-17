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
	fmt.Printf(dt)
	tmpCtx, doneTmpCtx := testutil.NewTempContext(t, dt)
	defer doneTmpCtx()

	sh := testutil.NewDebugShell(t, tmpCtx)
	defer sh.Close()
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
