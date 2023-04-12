package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/ktock/buildg/pkg/buildkit"
	"github.com/moby/buildkit/solver/pb"
	"github.com/urfave/cli"
)

const defaultListRange = 3

func listCommand(_ context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:      "list",
		Aliases:   []string{"ls", "l"},
		Usage:     "list source lines",
		UsageText: "list [OPTIONS]",
		Flags: []cli.Flag{
			cli.BoolFlag{
				Name:  "all",
				Usage: "show all lines",
			},
			cli.IntFlag{
				Name:  "A",
				Usage: "Print the specified number of lines after the current line",
				Value: defaultListRange,
			},
			cli.IntFlag{
				Name:  "B",
				Usage: "Print the specified number of lines before the current line",
				Value: defaultListRange,
			},
			cli.IntFlag{
				Name:  "range",
				Usage: "Print the specified number of lines before and after the current line",
				Value: defaultListRange,
			},
		},
		Action: func(clicontext *cli.Context) error {
			lineRange := clicontext.Int("range")
			before, after := lineRange, lineRange
			if b := clicontext.Int("B"); b != defaultListRange {
				before = b
			}
			if a := clicontext.Int("A"); a != defaultListRange {
				after = a
			}
			printLines(hCtx.handler, hCtx.stdout, hCtx.locs, before, after, clicontext.Bool("all"))
			return nil
		},
	}
}

func printLines(h *buildkit.Handler, w io.Writer, locs []*buildkit.Location, before, after int, all bool) {
	sources := make(map[*pb.SourceInfo][]*pb.Range)
	for _, l := range locs {
		sources[l.Source] = append(sources[l.Source], l.Ranges...)
	}

	for source, ranges := range sources {
		if len(ranges) == 0 {
			continue
		}
		fmt.Fprintf(w, "Filename: %q\n", source.Filename)
		scanner := bufio.NewScanner(bytes.NewReader(source.Data))
		lastLinePrinted := false
		firstPrint := true
		for i := 1; scanner.Scan(); i++ {
			doPrint := false
			target := false
			for _, r := range ranges {
				if all || int(r.Start.Line)-before <= i && i <= int(r.End.Line)+after {
					doPrint = true
					if int(r.Start.Line) <= i && i <= int(r.End.Line) {
						target = true
						break
					}
				}
			}

			if !doPrint {
				lastLinePrinted = false
				continue
			}
			if !lastLinePrinted && !firstPrint {
				fmt.Fprintln(w, "----------------")
			}

			prefix := " "
			h.Breakpoints().ForEach(func(key string, b buildkit.Breakpoint) bool {
				if b.IsMarked(source, int64(i)) {
					prefix = "*"
					return false
				}
				return true
			})
			prefix2 := "  "
			if target {
				prefix2 = "=>"
			}
			fmt.Fprintln(w, prefix+prefix2+fmt.Sprintf("%4d| ", i)+scanner.Text())
			lastLinePrinted = true
			firstPrint = false
		}
	}
}
