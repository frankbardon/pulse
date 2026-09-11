package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/frankbardon/pulse/synth"
	cli "github.com/urfave/cli/v3"
)

// errWriter resolves the stream diagnostics belong on.
//
// urfave/cli fills ErrWriter (os.Stderr by default) during command
// setup and a leaf inherits its parent's, so the first branch is what
// runs in production and what a test capturing stderr overrides. The
// fallbacks make the helper safe to call from a Command constructed
// directly in a test without a full Run.
func errWriter(cmd *cli.Command) io.Writer {
	if cmd == nil {
		return os.Stderr
	}
	if cmd.ErrWriter != nil {
		return cmd.ErrWriter
	}
	if root := cmd.Root(); root != nil && root.ErrWriter != nil {
		return root.ErrWriter
	}
	return os.Stderr
}

// maxWarningExamples caps how many individual lines each warning KIND
// contributes to the terminal summary.
//
// The cap lives here, in the presentation layer, and it is deliberately
// smaller than synth's own maxThinLevelWarnings / maxThinResidualPairWarnings
// (20): those bound a slice a reader pages through with jq, while this
// bounds one screen that every kind shares at once. Three lines per kind
// over the half-dozen kinds a wide-cohort capture produces is a summary
// a reader will actually read; twenty each is the 2,900-line dump this
// whole shape exists to avoid. The full, unbounded list is always still
// in the document the command writes — writeWarningSummary's `fullList`
// argument says where.
const maxWarningExamples = 3

// writeWarningSummary renders a flat synth warning slice as a grouped,
// counted, bounded summary.
//
// It writes to w, which every caller supplies as the command's ErrWriter
// (stderr), not its Writer: `pulse profile create … > out.json` and
// every shell pipeline over these leaves must keep producing exactly the
// bytes they did before, and a diagnostic on stdout would corrupt them.
//
// fullList names where the complete list can be read — the profile
// document, the fidelity report, or the flag that would produce one.
// Empty omits the pointer.
//
// Silent for an empty slice, so callers can invoke it unconditionally.
func writeWarningSummary(w io.Writer, warnings []string, fullList string) {
	groups := synth.GroupWarnings(dedupeWarnings(warnings))
	if len(groups) == 0 {
		return
	}
	attention, informational := synth.CountWarningsNeedingAttention(groups)
	// The headline separates the two counts because they mean opposite
	// things. A wide cohort legitimately emits thousands of
	// expected-outcome lines (a thin pair that still ships, a target
	// nothing explained, a pair claim arbitrated away); folding those
	// into one number would make "3005 warnings" the reading on a
	// perfectly healthy capture and on a broken one alike.
	writeText(w, "Warnings: %d in %d kind(s) — %d needing attention, %d expected\n",
		attention+informational, len(groups), attention, informational)
	for _, g := range groups {
		marker := "-"
		if g.Attention {
			marker = "!"
		}
		writeText(w, "  %s %s (%d)\n", marker, g.Kind, g.Count())
		shown := g.Members
		if len(shown) > maxWarningExamples {
			shown = shown[:maxWarningExamples]
		}
		for _, m := range shown {
			writeText(w, "      %s\n", m)
		}
		if rest := g.Count() - len(shown); rest > 0 {
			writeText(w, "      +%d more of this kind\n", rest)
		}
	}
	if fullList != "" {
		writeText(w, "  Full list: %s\n", fullList)
	}
}

// dedupeWarnings drops exact-duplicate warning strings, preserving first
// -occurrence order.
//
// It exists for one caller shape: `pulse synth from-profile` merges
// three channels — the capture-time warnings the profile document
// carries, the warnings SpecFromProfile raises translating it, and the
// warnings generate() raises compiling the spec — and the middle two
// both run resolveConflicts over the same *Spec, so every conditional
// relationship conflict arrives twice, verbatim. Two byte-identical
// strings are one finding reported twice, and a summary that says 1,590
// conflicts where there are 795 is wrong in the direction that matters.
//
// Only the terminal summary dedupes. Result.Warnings, Profile.Warnings
// and the fidelity report's warnings array are untouched, so no --json
// output and no written document moves.
func dedupeWarnings(warnings []string) []string {
	if len(warnings) < 2 {
		return warnings
	}
	seen := make(map[string]struct{}, len(warnings))
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		if _, dup := seen[w]; dup {
			continue
		}
		seen[w] = struct{}{}
		out = append(out, w)
	}
	return out
}

// writeSuggestedRulesCaveat renders the cross-cutting caveat for
// `profile create --suggest-rules`.
//
// It lives on STDERR, beside writeWarningSummary and for the same
// reason: `pulse profile create … > out.json` must keep producing
// exactly the bytes it did before.
//
// The caveat is here rather than in the file because it is about the
// DETECTION, not about any one candidate — what the pass looked for and,
// more importantly, what it did not. Each candidate carries its own
// proxy caveat in its `_evidence.note`, which is the part that has to
// survive being read in isolation; this is the part that would be
// repeated verbatim 20 times if it lived there.
//
// Deliberately not a warning: nothing went wrong. It is the reading
// instruction for a file whose whole risk is being applied unread.
func writeSuggestedRulesCaveat(w io.Writer, count int, path string) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, "\nWrote %d candidate rule(s) to %s — PROPOSED, not applied.\n", count, path)
	if count == 0 {
		fmt.Fprintf(w, "  Detection found no field whose levels split another field's null rate into ~1 and ~0.\n")
	}
	fmt.Fprintf(w, "  Each candidate is the STATISTICAL gate measured from the data. The SEMANTIC\n")
	fmt.Fprintf(w, "  cause may be a different field that moves with it, so read each `_evidence`,\n")
	fmt.Fprintf(w, "  correct the `when`, and delete what you do not believe before applying.\n")
	fmt.Fprintf(w, "  Detection looked for ONE thing: a low-cardinality field (categorical_*,\n")
	fmt.Fprintf(w, "  packed_bool, u4) whose levels split a target's null rate. It did NOT look for\n")
	fmt.Fprintf(w, "  co-missing blocks with no single gate, derived fields, or any relationship\n")
	fmt.Fprintf(w, "  among non-null VALUES.\n")
	fmt.Fprintf(w, "  A set_null gate repairs co-missingness only: each target's own null_rate still\n")
	fmt.Fprintf(w, "  fires beneath the gate, so the targets' marginal null rate rises above the\n")
	fmt.Fprintf(w, "  captured one.\n")
	fmt.Fprintf(w, "  Apply with: pulse synth from-profile --rules %s …\n", path)
}
