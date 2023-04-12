package main

import (
	"bufio"
	"context"
	"fmt"
	"io"

	"github.com/urfave/cli"
)

func logCommand(_ context.Context, hCtx *handlerContext) cli.Command {
	return cli.Command{
		Name:      "log",
		Usage:     "show build log",
		UsageText: "log [OPTIONS]",
		Flags: []cli.Flag{
			cli.IntFlag{
				Name:  "n",
				Usage: "Print recent n lines",
				Value: 10,
			},
			cli.BoolFlag{
				Name:  "all,a",
				Usage: "show all lines",
			},
			cli.BoolFlag{
				Name:  "more",
				Usage: "show buffered and unread lines",
			},
		},
		Action: func(clicontext *cli.Context) error {
			if clicontext.Bool("more") {
				_, err := io.Copy(hCtx.stdout, hCtx.progress.buffered())
				return err
			}
			r, err := hCtx.progress.reader()
			if err != nil {
				return err
			}
			defer r.Close()
			if clicontext.Bool("all") {
				_, err := io.Copy(hCtx.stdout, r)
				return err
			}
			n := clicontext.Int("n")
			if n <= 0 {
				return nil
			}

			buf := make([]string, n)
			cur := 0
			total := 0
			scanner := bufio.NewScanner(r)
			for scanner.Scan() {
				s := scanner.Text()
				buf[cur] = s
				cur++
				total++
				if cur >= len(buf) {
					cur = 0
				}
			}
			if total <= n {
				for i := 0; i < total; i++ {
					fmt.Fprintf(hCtx.stdout, "%s\n", buf[i])
				}
				return nil
			}
			for i := 0; i < n; i++ {
				fmt.Fprintf(hCtx.stdout, "%s\n", buf[cur])
				cur++
				if cur >= len(buf) {
					cur = 0
				}
			}
			return nil
		},
	}
}
