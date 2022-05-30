package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/moby/buildkit/solver/pb"
	"github.com/urfave/cli"
)

func breakCommand(ctx context.Context, hCtx *handlerContext) cli.Command {
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
			var b breakpoint
			if bp == "on-fail" {
				key = "on-fail"
				b = newOnFailBreakpoint()
			} else if l, err := strconv.ParseInt(bp, 10, 64); err == nil {
				b = newLineBreakpoint(hCtx.locs[0].source.Filename, l)
			}
			if b == nil {
				return fmt.Errorf("cannot parse breakpoint %q", bp)
			}
			return h.breakpoints.add(key, b)
		},
	}
}

func breakpointsCommand(ctx context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:      "breakpoints",
		Aliases:   []string{"bp"},
		Usage:     "Show breakpoints key-value pairs",
		UsageText: "breakpoints",
		Action: func(clicontext *cli.Context) error {
			hCtx.handler.breakpoints.forEach(func(key string, b breakpoint) bool {
				fmt.Printf("[%s]: %v\n", key, b)
				return true
			})
			return nil
		},
	}
}

func clearCommand(ctx context.Context, hCtx *handlerContext) cli.Command {
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
			if _, ok := hCtx.handler.breakpoints.get(bpKey); !ok {
				return fmt.Errorf("breakpoint %q not found", bpKey)
			}
			hCtx.handler.breakpoints.clear(bpKey)
			return nil
		},
	}
}

func clearAllCommand(ctx context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:      "clearall",
		Usage:     "Clear all breakpoints",
		UsageText: "clearall",
		Action: func(clicontext *cli.Context) error {
			hCtx.handler.breakpoints.clearAll()
			return nil
		},
	}
}

func nextCommand(ctx context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:      "next",
		Aliases:   []string{"n"},
		Usage:     "Proceed to the next line",
		UsageText: "next",
		Action: func(clicontext *cli.Context) error {
			hCtx.handler.breakEachVertex = true
			hCtx.continueRead = false
			return nil
		},
	}
}

func continueCommand(ctx context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:      "continue",
		Aliases:   []string{"c"},
		Usage:     "Proceed to the next breakpoint",
		UsageText: "continue",
		Action: func(clicontext *cli.Context) error {
			hCtx.handler.breakEachVertex = false
			hCtx.continueRead = false
			return nil
		},
	}
}

func newLineBreakpoint(filename string, line int64) breakpoint {
	return &lineBreakpoint{filename, line}
}

type lineBreakpoint struct {
	filename string
	line     int64
}

func (b *lineBreakpoint) isTarget(ctx context.Context, info breakpointContext) (bool, string, error) {
	for _, loc := range info.locs {
		if loc.source.Filename != b.filename {
			continue
		}
		for _, r := range loc.ranges {
			if int64(r.Start.Line) <= b.line && b.line <= int64(r.End.Line) {
				return true, "reached " + b.String(), nil
			}
		}
	}
	return false, "", nil
}

func (b *lineBreakpoint) String() string {
	return fmt.Sprintf("line: %s:%d", b.filename, b.line)
}

func (b *lineBreakpoint) addMark(source *pb.SourceInfo, line int64) bool {
	return source.Filename == b.filename && line == b.line
}

func newOnFailBreakpoint() breakpoint {
	return &onFailBreakpoint{}
}

type onFailBreakpoint struct{}

func (b *onFailBreakpoint) isTarget(ctx context.Context, info breakpointContext) (bool, string, error) {
	return info.status.err != nil, fmt.Sprintf("caught error %v", info.status.err), nil
}

func (b *onFailBreakpoint) String() string {
	return "breaks on fail"
}

func (b *onFailBreakpoint) addMark(source *pb.SourceInfo, line int64) bool {
	return false
}
