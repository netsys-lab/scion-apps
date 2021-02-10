package main

import (
	"os"

	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:  "scion-spate",
		Usage: "Scion performance analysis tool for empirical studies",
		Commands: []*cli.Command{
			{
				Name:    "server",
				Aliases: []string{"s"},
				Usage:   "run a Spate server for performance analysis",
				Flags: []cli.Flag{
					&cli.UintFlag{
						Name:        "port",
						Aliases:     []string{"p"},
						Usage:       "listening port of the server",
						DefaultText: "1337",
					},
					&cli.DurationFlag{
						Name:        "duration",
						Aliases:     []string{"d"},
						Usage:       "duration for the server to receive packets from the client",
						DefaultText: "1s",
					},
					&cli.IntFlag{
						Name:        "packet-size",
						Aliases:     []string{"s"},
						Usage:       "the size of the packets in byte received from the client",
						DefaultText: "1208",
					},
				},
				Action: func(c *cli.Context) error {
					if c.Args().Present() {
						return cli.NewExitError("Unexpected positional arguments", 1)
					}

					var serverSpawner = NewSpateServerSpawner()
					if c.IsSet("port") {
						serverSpawner = serverSpawner.Port(uint16(c.Uint("port")))
					}
					if c.IsSet("duration") {
						serverSpawner = serverSpawner.RuntimeDuration(c.Duration("duration"))
					}
					if c.IsSet("packet-size") {
						serverSpawner = serverSpawner.PacketSize(c.Int("packet-size"))
					}

					serverSpawner.Spawn()

					return nil
				},
			},
			{
				Name:    "client",
				Aliases: []string{"c"},
				Usage:   "run a Spate client connecting to a performance measuring Spate server",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "server-address",
						Aliases:  []string{"a"},
						Usage:    "the server address, e.g. 16-ffaa:0:1001,[172.31.0.23]:1337",
						Required: true,
					},
					&cli.IntFlag{
						Name:        "packet-size",
						Aliases:     []string{"s"},
						Usage:       "the size of the packets in byte sent to the server",
						DefaultText: "1208",
					},
					&cli.BoolFlag{
						Name:        "single-path",
						Aliases:     []string{"1"},
						Usage:       "use single-path transmission instead of multi-path",
						DefaultText: "false",
					},
				},
				Action: func(c *cli.Context) error {
					if c.Args().Present() {
						return cli.NewExitError("Unexpected positional arguments", 1)
					}

					var clientSpawner = NewSpateClientSpawner(c.String("server-address"))
					if c.IsSet("packet-size") {
						clientSpawner = clientSpawner.PacketSize(c.Int("packet-size"))
					}

					if c.IsSet("single-path") {
						clientSpawner = clientSpawner.SinglePath(c.Bool("single-path"))
					}

					clientSpawner.Spawn()

					return nil
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		Error("%v", err)
	}
}
