package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/containerd/console"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func withDebug(f gwclient.BuildFunc, debugController *debugController, debugImage string) (gwclient.BuildFunc, <-chan error) {
	handleError := make(chan error)
	return func(ctx context.Context, c gwclient.Client) (res *gwclient.Result, err error) {
		stdinR := newSharedReader(os.Stdin)
		defer stdinR.close()
		handler := &handler{
			gwclient: c,
			stdin:    stdinR,
			breakpoints: map[string]breakpoint{
				"on-fail": newOnFailBreakpoint(),
			},
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

	breakpoints     map[string]breakpoint
	breakpointIndex int
	breakEachVertex bool

	initialized bool

	mu sync.Mutex

	image   gwclient.Reference
	imageMu sync.Mutex
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
	if h.initialized && !h.breakEachVertex {
		bp, description := h.getBreakpoint(ctx, breakpointContext{info, locs})
		if bp == nil {
			logrus.Debugf("skipping non-breakpoint: %v", locs)
			return nil
		}
		fmt.Printf("Breakpoint: %s: %s\n", bp, description)
	}
	h.initialized = true
	h.printLines(locs, 3, false)
	for {
		ln, err := h.readLine(ctx)
		if err != nil {
			return err
		}
		if args := strings.Split(ln, " "); len(args) > 0 {
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
	fmt.Printf(">>> ")
	r, done := h.stdin.use()
	defer done()
	lnCh := make(chan string)
	errCh := make(chan error)
	scanner := bufio.NewScanner(r)
	go func() {
		if scanner.Scan() {
			lnCh <- scanner.Text()
		} else {
			errCh <- scanner.Err()
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

const helpString = `COMMANDS:

break, b BREAKPOINT_SPEC  set a breakpoint
  BREAKPOINT_SPEC
    NUMBER   line number in Dockerfile
    on-fail  step that returns an error
breakpoints, bp           list breakpoints
clear BREAKPOINT_KEY      clear a breakpoint
clearall                  clear all breakpoints
next, n                   proceed to the next line
continue, c               proceed to the next breakpoint
exec, e [OPTIONS] ARG...  execute command in the step
  OPTIONS
    --image          use debugger image
    --mountroot=DIR  mountpoint to mount the rootfs of the step. ignored if --image isn't specified.
    --init           execute commands in an initial state of that step (experimental)
list, ls, l [OPTIONS]     list lines
  OPTIONS
    --all  list all lines in the source file
exit, quit, q             exit the debugging
`

var errExit = errors.New("exit")

func (h *handler) dispatch(ctx context.Context, info *registeredStatus, locs []*location, args []string) (continueRead bool, _ error) {
	directive := args[0]
	switch directive {
	case "b", "break":
		if len(args) < 2 {
			fmt.Printf("line number must be specified for %q\n", directive)
			return true, nil
		}
		var key string
		var b breakpoint
		if args[1] == "on-fail" {
			key = "on-fail"
			b = newOnFailBreakpoint()
		} else if l, err := strconv.ParseInt(args[1], 10, 64); err == nil {
			currentIdx := h.breakpointIndex
			h.breakpointIndex++
			key = fmt.Sprintf("%d", currentIdx)
			b = newLineBreakpoint(locs[0].source.Filename, l)
		}
		if b == nil {
			fmt.Printf("cannot parse breakpoint %q\n", args[1])
			return true, nil
		}
		if b, ok := h.breakpoints[key]; ok {
			fmt.Printf("breakpoint %q is already registered: %s\n", key, b)
			return true, nil
		}
		if h.breakpoints == nil {
			h.breakpoints = make(map[string]breakpoint)
		}
		h.breakpoints[key] = b
	case "breakpoints", "bp":
		h.forEachBreakpoint(func(key string, b breakpoint) bool {
			fmt.Printf("[%s]: %v\n", key, b)
			return true
		})
	case "clear":
		if len(args) < 2 {
			fmt.Printf("breakpoint must be specified for %q\n", directive)
			return true, nil
		}
		delete(h.breakpoints, args[1])
	case "clearall":
		h.breakpoints = nil
		h.breakpointIndex = 0
	case "next", "n":
		h.breakEachVertex = true
		return false, nil
	case "continue", "c":
		h.breakEachVertex = false
		return false, nil
	case "e", "exec":
		if len(args) < 2 {
			fmt.Printf("arguments must be specified for %q\n", directive)
			return true, nil
		}
		r, done := h.stdin.use()
		defer done()
		cfg := containerConfig{
			info:   info,
			stdin:  io.NopCloser(r),
			stdout: os.Stdout,
			stderr: os.Stderr,
		}
		// TODO: use flag parser libraries
		args := args[1:]
		for i, a := range args {
			if a == "--init" {
				cfg.inputMount = true
			} else if a == "--image" {
				h.imageMu.Lock()
				cfg.image = h.image
				h.imageMu.Unlock()
			} else if strings.HasPrefix(a, "--mountroot=") {
				cfg.mountroot = strings.TrimPrefix(a, "--mountroot=")
			} else {
				cfg.args = append(cfg.args, args[i:]...)
				break
			}
		}
		if wErr := h.execContainer(ctx, cfg); wErr != nil {
			fmt.Printf("\nprocess execution failed: %v\n", wErr)
		}
	case "list", "ls", "l":
		all := false
		if len(args) > 1 {
			for _, a := range args[1:] {
				if a == "--all" {
					all = true
				}
			}
		}
		h.printLines(locs, 3, all)
	case "exit", "quit", "q":
		return false, errExit
	case "help":
		fmt.Printf("%s", helpString)
	case "":
		// nop
	default:
		fmt.Printf("unknown directive: %q\n", args[0])
	}
	return true, nil
}

type containerConfig struct {
	info           *registeredStatus
	args           []string
	stdin          io.ReadCloser
	stdout, stderr io.WriteCloser
	image          gwclient.Reference
	mountroot      string
	inputMount     bool
}

func (h *handler) execContainer(ctx context.Context, cfg containerConfig) error {
	op := cfg.info.op
	mountIDs := cfg.info.mountIDs
	if cfg.inputMount {
		mountIDs = cfg.info.inputIDs
	}
	var exec *pb.ExecOp
	switch op := op.GetOp().(type) {
	case *pb.Op_Exec:
		exec = op.Exec
	default:
		return fmt.Errorf("op doesn't support debugging")
	}
	var mounts []gwclient.Mount
	for i, mnt := range exec.Mounts {
		mounts = append(mounts, gwclient.Mount{
			Selector:  mnt.Selector,
			Dest:      mnt.Dest,
			ResultID:  mountIDs[i],
			Readonly:  mnt.Readonly,
			MountType: mnt.MountType,
			CacheOpt:  mnt.CacheOpt,
			SecretOpt: mnt.SecretOpt,
			SSHOpt:    mnt.SSHOpt,
		})
	}
	if cfg.image != nil {
		mountroot := "/debugroot"
		if cfg.mountroot != "" {
			mountroot = cfg.mountroot
		}
		for i := range mounts {
			mounts[i].Dest = filepath.Join(mountroot, mounts[i].Dest)
		}
		mounts = append([]gwclient.Mount{
			{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       cfg.image,
			},
		}, mounts...)
	}

	ctr, err := h.gwclient.NewContainer(ctx, gwclient.NewContainerRequest{
		Mounts:      mounts,
		NetMode:     exec.Network,
		Platform:    op.Platform,
		Constraints: op.Constraints,
	})
	if err != nil {
		return fmt.Errorf("failed to create debug container: %v", err)
	}
	defer ctr.Release(ctx)

	meta := exec.Meta
	proc, err := ctr.Start(ctx, gwclient.StartRequest{
		Args:         cfg.args,
		Env:          meta.Env,
		User:         meta.User,
		Cwd:          meta.Cwd,
		Tty:          true,
		Stdin:        cfg.stdin,
		Stdout:       cfg.stdout,
		Stderr:       cfg.stderr,
		SecurityMode: exec.Security,
	})
	if err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}
	con := console.Current()
	defer con.Reset()
	if err := con.SetRaw(); err != nil {
		return fmt.Errorf("failed to configure terminal: %v", err)
	}
	ioCtx, ioCancel := context.WithCancel(ctx)
	defer ioCancel()
	watchSignal(ioCtx, proc, console.Current())
	return proc.Wait()
}

func watchSignal(ctx context.Context, proc gwclient.ContainerProcess, con console.Console) {
	globalSignalHandler.disable()
	ch := make(chan os.Signal, 1)
	signals := []os.Signal{syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM}
	signal.Notify(ch, signals...)
	go func() {
		defer globalSignalHandler.enable()
		defer signal.Stop(ch)
		for {
			select {
			case ss := <-ch:
				switch ss {
				case syscall.SIGWINCH:
					size, err := con.Size()
					if err != nil {
						continue
					}
					proc.Resize(ctx, gwclient.WinSize{
						Cols: uint32(size.Width),
						Rows: uint32(size.Height),
					})
				default:
					proc.Signal(ctx, ss.(syscall.Signal))
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	ch <- syscall.SIGWINCH
}

func (h *handler) getBreakpoint(ctx context.Context, info breakpointContext) (targetBP breakpoint, description string) {
	h.forEachBreakpoint(func(key string, b breakpoint) bool {
		target, d, err := b.isTarget(ctx, info)
		if err != nil {
			logrus.WithError(err).Warnf("failed to check breakpoint")
		} else if target {
			targetBP = b
			description = d
			return false
		}
		return true
	})
	return
}

func (h *handler) forEachBreakpoint(f func(string, breakpoint) bool) {
	var keys []string
	for k := range h.breakpoints {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !f(k, h.breakpoints[k]) {
			return
		}
	}
}

func (h *handler) printLines(locs []*location, margin int, all bool) {
	sources := make(map[*pb.SourceInfo][]*pb.Range)
	for _, l := range locs {
		sources[l.source] = append(sources[l.source], l.ranges...)
	}

	for source, ranges := range sources {
		if len(ranges) == 0 {
			continue
		}
		fmt.Printf("Filename: %q\n", source.Filename)
		scanner := bufio.NewScanner(bytes.NewReader(source.Data))
		lastLinePrinted := false
		firstPrint := true
		for i := 1; scanner.Scan(); i++ {
			print := false
			target := false
			for _, r := range ranges {
				if all || int(r.Start.Line)-margin <= i && i <= int(r.End.Line)+margin {
					print = true
					if int(r.Start.Line) <= i && i <= int(r.End.Line) {
						target = true
						break
					}
				}
			}

			if !print {
				lastLinePrinted = false
				continue
			}
			if !lastLinePrinted && !firstPrint {
				fmt.Println("----------------")
			}

			prefix := " "
			h.forEachBreakpoint(func(key string, b breakpoint) bool {
				if b.addMark(source, int64(i)) {
					prefix = "*"
					return false
				}
				return true
			})
			prefix2 := "  "
			if target {
				prefix2 = "=>"
			}
			fmt.Println(prefix + prefix2 + fmt.Sprintf("%4d| ", i) + scanner.Text())
			lastLinePrinted = true
			firstPrint = false
		}
	}
	return
}

type location struct {
	source *pb.SourceInfo
	ranges []*pb.Range
}

func (l *location) String() string {
	return fmt.Sprintf("%q %+v", l.source.Filename, l.ranges)
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

type breakpointContext struct {
	status *registeredStatus
	locs   []*location
}

type breakpoint interface {
	isTarget(ctx context.Context, info breakpointContext) (yes bool, description string, err error)
	addMark(source *pb.SourceInfo, line int64) bool
	String() string
}

func newLineBreakpoint(filename string, line int64) breakpoint {
	return &lineBreakpoint{filename, line}
}

type lineBreakpoint struct {
	filename string
	line     int64
}

func (b *lineBreakpoint) isTarget(ctx context.Context, info breakpointContext) (bool, string, error) {
	for _, loc := range info.locs {
		if loc.source.Filename != b.filename {
			continue
		}
		for _, r := range loc.ranges {
			if int64(r.Start.Line) <= b.line && b.line <= int64(r.End.Line) {
				return true, "reached", nil
			}
		}
	}
	return false, "", nil
}

func (b *lineBreakpoint) String() string {
	return fmt.Sprintf("line: %s:%d", b.filename, b.line)
}

func (b *lineBreakpoint) addMark(source *pb.SourceInfo, line int64) bool {
	return source.Filename == b.filename && line == b.line
}

func newOnFailBreakpoint() breakpoint {
	return &onFailBreakpoint{}
}

type onFailBreakpoint struct{}

func (b *onFailBreakpoint) isTarget(ctx context.Context, info breakpointContext) (bool, string, error) {
	return info.status.err != nil, fmt.Sprintf("got error %v", info.status.err), nil
}

func (b *onFailBreakpoint) String() string {
	return fmt.Sprintf("breaks on fail")
}

func (b *onFailBreakpoint) addMark(source *pb.SourceInfo, line int64) bool {
	return false
}
