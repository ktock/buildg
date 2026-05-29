package buildkit

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/containerd/console"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/sirupsen/logrus"
)

type ContainerConfig struct {
	GatewayClient  gwclient.Client
	Info           *RegisteredStatus
	Args           []string
	Tty            bool
	Stdin          io.ReadCloser
	Stdout, Stderr io.WriteCloser
	Image          gwclient.Reference
	Mountroot      string
	InputMount     bool
	Env            []string
	Cwd            string
	WatchSignal    func(ctx context.Context, proc gwclient.ContainerProcess, con console.Console)
	NoSetRaw       bool // TODO: FIXME: execContainer should be agnostic about console config
}

func ExecContainer(ctx context.Context, cfg ContainerConfig) (_ gwclient.ContainerProcess, _ func(), retErr error) {
	op := cfg.Info.Op
	mountIDs := cfg.Info.MountIDs
	if cfg.InputMount {
		mountIDs = cfg.Info.InputIDs
	}
	var exec *pb.ExecOp
	switch op := op.GetOp().(type) {
	case *pb.Op_Exec:
		exec = op.Exec
	default:
		logrus.Infof("container execution on non-RUN instruction is experimental")
		if len(mountIDs) != 1 {
			return nil, nil, fmt.Errorf("one rootfs mount must be specified")
		}
	}

	var cleanups []func()
	defer func() {
		if retErr != nil {
			for i := len(cleanups) - 1; i >= 0; i-- {
				cleanups[i]()
			}
		}
	}()

	var networkMode pb.NetMode
	var securityMode pb.SecurityMode
	var mounts []gwclient.Mount
	var env []string
	var user string
	var cwd string
	if exec != nil {
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
		networkMode = exec.Network
		meta := exec.Meta
		cwd = meta.Cwd
		if cfg.Cwd != "" {
			cwd = cfg.Cwd
		}
		env = append(meta.Env, cfg.Env...)
		user = meta.User
		securityMode = exec.Security
	} else {
		logrus.Warnf("No execution info is provided from Op; Running a container with a default configuration")
		mounts = []gwclient.Mount{
			{
				Dest:      "/",
				ResultID:  mountIDs[0],
				MountType: pb.MountType_BIND,
			},
		}
		networkMode = pb.NetMode_NONE // TODO: support network
		cwd = "/"
		if cfg.Cwd != "" {
			cwd = cfg.Cwd
		}
		env = append([]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, cfg.Env...)
		user = ""
		securityMode = pb.SecurityMode_SANDBOX
	}
	if cfg.Image != nil {
		for i := range mounts {
			mounts[i].Dest = filepath.Join(cfg.Mountroot, mounts[i].Dest)
		}
		mounts = append([]gwclient.Mount{
			{
				Dest:      "/",
				MountType: pb.MountType_BIND,
				Ref:       cfg.Image,
			},
		}, mounts...)
	}
	ctr, err := cfg.GatewayClient.NewContainer(ctx, gwclient.NewContainerRequest{
		Mounts:      mounts,
		NetMode:     networkMode,
		Platform:    op.Platform,
		Constraints: op.Constraints,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create debug container: %v", err)
	}
	cleanups = append(cleanups, func() { ctr.Release(ctx) })
	proc, err := ctr.Start(ctx, gwclient.StartRequest{
		Args:         cfg.Args,
		Env:          env,
		User:         user,
		Cwd:          cwd,
		Tty:          cfg.Tty,
		Stdin:        cfg.Stdin,
		Stdout:       cfg.Stdout,
		Stderr:       cfg.Stderr,
		SecurityMode: securityMode,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start container: %w", err)
	}

	var con console.Console
	if cfg.Tty && !cfg.NoSetRaw {
		con = console.Current()
		if err := con.SetRaw(); err != nil {
			return nil, nil, fmt.Errorf("failed to configure terminal: %v", err)
		}
		cleanups = append(cleanups, func() { con.Reset() })
	}
	if cfg.WatchSignal != nil {
		ioCtx, ioCancel := context.WithCancel(ctx)
		cleanups = append(cleanups, func() { ioCancel() })
		cfg.WatchSignal(ioCtx, proc, con)
	}
	return proc, func() {
		logrus.Debugf("cleaning up container exec")
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
		logrus.Debugf("finished container exec")
	}, nil
}
