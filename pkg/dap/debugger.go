package dap

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	dockerconfig "github.com/docker/cli/cli/config"
	"github.com/ktock/buildg/pkg/buildkit"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/cmd/buildctl/build"
	"github.com/moby/buildkit/cmd/buildkitd/config"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/session/sshforward/sshprovider"
	"github.com/sirupsen/logrus"
)

type debugger struct {
	mesCh     chan string
	bps       *buildkit.Breakpoints
	config    *config.Config
	stoppedCh chan []int

	lConfig     *LaunchConfig
	debugCancel func() error

	bCtx       *buildkit.BreakContext
	cleanupAll bool
}

func newDebugger(cfg *config.Config, cleanupAll bool) (*debugger, error) {
	bp := buildkit.NewBreakpoints()
	if _, err := bp.Add("on-fail", buildkit.NewOnFailBreakpoint()); err != nil {
		return nil, err
	}
	return &debugger{
		mesCh:      make(chan string),
		bps:        bp,
		config:     cfg,
		stoppedCh:  make(chan []int),
		cleanupAll: cleanupAll,
	}, nil
}

func (d *debugger) launchConfig() *LaunchConfig {
	return d.lConfig
}

func (d *debugger) launch(cfg LaunchConfig, onStartHook, onFinishHook func(), stopOnEntry, disableBreakpoints bool, pw io.Writer) error {
	if d.lConfig != nil {
		// debugger has already been lauched
		return fmt.Errorf("multi session unsupported")
	}

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	doneCh := make(chan struct{})
	defer close(doneCh)
	d.debugCancel = func() error {
		cancel()
		<-doneCh
		logrus.Debugf("debug finished")
		return nil
	}

	solveOpt, err := parseDAPSolveOpt(cfg)
	if err != nil {
		return err
	}

	logrus.Debugf("root dir: %v", d.config.Root)
	defer logrus.Debugf("done debug on %v", d.config.Root)

	d.lConfig = &cfg
	onStartHook()
	err = buildkit.Debug(ctx, d.config, solveOpt, pw, buildkit.DebugConfig{
		BreakpointHandler:  d.breakHandler,
		DebugImage:         cfg.Image,
		Breakpoints:        d.bps,
		StopOnEntry:        stopOnEntry,
		DisableBreakpoints: disableBreakpoints,
		CleanupAll:         d.cleanupAll,
	})
	onFinishHook()
	return err
}

func parseDAPSolveOpt(cfg LaunchConfig) (*client.SolveOpt, error) {
	if cfg.Program == "" {
		return nil, fmt.Errorf("program must be specified")
	}

	var optStr []string
	dir, file := filepath.Split(cfg.Program)
	localDirs, err := build.ParseLocal([]string{
		"context=" + dir,
		"dockerfile=" + dir,
	})
	if err != nil {
		return nil, err
	}
	optStr = append(optStr, "filename="+file)
	if target := cfg.Target; target != "" {
		optStr = append(optStr, "target="+target)
	}
	for _, ba := range cfg.BuildArgs {
		optStr = append(optStr, "build-arg:"+ba)
	}
	frontendAttrs, err := build.ParseOpt(optStr)
	if err != nil {
		return nil, err
	}
	exports, err := build.ParseOutput([]string{"type=image"})
	if err != nil {
		return nil, err
	}
	attachable := []session.Attachable{authprovider.NewDockerAuthProvider(dockerconfig.LoadDefaultConfigFile(os.Stderr))}
	if ssh := cfg.SSH; len(ssh) > 0 {
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
	if secrets := cfg.Secrets; len(secrets) > 0 {
		secretProvider, err := build.ParseSecret(secrets)
		if err != nil {
			return nil, err
		}
		attachable = append(attachable, secretProvider)
	}
	return &client.SolveOpt{
		Exports:       exports,
		LocalDirs:     localDirs,
		FrontendAttrs: frontendAttrs,
		Session:       attachable,
		// CacheImports:
		// CacheExports:
	}, nil
}

func (d *debugger) cancel() error {
	close(d.stoppedCh)
	if d.debugCancel == nil {
		return nil
	}
	if err := d.debugCancel(); err != nil {
		logrus.WithError(err).Warnf("failed to close")
	}
	return nil
}

func (d *debugger) breakpoints() *buildkit.Breakpoints {
	return d.bps
}

func (d *debugger) doContinue() {
	if d.bCtx != nil && d.bCtx.Handler != nil {
		d.bCtx.Handler.BreakEachVertex(false)
		d.mesCh <- "continue"
	} else {
		logrus.Warnf("continue is reqested but no break happens")
	}
}

func (d *debugger) doNext() {
	if d.bCtx != nil && d.bCtx.Handler != nil {
		d.bCtx.Handler.BreakEachVertex(true)
		d.mesCh <- "next"
	} else {
		logrus.Warnf("next is reqested but no break happens")
	}
}

func (d *debugger) breakContext() *buildkit.BreakContext {
	return d.bCtx
}

func (d *debugger) stopped() <-chan []int {
	return d.stoppedCh
}

func (d *debugger) breakHandler(_ context.Context, bCtx buildkit.BreakContext) error {
	for key, desc := range bCtx.Hits {
		logrus.Debugf("Breakpoint[%s]: %s", key, desc)
	}
	bpIDs := make([]int, 0)
	for si := range bCtx.Hits {
		keyI, err := strconv.ParseInt(si, 10, 64)
		if err != nil {
			logrus.WithError(err).Warnf("failed to parse breakpoint key")
			continue
		}
		bpIDs = append(bpIDs, int(keyI))
	}
	d.stoppedCh <- bpIDs
	d.bCtx = &bCtx
	<-d.mesCh
	return nil
}
