package main

import (
	"context"

	"github.com/ktock/buildg/pkg/buildkit"
	"github.com/urfave/cli"
)

func exitCommand(ctx context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:    "exit",
		Aliases: []string{"quit", "q"},
		Usage:   "exit command",
		Action: func(clicontext *cli.Context) error {
			hCtx.continueRead = false
			hCtx.err = buildkit.ErrExit
			return nil
		},
	}
}
