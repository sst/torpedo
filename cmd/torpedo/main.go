package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/briandowns/spinner"
	"github.com/fatih/color"
	"github.com/manifoldco/promptui"
	"github.com/thdxr/torpedo/pkg/bastion"
	"github.com/thdxr/torpedo/pkg/client"
	"github.com/thdxr/torpedo/pkg/server"
	cli "github.com/urfave/cli/v2"

	"log/slog"
)

func main() {

	app := &cli.App{
		Name:           "torpedo",
		Usage:          "A tool to access AWS resources behind a VPC",
		DefaultCommand: "client",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name: "verbose",
			},
		},
		Before: func(c *cli.Context) error {
			level := slog.LevelWarn
			if c.Bool("verbose") {
				level = slog.LevelInfo
			}
			slog.SetDefault(
				slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
					Level: level,
				})),
			)
			return nil
		},
		Commands: []*cli.Command{
			{
				Name:    "client",
				Aliases: []string{"c"},
				Usage:   "Run the client command",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name: "port",
					},
				},
				Action: func(cli *cli.Context) error {

					ctx, cancel := context.WithCancelCause(cli.Context)

					spin := spinner.New(spinner.CharSets[11], 100*time.Millisecond)
					spin.Color("yellow")
					c, err := client.NewClient()
					if err != nil {
						return err
					}

					slog.Info("searching for targets")

					spin.Suffix = " searching for targets (may take a while on first run)"
					spin.Start()

					b, err := bastion.New()
					if err != nil {
						spin.FinalMSG = "❌" + err.Error()
						spin.Stop()
						return err
					}

					if len(b.DBS) == 0 {
						spin.Stop()
						fmt.Println("no databases found")
						return errors.New("no databases found")
					}

					target := b.DBS[0]

					if len(b.DBS) > 1 {
						spin.Disable()
						options := make([]string, len(b.DBS))
						for i, db := range b.DBS {
							options[i] = *db.DBClusterIdentifier
						}
						index, _, err := (&promptui.Select{
							Label:        "select database",
							Items:        options,
							HideHelp:     true,
							HideSelected: true,
						}).Run()
						if err != nil {
							return err
						}
						target = b.DBS[index]
						spin.Enable()
					}

					spin.Suffix = " starting proxy to " + color.BlueString(*target.DBClusterIdentifier)
					slog.Info("starting bastion", "target", *target.DBClusterIdentifier)
					_, ip, err := b.Start(b.DBS[0], c.PublicKey())
					if err != nil {
						return err
					}
					defer b.Shutdown()

					port := cli.String("port")
					if port == "" {
						port = fmt.Sprint(target.Endpoint.Port)
					}
					go func() {
						slog.Info("starting tunnel", "endpoint", *target.Endpoint.Address, "ip", ip)
						err = c.Start(
							client.ConnectConfig{
								Server:          ip + ":2222",
								DestinationHost: *target.Endpoint.Address,
								DestinationPort: fmt.Sprint(target.Endpoint.Port),
								BindPort:        port,
							},
						)
						if err != nil {
							cancel(err)
						}
					}()
					defer c.Shutdown()
					spin.Stop()
					fmt.Println("🚀 torpedo is ready")
					fmt.Println()
					fmt.Println("connect to port " + color.CyanString(port) + " on localhost")
					fmt.Println("it's forwarded to " + color.BlueString(*target.Endpoint.Address) + ":" + color.CyanString(fmt.Sprint(target.Endpoint.Port)))
					fmt.Println()
					fmt.Println("press ctrl+c to exit")
					fmt.Println()
					spin = spinner.New(spinner.CharSets[32], 100*time.Millisecond)
					spin.Start()

					sigCh := make(chan os.Signal, 1)
					signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
					select {
					case <-sigCh:
					case <-ctx.Done():
						spin.Stop()
						slog.Info("shutting down")
					}

					return context.Cause(ctx)
				},
			},
			{
				Name:    "server",
				Aliases: []string{"s"},
				Usage:   "Run the server command",
				Action: func(c *cli.Context) error {
					decoded, err := base64.StdEncoding.DecodeString(os.Getenv("TORPEDO_PUBLIC_KEY"))
					if err != nil {
						return err
					}
					server, err := server.NewServer(decoded)
					if err != nil {
						return err
					}
					server.Start()
					return nil
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}

}
