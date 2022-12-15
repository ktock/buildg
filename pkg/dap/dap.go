package dap

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/go-dap"
	"github.com/google/shlex"
	"github.com/ktock/buildg/pkg/buildkit"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/solver/pb"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"golang.org/x/sync/errgroup"
)

const AttachContainerCommand = "_dap_attach_container"

func NewServer(conn net.Conn, cfg *config.Config, cleanupFunc func() error, cleanupAll bool) (*Server, error) {
	ctx, cancel := context.WithCancel(context.TODO())
	eg := new(errgroup.Group)
	s := &Server{
		conn:        conn,
		cleanupFunc: cleanupFunc,
		ctx:         ctx,
		cancel:      cancel,
		eg:          eg,
	}
	var err error
	s.debugger, err = newDebugger(cfg, cleanupAll)
	if err != nil {
		return nil, err
	}
	return s, nil
}

type Server struct {
	conn        net.Conn
	debugger    *debugger
	debuggerMu  sync.Mutex
	sendMu      sync.Mutex
	cleanupFunc func() error

	ctx    context.Context
	cancel func()
	eg     *errgroup.Group
}

func (s *Server) Serve() error {
	var eg errgroup.Group
	r := bufio.NewReader(s.conn)
	for {
		req, err := dap.ReadProtocolMessage(r)
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		eg.Go(func() error { return s.handle(req) })
	}
	return eg.Wait()
}

func (s *Server) send(message dap.Message) {
	s.sendMu.Lock()
	dap.WriteProtocolMessage(s.conn, message)
	logrus.WithField("dst", s.conn.RemoteAddr()).Debugf("message sent %+v", message)
	s.sendMu.Unlock()
}

const (
	unsupportedError = 1000
	failedError      = 1001
	unknownError     = 9999
)

var errorMessage = map[int]string{
	unsupportedError: "unsupported",
	failedError:      "failed",
	unknownError:     "unknown",
}

func (s *Server) sendErrorResponse(requestSeq int, command string, errID int, message string, showUser bool) {
	id, summary := unknownError, errorMessage[unknownError]
	if m, ok := errorMessage[errID]; ok {
		id, summary = errID, m
	}
	r := &dap.ErrorResponse{}
	r.Response = *newResponse(requestSeq, command)
	r.Success = false
	r.Message = summary
	r.Body.Error.Format = message
	r.Body.Error.Id = id
	r.Body.Error.ShowUser = showUser
	s.send(r)
}

func (s *Server) sendUnsupportedResponse(requestSeq int, command string, message string) {
	s.sendErrorResponse(requestSeq, command, unsupportedError, message, false)
}

func (s *Server) outputStdoutWriter() io.Writer {
	return &outputWriter{s, "stdout"}
}

func (s *Server) handle(request dap.Message) error {
	logrus.Debugf("got request: %+v", request)
	switch request := request.(type) {
	case *dap.InitializeRequest:
		s.onInitializeRequest(request)
	case *dap.LaunchRequest:
		s.onLaunchRequest(request)
	case *dap.AttachRequest:
		s.onAttachRequest(request)
	case *dap.DisconnectRequest:
		s.onDisconnectRequest(request)
	case *dap.TerminateRequest:
		s.onTerminateRequest(request)
	case *dap.RestartRequest:
		s.onRestartRequest(request)
	case *dap.SetBreakpointsRequest:
		s.onSetBreakpointsRequest(request)
	case *dap.SetFunctionBreakpointsRequest:
		s.onSetFunctionBreakpointsRequest(request)
	case *dap.SetExceptionBreakpointsRequest:
		s.onSetExceptionBreakpointsRequest(request)
	case *dap.ConfigurationDoneRequest:
		s.onConfigurationDoneRequest(request)
	case *dap.ContinueRequest:
		s.onContinueRequest(request)
	case *dap.NextRequest:
		s.onNextRequest(request)
	case *dap.StepInRequest:
		s.onStepInRequest(request)
	case *dap.StepOutRequest:
		s.onStepOutRequest(request)
	case *dap.StepBackRequest:
		s.onStepBackRequest(request)
	case *dap.ReverseContinueRequest:
		s.onReverseContinueRequest(request)
	case *dap.RestartFrameRequest:
		s.onRestartFrameRequest(request)
	case *dap.GotoRequest:
		s.onGotoRequest(request)
	case *dap.PauseRequest:
		s.onPauseRequest(request)
	case *dap.StackTraceRequest:
		s.onStackTraceRequest(request)
	case *dap.ScopesRequest:
		s.onScopesRequest(request)
	case *dap.VariablesRequest:
		s.onVariablesRequest(request)
	case *dap.SetVariableRequest:
		s.onSetVariableRequest(request)
	case *dap.SetExpressionRequest:
		s.onSetExpressionRequest(request)
	case *dap.SourceRequest:
		s.onSourceRequest(request)
	case *dap.ThreadsRequest:
		s.onThreadsRequest(request)
	case *dap.TerminateThreadsRequest:
		s.onTerminateThreadsRequest(request)
	case *dap.EvaluateRequest:
		s.onEvaluateRequest(request)
	case *dap.StepInTargetsRequest:
		s.onStepInTargetsRequest(request)
	case *dap.GotoTargetsRequest:
		s.onGotoTargetsRequest(request)
	case *dap.CompletionsRequest:
		s.onCompletionsRequest(request)
	case *dap.ExceptionInfoRequest:
		s.onExceptionInfoRequest(request)
	case *dap.LoadedSourcesRequest:
		s.onLoadedSourcesRequest(request)
	case *dap.DataBreakpointInfoRequest:
		s.onDataBreakpointInfoRequest(request)
	case *dap.SetDataBreakpointsRequest:
		s.onSetDataBreakpointsRequest(request)
	case *dap.ReadMemoryRequest:
		s.onReadMemoryRequest(request)
	case *dap.DisassembleRequest:
		s.onDisassembleRequest(request)
	case *dap.CancelRequest:
		s.onCancelRequest(request)
	case *dap.BreakpointLocationsRequest:
		s.onBreakpointLocationsRequest(request)
	default:
		logrus.Warnf("Unable to process %#v\n", request)
	}
	return nil
}

func (s *Server) onInitializeRequest(request *dap.InitializeRequest) {
	response := &dap.InitializeResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body.SupportsConfigurationDoneRequest = true
	response.Body.SupportsFunctionBreakpoints = false
	response.Body.SupportsConditionalBreakpoints = false
	response.Body.SupportsHitConditionalBreakpoints = false
	response.Body.SupportsEvaluateForHovers = false
	response.Body.ExceptionBreakpointFilters = make([]dap.ExceptionBreakpointsFilter, 0)
	response.Body.SupportsStepBack = false
	response.Body.SupportsSetVariable = false
	response.Body.SupportsRestartFrame = false
	response.Body.SupportsGotoTargetsRequest = false
	response.Body.SupportsStepInTargetsRequest = false
	response.Body.SupportsCompletionsRequest = false
	response.Body.CompletionTriggerCharacters = make([]string, 0)
	response.Body.SupportsModulesRequest = false
	response.Body.AdditionalModuleColumns = make([]dap.ColumnDescriptor, 0)
	response.Body.SupportedChecksumAlgorithms = make([]dap.ChecksumAlgorithm, 0)
	response.Body.SupportsRestartRequest = false
	response.Body.SupportsExceptionOptions = false
	response.Body.SupportsValueFormattingOptions = false
	response.Body.SupportsExceptionInfoRequest = false
	response.Body.SupportTerminateDebuggee = false
	response.Body.SupportSuspendDebuggee = false
	response.Body.SupportsDelayedStackTraceLoading = false
	response.Body.SupportsLoadedSourcesRequest = false
	response.Body.SupportsLogPoints = false
	response.Body.SupportsTerminateThreadsRequest = false
	response.Body.SupportsSetExpression = false
	response.Body.SupportsTerminateRequest = false
	response.Body.SupportsDataBreakpoints = false
	response.Body.SupportsReadMemoryRequest = false
	response.Body.SupportsWriteMemoryRequest = false
	response.Body.SupportsDisassembleRequest = false
	response.Body.SupportsCancelRequest = false
	response.Body.SupportsBreakpointLocationsRequest = false
	response.Body.SupportsClipboardContext = false
	response.Body.SupportsSteppingGranularity = false
	response.Body.SupportsInstructionBreakpoints = false
	response.Body.SupportsExceptionFilterOptions = false

	s.send(response)
	s.send(&dap.InitializedEvent{Event: *newEvent("initialized")})
}

func (s *Server) onLaunchRequest(request *dap.LaunchRequest) {
	cfg := new(LaunchConfig)
	if err := json.Unmarshal(request.Arguments, cfg); err != nil {
		s.sendErrorResponse(request.Seq, request.Command, failedError, fmt.Sprintf("failed to launch: %v", err), true)
		return
	}
	if err := s.launchDebugger(*cfg); err != nil {
		s.sendErrorResponse(request.Seq, request.Command, failedError, fmt.Sprintf("failed to launch: %v", err), true)
		return
	}
	response := &dap.LaunchResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	s.send(response)
	go func() {
		for breakpoints := range s.debugger.stopped() {
			reason := "breakpoint"
			if len(breakpoints) == 0 {
				reason = "step"
			}
			s.send(&dap.StoppedEvent{
				Event: *newEvent("stopped"),
				Body:  dap.StoppedEventBody{Reason: reason, ThreadId: 1, AllThreadsStopped: true, HitBreakpointIds: breakpoints},
			})
		}
	}()
}

type LaunchConfig struct {
	Program     string `json:"program"`
	NoDebug     bool   `json:"noDebug"`
	StopOnEntry bool   `json:"stopOnEntry"`

	Target    string   `json:"target"`
	BuildArgs []string `json:"build-args"`
	SSH       []string `json:"ssh"`
	Secrets   []string `json:"secrets"`

	Image string `json:"image"`
}

func (s *Server) launchDebugger(cfg LaunchConfig) error {
	if cfg.Program == "" {
		return fmt.Errorf("launch error: program must be specified")
	}
	if s.debugger == nil {
		return fmt.Errorf("launch error: debugger is not available")
	}
	disableBreakpoints := false
	if cfg.NoDebug {
		disableBreakpoints = true
	}
	startedCh := make(chan struct{})
	errCh := make(chan error)
	go func() {
		err := s.debugger.launch(cfg,
			func() {
				s.send(&dap.ThreadEvent{Event: *newEvent("thread"), Body: dap.ThreadEventBody{Reason: "started", ThreadId: 1}})
				close(startedCh)
			},
			func() {
				s.send(&dap.ThreadEvent{Event: *newEvent("thread"), Body: dap.ThreadEventBody{Reason: "exited", ThreadId: 1}})
				if s.cleanupFunc != nil {
					// Cleanup before exit event to make sure cleanup happens before this server killed by emacs
					if err := s.cleanupFunc(); err != nil {
						logrus.WithError(err).Warnf("failed to cleanup")
					}
				}
				s.send(&dap.TerminatedEvent{Event: *newEvent("terminated")})
				s.send(&dap.ExitedEvent{Event: *newEvent("exited")})
			}, cfg.StopOnEntry, disableBreakpoints, s.outputStdoutWriter(),
		)
		if err != nil {
			errCh <- err
			return
		}
	}()
	select {
	case <-startedCh:
	case err := <-errCh:
		return err
	}
	return nil
}

func (s *Server) onDisconnectRequest(request *dap.DisconnectRequest) {
	s.debuggerMu.Lock()
	defer s.debuggerMu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	if err := s.eg.Wait(); err != nil { // wait for container cleanup
		logrus.WithError(err).Warnf("failed to close tasks")
	}
	if s.debugger != nil {
		if err := s.debugger.cancel(); err != nil {
			logrus.WithError(err).Warnf("failed to cancel debugger")
		}
		s.debugger = nil
	}
	response := &dap.DisconnectResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	s.send(response)
	os.Exit(0) // TODO: should return the control to the main func
}

func (s *Server) onSetBreakpointsRequest(request *dap.SetBreakpointsRequest) {
	args := request.Arguments
	breakpoints := make([]dap.Breakpoint, 0)

	s.debugger.breakpoints().ClearAll()
	if _, err := s.debugger.breakpoints().Add("on-fail", buildkit.NewOnFailBreakpoint()); err != nil {
		logrus.WithError(err).Warnf("failed to add on-fail breakpoints")
	}
	for i := 0; i < len(args.Breakpoints); i++ {
		bp := buildkit.NewLineBreakpoint(args.Source.Name, int64(args.Breakpoints[i].Line))
		key, err := s.debugger.breakpoints().Add("", bp)
		if err != nil {
			logrus.WithError(err).Warnf("failed to add breakpoints")
			continue
		}
		keyI, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			logrus.WithError(err).Warnf("failed to parse breakpoint key")
			continue
		}
		breakpoints = append(breakpoints, dap.Breakpoint{
			Id:       int(keyI),
			Source:   &args.Source,
			Line:     args.Breakpoints[i].Line,
			Verified: true,
		})
	}

	response := &dap.SetBreakpointsResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body.Breakpoints = breakpoints
	s.send(response)
}

func (s *Server) onConfigurationDoneRequest(request *dap.ConfigurationDoneRequest) {
	response := &dap.ConfigurationDoneResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	s.send(response)
}

func (s *Server) onContinueRequest(request *dap.ContinueRequest) {
	response := &dap.ContinueResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	s.send(response)
	s.debugger.doContinue()
}

func (s *Server) onNextRequest(request *dap.NextRequest) {
	response := &dap.NextResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	s.send(response)
	s.debugger.doNext()
}

func (s *Server) onStackTraceRequest(request *dap.StackTraceRequest) {
	response := &dap.StackTraceResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body = dap.StackTraceResponseBody{
		StackFrames: make([]dap.StackFrame, 0),
	}

	bCtx := s.debugger.breakContext()
	launchConfig := s.debugger.launchConfig()
	if bCtx == nil || launchConfig == nil {
		// no stack trace is available now
		s.send(response)
		return
	}

	var lines []*pb.Range

	// If there are hit breakpoints on the current Op, return them.
	// FIXME: This is a workaround against stackFrame doesn't support
	//        multiple sources per frame. Once dap support it, we can
	//        return all current locations.
	// TODO: show non-breakpoint locations to output as well
	for _, bpInfo := range bCtx.Hits {
		for _, loc := range bpInfo.Hits {
			lines = append(lines, loc.Ranges...)
		}
	}
	if len(lines) == 0 {
		// no breakpoints on the current Op. This can happen on
		// step execution.
		for _, loc := range bCtx.Locs {
			lines = append(lines, loc.Ranges...)
		}
	}
	if len(lines) > 0 {
		name := "instruction"
		if bCtx.Info.Name != "" {
			name = bCtx.Info.Name
		}
		f := launchConfig.Program
		response.Body.StackFrames = []dap.StackFrame{
			{
				Id:     0,
				Source: dap.Source{Name: filepath.Base(f), Path: f},
				// FIXME: We only return lines[0] because stackFrame doesn't support
				//        multiple sources per frame. Once dap support it, we can
				//        return all current locations.
				Line:    int(lines[0].Start.Line),
				EndLine: int(lines[0].End.Line),
				Name:    name,
			},
		}
		response.Body.TotalFrames = 1
	}
	s.send(response)
}

func (s *Server) onScopesRequest(request *dap.ScopesRequest) {
	response := &dap.ScopesResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body = dap.ScopesResponseBody{
		Scopes: []dap.Scope{
			{
				Name:               "Environment Variables",
				VariablesReference: 1,
			},
		},
	}
	s.send(response)
}

func (s *Server) onVariablesRequest(request *dap.VariablesRequest) {
	response := &dap.VariablesResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body = dap.VariablesResponseBody{
		Variables: make([]dap.Variable, 0), // neovim doesn't allow nil
	}

	bCtx := s.debugger.breakContext()
	if bCtx == nil {
		s.send(response)
		return
	}

	var variables []dap.Variable
	if info := bCtx.Info; info != nil {
		switch op := info.Op.GetOp().(type) {
		case *pb.Op_Exec:
			for _, e := range op.Exec.Meta.Env {
				var k, v string
				if kv := strings.SplitN(e, "=", 2); len(kv) >= 2 {
					k, v = kv[0], kv[1]
				} else if len(kv) == 1 {
					k = kv[0]
				} else {
					continue
				}
				variables = append(variables, dap.Variable{
					Name:  k,
					Value: v,
				})
			}
		default:
			// TODO: support other Ops
		}
	}

	if s := request.Arguments.Start; s > 0 {
		if s < len(variables) {
			variables = variables[s:]
		} else {
			variables = nil
		}
	}
	if c := request.Arguments.Count; c > 0 {
		if c < len(variables) {
			variables = variables[:c]
		}
	}
	response.Body.Variables = append(response.Body.Variables, variables...)
	s.send(response)
}

func (s *Server) onThreadsRequest(request *dap.ThreadsRequest) {
	response := &dap.ThreadsResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body = dap.ThreadsResponseBody{Threads: []dap.Thread{{Id: 1, Name: "build"}}}
	s.send(response)
}

type handlerContext struct {
	breakContext         buildkit.BreakContext
	stdout               io.Writer
	evaluateDoneCallback func()
}

type replCommand func(ctx context.Context, hCtx *handlerContext) cli.Command

func (s *Server) onEvaluateRequest(request *dap.EvaluateRequest) {
	if request.Arguments.Context != "repl" { // TODO: support other contexts
		s.sendUnsupportedResponse(request.Seq, request.Command, "evaluating non-repl input is unsupported as of now")
		return
	}

	bCtx := s.debugger.breakContext()
	if bCtx == nil {
		s.sendErrorResponse(request.Seq, request.Command, failedError, "no breakpoint available", true)
		return
	}

	replCommands := []replCommand{s.execCommand} // TODO: breakpointsCommand, listCommand, ...

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	hCtx := new(handlerContext)
	out := new(bytes.Buffer)
	if args, err := shlex.Split(request.Arguments.Expression); err != nil {
		logrus.WithError(err).Warnf("failed to parse line")
	} else if len(args) > 0 && args[0] != "" {
		app := cli.NewApp()
		rootCmd := "buildg"
		app.Name = rootCmd
		app.HelpName = rootCmd
		app.Usage = "Interactive debugger for Dockerfile"
		app.UsageText = "command [command options] [arguments...]"
		app.ExitErrHandler = func(context *cli.Context, err error) {}
		app.UseShortOptionHandling = true
		app.Writer = out
		hCtx = &handlerContext{
			breakContext: *bCtx,
			stdout:       out,
		}
		for _, fn := range replCommands {
			app.Commands = append(app.Commands, fn(ctx, hCtx))
		}
		if err := app.Run(append([]string{rootCmd}, args...)); err != nil {
			out.WriteString(err.Error() + "\n")
		}
	}
	response := &dap.EvaluateResponse{}
	response.Response = *newResponse(request.Seq, request.Command)
	response.Body = dap.EvaluateResponseBody{
		Result: out.String(),
	}
	s.send(response)
	if hCtx.evaluateDoneCallback != nil {
		hCtx.evaluateDoneCallback()
	}
}

func (s *Server) execCommand(_ context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:    "exec",
		Aliases: []string{"e"},
		Usage:   "Execute command in the step",
		UsageText: `exec [OPTIONS] [ARGS...]

If ARGS isn't provided, "/bin/sh" is used by default.
container execution on non-RUN instruction is experimental
`,
		Flags: []cli.Flag{
			cli.BoolFlag{
				Name:  "image",
				Usage: "Execute command in the debuger image. If not specified, the command is executed on the rootfs of the current step.",
			},
			cli.StringFlag{
				Name:  "mountroot",
				Usage: "Mountpoint to mount the rootfs of the current step. Ignored if --image isn't specified.",
				Value: "/debugroot",
			},
			cli.BoolFlag{
				Name:  "init-state",
				Usage: "Execute commands in an initial state of that step (experimental)",
			},
			cli.BoolTFlag{
				Name:  "tty,t",
				Usage: "Allocate tty (enabled by default)",
			},
			cli.BoolTFlag{
				Name:  "i",
				Usage: "Enable stdin (FIXME: must be set with tty) (enabled by default)",
			},
			cli.StringSliceFlag{
				Name:  "env,e",
				Usage: "Set environment variables",
			},
			cli.StringFlag{
				Name:  "workdir,w",
				Usage: "Working directory inside the container",
			},
		},
		Action: func(clicontext *cli.Context) (retErr error) {
			args := clicontext.Args()
			if len(args) == 0 || args[0] == "" {
				args = []string{"/bin/sh"}
			}
			flagI := clicontext.Bool("i")
			flagT := clicontext.Bool("tty")
			if flagI && !flagT || !flagI && flagT {
				return fmt.Errorf("flag \"-i\" and \"-t\" must be set together") // FIXME
			}

			gCtx := s.ctx // cancelled on disconnect
			var cleanups []func()
			defer func() {
				if retErr != nil {
					for i := len(cleanups) - 1; i >= 0; i-- {
						cleanups[i]()
					}
				}
			}()

			// Prepare state dir
			tmpRoot, err := os.MkdirTemp("", "buildg-serve-state")
			if err != nil {
				return err
			}
			cleanups = append(cleanups, func() { os.RemoveAll(tmpRoot) })

			// Server IO
			logrus.Debugf("container root %+v", tmpRoot)
			stdin, stdout, stderr, sigForwarder, done, err := serveContainerIO(gCtx, tmpRoot)
			if err != nil {
				return err
			}
			cleanups = append(cleanups, func() { done() })

			// Search container client
			self, err := os.Executable() // TODO: make it configurable
			if err != nil {
				return err
			}

			// Launch container
			switch hCtx.breakContext.Info.Op.GetOp().(type) {
			case *pb.Op_Exec:
			default:
				s.outputStdoutWriter().Write([]byte("container execution on non-RUN instruction is experimental"))
			}
			execCfg := buildkit.ContainerConfig{
				Info:          hCtx.breakContext.Info,
				Args:          args,
				Stdout:        stdout,
				Stderr:        stderr,
				Tty:           clicontext.Bool("tty"),
				Mountroot:     clicontext.String("mountroot"),
				InputMount:    clicontext.Bool("init-state"),
				Env:           clicontext.StringSlice("env"),
				Cwd:           clicontext.String("workdir"),
				WatchSignal:   sigForwarder.watchSignal,
				GatewayClient: hCtx.breakContext.Handler.GatewayClient(),
				NoSetRaw:      true,
			}
			if clicontext.Bool("image") {
				execCfg.Image = hCtx.breakContext.Handler.DebuggerImage()
			}
			if flagI {
				execCfg.Stdin = stdin
			}
			proc, containerCleanups, err := buildkit.ExecContainer(context.TODO(), execCfg) // do not pass gCtx here to allow buildkit graceful cleanup
			if err != nil {
				return err
			}
			cleanups = append(cleanups, func() { containerCleanups() })

			// Let the caller to attach to the container after evaluation response received.
			hCtx.evaluateDoneCallback = func() {
				s.send(&dap.RunInTerminalRequest{
					Request: dap.Request{
						ProtocolMessage: dap.ProtocolMessage{
							Seq:  0,
							Type: "request",
						},
						Command: "runInTerminal",
					},
					Arguments: dap.RunInTerminalRequestArguments{
						Kind:  "integrated",
						Title: "containerclient",
						Args:  []string{self, "dap", AttachContainerCommand, "--set-tty-raw=" + strconv.FormatBool(clicontext.Bool("tty")), tmpRoot},
						Env:   make(map[string]interface{}),
						// emacs requires this nonempty otherwise error (Wrong type argument: stringp, nil) will occur on dap-ui-breakpoints()
						Cwd: filepath.Dir(s.debugger.launchConfig().Program),
					},
				})
			}
			s.eg.Go(func() error {
				// let disconnect API to wait for the cleanup of container processes
				doneCh := make(chan struct{})
				errCh := make(chan error)
				go func() {
					defer close(doneCh)
					if err := proc.Wait(); err != nil {
						errCh <- err
						return
					}
				}()
				select {
				case <-doneCh:
					s.outputStdoutWriter().Write([]byte("container finished"))
				case err := <-errCh:
					s.outputStdoutWriter().Write([]byte(fmt.Sprintf("container finished with error: %v", err)))
				case err := <-gCtx.Done():
					s.outputStdoutWriter().Write([]byte(fmt.Sprintf("finishing container due to server shutdown: %v", err)))
				}
				for i := len(cleanups) - 1; i >= 0; i-- {
					cleanups[i]()
				}
				select {
				case <-doneCh:
				case err := <-errCh:
					s.outputStdoutWriter().Write([]byte(fmt.Sprintf("container exit with error: %v", err)))
				case <-time.After(3 * time.Second):
					s.outputStdoutWriter().Write([]byte("container exit timeout"))
				}
				return nil
			})
			return nil
		},
	}
}

func (s *Server) onSetExceptionBreakpointsRequest(request *dap.SetExceptionBreakpointsRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "Request unsupported")
}

func (s *Server) onRestartRequest(request *dap.RestartRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "RestartRequest unsupported")
}

func (s *Server) onAttachRequest(request *dap.AttachRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "AttachRequest unsupported")
}

func (s *Server) onTerminateRequest(request *dap.TerminateRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "TerminateRequest unsupported")
}

func (s *Server) onSetFunctionBreakpointsRequest(request *dap.SetFunctionBreakpointsRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "FunctionBreakpointsRequest unsupported")
}

func (s *Server) onStepInRequest(request *dap.StepInRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "StepInRequest unsupported")
}

func (s *Server) onStepOutRequest(request *dap.StepOutRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "StepOutRequest unsupported")
}

func (s *Server) onStepBackRequest(request *dap.StepBackRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "StepBackRequest unsupported")
}

func (s *Server) onReverseContinueRequest(request *dap.ReverseContinueRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "ReverseContinueRequest unsupported")
}

func (s *Server) onRestartFrameRequest(request *dap.RestartFrameRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "RestartFrameRequest unsupported")
}

func (s *Server) onGotoRequest(request *dap.GotoRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "GotoRequest unsupported")
}

func (s *Server) onPauseRequest(request *dap.PauseRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "PauseRequest unsupported")
}

func (s *Server) onSetVariableRequest(request *dap.SetVariableRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "SetVariablesRequest unsupported")
}

func (s *Server) onSetExpressionRequest(request *dap.SetExpressionRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "SetExpressionRequest unsupported")
}

func (s *Server) onSourceRequest(request *dap.SourceRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "SourceRequest unsupported")
}

func (s *Server) onTerminateThreadsRequest(request *dap.TerminateThreadsRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "TerminateRequest unsupported")
}

func (s *Server) onStepInTargetsRequest(request *dap.StepInTargetsRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "StepInTargetsRequest unsupported")
}

func (s *Server) onGotoTargetsRequest(request *dap.GotoTargetsRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "GotoTargetsRequest unsupported")
}

func (s *Server) onCompletionsRequest(request *dap.CompletionsRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "CompletionsRequest unsupported")
}

func (s *Server) onExceptionInfoRequest(request *dap.ExceptionInfoRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "ExceptionInfoRequest unsupported")
}

func (s *Server) onLoadedSourcesRequest(request *dap.LoadedSourcesRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "LoadedSourcesRequest unsupported")
}

func (s *Server) onDataBreakpointInfoRequest(request *dap.DataBreakpointInfoRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "DataBreakpointInfoRequest unsupported")
}

func (s *Server) onSetDataBreakpointsRequest(request *dap.SetDataBreakpointsRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "SetDataBreakpointsRequest unsupported")
}

func (s *Server) onReadMemoryRequest(request *dap.ReadMemoryRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "ReadMemoryRequest unsupported")
}

func (s *Server) onDisassembleRequest(request *dap.DisassembleRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "DisassembleRequest unsupported")
}

func (s *Server) onCancelRequest(request *dap.CancelRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "CancelRequest unsupported")
}

func (s *Server) onBreakpointLocationsRequest(request *dap.BreakpointLocationsRequest) {
	s.sendUnsupportedResponse(request.Seq, request.Command, "BreakpointLocationsRequest unsupported")
}

type outputWriter struct {
	s        *Server
	category string
}

func (w *outputWriter) Write(p []byte) (int, error) {
	w.s.send(&dap.OutputEvent{Event: *newEvent("output"), Body: dap.OutputEventBody{Category: w.category, Output: string(p)}})
	return len(p), nil
}

func newEvent(event string) *dap.Event {
	return &dap.Event{
		ProtocolMessage: dap.ProtocolMessage{
			Seq:  0,
			Type: "event",
		},
		Event: event,
	}
}

func newResponse(requestSeq int, command string) *dap.Response {
	return &dap.Response{
		ProtocolMessage: dap.ProtocolMessage{
			Seq:  0,
			Type: "response",
		},
		Command:    command,
		RequestSeq: requestSeq,
		Success:    true,
	}
}
