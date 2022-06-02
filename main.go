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
	"text/tabwriter"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/containerd/pkg/userns"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/ktock/buildg/pkg/version"
	"github.com/moby/buildkit/cache/remotecache"
	localremotecache "github.com/moby/buildkit/cache/remotecache/local"
	registryremotecache "github.com/moby/buildkit/cache/remotecache/registry"
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
	"github.com/moby/buildkit/session/sshforward/sshprovider"
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
	app.Usage = "Interactive debugger for Dockerfile"
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
	app.Flags = append([]cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "enable debug logs",
		},
		cli.StringSliceFlag{
			Name:  "root",
			Usage: "Path to the root directory for storing data (e.g. \"/var/lib/buildg\")",
		},
		cli.StringFlag{
			Name:  "oci-worker-net",
			Usage: "Worker network type: \"auto\", \"cni\", \"host\"",
			Value: "auto",
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
	}, flags...)
	app.Commands = []cli.Command{
		newDebugCommand(),
		newDuCommand(),
		newPruneCommand(),
		newVersionCommand(),
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

func newVersionCommand() cli.Command {
	return cli.Command{
		Name:  "version",
		Usage: "Version info",
		Action: func(clicontext *cli.Context) error {
			fmt.Println("buildg", version.Version, version.Revision)
			return nil
		},
	}
}

func newDebugCommand() cli.Command {
	return cli.Command{
		Name:   "debug",
		Usage:  "Debug a build",
		Action: debugAction,
		Flags: []cli.Flag{
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
				Name:  "image",
				Usage: "Image to use for debugging stage",
			},
			cli.StringSliceFlag{
				Name:  "secret",
				Usage: "Expose secret value to the build. Format: id=secretname,src=filepath",
			},
			cli.StringSliceFlag{
				Name:  "ssh",
				Usage: "Allow forwarding SSH agent to the build. Format: default|<id>[=<socket>|<key>[,<key>]]",
			},
			cli.StringSliceFlag{
				Name:  "cache-from",
				Usage: "Import build cache from the specified location. e.g. user/app:cache, type=local,src=path/to/dir",
			},
			cli.BoolFlag{
				Name:  "cache-reuse",
				Usage: "Reuse locally cached previous results (experimental).",
			},
		},
	}
}

func newPruneCommand() cli.Command {
	return cli.Command{
		Name:   "prune",
		Usage:  "Prune cache",
		Action: pruneAction,
		Flags: []cli.Flag{
			cli.BoolFlag{
				Name:  "all",
				Usage: "Prune including internal/frontend references",
			},
		},
	}
}

func newDuCommand() cli.Command {
	return cli.Command{
		Name:   "du",
		Usage:  "Show disk usage",
		Action: duAction,
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

	// Parse config options
	cfg, doneCfg, err := parseWorkerConfig(clicontext)
	if err != nil {
		return err
	}
	defer doneCfg()
	logrus.Debugf("root dir: %q", cfg.Root)

	// Prepare controller
	debugController := newDebugController()
	var c *client.Client
	var doneController func()
	createdCh := make(chan error)
	errCh := make(chan error)
	go func() {
		var err error
		c, doneController, err = newController(ctx, cfg, debugController)
		if err != nil {
			errCh <- err
			return
		}
		close(createdCh)
	}()
	select {
	case <-ctx.Done():
		err := ctx.Err()
		return err
	case <-time.After(3 * time.Second):
		return fmt.Errorf("timed out to access cache storage. other debug session is running?")
	case err := <-errCh:
		return err
	case <-createdCh:
	}
	defer doneController()

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

func pruneAction(clicontext *cli.Context) error {
	ctx, ctxCancel := context.WithCancel(context.Background())
	globalSignalHandler = &signalHandler{
		handler: func(sig os.Signal) error {
			ctxCancel()
			return nil
		},
	}
	globalSignalHandler.start()

	// Parse config options
	cfg, rootDir, err := parseGlobalWorkerConfig(clicontext)
	if err != nil {
		return err
	}
	cfg.Root = defaultServeRootDir(rootDir)
	c, done, err := newController(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer done()

	ch := make(chan client.UsageInfo)
	donePrint := make(chan struct{})
	tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
	first := true
	total := int64(0)
	go func() {
		defer close(donePrint)
		for du := range ch {
			total += du.Size
			if first {
				fmt.Fprintln(tw, "ID\tRECLAIMABLE\tSIZE")
				first = false
			}
			fmt.Fprintf(tw, "%-71s\t%-11v\t%s\n", du.ID, !du.InUse, fmt.Sprintf("%d B", du.Size))
			tw.Flush()
		}
	}()
	var opts []client.PruneOption
	if clicontext.Bool("all") {
		opts = append(opts, client.PruneAll)
	}

	err = c.Prune(ctx, ch, opts...)
	close(ch)
	<-donePrint
	if err != nil {
		return err
	}
	fmt.Fprintf(tw, "Total:\t%d B\n", total)
	tw.Flush()

	return nil
}

func duAction(clicontext *cli.Context) error {
	ctx, ctxCancel := context.WithCancel(context.Background())
	globalSignalHandler = &signalHandler{
		handler: func(sig os.Signal) error {
			ctxCancel()
			return nil
		},
	}
	globalSignalHandler.start()

	// Parse config options
	cfg, rootDir, err := parseGlobalWorkerConfig(clicontext)
	if err != nil {
		return err
	}
	cfg.Root = defaultServeRootDir(rootDir)
	c, done, err := newController(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer done()

	tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
	fmt.Fprintln(tw, "ID\tRECLAIMABLE\tSIZE")
	dus, err := c.DiskUsage(ctx)
	if err != nil {
		return err
	}
	total := int64(0)
	for _, du := range dus {
		total += du.Size
		fmt.Fprintf(tw, "%-71s\t%-11v\t%s\n", du.ID, !du.InUse, fmt.Sprintf("%d B", du.Size))
		tw.Flush()
	}
	fmt.Fprintf(tw, "Total:\t%d B\n", total)
	tw.Flush()

	return nil
}

func parseGlobalWorkerConfig(clicontext *cli.Context) (cfg *config.Config, rootDir string, err error) {
	cfg = &config.Config{}
	cfg.Workers.OCI.Rootless = clicontext.GlobalBool("rootless")
	cfg.Workers.OCI.NetworkConfig = config.NetworkConfig{
		Mode:          clicontext.GlobalString("oci-worker-net"),
		CNIConfigPath: clicontext.GlobalString("oci-cni-config-path"),
		CNIBinaryPath: clicontext.GlobalString("oci-cni-binary-path"),
	}
	rootDir = clicontext.GlobalString("root")
	if rootDir == "" {
		rootDir, err = rootDataDir(cfg.Workers.OCI.Rootless)
		if err != nil {
			return nil, "", err
		}
		if err := os.MkdirAll(rootDir, 0700); err != nil {
			return nil, "", err
		}
	}
	return cfg, rootDir, nil
}

func parseWorkerConfig(clicontext *cli.Context) (*config.Config, func(), error) {
	cfg, rootDir, err := parseGlobalWorkerConfig(clicontext)
	if err != nil {
		return nil, nil, err
	}
	var serveRoot string
	if clicontext.Bool("cache-reuse") {
		// common location for storing caches for reuse.
		// TODO: multiple concurrent build isn't supported as of now
		// TODO: make this option true by defaut once we support multiple concurrent build
		serveRoot = defaultServeRootDir(rootDir)
	}
	done := func() {}
	if serveRoot == "" {
		tmpServeRoot, err := os.MkdirTemp(rootDir, "buildg")
		if err != nil {
			return nil, nil, err
		}
		done = func() {
			if err := os.RemoveAll(tmpServeRoot); err != nil {
				logrus.WithError(err).Warnf("failed to cleanup %q", serveRoot)
			}
		}
		serveRoot = tmpServeRoot
	}
	cfg.Root = serveRoot
	return cfg, done, nil
}

// TODO:
// - cache importer/exporter
// - multi-platform
func parseSolveOpt(clicontext *cli.Context) (*client.SolveOpt, error) {
	buildContext := clicontext.Args().First()
	if buildContext == "" {
		return nil, fmt.Errorf("context needs to be specified")
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
	attachable := []session.Attachable{authprovider.NewDockerAuthProvider(os.Stderr)}
	if ssh := clicontext.StringSlice("ssh"); len(ssh) > 0 {
		configs, err := build.ParseSSH(ssh)
		if err != nil {
			return nil, err
		}
		sp, err := sshprovider.NewSSHAgentProvider(configs)
		if err != nil {
			return nil, err
		}
		attachable = append(attachable, sp)
	}
	if secrets := clicontext.StringSlice("secret"); len(secrets) > 0 {
		secretProvider, err := build.ParseSecret(secrets)
		if err != nil {
			return nil, err
		}
		attachable = append(attachable, secretProvider)
	}
	var cacheImports []client.CacheOptionsEntry
	if cacheFrom := clicontext.StringSlice("cache-from"); len(cacheFrom) > 0 {
		var cacheImportOpts []string
		for _, opt := range cacheFrom {
			if !strings.Contains(opt, "type=") {
				opt = "type=registry,ref=" + opt
			}
			cacheImportOpts = append(cacheImportOpts, opt)
		}
		cacheImports, err = build.ParseImportCache(cacheImportOpts)
		if err != nil {
			return nil, err
		}
	}
	return &client.SolveOpt{
		Exports:       exports,
		LocalDirs:     localDirs,
		FrontendAttrs: frontendAttrs,
		Session:       attachable,
		CacheImports:  cacheImports,
		// CacheExports:
	}, nil
}

// newController creates controller client based on the passed config.
// optional debugController allows adding debugger support to the controller.
func newController(ctx context.Context, cfg *config.Config, debugController *debugController) (_ *client.Client, _ func(), retErr error) {
	if cfg.Root == "" {
		return nil, nil, fmt.Errorf("root directory must be specified")
	}
	var closeFuncs []func()
	done := func() {
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
	w, resolverFunc, err := newWorker(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	if debugController != nil {
		w = debugController.debugWorker(w)
	}
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
	gwfrontend := gateway.NewGatewayFrontend(wc)
	if debugController != nil {
		gwfrontend = debugController.frontendWithDebug(gwfrontend)
	}
	frontends := map[string]frontend.Frontend{}
	frontends["gateway.v0"] = gwfrontend
	remoteCacheImporterFuncs := map[string]remotecache.ResolveCacheImporterFunc{
		"registry": registryremotecache.ResolveCacheImporterFunc(sessionManager, w.ContentStore(), resolverFunc),
		"local":    localremotecache.ResolveCacheImporterFunc(sessionManager),
	}
	controller, err := control.NewController(control.Opt{
		SessionManager:            sessionManager,
		WorkerController:          wc,
		CacheKeyStorage:           cacheStorage,
		Frontends:                 frontends,
		ResolveCacheImporterFuncs: remoteCacheImporterFuncs,
		ResolveCacheExporterFuncs: map[string]remotecache.ResolveCacheExporterFunc{}, // TODO: support remote cahce exporter
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
	c, err := client.New(ctx, "", client.WithContextDialer(lt.dial))
	if err != nil {
		return nil, nil, err
	}

	return c, done, nil
}

func newWorker(ctx context.Context, cfg *config.Config) (worker.Worker, docker.RegistryHosts, error) {
	root := cfg.Root
	if root == "" {
		return nil, nil, fmt.Errorf("failed to init worker: root directory must be set")
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
		return nil, nil, err
	}
	resolverFunc := resolver.NewRegistryConfig(cfg.Registries)
	opt.RegistryHosts = resolverFunc
	w, err := base.NewWorker(ctx, opt)
	if err != nil {
		return nil, nil, err
	}
	return w, resolverFunc, nil
}

func defaultServeRootDir(rootDir string) string {
	return filepath.Join(rootDir, "data")
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
