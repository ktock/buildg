package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/google/shlex"
	"github.com/ktock/buildg/pkg/buildkit"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	promptEnvKey  = "BUILDG_PS1"
	defaultPrompt = "(buildg) "
)

type handlerContext struct {
	handler          *buildkit.Handler
	stdin            *sharedReader
	stdout           io.Writer // TODO: use cli.Context.App.Writer
	signalHandler    *signalHandler
	info             *buildkit.RegisteredStatus
	locs             []*buildkit.Location
	continueRead     bool
	progress         *progressWriter
	targetBreakpoint string
	err              error
}

type handlerCommandFn func(ctx context.Context, hCtx *handlerContext) cli.Command

var handlerCommands = []handlerCommandFn{
	breakCommand,
	breakpointsCommand,
	clearCommand,
	clearAllCommand,
	nextCommand,
	continueCommand,
	execCommand,
	listCommand,
	logCommand,
	reloadCommand,
	exitCommand,
}

type commandHandler struct {
	stdin            *sharedReader
	stdout           io.Writer
	prompt           string
	signalHandler    *signalHandler
	targetBreakpoint string
}

func newCommandHandler(stdin *sharedReader, stdout io.Writer, sig *signalHandler) *commandHandler {
	prompt := defaultPrompt
	if p := os.Getenv(promptEnvKey); p != "" {
		prompt = p
	}
	return &commandHandler{
		stdin:         stdin,
		stdout:        stdout,
		prompt:        prompt,
		signalHandler: sig,
	}
}

func (h *commandHandler) breakHandler(ctx context.Context, bCtx buildkit.BreakContext, progress *progressWriter) error {
	for key, bpInfo := range bCtx.Hits {
		fmt.Fprintf(h.stdout, "Breakpoint[%s]: %s\n", key, bpInfo.Description)
	}
	if target := h.targetBreakpoint; target != "" {
		var allKeys []string
		hit := false
		for k := range bCtx.Hits {
			allKeys = append(allKeys, k)
			if k == target {
				hit = true
			}
		}
		if !hit {
			logrus.Infof("ignoring non-target breakpoint %v", allKeys)
			return nil
		}
	}
	h.targetBreakpoint = "" // hit the breakpoint so reset it.
	printLines(bCtx.Handler, h.stdout, bCtx.Locs, defaultListRange, defaultListRange, false)
	for {
		ln, err := h.readLine(ctx)
		if err != nil {
			return err
		}
		if args, err := shlex.Split(ln); err != nil {
			logrus.WithError(err).Warnf("failed to parse line")
		} else if len(args) > 0 {
			cont, err := h.dispatch(ctx, bCtx, args, progress)
			if err != nil {
				return err
			}
			if !cont {
				break
			}
		}
	}
	return nil
}

func (h *commandHandler) readLine(ctx context.Context) (string, error) {
	fmt.Fprintf(h.stdout, h.prompt)
	r, done := h.stdin.use()
	defer done()
	lnCh := make(chan string)
	errCh := make(chan error)
	scanner := bufio.NewScanner(r)
	go func() {
		if scanner.Scan() {
			lnCh <- scanner.Text()
		} else if err := scanner.Err(); err != nil {
			errCh <- err
		} else {
			// EOF thus exit
			errCh <- buildkit.ErrExit
		}
	}()
	var ln string
	select {
	case ln = <-lnCh:
	case err := <-errCh:
		return "", err
	case <-ctx.Done():
		return "", fmt.Errorf("canceled reading line: %w", ctx.Err())
	}
	return ln, scanner.Err()
}

func (h *commandHandler) dispatch(ctx context.Context, bCtx buildkit.BreakContext, args []string, progress *progressWriter) (continueRead bool, err error) {
	if len(args) == 0 || args[0] == "" {
		return true, nil // nop
	}
	continueRead = true
	app := cli.NewApp()
	rootCmd := "buildg"
	app.Name = rootCmd
	app.HelpName = rootCmd
	app.Usage = "Interactive debugger for Dockerfile"
	app.UsageText = "command [command options] [arguments...]"
	app.ExitErrHandler = func(context *cli.Context, err error) {}
	app.UseShortOptionHandling = true
	hCtx := &handlerContext{
		handler:       bCtx.Handler,
		stdin:         h.stdin,
		stdout:        h.stdout,
		signalHandler: h.signalHandler,
		info:          bCtx.Info,
		locs:          bCtx.Locs,
		continueRead:  true,
		progress:      progress,
		err:           nil,
	}
	for _, fn := range handlerCommands {
		app.Commands = append(app.Commands, fn(ctx, hCtx))
	}
	if cliErr := app.Run(append([]string{rootCmd}, args...)); cliErr != nil {
		fmt.Fprintf(h.stdout, "%v\n", cliErr)
	}
	if hCtx.targetBreakpoint != "" {
		// continue target specified. All breaktpoins will be ignored until the build
		// reaches to this breakpoint.
		h.targetBreakpoint = hCtx.targetBreakpoint
	}
	return hCtx.continueRead, hCtx.err
}

type sharedReader struct {
	r          io.Reader
	currentW   *io.PipeWriter
	currentWMu sync.Mutex
	useMu      sync.Mutex

	closeOnce sync.Once
	done      chan struct{}
}

func newSharedReader(r io.Reader) *sharedReader {
	s := &sharedReader{
		r:    r,
		done: make(chan struct{}),
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			select {
			case <-s.done:
				return
			default:
			}
			n, err := s.r.Read(buf)
			if err != nil {
				if err != io.EOF {
					logrus.WithError(err).Warnf("sharedReader: failed to read stdin data")
					return
				}
				s.currentWMu.Lock()
				if w := s.currentW; w != nil {
					w.Close()
					s.currentW = nil
				}
				s.currentWMu.Unlock()
				continue
			}
			dest := io.Discard
			s.currentWMu.Lock()
			w := s.currentW
			s.currentWMu.Unlock()
			if w != nil {
				dest = w
			}
			if _, err := dest.Write(buf[:n]); err != nil {
				logrus.WithError(err).Warnf("sharedReader: failed to write stdin data")
			}
		}
	}()
	return s
}

func (s *sharedReader) use() (r io.Reader, done func()) {
	s.useMu.Lock()
	pr, pw := io.Pipe()
	s.currentWMu.Lock()
	if s.currentW != nil {
		panic("non nil writer")
	}
	s.currentW = pw
	s.currentWMu.Unlock()
	return pr, func() {
		s.currentWMu.Lock()
		if s.currentW != nil {
			s.currentW = nil
			pw.Close()
			pr.Close()
		}
		s.currentWMu.Unlock()
		s.useMu.Unlock()
	}
}

func (s *sharedReader) close() {
	s.closeOnce.Do(func() {
		close(s.done)
	})
}
