package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"

	"github.com/moby/buildkit/solver/pb"
	"github.com/urfave/cli"
)

func listCommand(ctx context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:    "list",
		Aliases: []string{"ls", "l"},
		Usage:   "list source lines",
		Flags: []cli.Flag{
			cli.BoolFlag{
				Name:  "all",
				Usage: "show all lines",
			},
		},
		Action: func(clicontext *cli.Context) error {
			printLines(hCtx.handler, hCtx.locs, 3, clicontext.Bool("all"))
			return nil
		},
	}
}

func printLines(h *handler, locs []*location, margin int, all bool) {
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
				if all || int(r.Start.Line)-margin <= i && i <= int(r.End.Line)+margin {
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
