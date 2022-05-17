package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"

	"github.com/google/shlex"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	promptEnvKey  = "BUILDG_PS1"
	defaultPrompt = "(buildg) "
)

var errExit = errors.New("exit")

type handlerContext struct {
	handler      *handler
	info         *registeredStatus
	locs         []*location
	continueRead bool
	err          error
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
	exitCommand,
}

func exitCommand(ctx context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:    "exit",
		Aliases: []string{"quit", "q"},
		Usage:   "exit command",
		Action: func(clicontext *cli.Context) error {
			hCtx.continueRead = false
			hCtx.err = errExit
			return nil
		},
	}
}

func withDebug(f gwclient.BuildFunc, debugController *debugController, debugImage string) (gwclient.BuildFunc, <-chan error) {
	handleError := make(chan error)
	return func(ctx context.Context, c gwclient.Client) (res *gwclient.Result, err error) {
		stdinR := newSharedReader(os.Stdin)
		defer stdinR.close()
		handler := &handler{
			gwclient: c,
			stdin:    stdinR,
			breakpoints: &breakpoints{
				breakpoints: map[string]breakpoint{
					"on-fail": newOnFailBreakpoint(),
				},
			},
			prompt: defaultPrompt,
		}
		if p := os.Getenv(promptEnvKey); p != "" {
			handler.prompt = p
		}

		doneCh := make(chan struct{})
		defer close(doneCh)
		go func() {
			defer close(handleError)
			select {
			case handleError <- debugController.handle(context.Background(), handler):
			case <-doneCh:
			}
		}()

		if debugImage != "" {
			def, err := llb.Image(debugImage, withDescriptor(map[string]string{"debug": "no"})).Marshal(ctx)
			if err != nil {
				return nil, err
			}
			r, err := c.Solve(ctx, gwclient.SolveRequest{
				Definition: def.ToPB(),
			})
			if err != nil {
				return nil, err
			}
			handler.imageMu.Lock()
			handler.image = r.Ref
			handler.imageMu.Unlock()
		}

		return f(ctx, debugController.gatewayClientWithDebug(c))
	}, handleError
}

type withDescriptor map[string]string

func (o withDescriptor) SetImageOption(ii *llb.ImageInfo) {
	if ii.Metadata.Description == nil {
		ii.Metadata.Description = make(map[string]string)
	}
	for k, v := range o {
		ii.Metadata.Description[k] = v
	}
}

type handler struct {
	gwclient gwclient.Client
	stdin    *sharedReader

	breakpoints     *breakpoints
	breakEachVertex bool

	initialized bool

	mu sync.Mutex

	image   gwclient.Reference
	imageMu sync.Mutex

	prompt string
}

func (h *handler) handle(ctx context.Context, info *registeredStatus, locs []*location) error {
	globalProgressWriter.disable()
	defer globalProgressWriter.enable()
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(locs) == 0 {
		logrus.Warnf("no location info: %v", locs)
		return nil
	}
	isBreakpoint := false
	for key, bp := range h.breakpoints.check(ctx, breakpointContext{info, locs}) {
		fmt.Printf("Breakpoint[%s]: %s\n", key, bp.description)
		isBreakpoint = true
	}
	if h.initialized && !h.breakEachVertex && !isBreakpoint {
		logrus.Debugf("skipping non-breakpoint: %v", locs)
		return nil
	}
	if !h.initialized {
		logrus.Infof("debug session started. type \"help\" for command reference.")
		h.initialized = true
	}
	printLines(h, locs, 3, false)
	for {
		ln, err := h.readLine(ctx)
		if err != nil {
			return err
		}
		if args, err := shlex.Split(ln); err != nil {
			logrus.WithError(err).Warnf("failed to parse line")
		} else if len(args) > 0 {
			cont, err := h.dispatch(ctx, info, locs, args)
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

func (h *handler) readLine(ctx context.Context) (string, error) {
	fmt.Printf(h.prompt)
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
			errCh <- errExit
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

func (h *handler) dispatch(ctx context.Context, info *registeredStatus, locs []*location, args []string) (continueRead bool, err error) {
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
		handler:      h,
		info:         info,
		locs:         locs,
		continueRead: true,
		err:          nil,
	}
	for _, fn := range handlerCommands {
		app.Commands = append(app.Commands, fn(ctx, hCtx))
	}
	if cliErr := app.Run(append([]string{rootCmd}, args...)); cliErr != nil {
		fmt.Printf("%v\n", cliErr)
	}
	return hCtx.continueRead, hCtx.err
}

type breakpoint interface {
	isTarget(ctx context.Context, info breakpointContext) (yes bool, description string, err error)
	addMark(source *pb.SourceInfo, line int64) bool
	String() string
}

type breakpointContext struct {
	status *registeredStatus
	locs   []*location
}

type breakpoints struct {
	breakpoints     map[string]breakpoint
	breakpointIndex int
}

func (b *breakpoints) add(key string, bp breakpoint) error {
	if b.breakpoints == nil {
		b.breakpoints = make(map[string]breakpoint)
	}
	if key == "" {
		currentIdx := b.breakpointIndex
		b.breakpointIndex++
		key = fmt.Sprintf("%d", currentIdx)
	}
	if _, ok := b.breakpoints[key]; ok {
		return fmt.Errorf("breakpoint %q already exists: %v", key, b)
	}
	b.breakpoints[key] = bp
	return nil
}

func (b *breakpoints) get(key string) (breakpoint, bool) {
	bp, ok := b.breakpoints[key]
	return bp, ok
}

func (b *breakpoints) clear(key string) {
	delete(b.breakpoints, key)
}

func (b *breakpoints) clearAll() {
	b.breakpoints = nil
	b.breakpointIndex = 0
}

func (b *breakpoints) forEach(f func(key string, bp breakpoint) bool) {
	var keys []string
	for k := range b.breakpoints {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !f(k, b.breakpoints[k]) {
			return
		}
	}
}

type breakpointInfo struct {
	breakpoint  breakpoint
	description string
}

func (b *breakpoints) check(ctx context.Context, info breakpointContext) (hit map[string]breakpointInfo) {
	hit = make(map[string]breakpointInfo)
	b.forEach(func(key string, bp breakpoint) bool {
		if yes, desc, err := bp.isTarget(ctx, info); err != nil {
			logrus.WithError(err).Warnf("failed to check breakpoint")
		} else if yes {
			hit[key] = breakpointInfo{bp, desc}
		}
		return true
	})
	return
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
