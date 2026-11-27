package cli

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestLeavesShape pins the production walker's output: every runnable
// command under the set-up tree, sorted and unique, root and urfave's
// help commands excluded, framework-mounted completion leaves included,
// each flag with its names, usage, default, env sources and required bit.
// Two walks of two fresh trees are identical.
func TestLeavesShape(t *testing.T) {
	t.Parallel()
	leaves := Leaves(SetUpTree(App()))
	if again := Leaves(SetUpTree(App())); !reflect.DeepEqual(leaves, again) {
		t.Fatal("two walks of two fresh trees differ: the walker is not deterministic")
	}
	if len(leaves) < 70 {
		t.Fatalf("walked %d leaves; the tree walk is broken", len(leaves))
	}
	paths := make([]string, len(leaves))
	byPath := map[string]LeafDoc{}
	for i, l := range leaves {
		paths[i] = l.Path
		byPath[l.Path] = l
		if strings.TrimSpace(l.Usage) == "" {
			t.Errorf("%s: empty usage", l.Path)
		}
		for _, f := range l.Flags {
			if len(f.Names) == 0 || f.Names[0] == "" {
				t.Errorf("%s: a flag with no name", l.Path)
			}
		}
	}
	if !slices.IsSorted(paths) || len(slices.Compact(slices.Clone(paths))) != len(paths) {
		t.Errorf("leaf paths are not sorted and unique: %v", paths)
	}
	for _, p := range []string{"pulse api", "pulse shard", "pulse index"} {
		if _, ok := byPath[p]; ok {
			t.Errorf("walker yielded group %q, which has no Action and is not a leaf", p)
		}
	}
	for _, p := range paths {
		if p == "pulse" || strings.HasSuffix(p, " help") {
			t.Errorf("walker yielded %q: the root and urfave's help commands are not leaves", p)
		}
	}
	for _, p := range []string{"pulse completion bash", "pulse api process", "pulse convert", "pulse convert predict"} {
		if _, ok := byPath[p]; !ok {
			t.Errorf("leaf %q missing", p)
		}
	}
	flag := func(leaf, name string) FlagDoc {
		t.Helper()
		for _, f := range byPath[leaf].Flags {
			if f.Names[0] == name {
				return f
			}
		}
		t.Fatalf("%s has no --%s", leaf, name)
		return FlagDoc{}
	}
	if got := flag("pulse mcp", "data-dir"); !slices.Equal(got.Env, []string{"PULSE_DATA_DIR"}) || !got.TakesValue {
		t.Errorf("pulse mcp --data-dir = %+v, want env PULSE_DATA_DIR and a value", got)
	}
	if got := flag("pulse import csv", "input"); !got.Required || !slices.Equal(got.Names, []string{"input", "i"}) {
		t.Errorf("pulse import csv --input = %+v, want required, names [input i]", got)
	}
	if got := flag("pulse import csv", "float"); got.Default != "f64" {
		t.Errorf("pulse import csv --float default = %q, want f64 (unquoted)", got.Default)
	}
	if got := flag("pulse api sample", "count"); got.Default != "10" {
		t.Errorf("pulse api sample --count default = %q, want 10", got.Default)
	}
	if got := flag("pulse api process", "json"); got.TakesValue || got.Default != "" || got.Required {
		t.Errorf("pulse api process --json = %+v, want a switch with no default", got)
	}
	// A bool that defaults to true records it (a false default is the
	// zero value help omits); its usage no longer carries it by hand.
	for _, c := range [][2]string{{"pulse profile create", "include-stats"}, {"pulse mcp", "bind-on-open"}} {
		got := flag(c[0], c[1])
		if got.TakesValue || got.Default != "true" {
			t.Errorf("%s --%s = %+v, want a switch with default true", c[0], c[1], got)
		}
		if strings.Contains(got.Usage, "default true") {
			t.Errorf("%s --%s usage %q still hand-writes its default", c[0], c[1], got.Usage)
		}
	}
	if got := flag("pulse api process", "help"); !slices.Equal(got.Names, []string{"help", "h"}) {
		t.Errorf("the set-up tree mounts --help on every leaf; got %+v", got)
	}
	globals := map[string]bool{}
	for _, g := range GlobalFlags(SetUpTree(App())) {
		globals[g.Names[0]] = true
	}
	for _, n := range []string{"log-level", "limit", "memory-limit", "version"} {
		if !globals[n] {
			t.Errorf("global flag --%s missing from GlobalFlags", n)
		}
	}
}

// usageVariant is a shared flag name whose meaning differs by leaf, so
// each leaf words it for itself: wordings is the exact number of distinct
// usage strings the tree carries for it.
type usageVariant struct {
	wordings int
	why      string
}

// usageVariants lists every shared flag name allowed more than one usage
// wording. Any other name mounted on two or more leaves reads the same
// everywhere. The counts are exact: a new wording fails until it is
// unified or the count raised with a reason; a unified one fails until the
// count drops.
var usageVariants = map[string]usageVariant{
	"charset":           {2, "decodes .sav input on import/convert; sets the written .sav encoding on export"},
	"dedup-ratio-floor": {2, "import predict also judges --suggest-groups candidates against it"},
	"echo-request":      {8, "each leaf echoes its own request type and says what streaming skips"},
	"elide-constants":   {2, "import predict reports what would be elided instead of eliding"},
	"feature-profile":   {2, "docs export scopes the reference; mcp scopes the server and reads PULSE_FEATURE_PROFILE"},
	"field":             {4, "a different field role per leaf (widen, facet, zone set, recommend)"},
	"format":            {3, "input format, import auto override, export predict target"},
	"from":              {2, "an example profile on features init; a source format on convert"},
	"group":             {4, "dedup regroups, import declares, import auto warns, import predict evaluates"},
	"include":           {2, "a source field on export; a shard on shard create"},
	"input":             {8, "a source file to import, a .pulse cohort, a transfer artifact, or a positional alternative"},
	"intent":            {2, "a filter on examples search; the required question kind on recommend"},
	"json":              {3, "examples show wraps a runnable body; profile create also prints to stdout"},
	"key":               {4, "index build/verify/drop name columns; api lookup takes column=value pairs"},
	"labels":            {5, "each leaf names the label refusals it raises or predicts"},
	"level":             {2, "a zstd level on export transfer; an operator level on recommend"},
	"no-components":     {3, "per-slot, per-stage or single Response"},
	"out":               {5, "a different artefact per leaf (cohort, raked cohort, deduplicated cohort, reference directory, example-data directory)"},
	"output":            {6, "a different artefact per leaf (cohort, exported file, transfer artifact, profile JSON)"},
	"overwrite":         {5, "a handle, a cohort, a cohort plus a weight column, or the example fixture files"},
	"query":             {2, "error search versus example search semantics"},
	"request":           {10, "each leaf reads its own request type and layering rule"},
	"return":            {2, "a field projection on lookup; a response preset on mcp"},
	"rows":              {2, "a spec override on from-schema; new rows on from-profile"},
	"schema":            {2, "import auto refuses it on a .pulse source"},
	"seed":              {2, "profile create seeds two reservoirs and says so"},
	"source-tz":         {2, "convert also writes a zoned value to the target as its UTC instant"},
	"stream":            {2, "compose streams extra trailer lines"},
	"strict":            {9, "each leaf promotes its own warnings"},
	"suggest-groups":    {2, "dedup measures records of a cohort; import predict measures source rows"},
	"time-zone":         {2, "a to_day() zone on cohort filter; a capture zone on profile create"},
	"to":                {2, "a target format on convert; a set type on widen"},
	"top-k":             {2, "discrete values on facet; categorical values on profile create"},
	"zone-field":        {3, "convert needs --keep-pulse; import predict judges the declaration"},
}

// minUsageLen is the shortest usage string that can say what a flag takes.
const minUsageLen = 20

// terseUsageExempt are framework flags whose wording urfave/cli owns.
var terseUsageExempt = map[string]bool{"help": true, "version": true}

// flagUsageProblems checks a leaf list against the one-wording rule and the
// minimum usage length.
func flagUsageProblems(leaves []LeafDoc, variants map[string]usageVariant) []string {
	wordings := map[string]map[string][]string{}
	var out []string
	for _, l := range leaves {
		for _, f := range l.Flags {
			n := f.Names[0]
			if wordings[n] == nil {
				wordings[n] = map[string][]string{}
			}
			wordings[n][f.Usage] = append(wordings[n][f.Usage], l.Path)
			if len(f.Usage) < minUsageLen && !terseUsageExempt[n] {
				out = append(out, fmt.Sprintf("%s --%s: usage %q is under %d characters; say what the flag takes", l.Path, n, f.Usage, minUsageLen))
			}
		}
	}
	names := make([]string, 0, len(wordings))
	for n := range wordings {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		got := len(wordings[n])
		want := 1
		if v, ok := variants[n]; ok {
			want = v.wordings
		}
		if got == want {
			continue
		}
		var lines []string
		for u, ps := range wordings[n] {
			lines = append(lines, fmt.Sprintf("    %d× %q (e.g. %s)", len(ps), u, ps[0]))
		}
		sort.Strings(lines)
		out = append(out, fmt.Sprintf("--%s carries %d usage wordings, want %d: unify them (a shared const) or, for a deliberate per-leaf meaning, set its usageVariants count with a reason\n%s",
			n, got, want, strings.Join(lines, "\n")))
	}
	for n := range variants {
		if _, ok := wordings[n]; !ok {
			out = append(out, fmt.Sprintf("usageVariants names --%s, which no leaf mounts; drop it", n))
		}
	}
	sort.Strings(out)
	return out
}

// TestFlagUsageOneWordingPerName: a flag name mounted on several leaves
// reads the same on each (or is a declared per-leaf variant with an exact
// wording count), and no usage string is under minUsageLen characters.
func TestFlagUsageOneWordingPerName(t *testing.T) {
	t.Parallel()
	for _, p := range flagUsageProblems(Leaves(SetUpTree(App())), usageVariants) {
		t.Error(p)
	}
}

func TestFlagUsageProblems(t *testing.T) {
	t.Parallel()
	fd := func(name, usage string) FlagDoc { return FlagDoc{Names: []string{name}, Usage: usage} }
	leaves := []LeafDoc{
		{Path: "pulse a", Flags: []FlagDoc{fd("json", "Output the result as a JSON envelope"), fd("input", "Input file path"), fd("help", "show help")}},
		{Path: "pulse b", Flags: []FlagDoc{fd("json", "Emit the JSON envelope envelope"), fd("strict", "Treat warnings as errors here")}},
		{Path: "pulse c", Flags: []FlagDoc{fd("strict", "Treat warnings as errors there")}},
	}
	got := flagUsageProblems(leaves, map[string]usageVariant{"strict": {2, "x"}, "gone": {2, "x"}})
	want := []string{"--json carries 2", "pulse a --input: usage", "usageVariants names --gone"}
	if len(got) != len(want) {
		t.Fatalf("problems = %q, want %d", got, len(want))
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], w) {
			t.Errorf("problem %d = %q, want prefix %q", i, got[i], w)
		}
	}
	if p := flagUsageProblems(leaves[2:], map[string]usageVariant{"strict": {2, "x"}}); len(p) != 1 || !strings.Contains(p[0], "carries 1 usage wordings, want 2") {
		t.Errorf("a variant count above the tree's wordings must fail (ratchet); got %q", p)
	}
}
