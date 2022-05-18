package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"

	"github.com/moby/buildkit/solver/pb"
	"github.com/urfave/cli"
)

const defaultListRange = 3

func listCommand(ctx context.Context, hCtx *handlerContext) cli.Command {
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
			printLines(hCtx.handler, hCtx.locs, before, after, clicontext.Bool("all"))
			return nil
		},
	}
}

func printLines(h *handler, locs []*location, before, after int, all bool) {
	sources := make(map[*pb.SourceInfo][]*pb.Range)
	for _, l := range locs {
		sources[l.source] = append(sources[l.source], l.ranges...)
	}

	for source, ranges := range sources {
		if len(ranges) == 0 {
			continue
		}
		fmt.Printf("Filename: %q\n", source.Filename)
		scanner := bufio.NewScanner(bytes.NewReader(source.Data))
		lastLinePrinted := false
		firstPrint := true
		for i := 1; scanner.Scan(); i++ {
			print := false
			target := false
			for _, r := range ranges {
				if all || int(r.Start.Line)-before <= i && i <= int(r.End.Line)+after {
					print = true
					if int(r.Start.Line) <= i && i <= int(r.End.Line) {
						target = true
						break
					}
				}
			}

			if !print {
				lastLinePrinted = false
				continue
			}
			if !lastLinePrinted && !firstPrint {
				fmt.Println("----------------")
			}

			prefix := " "
			h.breakpoints.forEach(func(key string, b breakpoint) bool {
				if b.addMark(source, int64(i)) {
					prefix = "*"
					return false
				}
				return true
			})
			prefix2 := "  "
			if target {
				prefix2 = "=>"
			}
			fmt.Println(prefix + prefix2 + fmt.Sprintf("%4d| ", i) + scanner.Text())
			lastLinePrinted = true
			firstPrint = false
		}
	}
	return
}
