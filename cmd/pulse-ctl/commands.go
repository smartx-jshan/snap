package main

import "github.com/codegangsta/cli"

var (
	commands = []cli.Command{
		{
			Name: "task",
			Subcommands: []cli.Command{
				{
					Name:   "create",
					Usage:  "create <task file json|yaml>",
					Action: createTask,
				},
				{
					Name:   "list",
					Usage:  "list",
					Action: listTask,
				},
				{
					Name:   "start",
					Usage:  "start <task_id>",
					Action: startTask,
				},
			},
		},
		{
			Name: "plugin",
			Subcommands: []cli.Command{
				{
					Name:   "load",
					Usage:  "load <plugin path>",
					Action: loadPlugin,
				},
				{
					Name:   "list",
					Usage:  "list",
					Action: listPlugins,
					Flags: []cli.Flag{
						flRunning,
					},
				},
			},
		},
		{
			Name: "metric",
			Subcommands: []cli.Command{
				{
					Name:   "list",
					Usage:  "list",
					Action: listMetrics,
				},
			},
		},
	}
)
