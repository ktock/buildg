package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/moby/buildkit/frontend"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	llberrdefs "github.com/moby/buildkit/solver/llbsolver/errdefs"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/worker"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func newDebugController() *debugController {
	return &debugController{
		eventCh: make(chan *registeredStatus),
		pause:   make(map[string]*chan struct{}),
	}
}

type debugController struct {
	eventCh chan *registeredStatus
	pause   map[string]*chan struct{}
	mu      sync.Mutex

	sources   map[*pb.Source]int
	sourcesMu sync.Mutex

	handleStarted bool
}

type location struct {
	source *pb.SourceInfo
	ranges []*pb.Range
}

func (l *location) String() string {
	return fmt.Sprintf("%q %+v", l.source.Filename, l.ranges)
}

func (d *debugController) handle(ctx context.Context, handler *handler) error {
	if d.handleStarted {
		return fmt.Errorf("on going handler exists")
	}
	d.handleStarted = true
	defer func() { d.handleStarted = false }()
	logrus.Debugf("starting listening debug events")
	defer logrus.Debugf("finishing listening debug events")
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-d.eventCh:
			logrus.Debugf("got debug event %q", msg.debugID)
			if locs, err := d.getLocation(msg.vertex.String()); err != nil {
				logrus.WithError(err).Debug("failed to get location info")
			} else {
				if err := handler.handle(ctx, msg, locs); err != nil {
					if err == nil && ctx.Err() != nil {
						err = ctx.Err()
					}
					return err
				}
			}
			d.continueID(msg.debugID)
		}
	}
}

func (d *debugController) addLocation(source *pb.Source) {
	d.sourcesMu.Lock()
	if d.sources == nil {
		d.sources = make(map[*pb.Source]int)
	}
	if _, ok := d.sources[source]; ok {
		d.sources[source]++
	} else {
		d.sources[source] = 1
	}
	d.sourcesMu.Unlock()
}

func (d *debugController) deleteLocation(source *pb.Source) {
	d.sourcesMu.Lock()
	if _, ok := d.sources[source]; ok {
		d.sources[source]--
		if d.sources[source] == 0 {
			delete(d.sources, source)
		}
	}
	d.sourcesMu.Unlock()
}

func (d *debugController) getLocation(v string) (locs []*location, err error) {
	d.sourcesMu.Lock()
	defer d.sourcesMu.Unlock()
	for s := range d.sources {
		if locsInfo, ok := s.Locations[v]; ok {
			for _, loc := range locsInfo.Locations {
				locs = append(locs, &location{s.Infos[loc.SourceIndex], loc.Ranges})
			}
		}
	}
	if len(locs) == 0 {
		return nil, fmt.Errorf("location info for vertex %v not found", v)
	}
	return
}

func (d *debugController) wait(id string) chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	pause := make(chan struct{})
	d.pause[id] = &pause
	return pause
}

func (d *debugController) continueID(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ch, ok := d.pause[id]; ok {
		close(*ch)
		delete(d.pause, id)
	}
}

func (d *debugController) frontendWithDebug(f frontend.Frontend) frontend.Frontend {
	return &debugFrontend{f, d}
}

type debugFrontend struct {
	frontend.Frontend
	debugController *debugController
}

func (f *debugFrontend) Solve(ctx context.Context, llb frontend.FrontendLLBBridge, opt map[string]string, inputs map[string]*pb.Definition, sid string, sm *session.Manager) (*frontend.Result, error) {
	return f.Frontend.Solve(ctx, &debugFrontendBridge{llb, f.debugController}, opt, inputs, sid, sm)
}

type debugFrontendBridge struct {
	frontend.FrontendLLBBridge
	debugController *debugController
}

func (f *debugFrontendBridge) Solve(ctx context.Context, req frontend.SolveRequest, sid string) (*frontend.Result, error) {
	req.Evaluate = true
	if req.Definition != nil && req.Definition.Source != nil {
		f.debugController.addLocation(req.Definition.Source)
		defer f.debugController.deleteLocation(req.Definition.Source)
	}
	return f.FrontendLLBBridge.Solve(ctx, req, sid)
}

func (d *debugController) gatewayClientWithDebug(c gwclient.Client) gwclient.Client {
	return &debugGatewayClient{c, d}
}

type debugGatewayClient struct {
	gwclient.Client
	debugController *debugController
}

func (c *debugGatewayClient) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	req.Evaluate = true
	if req.Definition != nil && req.Definition.Source != nil {
		c.debugController.addLocation(req.Definition.Source)
		defer c.debugController.deleteLocation(req.Definition.Source)
	}
	return c.Client.Solve(ctx, req)
}

func (d *debugController) debugWorker(w worker.Worker) *debugWorkerWrapper {
	return &debugWorkerWrapper{
		Worker:        w,
		workerRefByID: make(map[string]*worker.WorkerRef),
		controller:    d,
	}
}

type debugWorkerWrapper struct {
	worker.Worker
	workerRefByID   map[string]*worker.WorkerRef
	workerRefByIDMu sync.Mutex
	controller      *debugController
}

func (d *debugWorkerWrapper) ResolveOp(v solver.Vertex, s frontend.FrontendLLBBridge, sm *session.Manager) (solver.Op, error) {
	op, err := d.Worker.ResolveOp(v, s, sm)
	if err != nil {
		return nil, err
	}
	for descK, descV := range v.Options().Description {
		if descK == "debug" && descV == "no" {
			logrus.WithField("vertex", v.Digest().String()).Debugf("debug disabled on this vertex")
			return op, err
		}
	}
	return &debugOpWrapper{op, v, d}, nil
}

func (d *debugWorkerWrapper) WorkerRefByID(id string) (*worker.WorkerRef, bool) {
	d.workerRefByIDMu.Lock()
	r, ok := d.workerRefByID[id]
	d.workerRefByIDMu.Unlock()
	return r, ok
}

type status struct {
	inputs []solver.Result
	mounts []solver.Result
	vertex digest.Digest
	op     *pb.Op
	err    error
}

type registeredStatus struct {
	debugID  string
	inputIDs []string
	mountIDs []string
	vertex   digest.Digest
	op       *pb.Op
	err      error
}

func (d *debugWorkerWrapper) notifyAndWait(ctx context.Context, s status) error {
	inputIDs, err := d.registerResultIDs(s.inputs...)
	if err != nil {
		return err
	}
	mountIDs, err := d.registerResultIDs(s.mounts...)
	if err != nil {
		return err
	}
	id := identity.NewID()
	logrus.Debugf("notifying %q", id)
	waitCh := d.controller.wait(id)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case d.controller.eventCh <- &registeredStatus{
		debugID:  id,
		vertex:   s.vertex,
		op:       s.op,
		inputIDs: inputIDs,
		mountIDs: mountIDs,
		err:      s.err,
	}:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-waitCh:
	}
	return nil
}

func (d *debugWorkerWrapper) registerResultIDs(results ...solver.Result) (ids []string, err error) {
	ids = make([]string, len(results))
	for i, res := range results {
		if res == nil {
			continue
		}
		workerRef, ok := res.Sys().(*worker.WorkerRef)
		if !ok {
			return ids, errors.Errorf("unexpected type for result, got %T", res.Sys())
		}
		ids[i] = workerRef.ID()
		d.workerRefByIDMu.Lock()
		d.workerRefByID[workerRef.ID()] = workerRef
		d.workerRefByIDMu.Unlock()
	}
	return ids, nil
}

type debugOpWrapper struct {
	solver.Op
	vertex solver.Vertex
	worker *debugWorkerWrapper
}

func (o *debugOpWrapper) Exec(ctx context.Context, g session.Group, inputs []solver.Result) (results []solver.Result, err error) {
	var execInputs, execMounts []solver.Result

	outputs, err := o.Op.Exec(ctx, g, inputs)
	if err != nil {
		var ee *llberrdefs.ExecError
		if errors.As(err, &ee) {
			execInputs, execMounts = ee.Inputs, ee.Mounts
		}
	} else if execOp, ok := o.vertex.Sys().(*pb.Op).Op.(*pb.Op_Exec); ok {
		execInputs = make([]solver.Result, len(execOp.Exec.Mounts))
		for i, m := range execOp.Exec.Mounts {
			if m.Input < 0 {
				continue
			}
			execInputs[i] = inputs[m.Input].Clone()
		}
		execMounts = make([]solver.Result, len(execOp.Exec.Mounts))
		copy(execMounts, execInputs)
		for i, m := range execOp.Exec.Mounts {
			if m.Output < 0 {
				continue
			}
			execMounts[i] = outputs[m.Output].Clone()
		}
	}
	if nErr := o.worker.notifyAndWait(ctx, status{
		inputs: execInputs,
		mounts: execMounts,
		vertex: o.vertex.Digest(),
		op:     o.vertex.Sys().(*pb.Op),
		err:    err,
	}); nErr != nil {
		if err == nil {
			err = nErr
		}
	}

	return outputs, err
}
