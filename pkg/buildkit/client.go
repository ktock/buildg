package buildkit

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/containerd/containerd/remotes/docker"
	ctdsnapshots "github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/native"
	"github.com/containerd/containerd/snapshots/overlay"
	"github.com/containerd/containerd/snapshots/overlay/overlayutils"
	"github.com/moby/buildkit/cache/remotecache"
	localremotecache "github.com/moby/buildkit/cache/remotecache/local"
	registryremotecache "github.com/moby/buildkit/cache/remotecache/registry"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/control"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/frontend"
	dockerfile "github.com/moby/buildkit/frontend/dockerfile/builder"
	"github.com/moby/buildkit/frontend/gateway"
	"github.com/moby/buildkit/session"
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
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
)

var (
	ErrExit   = errors.New("exit")
	ErrReload = errors.New("reload")
)

type DebugConfig struct {
	BreakpointHandler  BreakpointHandler
	DebugImage         string
	Breakpoints        *Breakpoints
	StopOnEntry        bool
	DisableBreakpoints bool
	CleanupAll         bool
}

func Debug(ctx context.Context, cfg *config.Config, solveOpt *client.SolveOpt, progressWriter io.Writer, debugConfig DebugConfig) error {
	if debugConfig.BreakpointHandler == nil {
		return fmt.Errorf("BreakpointHandler must not be nil")
	}

	// Prepare controller
	debugController := newDebugController()
	var c *client.Client
	var doneController func()
	createdCh := make(chan error)
	errCh := make(chan error)
	go func() {
		var err error
		c, doneController, err = newClient(ctx, cfg, debugController)
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

	debugErr := debug(ctx, c, solveOpt, progressWriter, debugConfig, debugController)

	if debugConfig.CleanupAll {
		logrus.Infof("cleaning up cache")
		if err := c.Prune(context.TODO(), nil, client.PruneAll); err != nil {
			logrus.WithError(err).Warnf("failed to cleaning up cache")
		}
	}

	if debugErr != nil {
		if errors.Is(debugErr, ErrExit) {
			return nil
		}
		return debugErr
	}

	return nil
}

func debug(ctx context.Context, c *client.Client, solveOpt *client.SolveOpt, progressWriter io.Writer, debugConfig DebugConfig, debugController *debugController) error {
	// Prepare progress writer
	progressCtx := context.TODO()
	pw, err := progresswriter.NewPrinter(progressCtx, &nopConsoleFile{progressWriter}, "plain")
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
	buildFunc, handleErrorCh := withDebug(dockerfile.Build, debugController, debugConfig)
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

	if waitErr := eg.Wait(); waitErr != nil {
		if errors.Is(waitErr, ErrReload) {
			return debug(ctx, c, solveOpt, progressWriter, debugConfig, debugController)
		}
		return waitErr
	}

	return nil
}

func Prune(ctx context.Context, cfg *config.Config, all bool, w io.Writer) error {
	c, done, err := newClient(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer done()

	ch := make(chan client.UsageInfo)
	donePrint := make(chan struct{})
	tw := tabwriter.NewWriter(w, 1, 8, 1, '\t', 0)
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
	if all {
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

func Du(ctx context.Context, cfg *config.Config, w io.Writer) error {
	c, done, err := newClient(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer done()

	tw := tabwriter.NewWriter(w, 1, 8, 1, '\t', 0)
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

// newClient creates controller client based on the passed config.
// optional debugController allows adding debugger support to the controller.
func newClient(ctx context.Context, cfg *config.Config, debugController *debugController) (_ *client.Client, _ func(), retErr error) {
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
	snName := cfg.Workers.OCI.Snapshotter
	if snName == "auto" {
		if err := overlayutils.Supported(root); err == nil {
			snName = "overlayfs"
		} else {
			logrus.Debugf("overlayfs isn't supported. falling back to native snapshotter")
			snName = "native"
		}
		logrus.Debugf("%q is used as the auto snapshotter", snName)
	}
	var snFactory runc.SnapshotterFactory
	switch snName {
	case "native":
		snFactory = runc.SnapshotterFactory{
			Name: snName,
			New:  native.NewSnapshotter,
		}
	case "overlayfs":
		snFactory = runc.SnapshotterFactory{
			Name: snName,
			New: func(root string) (ctdsnapshots.Snapshotter, error) {
				return overlay.NewSnapshotter(root, overlay.AsynchronousRemove)
			},
		}
	default:
		return nil, nil, fmt.Errorf("unknown snapshotter %q", snName)
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

func (l *pipeListener) dial(_ context.Context, _ string) (net.Conn, error) {
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

type nopConsoleFile struct {
	io.Writer
}

func (f *nopConsoleFile) Close() error { return nil }

func (f *nopConsoleFile) Read(_ []byte) (int, error) { return 0, io.EOF }

func (f *nopConsoleFile) Fd() uintptr { return 0 }

func (f *nopConsoleFile) Name() string { return "dummy" }
