package main

import (
	"context"
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/ktock/buildg/pkg/buildkit"
	"github.com/urfave/cli"
)

func breakCommand(_ context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:    "break",
		Aliases: []string{"b"},
		Usage:   "set a breakpoint",
		UsageText: `break BREAKPOINT

The following value can be set as a BREAKPOINT

NUMBER   breaks on line number in Dockerfile
on-fail  breaks on step that returns an error
`,
		Action: func(clicontext *cli.Context) error {
			bp := clicontext.Args().First()
			if bp == "" {
				return fmt.Errorf("breakpoint must be set")
			}
			h := hCtx.handler
			var key string
			var b buildkit.Breakpoint
			if bp == "on-fail" {
				key = "on-fail"
				b = buildkit.NewOnFailBreakpoint()
			} else if l, err := strconv.ParseInt(bp, 10, 64); err == nil {
				b = buildkit.NewLineBreakpoint(hCtx.locs[0].Source.Filename, l)
			}
			if b == nil {
				return fmt.Errorf("cannot parse breakpoint %q", bp)
			}
			_, err := h.Breakpoints().Add(key, b)
			return err
		},
	}
}

func breakpointsCommand(_ context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:      "breakpoints",
		Aliases:   []string{"bp"},
		Usage:     "Show breakpoints key-value pairs",
		UsageText: "breakpoints",
		Action: func(clicontext *cli.Context) error {
			tw := tabwriter.NewWriter(hCtx.stdout, 4, 8, 4, ' ', 0)
			fmt.Fprintln(tw, "KEY\tDESCRIPTION")
			hCtx.handler.Breakpoints().ForEach(func(key string, b buildkit.Breakpoint) bool {
				fmt.Fprintf(tw, "%s\t%s\n", key, b)
				return true
			})
			tw.Flush()
			return nil
		},
	}
}

func clearCommand(_ context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:  "clear",
		Usage: "Clear a breakpoint. Specify breakpoint key.",
		UsageText: `clear BREAKPOINT_KEY

BREAKPOINT_KEY is the key of a breakpoint which is printed when executing "breakpoints" command.
`,
		Action: func(clicontext *cli.Context) error {
			bpKey := clicontext.Args().First()
			if bpKey == "" {
				return fmt.Errorf("breakpoint key must be set")
			}
			if _, ok := hCtx.handler.Breakpoints().Get(bpKey); !ok {
				return fmt.Errorf("breakpoint %q not found", bpKey)
			}
			hCtx.handler.Breakpoints().Clear(bpKey)
			return nil
		},
	}
}

func clearAllCommand(_ context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:      "clearall",
		Usage:     "Clear all breakpoints",
		UsageText: "clearall",
		Action: func(clicontext *cli.Context) error {
			hCtx.handler.Breakpoints().ClearAll()
			return nil
		},
	}
}

func nextCommand(_ context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:      "next",
		Aliases:   []string{"n"},
		Usage:     "Proceed to the next line",
		UsageText: "next",
		Action: func(clicontext *cli.Context) error {
			hCtx.handler.BreakEachVertex(true)
			hCtx.continueRead = false
			return nil
		},
	}
}

func continueCommand(_ context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:    "continue",
		Aliases: []string{"c"},
		Usage:   "Proceed to the next or the specified breakpoint",
		UsageText: `continue [BREAKPOINT_KEY]

Optional arg BREAKPOINT_KEY is the key of a breakpoint until which continue the build.
Use "breakpoints" command to list all registered breakpoints.
`,
		Action: func(clicontext *cli.Context) error {
			bp := clicontext.Args().First()
			if bp != "" {
				if _, ok := hCtx.handler.Breakpoints().Get(bp); !ok {
					return fmt.Errorf("unknown breakpoint %v", bp)
				}
				hCtx.targetBreakpoint = bp
			}
			hCtx.handler.BreakEachVertex(false)
			hCtx.continueRead = false
			return nil
		},
	}
}
