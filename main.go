package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/containerd/pkg/userns"
	dockerconfig "github.com/docker/cli/cli/config"
	"github.com/ktock/buildg/pkg/buildkit"
	"github.com/ktock/buildg/pkg/dap"
	"github.com/ktock/buildg/pkg/version"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/cmd/buildctl/build"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/session/sshforward/sshprovider"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

func main() {
	app := cli.NewApp()
	app.Usage = "Interactive debugger for Dockerfile"
	app.Version = version.Version
	var flags []cli.Flag
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
			Name:  "oci-worker-snapshotter",
			Usage: "Worker snapshotter: \"auto\", \"overlayfs\", \"native\"",
			Value: "auto",
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
		cli.StringFlag{
			Name:   "rootlesskit-args",
			Usage:  "Change arguments for rootlesskit in JSON format",
			EnvVar: "BUILDG_ROOTLESSKIT_ARGS",
			Value:  "",
		},
	}, flags...)
	app.Commands = []cli.Command{
		newDebugCommand(),
		newDuCommand(),
		newPruneCommand(),
		newDapCommand(),
		newVersionCommand(),
	}
	app.Before = func(context *cli.Context) error {
		logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
		if context.GlobalBool("debug") {
			logrus.SetLevel(logrus.DebugLevel)
		}
		if os.Geteuid() != 0 {
			// Running by nonroot user. Enter to the rootless mode.
			if err := reexecRootless(context); err != nil {
				fmt.Fprintf(os.Stderr, "failed to run by non-root user: %v\n", err)
			}
			os.Exit(1) // shouldn't reach here if reexec succeeds
		}
		if userns.RunningInUserNS() && os.Getenv("ROOTLESSKIT_STATE_DIR") != "" && os.Getenv("_BUILDG_ROOTLESSKIT_ENABLED") != "" {
			// Running in the rootlesskit user namespace created for buildg. Do preparation for this environment.
			if err := prepareRootlessChild(); err != nil {
				fmt.Fprintf(os.Stderr, "failed to prepare rootless child: %v\n", err)
				os.Exit(1)
			}
		}
		return nil
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "%+v\n", err)
		os.Exit(1)
	}
}

func reexecRootless(context *cli.Context) error {
	arg0, err := exec.LookPath("rootlesskit")
	if err != nil {
		return err
	}
	var args []string
	if argsStr := context.String("rootlesskit-args"); argsStr != "" {
		if json.Unmarshal([]byte(argsStr), &args); err != nil {
			return fmt.Errorf("failed to parse \"--rootlesskit-args\": %v", err)
		}
	}
	if len(args) == 0 {
		args = []string{
			"--net=slirp4netns",
			"--copy-up=/etc",
			"--copy-up=/run",
			"--disable-host-loopback",
		}
	}
	args = append(args, os.Args...)
	logrus.Debugf("running rootlesskit with args: %+v", args)
	// Tell the child process that this is the namespace for buildg
	env := append(os.Environ(), "_BUILDG_ROOTLESSKIT_ENABLED=1")
	return syscall.Exec(arg0, args, env)
}

func prepareRootlessChild() error {
	// rootlesskit creates the "copied-up" symlink on `/run/runc` which is not accessible
	// from rootless user. We don't need this because we create runc rootdir for our own usage.
	runcRoot := "/run/runc"
	if _, err := os.Lstat(runcRoot); err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to do
		}
		return err
	}
	rInfo, err := os.Lstat(runcRoot)
	if err != nil {
		return fmt.Errorf("failed to stat runc root: %v", err)
	}
	if mode := rInfo.Mode(); mode&fs.ModeSymlink == 0 {
		return fmt.Errorf("unexpected runc root file mode: %v", mode)
	}
	return os.Remove("/run/runc")
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
			cli.BoolTFlag{
				Name:  "cache-reuse",
				Usage: "Reuse locally cached previous results.",
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

func newDapCommand() cli.Command {
	return cli.Command{
		Name:  "dap",
		Usage: "DAP utilities",
		Subcommands: []cli.Command{
			newDapServeCommand(),
			newDapAttachContainerCommand(),
			newDapPruneCommand(),
			newDapDuCommand(),
		},
	}
}

func newDapPruneCommand() cli.Command {
	return cli.Command{
		Name:   "prune",
		Usage:  "prune DAP cache",
		Action: dapPruneAction,
		Flags: []cli.Flag{
			cli.BoolFlag{
				Name:  "all",
				Usage: "Prune including internal/frontend references",
			},
		},
	}
}

func newDapAttachContainerCommand() cli.Command {
	return cli.Command{
		Name: dap.AttachContainerCommand,
		Flags: []cli.Flag{
			cli.BoolFlag{
				Name:  "set-tty-raw",
				Usage: "Set tty raw",
			},
		},
		Action: dapAttachContainerAction,
		Hidden: true,
	}
}

func newDapServeCommand() cli.Command {
	return cli.Command{
		Name:   "serve",
		Usage:  "serve DAP via stdio",
		Action: dapServeAction,
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "log-file",
				Usage: "Path to the file to output logs",
			},
		},
	}
}

func newDapDuCommand() cli.Command {
	return cli.Command{
		Name:   "du",
		Usage:  "show disk usage of DAP cache",
		Action: dapDuAction,
	}
}

func debugAction(clicontext *cli.Context) error {
	ctx, ctxCancel := context.WithCancel(context.Background())
	sigHandler := &signalHandler{
		handler: func(sig os.Signal) error {
			ctxCancel()
			return nil
		},
	}
	sigHandler.start()

	// Parse build options
	solveOpt, err := parseSolveOpt(clicontext)
	if err != nil {
		return err
	}

	// Parse config options
	cfg, rootDir, err := parseGlobalWorkerConfig(clicontext)
	if err != nil {
		return err
	}
	var serveRoot string
	var cleanupAll bool
	if clicontext.Bool("cache-reuse") {
		// common location for storing caches for reuse.
		// TODO: multiple concurrent build isn't supported as of now
		defaultRoot := defaultServeRootDir(rootDir)
		if err := os.MkdirAll(defaultRoot, 0700); err != nil {
			return err
		}
		ok, unlock, err := tryLockOnBuildKitRootDir(defaultRoot)
		if err != nil {
			return err
		} else if ok {
			defer unlock()
			serveRoot = defaultRoot
		} else {
			// Failed to get lock on the root dir. Fallback to the temporary location. Cache won't be used.
			// TODO: add option to disable the fallback behaviour
			// TODO: allow sharing root dir among instances
			logrus.Warnf("Disabling cache because failed to acquire lock on the root dir %q; other buildg instance is running?", defaultRoot)
		}
	}
	if serveRoot == "" {
		tmpServeRoot, err := os.MkdirTemp(rootDir, "buildg")
		if err != nil {
			return err
		}
		defer func() {
			if err := os.RemoveAll(tmpServeRoot); err != nil {
				logrus.WithError(err).Warnf("failed to cleanup %q", serveRoot)
			}
		}()
		serveRoot = tmpServeRoot
		cleanupAll = true
	}
	cfg.Root = serveRoot
	logrus.Debugf("root dir: %q", cfg.Root)

	r := newSharedReader(os.Stdin)
	defer r.close()
	f, err := os.CreateTemp(serveRoot, "buildg-log")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	logrus.Debugf("log file: %q", f.Name())
	progressWriter := newProgressWriter(os.Stderr, f)
	h := newCommandHandler(r, os.Stdout, sigHandler)
	bp := buildkit.NewBreakpoints()
	if _, err := bp.Add("on-fail", buildkit.NewOnFailBreakpoint()); err != nil {
		return err
	}
	return buildkit.Debug(ctx, cfg, solveOpt, progressWriter, buildkit.DebugConfig{
		BreakpointHandler: func(ctx context.Context, bCtx buildkit.BreakContext) error {
			progressWriter.disable()
			defer progressWriter.enable()
			return h.breakHandler(ctx, bCtx, progressWriter)
		},
		Breakpoints: bp,
		DebugImage:  clicontext.String("image"),
		StopOnEntry: true,
		CleanupAll:  cleanupAll,
	})
}

func pruneAction(clicontext *cli.Context) error {
	ctx, ctxCancel := context.WithCancel(context.Background())
	sigHandler := &signalHandler{
		handler: func(sig os.Signal) error {
			ctxCancel()
			return nil
		},
	}
	sigHandler.start()

	// Parse config options
	cfg, rootDir, err := parseGlobalWorkerConfig(clicontext)
	if err != nil {
		return err
	}
	serveRoot := defaultServeRootDir(rootDir)
	if err := os.MkdirAll(serveRoot, 0700); err != nil {
		return err
	}
	ok, unlock, err := tryLockOnBuildKitRootDir(serveRoot)
	if err != nil {
		return err
	} else if ok {
		defer unlock()
	} else {
		return fmt.Errorf("failed to acquire lock on the root dir; other buildg instance is running?")
	}
	cfg.Root = serveRoot
	return buildkit.Prune(ctx, cfg, clicontext.Bool("all"), os.Stdout)
}

func duAction(clicontext *cli.Context) error {
	ctx, ctxCancel := context.WithCancel(context.Background())
	sigHandler := &signalHandler{
		handler: func(sig os.Signal) error {
			ctxCancel()
			return nil
		},
	}
	sigHandler.start()

	// Parse config options
	cfg, rootDir, err := parseGlobalWorkerConfig(clicontext)
	if err != nil {
		return err
	}
	serveRoot := defaultServeRootDir(rootDir)
	if err := os.MkdirAll(serveRoot, 0700); err != nil {
		return err
	}
	ok, unlock, err := tryLockOnBuildKitRootDir(serveRoot)
	if err != nil {
		return err
	} else if ok {
		defer unlock()
	} else {
		return fmt.Errorf("failed to acquire lock on the root dir; other buildg instance is running?")
	}
	cfg.Root = serveRoot
	return buildkit.Du(ctx, cfg, os.Stdout)
}

func dapServeAction(clicontext *cli.Context) error {
	logrus.SetOutput(os.Stderr)
	if logFile := clicontext.String("log-file"); logFile != "" {
		f, _ := os.Create(logFile)
		logrus.SetOutput(f)
	}
	cfg, rootDir, err := parseGlobalWorkerConfig(clicontext)
	if err != nil {
		return err
	}

	// Determine root dir to use
	rootDir, serveRoot := defaultDAPRootDir(rootDir)
	if err := os.MkdirAll(serveRoot, 0700); err != nil {
		return err
	}
	var cleanupFunc func() error
	var cleanupAll bool
	ok, unlock, err := tryLockOnBuildKitRootDir(serveRoot)
	if err != nil {
		return err
	} else if ok {
		cleanupFunc = unlock
	} else {
		// failed to get lock; use temporary root
		logrus.Warnf("Disabling cache because failed to acquire lock on the root dir %q; other buildg dap session is running?", serveRoot)
		// NOTE: all previous cache is ignored as of now.
		// TODO: eliminate this limitation
		serveRoot, err = os.MkdirTemp(rootDir, "buildg")
		if err != nil {
			return err
		}
		cleanupAll = true
		cleanupFunc = func() error {
			return os.RemoveAll(serveRoot)
		}
	}
	cfg.Root = serveRoot
	s, err := dap.NewServer(&stdioConn{os.Stdin, os.Stdout}, cfg, cleanupFunc, cleanupAll)
	if err != nil {
		return err
	}
	// NOTE: disconnect request will exit this process
	// TODO: disconnect should return the control to this func rather than exit
	if err := s.Serve(); err != nil {
		logrus.WithError(err).Warnf("failed to serve") // TODO: should return error
	}
	logrus.Info("finishing server")
	return nil
}

func dapAttachContainerAction(clicontext *cli.Context) error {
	root := clicontext.Args().First()
	if root == "" {
		return fmt.Errorf("root needs to be specified")
	}
	return dap.AttachContainerIO(root, clicontext.Bool("set-tty-raw"))
}
func dapPruneAction(clicontext *cli.Context) error {
	cfg, rootDir, err := parseGlobalWorkerConfig(clicontext)
	if err != nil {
		return err
	}
	_, serveRoot := defaultDAPRootDir(rootDir)
	if err := os.MkdirAll(serveRoot, 0700); err != nil {
		return err
	}
	ok, unlock, err := tryLockOnBuildKitRootDir(serveRoot)
	if err != nil {
		return err
	} else if ok {
		defer unlock()
	} else {
		return fmt.Errorf("failed to acquire lock on the root dir; other buildg dap session is running?")
	}
	cfg.Root = serveRoot
	return buildkit.Prune(context.TODO(), cfg, clicontext.Bool("all"), os.Stdout)
}

func dapDuAction(clicontext *cli.Context) error {
	cfg, rootDir, err := parseGlobalWorkerConfig(clicontext)
	if err != nil {
		return err
	}
	_, serveRoot := defaultDAPRootDir(rootDir)
	if err := os.MkdirAll(serveRoot, 0700); err != nil {
		return err
	}
	ok, unlock, err := tryLockOnBuildKitRootDir(serveRoot)
	if err != nil {
		return err
	} else if ok {
		defer unlock()
	} else {
		return fmt.Errorf("failed to acquire lock on the root dir; other buildg dap session is running?")
	}
	cfg.Root = serveRoot
	return buildkit.Du(context.TODO(), cfg, os.Stdout)
}

func parseGlobalWorkerConfig(clicontext *cli.Context) (cfg *config.Config, rootDir string, err error) {
	cfg = &config.Config{}
	cfg.Workers.OCI.Rootless = userns.RunningInUserNS()
	cfg.Workers.OCI.NetworkConfig = config.NetworkConfig{
		Mode:          clicontext.GlobalString("oci-worker-net"),
		CNIConfigPath: clicontext.GlobalString("oci-cni-config-path"),
		CNIBinaryPath: clicontext.GlobalString("oci-cni-binary-path"),
	}
	cfg.Workers.OCI.Snapshotter = clicontext.GlobalString("oci-worker-snapshotter")
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
	attachable := []session.Attachable{authprovider.NewDockerAuthProvider(dockerconfig.LoadDefaultConfigFile(os.Stderr))}
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

func defaultServeRootDir(rootDir string) string {
	return filepath.Join(rootDir, "data")
}

func defaultDAPRootDir(rootDir string) (dapRootDir string, dapServeRoot string) {
	dapRootDir = filepath.Join(rootDir, "dap") // use a dedicated root dir for dap as of now; TODO: share cache globally
	return dapRootDir, filepath.Join(dapRootDir, "buildg")
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

func tryLockOnBuildKitRootDir(dir string) (ok bool, unlock func() error, retErr error) {
	f, err := os.Create(filepath.Join(dir, "lock"))
	if err != nil {
		return false, nil, err
	}
	defer func() {
		if !ok || retErr != nil {
			f.Close()
		}
	}()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		logrus.Debugf("acquired lock on the root dir %q", dir)
		return true, func() (err error) {
			if uErr := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); uErr != nil {
				err = uErr
			}
			if cErr := f.Close(); cErr != nil {
				err = cErr
			}
			return err
		}, nil
	} else if err == syscall.EWOULDBLOCK {
		return false, func() error { return nil }, nil
	} else {
		return false, nil, err
	}
}

type stdioConn struct {
	io.Reader
	io.Writer
}

func (c *stdioConn) Read(b []byte) (n int, err error) {
	return c.Reader.Read(b)
}
func (c *stdioConn) Write(b []byte) (n int, err error) {
	return c.Writer.Write(b)
}
func (c *stdioConn) Close() error                       { return nil }
func (c *stdioConn) LocalAddr() net.Addr                { return dummyAddr{} }
func (c *stdioConn) RemoteAddr() net.Addr               { return dummyAddr{} }
func (c *stdioConn) SetDeadline(_ time.Time) error      { return nil }
func (c *stdioConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *stdioConn) SetWriteDeadline(_ time.Time) error { return nil }

type dummyAddr struct{}

func (a dummyAddr) Network() string { return "dummy" }
func (a dummyAddr) String() string  { return "dummy" }

func newProgressWriter(dst console.File, logFile *os.File) *progressWriter {
	return &progressWriter{File: dst, enabled: true, logFile: logFile, logFilePath: logFile.Name()}
}

type progressWriter struct {
	console.File
	enabled     bool
	buf         []byte
	mu          sync.Mutex
	logFile     *os.File
	logFilePath string
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

func (w *progressWriter) buffered() io.Reader {
	w.mu.Lock()
	defer w.mu.Unlock()
	var buf []byte
	if len(w.buf) > 0 {
		buf = w.buf
	}
	return bytes.NewReader(buf)
}

func (w *progressWriter) reader() (io.ReadCloser, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return os.Open(w.logFilePath)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.logFile != nil {
		if _, err := w.logFile.Write(p); err != nil {
			logrus.Debugf("failed to writer log to file: %v", err)
		}
	}
	if !w.enabled {
		w.buf = append(w.buf, p...) // TODO: add limit
		return len(p), nil
	}
	return w.File.Write(p)
}

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
