package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"

	"github.com/containerd/console"
	"github.com/containerd/containerd/pkg/userns"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/moby/buildkit/cache/remotecache"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/cmd/buildctl/build"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/control"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/frontend"
	dockerfile "github.com/moby/buildkit/frontend/dockerfile/builder"
	"github.com/moby/buildkit/frontend/gateway"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/solver/bboltcachestorage"
	"github.com/moby/buildkit/util/grpcerrors"
	"github.com/moby/buildkit/util/network/cniprovider"
	"github.com/moby/buildkit/util/network/netproviders"
	"github.com/moby/buildkit/util/progress/progresswriter"
	"github.com/moby/buildkit/util/resolver"
	"github.com/moby/buildkit/worker"
	"github.com/moby/buildkit/worker/base"
	"github.com/moby/buildkit/worker/runc"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

func main() {
	app := cli.NewApp()
	app.Usage = "A debug tool for Dockerfile based on BuildKit"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "enable debug logs",
		},
	}
	app.Commands = []cli.Command{
		newDebugCommand(),
	}
	app.Before = func(context *cli.Context) error {
		logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
		if context.GlobalBool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
		}
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%+v\n", err)
		os.Exit(1)
	}
}

func newDebugCommand() cli.Command {
	var flags []cli.Flag
	if userns.RunningInUserNS() {
		flags = append(flags, cli.BoolTFlag{
			Name:  "rootless",
			Usage: "Enable rootless configuration (default:true)",
		})
	} else {
		flags = append(flags, cli.BoolFlag{
			Name:  "rootless",
			Usage: "Enable rootless configuration",
		})
	}
	return cli.Command{
		Name:   "debug",
		Usage:  "Debug a build",
		Action: debugAction,
		Flags: append([]cli.Flag{
			cli.StringFlag{
				Name:  "file,f",
				Usage: "Name of the Dockerfile",
			},
			cli.StringFlag{
				Name:  "target",
				Usage: "Target build stage to build.",
			},
			cli.StringSliceFlag{
				Name:  "build-arg",
				Usage: "Build-time variables",
			},
			cli.StringFlag{
				Name:  "oci-worker-net",
				Usage: "Worker network type: \"auto\", \"cni\", \"host\"",
				Value: "auto",
			},
			cli.StringFlag{
				Name:  "image",
				Usage: "Image to use for debugging stage",
			},
			cli.StringFlag{
				Name:  "oci-cni-config-path",
				Usage: "Path to CNI config file",
				Value: "/etc/buildkit/cni.json",
			},
			cli.StringFlag{
				Name:  "oci-cni-binary-path",
				Usage: "Path to CNI plugin binary dir",
				Value: "/opt/cni/bin",
			},
			// TODO: no-cache, output, tag, ssh, secret, quiet, cache-from, cache-to, rm
		}, flags...),
	}
}

// TODO: avoid global var
var globalSignalHandler *signalHandler

type signalHandler struct {
	handler func(sig os.Signal) error
	enabled bool
	mu      sync.Mutex
}

func (h *signalHandler) disable() {
	h.mu.Lock()
	h.enabled = false
	h.mu.Unlock()
}

func (h *signalHandler) enable() {
	h.mu.Lock()
	h.enabled = true
	h.mu.Unlock()
}

func (h *signalHandler) start() {
	h.enable()
	ch := make(chan os.Signal, 1)
	signals := []os.Signal{os.Interrupt}
	signal.Notify(ch, signals...)
	go func() {
		for {
			ss := <-ch
			h.mu.Lock()
			enabled := h.enabled
			h.mu.Unlock()
			if !enabled {
				continue
			}
			h.handler(ss)
		}
	}()
}

// TODO: avoid global var
var globalProgressWriter = &progressWriter{File: os.Stderr, enabled: true}

type progressWriter struct {
	console.File
	enabled bool
	buf     []byte
	mu      sync.Mutex
}

func (w *progressWriter) disable() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.enabled = false
}

func (w *progressWriter) enable() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.enabled = true
	if len(w.buf) > 0 {
		w.File.Write(w.buf)
		w.buf = nil
	}
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.enabled {
		w.buf = append(w.buf, p...) // TODO: add limit
		return len(p), nil
	}
	return w.File.Write(p)
}

func debugAction(clicontext *cli.Context) error {
	ctx, ctxCancel := context.WithCancel(context.Background())
	globalSignalHandler = &signalHandler{
		handler: func(sig os.Signal) error {
			ctxCancel()
			return nil
		},
	}
	globalSignalHandler.start()

	// Parse build options
	solveOpt, err := parseSolveOpt(clicontext)
	if err != nil {
		return err
	}

	// Prepare controller
	debugController := newDebugController()
	cfg := &config.Config{}
	cfg.Workers.OCI.Rootless = clicontext.Bool("rootless")
	cfg.Workers.OCI.NetworkConfig = config.NetworkConfig{
		Mode:          clicontext.String("oci-worker-net"),
		CNIConfigPath: clicontext.String("oci-cni-config-path"),
		CNIBinaryPath: clicontext.String("oci-cni-binary-path"),
	}
	rootDir, err := rootDataDir(cfg.Workers.OCI.Rootless)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(rootDir, 0700); err != nil {
		return err
	}
	serveRoot, err := os.MkdirTemp(rootDir, "buildg")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(serveRoot); err != nil {
			logrus.WithError(err).Warnf("failed to cleanup %q", serveRoot)
		}
	}()
	cfg.Root = serveRoot
	logrus.Debugf("root dir: %q", serveRoot)
	c, done, err := newController(ctx, cfg, debugController)
	if err != nil {
		return err
	}
	defer done()

	// Prepare progress writer
	progressCtx := context.TODO()
	pw, err := progresswriter.NewPrinter(progressCtx, globalProgressWriter, "plain")
	if err != nil {
		return err
	}
	mw := progresswriter.NewMultiWriter(pw)
	var writers []progresswriter.Writer
	for _, at := range solveOpt.Session {
		if s, ok := at.(interface {
			SetLogger(progresswriter.Logger)
		}); ok {
			w := mw.WithPrefix("", false)
			s.SetLogger(func(s *client.SolveStatus) {
				w.Status() <- s
			})
			writers = append(writers, w)
		}
	}

	// Start build with debugging
	buildFunc, handleErrorCh := withDebug(dockerfile.Build, debugController, clicontext.String("image"))
	eg, egCtx := errgroup.WithContext(ctx)
	doneCh := make(chan struct{})
	eg.Go(func() (err error) {
		select {
		case err = <-handleErrorCh:
		case <-doneCh:
		}
		if err != nil {
			return fmt.Errorf("handler error: %w", err)
		}
		return nil
	})
	eg.Go(func() error {
		defer close(doneCh)
		defer func() {
			for _, w := range writers {
				close(w.Status())
			}
		}()
		if _, err := c.Build(egCtx, *solveOpt, "", buildFunc, progresswriter.ResetTime(mw.WithPrefix("", false)).Status()); err != nil {
			return fmt.Errorf("failed to build: %w", err)
		}
		return nil
	})
	eg.Go(func() error {
		<-pw.Done()
		return pw.Err()
	})

	if err := eg.Wait(); err != nil {
		if errors.Is(err, errExit) {
			return nil
		}
		return err
	}

	return nil
}

// TODO:
// - ssh session
// - secret session
// - cache importer/exporter
// - multi-platform
func parseSolveOpt(clicontext *cli.Context) (*client.SolveOpt, error) {
	buildContext := clicontext.Args().First()
	if buildContext == "" {
		return nil, errors.New("context needs to be specified")
	} else if buildContext == "-" || strings.Contains(buildContext, "://") {
		return nil, fmt.Errorf("unsupported build context: %q", buildContext)
	}
	var optStr []string
	localStr := []string{"context=" + buildContext, "dockerfile=" + buildContext}
	if filename := clicontext.String("file"); filename == "-" {
		return nil, fmt.Errorf("Dockerfile from stdin isn't supported as of now")
	} else if filename != "" {
		dir, file := filepath.Split(filename)
		if dir != "" {
			localStr = append(localStr, "dockerfile="+dir)
		}
		optStr = append(optStr, "filename="+file)
	}
	if target := clicontext.String("target"); target != "" {
		optStr = append(optStr, "target="+target)
	}
	for _, ba := range clicontext.StringSlice("build-arg") {
		optStr = append(optStr, "build-arg:"+ba)
	}
	localDirs, err := build.ParseLocal(localStr)
	if err != nil {
		return nil, err
	}
	frontendAttrs, err := build.ParseOpt(optStr)
	if err != nil {
		return nil, err
	}
	exports, err := build.ParseOutput([]string{"type=image"}) // TODO: support more exporters
	if err != nil {
		return nil, err
	}
	return &client.SolveOpt{
		Exports:       exports,
		LocalDirs:     localDirs,
		FrontendAttrs: frontendAttrs,
		Session:       []session.Attachable{authprovider.NewDockerAuthProvider(os.Stderr)},
		// CacheExports:
		// CacheImports:
	}, nil
}

func newController(ctx context.Context, cfg *config.Config, debugController *debugController) (c *client.Client, done func(), retErr error) {
	if cfg.Root == "" {
		return nil, nil, fmt.Errorf("root directory must be specified")
	}
	var closeFuncs []func()
	done = func() {
		for i := len(closeFuncs) - 1; i >= 0; i-- {
			closeFuncs[i]()
		}
	}
	defer func() {
		if retErr != nil {
			done()
		}
	}()

	// Initialize OCI worker with debugging support
	baseW, err := newWorker(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	w := debugController.debugWorker(baseW)
	wc := &worker.Controller{}
	if err := wc.Add(w); err != nil {
		return nil, nil, err
	}

	// Create controller
	sessionManager, err := session.NewManager()
	if err != nil {
		return nil, nil, err
	}
	cacheStorage, err := bboltcachestorage.NewStore(filepath.Join(cfg.Root, "cache.db"))
	if err != nil {
		return nil, nil, err
	}
	frontends := map[string]frontend.Frontend{}
	frontends["gateway.v0"] = debugController.frontendWithDebug(gateway.NewGatewayFrontend(wc))
	controller, err := control.NewController(control.Opt{
		SessionManager:            sessionManager,
		WorkerController:          wc,
		CacheKeyStorage:           cacheStorage,
		Frontends:                 frontends,
		ResolveCacheExporterFuncs: map[string]remotecache.ResolveCacheExporterFunc{}, // TODO: support remote cahce exporter
		ResolveCacheImporterFuncs: map[string]remotecache.ResolveCacheImporterFunc{}, // TODO: support remote cache importer
		Entitlements:              []string{},                                        // TODO
	})
	if err != nil {
		return nil, nil, err
	}

	// Create client for the controller
	controlServer := grpc.NewServer(grpc.UnaryInterceptor(grpcerrors.UnaryServerInterceptor), grpc.StreamInterceptor(grpcerrors.StreamServerInterceptor))
	controller.Register(controlServer)
	lt := newPipeListener()
	go controlServer.Serve(lt)
	closeFuncs = append(closeFuncs, controlServer.GracefulStop)
	c, err = client.New(ctx, "", client.WithContextDialer(lt.dial))
	if err != nil {
		return nil, nil, err
	}

	return c, done, nil
}

func newWorker(ctx context.Context, cfg *config.Config) (worker.Worker, error) {
	root := cfg.Root
	if root == "" {
		return nil, fmt.Errorf("failed to init worker: root directory must be set")
	}
	snFactory := runc.SnapshotterFactory{
		Name: "native", // TODO: support other snapshotters
		New:  native.NewSnapshotter,
	}
	rootless := cfg.Workers.OCI.Rootless
	nc := netproviders.Opt{
		Mode: cfg.Workers.OCI.Mode,
		CNI: cniprovider.Opt{
			Root:       root,
			ConfigPath: cfg.Workers.OCI.CNIConfigPath,
			BinaryDir:  cfg.Workers.OCI.CNIBinaryPath,
		},
	}
	opt, err := runc.NewWorkerOpt(root, snFactory, rootless, oci.ProcessSandbox, nil, nil, nc, nil, "", "", nil, "", "")
	if err != nil {
		return nil, err
	}
	opt.RegistryHosts = resolver.NewRegistryConfig(cfg.Registries)
	return base.NewWorker(ctx, opt)
}

func rootDataDir(rootless bool) (string, error) {
	if !rootless {
		return "/var/lib/buildg", nil
	}
	if xdh := os.Getenv("XDG_DATA_HOME"); xdh != "" {
		return filepath.Join(xdh, "buildg"), nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		return "", fmt.Errorf("environment variable HOME is not set")
	}
	return filepath.Join(home, ".local/share/buildg"), nil
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		ch:   make(chan net.Conn),
		done: make(chan struct{}),
	}
}

type pipeListener struct {
	ch        chan net.Conn
	done      chan struct{}
	closed    bool
	closeOnce sync.Once
	closedMu  sync.Mutex
}

func (l *pipeListener) dial(ctx context.Context, _ string) (net.Conn, error) {
	if l.isClosed() {
		return nil, fmt.Errorf("closed")
	}
	c1, c2 := net.Pipe()
	select {
	case <-l.done:
		return nil, fmt.Errorf("closed")
	case l.ch <- c1:
	}
	return c2, nil
}

func (l *pipeListener) Accept() (net.Conn, error) {
	if l.isClosed() {
		return nil, fmt.Errorf("closed")
	}
	select {
	case <-l.done:
		return nil, fmt.Errorf("closed")
	case conn := <-l.ch:
		return conn, nil
	}
}

func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() {
		l.closedMu.Lock()
		l.closed = true
		close(l.done)
		l.closedMu.Unlock()
	})
	return nil
}

func (l *pipeListener) isClosed() bool {
	l.closedMu.Lock()
	defer l.closedMu.Unlock()
	return l.closed
}

func (l *pipeListener) Addr() net.Addr {
	return dummyAddr{}
}

type dummyAddr struct{}

func (a dummyAddr) Network() string { return "dummy" }
func (a dummyAddr) String() string  { return "dummy" }
