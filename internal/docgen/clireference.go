package docgen

import (
	"sort"
	"strings"
)

// The CLI flag reference (docs/src/cli/reference.md) is rendered here from
// the command tree as data. internal/cli walks its own tree (it alone may
// reach the CLI framework) and hands the result to RenderCLIReference, so
// this package never imports the CLI and the page lists exactly what the
// binary mounts.

// CLIReferencePath is the generated page, repo-relative.
const CLIReferencePath = "docs/src/cli/reference.md"

// CLIFlag is one flag mount as the reference shows it.
type CLIFlag struct {
	// Names are the spellings a user can type, primary name first, then
	// its aliases, without dashes.
	Names []string
	// Usage is the flag's help text.
	Usage string
	// Type is the value's type as help spells it ("string", "int",
	// "float", "duration"); empty for a boolean switch.
	Type string
	// Repeatable marks a flag given once per value.
	Repeatable bool
	// Default is the flag's default value as help would show it; empty
	// when the flag has no meaningful default (a zero value, false, an
	// empty list).
	Default string
	// Env names the environment variables the flag reads, in order.
	Env []string
	// Required marks a flag the command refuses to run without.
	Required bool
	// TakesValue is false for a boolean switch.
	TakesValue bool
}

// CLILink is a page that documents a leaf in depth.
type CLILink struct {
	// Text is the link text.
	Text string
	// Target is the destination relative to docs/src/cli/.
	Target string
}

// CLILeaf is one runnable command with its own flags.
type CLILeaf struct {
	// Path is the full invocation path ("pulse cohort info").
	Path string
	// Usage is the command's one-line usage text.
	Usage string
	// Args is the positional-argument synopsis ("COHORT FIELD"), if any.
	Args string
	// Flags are the command's own flags in mount order (the root's
	// persistent global flags are listed once, in CLIReference.Globals).
	Flags []CLIFlag
	// Links are the pages that document the leaf in depth.
	Links []CLILink
}

// CLIReference is the whole command tree as the reference page shows it.
type CLIReference struct {
	// Globals are the root's flags, accepted by every leaf.
	Globals []CLIFlag
	// Leaves are every runnable command, sorted by path.
	Leaves []CLILeaf
}

// cliHelpFlag is urfave/cli's help flag: mounted on every leaf, so the
// page lists it once, with the global flags, instead of in every table.
const cliHelpFlag = "help"

// CLIAnchor is the mdBook heading id of a leaf's section.
func CLIAnchor(leaf string) string { return strings.ReplaceAll(leaf, " ", "-") }

// RenderCLIReference renders the generated per-leaf flags reference.
// The output is deterministic: leaves and the cross index are sorted,
// flags keep their mount order.
func RenderCLIReference(ref CLIReference) []byte {
	var b strings.Builder
	b.WriteString("# CLI command and flag reference\n\n")
	b.WriteString("<!-- Generated from the command tree (internal/cli) by `make docs`; do not edit. -->\n\n")
	b.WriteString("Every runnable `pulse` command with the flags it accepts, read from the command tree the binary mounts, so this page and `--help` always agree. ")
	b.WriteString("Concepts several commands share (environment variables, the `--json` envelope, exit status, parent groups, source zones, labels) are explained in [CLI concepts](flags.md); ")
	b.WriteString("[Flags by name](#flags-by-name) lists the commands that mount each flag.\n\n")

	b.WriteString("## Global flags\n\n")
	b.WriteString("Every command accepts these as well as its own (see [Global flags](flags.md#global-flags)). With no command, `--json` prints the manifest. `--help` / `-h` is on every command.\n\n")
	writeFlagTable(&b, ref.Globals)

	b.WriteString("## Commands\n\n")
	for _, l := range ref.Leaves {
		b.WriteString("### `" + l.Path + "`\n\n")
		if u := strings.TrimSpace(l.Usage); u != "" {
			b.WriteString(cliText(u) + "\n\n")
		}
		synopsis := l.Path
		if len(visibleFlags(l.Flags)) > 0 {
			synopsis += " [options]"
		}
		if l.Args != "" {
			synopsis += " " + l.Args
		}
		b.WriteString("Usage: `" + synopsis + "`\n\n")
		if len(l.Links) > 0 {
			parts := make([]string, len(l.Links))
			for i, k := range l.Links {
				parts[i] = "[" + k.Text + "](" + k.Target + ")"
			}
			b.WriteString("In depth: " + strings.Join(parts, ", ") + "\n\n")
		}
		if fs := visibleFlags(l.Flags); len(fs) > 0 {
			writeFlagTable(&b, fs)
		} else {
			b.WriteString("No flags of its own.\n\n")
		}
	}

	b.WriteString("## Flags by name\n\n")
	b.WriteString("Each flag a command mounts, with the commands that mount it. The global flags are in [Global flags](#global-flags).\n\n")
	b.WriteString("| Flag | Commands |\n|---|---|\n")
	mounts := map[string][]string{}
	for _, l := range ref.Leaves {
		for _, f := range visibleFlags(l.Flags) {
			mounts[flagCell(f)] = append(mounts[flagCell(f)], l.Path)
		}
	}
	keys := make([]string, 0, len(mounts))
	for k := range mounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		links := make([]string, len(mounts[k]))
		for i, p := range mounts[k] {
			links[i] = "[`" + strings.TrimPrefix(p, "pulse ") + "`](#" + CLIAnchor(p) + ")"
		}
		b.WriteString("| " + k + " | " + strings.Join(links, ", ") + " |\n")
	}
	return []byte(b.String())
}

// visibleFlags drops the help flag, listed once with the globals.
func visibleFlags(fs []CLIFlag) []CLIFlag {
	out := make([]CLIFlag, 0, len(fs))
	for _, f := range fs {
		if len(f.Names) > 0 && f.Names[0] != cliHelpFlag {
			out = append(out, f)
		}
	}
	return out
}

func writeFlagTable(b *strings.Builder, fs []CLIFlag) {
	b.WriteString("| Flag | Value | Default | Description |\n|---|---|---|---|\n")
	for _, f := range fs {
		value := f.Type
		if f.Repeatable {
			value += ", repeatable"
		}
		def := ""
		if f.Default != "" {
			def = "`" + f.Default + "`"
		}
		desc := cliText(f.Usage)
		if f.Required {
			desc = "**Required.** " + desc
		}
		if len(f.Env) > 0 {
			env := make([]string, len(f.Env))
			for i, e := range f.Env {
				env[i] = "`" + e + "`"
			}
			desc += " Environment: " + strings.Join(env, ", ") + "."
		}
		b.WriteString("| " + flagCell(f) + " | " + value + " | " + def + " | " + desc + " |\n")
	}
	b.WriteString("\n")
}

// flagCell spells a flag's names as a user types them, each in code.
func flagCell(f CLIFlag) string {
	parts := make([]string, len(f.Names))
	for i, n := range f.Names {
		if len(n) == 1 {
			parts[i] = "`-" + n + "`"
		} else {
			parts[i] = "`--" + n + "`"
		}
	}
	return strings.Join(parts, ", ")
}

// cliText makes help text safe in a Markdown table cell: a pipe would end
// the cell, a bare <word> would be swallowed as HTML, an asterisk could
// open emphasis. Help text carries no backticks (TestFlagUsageHasNoBackticks).
func cliText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "*", `\*`)
	return strings.ReplaceAll(strings.ReplaceAll(s, "<", "&lt;"), ">", "&gt;")
}
