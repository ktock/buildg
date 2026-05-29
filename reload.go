package main

import (
	"context"
	"fmt"

	"github.com/ktock/buildg/pkg/buildkit"
	"github.com/urfave/cli"
)

func reloadCommand(_ context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:      "reload",
		Usage:     "reload context and restart build",
		UsageText: "reload",
		Action: func(clicontext *cli.Context) error {
			fmt.Fprintf(hCtx.stdout, "Reloading and restarting the build\n")
			hCtx.continueRead = false
			hCtx.err = buildkit.ErrReload
			return nil
		},
	}
}
