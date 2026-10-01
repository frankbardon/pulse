package main

import (
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// TestFlagUsageHasNoBackticks guards `--help` rendering. urfave/cli treats a
// back-quoted span inside a flag's Usage as that flag's value placeholder, so
// Usage "as on `import <format> --group`" renders the flag line as
// "--group import <format> --group" — garbled help text that no other test
// sees. Command-level Usage strings are not affected (they have no
// placeholder), so only flags are checked.
func TestFlagUsageHasNoBackticks(t *testing.T) {
	var walk func(path string, cmd *cli.Command)
	walk = func(path string, cmd *cli.Command) {
		for _, f := range cmd.Flags {
			df, ok := f.(cli.DocGenerationFlag)
			if !ok {
				continue
			}
			if strings.Contains(df.GetUsage(), "`") {
				t.Errorf("%s --%s: Usage contains a backtick, which urfave/cli renders as the value placeholder: %q",
					path, f.Names()[0], df.GetUsage())
			}
		}
		for _, sub := range cmd.Commands {
			walk(path+" "+sub.Name, sub)
		}
	}
	app := buildApp()
	walk(app.Name, app)
}
