package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/containerd/console"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/urfave/cli"
)

func execCommand(ctx context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:    "exec",
		Aliases: []string{"e"},
		Usage:   "Execute command in the step",
		UsageText: `exec [OPTIONS] [ARGS...]

If ARGS isn't provided, "/bin/sh" is used by default.
Only supported on RUN instructions as of now.
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
		Action: func(clicontext *cli.Context) error {
			args := clicontext.Args()
			if len(args) == 0 || args[0] == "" {
				args = []string{"/bin/sh"}
			}
			flagI := clicontext.Bool("i")
			flagT := clicontext.Bool("tty")
			if flagI && !flagT || !flagI && flagT {
				return fmt.Errorf("flag \"-i\" and \"-t\" must be set together") // FIXME
			}
			h := hCtx.handler
			r, done := h.stdin.use()
			defer done()
			cfg := containerConfig{
				info:       hCtx.info,
				args:       args,
				stdout:     os.Stdout,
				stderr:     os.Stderr,
				tty:        flagT,
				mountroot:  clicontext.String("mountroot"),
				inputMount: clicontext.Bool("init-state"),
				env:        clicontext.StringSlice("env"),
				cwd:        clicontext.String("workdir"),
			}
			if clicontext.Bool("image") {
				h.imageMu.Lock()
				cfg.image = h.image
				h.imageMu.Unlock()
			}
			if flagI {
				cfg.stdin = io.NopCloser(r)
			}
			if err := h.execContainer(ctx, cfg); err != nil {
				return fmt.Errorf("process execution failed: %w", err)
			}
			return nil
		},
	}
}

type containerConfig struct {
	info           *registeredStatus
	args           []string
	tty            bool
	stdin          io.ReadCloser
	stdout, stderr io.WriteCloser
	image          gwclient.Reference
	mountroot      string
	inputMount     bool
	env            []string
	cwd            string
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
		return fmt.Errorf("this instruction doesn't support exec; try on RUN instructions")
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
		for i := range mounts {
			mounts[i].Dest = filepath.Join(cfg.mountroot, mounts[i].Dest)
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
	cwd := meta.Cwd
	if cfg.cwd != "" {
		cwd = cfg.cwd
	}
	proc, err := ctr.Start(ctx, gwclient.StartRequest{
		Args:         cfg.args,
		Env:          append(meta.Env, cfg.env...),
		User:         meta.User,
		Cwd:          cwd,
		Tty:          cfg.tty,
		Stdin:        cfg.stdin,
		Stdout:       cfg.stdout,
		Stderr:       cfg.stderr,
		SecurityMode: exec.Security,
	})
	if err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	var con console.Console
	if cfg.tty {
		con := console.Current()
		if err := con.SetRaw(); err != nil {
			return fmt.Errorf("failed to configure terminal: %v", err)
		}
		defer con.Reset()
	}
	ioCtx, ioCancel := context.WithCancel(ctx)
	defer ioCancel()
	watchSignal(ioCtx, proc, con)
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
					if con != nil {
						size, err := con.Size()
						if err != nil {
							continue
						}
						proc.Resize(ctx, gwclient.WinSize{
							Cols: uint32(size.Width),
							Rows: uint32(size.Height),
						})
					}
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
