package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	cli "github.com/urfave/cli/v3"
)

// Candidate escaping. The completion protocol prints one candidate per
// line as `name` or `name:usage`, and every script splits a line at its
// first ":". A candidate that itself holds a ":" — a Windows cohort path
// such as C:\data\x.pulse — would complete as "C". So every value
// candidate Pulse prints escapes "\" as `\\` and ":" as `\:`
// (escapeCandidate), and each script splits at the first UNESCAPED ":"
// and drops the escapes (UnescapeCandidate is the reference parse):
//
//   - zsh: urfave/cli's script hands the lines to _describe, whose
//     compdescribe builtin already parses exactly this escaping (it
//     removes every backslash escape from the name part).
//   - bash and fish: urfave/cli's scripts cut at the first ":"
//     unconditionally; patchCompletionScript rewrites that parse (and,
//     for bash, the matching and insertion, which would otherwise expand
//     or shell-evaluate a backslash away).
//
// A candidate holding neither "\" nor ":" prints unchanged, so Unix
// output is byte-identical for ordinary paths.

// escapeCandidate escapes v for one completion output line.
func escapeCandidate(v string) string {
	if !strings.ContainsAny(v, `\:`) {
		return v
	}
	v = strings.ReplaceAll(v, `\`, `\\`)
	return strings.ReplaceAll(v, `:`, `\:`)
}

// UnescapeCandidate is the parse every Pulse completion script applies
// to one output line: split at the first unescaped ":" into the
// candidate and its usage (empty when absent), each with its backslash
// escapes dropped (`\\` → `\`, `\:` → `:`). It inverts escapeCandidate.
func UnescapeCandidate(line string) (name, usage string) {
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line):
			i++
			b.WriteByte(line[i])
		case c == ':':
			return b.String(), unescapeAll(line[i+1:])
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), ""
}

// unescapeAll drops every backslash escape of s.
func unescapeAll(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// scriptPatch is one exact rewrite of a urfave/cli-generated script.
type scriptPatch struct{ old, new string }

// completionScriptPatches rewrites the candidate parse of the bash and
// fish scripts (zsh needs none). Each old text must occur exactly once
// in the rendered script, so a urfave/cli upgrade that reshapes it fails
// `pulse completion <shell>` (and TestCompletionScripts_Patched) loudly
// instead of shipping the unescaped parse.
var completionScriptPatches = map[string][]scriptPatch{
	"bash": {
		{
			old: `    local line
    local longest=0
`,
			new: `    local line
    local longest=0
    # Pulse: a candidate escapes "\" as "\\" and ":" as "\:"; hide the
    # escapes, split at the first ":", restore them unescaped.
    local __pulse_bs='\' __pulse_bs2='\\' __pulse_ec='\:'
`,
		},
		{
			old: `      if [[ "${line}" == *:* ]]; then
        token="${line%%:*}"
        description="${line#*:}"
      fi
`,
			new: `      local esc="${line//"${__pulse_bs2}"/$'\x1e'}"
      esc="${esc//"${__pulse_ec}"/$'\x1f'}"
      token="${esc}"
      if [[ "${esc}" == *:* ]]; then
        token="${esc%%:*}"
        description="${esc#*:}"
      fi
      token="${token//$'\x1f'/:}"
      token="${token//$'\x1e'/${__pulse_bs}}"
      description="${description//$'\x1f'/:}"
      description="${description//$'\x1e'/${__pulse_bs}}"
`,
		},
		{
			old: `    local matches=( $(compgen -W "${__cli_completion_tokens[*]}" -- "${cur}") )
`,
			new: `    # Pulse: prefix-match by hand; compgen -W would expand each
    # candidate (eating its backslashes) and split one holding a space.
    local matches=() __pulse_t
    for __pulse_t in "${__cli_completion_tokens[@]}"; do
      [[ "${__pulse_t}" == "${cur}"* ]] && matches+=("${__pulse_t}")
    done
`,
		},
		{
			old: `    else
      COMPREPLY=("${matches[@]}")
`,
			new: `    else
      # Pulse: quote each reply for the command line (the shell would
      # eat a Windows path's backslashes) and, ":" being a word break,
      # drop the part of the word up to its last ":".
      local __pulse_m __pulse_pre=""
      if [[ "${cur}" == *:* && "${COMP_WORDBREAKS}" == *:* ]]; then
        __pulse_pre="${cur%"${cur##*:}"}"
      fi
      COMPREPLY=()
      for __pulse_m in "${matches[@]}"; do
        __pulse_m="${__pulse_m#"${__pulse_pre}"}"
        COMPREPLY+=("$(printf '%q' "${__pulse_m}")")
      done
`,
		},
	},
	"fish": {
		{
			old: `            set -l parts (string split -m 1 ":" -- "$line")
`,
			new: `            # Pulse: a candidate escapes "\" as "\\" and ":" as "\:";
            # hide the escapes, split at the first ":", restore them.
            set -l esc (string replace -a -- '\\\\' \x1e "$line" | string replace -a -- '\\:' \x1f)
            set -l parts (string split -m 1 ":" -- "$esc" | string replace -a -- \x1f ":" | string replace -a -- \x1e '\\')
`,
		},
		{
			old: `                printf "%s\n" "$line"
`,
			new: `                printf "%s\n" "$parts[1]"
`,
		},
	},
}

// patchCompletionScript applies shell's patches to a rendered script.
func patchCompletionScript(shell, script string) (string, error) {
	for _, p := range completionScriptPatches[shell] {
		if n := strings.Count(script, p.old); n != 1 {
			return "", fmt.Errorf("pulse: %s completion script: expected 1 occurrence of the patched text, found %d (urfave/cli changed its script?)", shell, n)
		}
		script = strings.Replace(script, p.old, p.new, 1)
	}
	return script, nil
}

// patchCompletionAction wraps a urfave/cli `completion <shell>` action:
// the script is rendered into a buffer, patched, then written.
func patchCompletionAction(sub *cli.Command) {
	if _, ok := completionScriptPatches[sub.Name]; !ok || sub.Action == nil {
		return
	}
	render := sub.Action
	sub.Action = func(ctx context.Context, cmd *cli.Command) error {
		root := cmd.Root()
		out := root.Writer
		var buf bytes.Buffer
		root.Writer = &buf
		err := render(ctx, cmd)
		root.Writer = out
		if err != nil {
			return err
		}
		script, err := patchCompletionScript(sub.Name, buf.String())
		if err != nil {
			return cli.Exit(err, 1)
		}
		_, err = out.Write([]byte(script))
		return err
	}
}
