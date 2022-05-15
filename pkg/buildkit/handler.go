package buildkit

import (
	"context"
	"sync"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/sirupsen/logrus"
)

func withDebug(f gwclient.BuildFunc, debugController *debugController, cfg DebugConfig) (gwclient.BuildFunc, <-chan error) {
	handleError := make(chan error)
	return func(ctx context.Context, c gwclient.Client) (res *gwclient.Result, err error) {
		b := cfg.Breakpoints
		if b == nil {
			b = &Breakpoints{
				breakpoints: map[string]Breakpoint{
					"on-fail": NewOnFailBreakpoint(),
				},
			}
		}
		handler := &Handler{
			gwclient:           c,
			breakpoints:        b,
			breakpointHandler:  cfg.BreakpointHandler,
			stopOnEntry:        cfg.StopOnEntry,
			disableBreakpoints: cfg.DisableBreakpoints,
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

		if cfg.DebugImage != "" {
			def, err := llb.Image(cfg.DebugImage, withDescriptor(map[string]string{"debug": "no"})).Marshal(ctx)
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

type Handler struct {
	gwclient gwclient.Client

	breakpoints     *Breakpoints
	breakEachVertex bool

	entried            bool
	stopOnEntry        bool
	disableBreakpoints bool

	mu sync.Mutex

	image   gwclient.Reference
	imageMu sync.Mutex

	breakpointHandler BreakpointHandler
}

func (h *Handler) Breakpoints() *Breakpoints {
	return h.breakpoints
}

func (h *Handler) GatewayClient() gwclient.Client {
	return h.gwclient
}

func (h *Handler) DebuggerImage() gwclient.Reference {
	h.imageMu.Lock()
	defer h.imageMu.Unlock()
	return h.image
}

func (h *Handler) BreakEachVertex(b bool) {
	h.breakEachVertex = b
}

func (h *Handler) handle(ctx context.Context, info *RegisteredStatus, locs []*Location) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(locs) == 0 {
		logrus.Warnf("no location info: %v", locs)
		return nil
	}
	isBreakpoint := false
	hits := make(map[string]BreakpointInfo)
	for key, bp := range getHitBreakpoints(ctx, h.breakpoints, info, locs) {
		hits[key] = bp
		isBreakpoint = true
	}
	if h.disableBreakpoints || !(h.stopOnEntry && !h.entried) && !h.breakEachVertex && !isBreakpoint {
		logrus.Debugf("skipping non-breakpoint: %v", locs)
		return nil
	}
	if !h.entried {
		logrus.Infof("debug session started. type \"help\" for command reference.")
		h.entried = true
	}
	return h.breakpointHandler(ctx, BreakContext{h, info, locs, hits})
}

func getHitBreakpoints(ctx context.Context, b *Breakpoints, info *RegisteredStatus, locs []*Location) (hit map[string]BreakpointInfo) {
	hit = make(map[string]BreakpointInfo)
	b.ForEach(func(key string, bp Breakpoint) bool {
		if yes, desc, hits, err := bp.isTarget(ctx, info, locs); err != nil {
			logrus.WithError(err).Warnf("failed to check breakpoint")
		} else if yes {
			hit[key] = BreakpointInfo{desc, hits}
		}
		return true
	})
	return
}
