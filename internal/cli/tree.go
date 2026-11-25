package cli

import (
	"context"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"

	cli "github.com/urfave/cli/v3"

	"github.com/frankbardon/pulse/internal/docgen"
)

// The command tree as data. Documentation tooling (the generated per-leaf
// flags reference, the coverage matrix) and the CLI tests read the tree
// through these helpers, so every walk sees the same commands and flags the
// binary mounts.

// SetUpTree runs root once with --help, output discarded, so urfave/cli
// mounts what it adds at setup (the `completion` command and its shell
// leaves, the help flag), and returns root. Writer and ErrWriter are left
// discarding; a caller that runs the tree for real sets its own.
func SetUpTree(root *cli.Command) *cli.Command {
	root.Writer, root.ErrWriter = io.Discard, io.Discard
	_ = root.Run(context.Background(), []string{root.Name, "--help"})
	return root
}

// IsHelpCommand reports whether c is urfave/cli's auto-added `help`
// command (mounted under every command at setup). It is framework
// furniture, not a Pulse command, so the walks skip it.
func IsHelpCommand(c *cli.Command) bool { return c.Name == "help" }

// WalkCommands calls fn for every command under root (root excluded),
// depth first in mount order, with its invocation path (root's name
// first) and its parent. urfave's help commands are skipped.
func WalkCommands(root *cli.Command, fn func(path []string, parent, c *cli.Command)) {
	var walk func(path []string, parent *cli.Command)
	walk = func(path []string, parent *cli.Command) {
		for _, c := range parent.Commands {
			if IsHelpCommand(c) {
				continue
			}
			p := append(slices.Clone(path), c.Name)
			fn(p, parent, c)
			walk(p, c)
		}
	}
	walk([]string{root.Name}, root)
}

// FlagDoc is one flag mount as documentation sees it (the generated flags
// reference renders it; docgen owns the type so it never imports the CLI).
type FlagDoc = docgen.CLIFlag

// LeafDoc is one runnable command (a non-nil Action) with its own flags;
// the root's persistent global flags are reported once, by GlobalFlags.
type LeafDoc = docgen.CLILeaf

// DescribeFlag reads f through urfave/cli's optional flag interfaces.
func DescribeFlag(f cli.Flag) FlagDoc {
	d := FlagDoc{Names: slices.Clone(f.Names())}
	if g, ok := f.(cli.DocGenerationFlag); ok {
		d.Usage = g.GetUsage()
		d.TakesValue = g.TakesValue()
		d.Env = slices.Clone(g.GetEnvVars())
		d.Default = g.GetDefaultText()
		if d.Default == "" && d.TakesValue {
			d.Default = g.GetValue()
		}
	}
	if d.TakesValue {
		if tn, ok := f.(interface{ TypeName() string }); ok {
			d.Type = tn.TypeName()
		}
	}
	if m, ok := f.(cli.DocGenerationMultiValueFlag); ok {
		d.Repeatable = m.IsMultiValueFlag()
	}
	if isZeroDefault(d.Default) {
		d.Default = ""
	} else if u, err := strconv.Unquote(d.Default); err == nil {
		d.Default = u
	}
	if r, ok := f.(cli.RequiredFlag); ok {
		d.Required = r.IsRequired()
	}
	return d
}

// isZeroDefault reports whether a rendered default carries no information:
// the zero value of its type, which help omits too.
func isZeroDefault(v string) bool {
	switch strings.TrimSpace(v) {
	case "", `""`, "false", "0", "0s", "[]", "map[]":
		return true
	}
	return false
}

// describeFlags describes every visible flag in fs, mount order kept.
func describeFlags(fs []cli.Flag) []FlagDoc {
	out := make([]FlagDoc, 0, len(fs))
	for _, f := range fs {
		if v, ok := f.(cli.VisibleFlag); ok && !v.IsVisible() {
			continue
		}
		out = append(out, DescribeFlag(f))
	}
	return out
}

// Leaves returns every runnable command under root (root excluded),
// sorted by path, each with its own visible flags. root should be set up
// (SetUpTree) so the framework-mounted leaves and help flag are included.
func Leaves(root *cli.Command) []LeafDoc {
	var out []LeafDoc
	WalkCommands(root, func(path []string, _, c *cli.Command) {
		if c.Action == nil {
			return
		}
		out = append(out, LeafDoc{Path: strings.Join(path, " "), Usage: c.Usage, Args: c.ArgsUsage, Flags: describeFlags(c.Flags)})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// GlobalFlags returns root's own visible flags in mount order: the
// global flags every leaf inherits.
func GlobalFlags(root *cli.Command) []FlagDoc { return describeFlags(root.Flags) }
