package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/containerd/console"
	"github.com/ktock/buildg/pkg/buildkit"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/urfave/cli"
)

func execCommand(ctx context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:    "exec",
		Aliases: []string{"e"},
		Usage:   "Execute command in the step",
		UsageText: `exec [OPTIONS] [ARGS...]

If ARGS isn't provided, "/bin/sh" is used by default.
container execution on non-RUN instruction is experimental.
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
			r, done := hCtx.stdin.use()
			defer done()
			cfg := buildkit.ContainerConfig{
				Info:          hCtx.info,
				Args:          args,
				Stdout:        os.Stdout,
				Stderr:        os.Stderr,
				Tty:           flagT,
				Mountroot:     clicontext.String("mountroot"),
				InputMount:    clicontext.Bool("init-state"),
				Env:           clicontext.StringSlice("env"),
				Cwd:           clicontext.String("workdir"),
				WatchSignal:   watchSignal,
				GatewayClient: h.GatewayClient(),
			}
			if clicontext.Bool("image") {
				cfg.Image = h.DebuggerImage()
			}
			if flagI {
				cfg.Stdin = io.NopCloser(r)
			}
			hCtx.signalHandler.disable() // let the container catch signals
			defer hCtx.signalHandler.enable()
			proc, cleanup, err := buildkit.ExecContainer(ctx, cfg)
			if err != nil {
				return err
			}
			defer cleanup()
			errCh := make(chan error)
			doneCh := make(chan struct{})
			go func() {
				if err := proc.Wait(); err != nil {
					errCh <- err
					return
				}
				close(doneCh)
			}()
			select {
			case <-doneCh:
			case <-ctx.Done():
				return ctx.Err()
			case err := <-errCh:
				return fmt.Errorf("process execution failed: %w", err)
			}
			return nil
		},
	}
}

func watchSignal(ctx context.Context, proc gwclient.ContainerProcess, con console.Console) {
	ch := make(chan os.Signal, 1)
	signals := []os.Signal{syscall.SIGWINCH, syscall.SIGINT, syscall.SIGTERM}
	signal.Notify(ch, signals...)
	go func() {
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
