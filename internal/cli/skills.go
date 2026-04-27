package cli

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/skills"
	cli "github.com/urfave/cli/v3"
)

// SkillsCommand returns the skills command group.
func SkillsCommand() *cli.Command {
	return &cli.Command{
		Name:  "skills",
		Usage: "List and view bundled skills documentation",
		Commands: []*cli.Command{
			skillsListCmd(),
			skillsShowCmd(),
		},
	}
}

func skillsListCmd() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List all available skills",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "json", Usage: "Output result as JSON envelope"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			jsonOut := cmd.Bool("json")
			items := skills.List()

			if jsonOut {
				return writeEnvelope(cmd.Writer, items)
			}

			for _, s := range items {
				writeText(cmd.Writer, "%-30s %s\n", s.Name, s.Description)
			}
			return nil
		},
	}
}

func skillsShowCmd() *cli.Command {
	return &cli.Command{
		Name:      "show",
		Usage:     "Show a skill's documentation",
		ArgsUsage: "NAME",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args()
			if args.Len() < 1 {
				return fmt.Errorf("usage: pulse skills show NAME")
			}
			name := args.First()

			content, ok := skills.Get(name)
			if !ok {
				return fmt.Errorf("skill %q not found", name)
			}

			writeText(cmd.Writer, "%s", content)
			return nil
		},
	}
}
