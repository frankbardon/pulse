package cli

import (
	"context"
	"slices"
	"strings"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
	cli "github.com/urfave/cli/v3"
)

// Flag-value and positional completion.
//
// urfave/cli has no per-flag completion hook: one shared ShellComplete
// (completeValues) runs for every command. In completion mode a
// value-taking flag typed without its value stays the LAST positional
// argument (cmd.Args()), so a trailing "--mode" means "complete the
// values of --mode". Otherwise the leaf's positional table answers, and
// anything else falls back to urfave's flag / subcommand suggestions.
//
// The scripts never pass the word being completed unless it starts with
// "-": the binary prints every candidate and the shell filters by
// prefix. Output is sorted and de-duplicated; an empty answer lets the
// shell fall back to its own (file name) completion.
//
// SINGLE SOURCE (TestCompletion_SingleSource): no candidate is ever
// written down here. Every table entry is either a class marker
// (freeValue, fileValue, cohortValue, fieldValue) or a constructor call
// that references the library lister the flag's validator also reads.
// The CLI-owned logging vocabulary (logLevelNames / logFormatNames) is
// the one allowlisted exception.

// valueClass is what a flag value or positional argument names.
type valueClass int

const (
	// classFree is free text or a number: nothing completes.
	classFree valueClass = iota
	// classFile is a host file path: the shell's file completion.
	classFile
	// classCohort is a .pulse cohort path. It completes as a file
	// today; the cohort-aware completer (E3) attaches here.
	classCohort
	// classField is a cohort field name (or a grammar that opens with
	// one). Nothing completes today; the field completer (E3) attaches
	// here and reads the cohort the leaf names.
	classField
	// classVocab is a closed vocabulary a library lister enumerates.
	classVocab
)

var valueClassNames = map[valueClass]string{
	classFree:   "free",
	classFile:   "file",
	classCohort: "cohort",
	classField:  "field",
	classVocab:  "vocab",
}

// completionEnv is what a provider may consult: the request ctx, the
// leaf's positional arguments typed so far and a lazily built,
// read-only Pulse instance.
type completionEnv struct {
	ctx   context.Context
	args  []string
	pulse func() (*pulse.Pulse, error)
}

// valueSource classifies one flag value or positional; list is set only
// for classVocab.
type valueSource struct {
	class valueClass
	list  func(env *completionEnv) ([]string, error)
}

// Class markers for values Pulse does not enumerate.
var (
	freeValue   = valueSource{class: classFree}
	fileValue   = valueSource{class: classFile}
	cohortValue = valueSource{class: classCohort}
	fieldValue  = valueSource{class: classField}
)

// vocab lists a library enumeration of string-kinded values.
func vocab[T ~string](fn func() []T) valueSource {
	return valueSource{class: classVocab, list: func(*completionEnv) ([]string, error) {
		return strs(fn()), nil
	}}
}

// vocabBy lists a library enumeration of records by their name.
func vocabBy[T any](fn func() []T, name func(T) string) valueSource {
	return valueSource{class: classVocab, list: func(*completionEnv) ([]string, error) {
		return names(fn(), name), nil
	}}
}

// assignments lists "name=" for every name a NAME=VALUE flag accepts.
func assignments(fn func() []string) valueSource {
	return valueSource{class: classVocab, list: func(*completionEnv) ([]string, error) {
		out := fn()
		for i, n := range out {
			out[i] = n + "="
		}
		return out, nil
	}}
}

// cliOwned lists a vocabulary the CLI itself owns. Only the allowlisted
// logging spellings may be passed (TestCompletion_SingleSource).
func cliOwned(vs []string) valueSource {
	return valueSource{class: classVocab, list: func(*completionEnv) ([]string, error) {
		return slices.Clone(vs), nil
	}}
}

// fromPulse lists names a Pulse instance knows (skills, examples,
// managed imports, an archive's shards). fn sees the positionals typed
// so far.
func fromPulse[T any](fn func(context.Context, *pulse.Pulse, []string) ([]T, error), name func(T) string) valueSource {
	return valueSource{class: classVocab, list: func(env *completionEnv) ([]string, error) {
		p, err := env.pulse()
		if err != nil {
			return nil, err
		}
		items, err := fn(env.ctx, p, env.args)
		if err != nil {
			return nil, err
		}
		return names(items, name), nil
	}}
}

// fromManifest lists a manifest vocabulary (example categories, tags).
func fromManifest(fn func(*descriptor.Manifest) []string) valueSource {
	return valueSource{class: classVocab, list: func(env *completionEnv) ([]string, error) {
		p, err := env.pulse()
		if err != nil {
			return nil, err
		}
		return slices.Clone(fn(p.Manifest(env.ctx))), nil
	}}
}

func names[T any](items []T, name func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = name(it)
	}
	return out
}

// flagValues classifies every value-taking flag by its primary name.
// flagValuesIn overrides it for a command path and everything under it
// (the nearest scope wins).
var flagValues = map[string]valueSource{
	// root persistent
	"intent":         vocabBy(pulse.Intents, func(i descriptor.Intent) string { return i.ID }),
	"log-level":      cliOwned(logLevelNames),
	"log-format":     cliOwned(logFormatNames),
	"shard-workers":  freeValue,
	"decode-workers": freeValue,

	// closed vocabularies
	"float":        vocab(pio.FloatWidths),
	"dst-policy":   vocab(pio.DSTPolicies),
	"spss-missing": vocab(pio.SPSSMissingModes),
	"format":       vocab(pio.Formats),
	"mode":         vocab(types.AllLookupMultiplicities),
	"domain":       vocab(perrors.AllDomains),
	"missing":      vocab(pulse.RakeMissingPolicies),
	"root":         vocab(descriptor.ExplainRequestRoots),
	"detail":       vocab(descriptor.ExplainDetails),
	"category":     fromManifest(func(m *descriptor.Manifest) []string { return m.ExampleCategories }),
	"tag":          fromManifest(func(m *descriptor.Manifest) []string { return m.ExampleTags }),

	// host files
	"output":          fileValue,
	"out":             fileValue,
	"schema":          fileValue,
	"request":         fileValue,
	"response":        fileValue,
	"spec":            fileValue,
	"profile":         fileValue,
	"fidelity-report": fileValue,
	"rules":           fileValue,
	"emit-spec":       fileValue,
	"suggest-rules":   fileValue,
	"margins":         fileValue,
	"include-from":    fileValue,
	"keep-pulse":      fileValue,
	"data-dir":        fileValue,
	"feature-profile": fileValue,

	// cohorts
	"input":  cohortValue,
	"cohort": cohortValue,
	"source": cohortValue,

	// cohort fields (and grammars that open with one)
	"field":         fieldValue,
	"additive":      fieldValue,
	"key":           fieldValue,
	"include":       fieldValue,
	"include-field": fieldValue,
	"labels":        fieldValue,
	"group":         fieldValue,
	"weight-field":  fieldValue,
	"by":            fieldValue,
	"base-weight":   fieldValue,
	"return":        fieldValue,

	// free text and numbers
	"sample-rows":       freeValue,
	"dedup-ratio-floor": freeValue,
	"source-tz":         freeValue,
	"source-tz-field":   freeValue,
	"tz":                freeValue,
	"time-zone":         freeValue,
	"vector":            freeValue,
	"vector-kind":       freeValue,
	"charset":           freeValue,
	"sheet":             freeValue,
	"handle":            freeValue,
	"ttl":               freeValue,
	"filter":            freeValue,
	"count":             freeValue,
	"top-k":             freeValue,
	"percentile":        freeValue,
	"histogram-bins":    freeValue,
	"histogram-min":     freeValue,
	"histogram-max":     freeValue,
	"parallel":          freeValue,
	"query":             freeValue,
	"metrics-addr":      freeValue,
	"rows":              freeValue,
	"seed":              freeValue,
	"correlation-top-k": freeValue,
	"sample-limit":      freeValue,
	"min-weight":        freeValue,
	"max-weight":        freeValue,
	"max-iter":          freeValue,
	"tol":               freeValue,
	"total":             freeValue,
	"level":             freeValue,
	"limit":             freeValue,
}

var flagValuesIn = map[string]map[string]valueSource{
	// import reads source files, not cohorts: --input is a host file
	// and --group names source columns.
	"pulse import": {
		"input": fileValue,
		"group": freeValue,
	},
	"pulse convert": {
		"from": vocab(pio.Formats),
		"to":   vocab(pio.Formats),
	},
	"pulse widen": {
		"to": vocab(pulse.WidenSetFieldTargets),
	},
	"pulse features init": {
		"from": vocab(pulse.ExampleFeatureProfiles),
	},
	"pulse shard create": {
		"include": fileValue,
	},
	"pulse mcp": {
		"return": vocab(types.AllReturnPresets),
		"limit":  assignments(pulse.LimitNames),
	},
	"pulse recommend": {
		"level": vocab(descriptor.Levels),
	},
}

// positionalValues lists, per leaf, what each positional argument
// names, in order. A leaf absent here (or a position past the end)
// completes as the shell's default.
var positionalValues = map[string][]valueSource{
	"pulse errors lookup": {vocab(perrors.SortedCodeNames)},
	"pulse skills show": {fromPulse(
		func(_ context.Context, p *pulse.Pulse, _ []string) ([]pulse.SkillMetadata, error) {
			return p.Skills(), nil
		},
		func(s pulse.SkillMetadata) string { return s.Name })},
	"pulse examples show": {fromPulse(
		func(_ context.Context, p *pulse.Pulse, _ []string) ([]pulse.ExampleSummary, error) {
			return p.ExamplesSearchWith(pulse.ExamplesQuery{})
		},
		func(e pulse.ExampleSummary) string { return e.Name })},
	"pulse import drop": {fromPulse(
		func(ctx context.Context, p *pulse.Pulse, _ []string) ([]pulse.ImportEntry, error) {
			return p.Imports(ctx)
		},
		func(e pulse.ImportEntry) string { return e.Handle })},
	"pulse shard remove":  {cohortValue, archiveShards},
	"pulse shard extract": {cohortValue, archiveShards},
}

// archiveShards lists the shard basenames of the archive named by the
// first positional.
var archiveShards = fromPulse(
	func(ctx context.Context, p *pulse.Pulse, args []string) ([]pulse.ShardEntry, error) {
		return p.ListShards(ctx, args[0])
	},
	func(s pulse.ShardEntry) string { return s.Filename })

// installValueCompletion sets completeValues as the ShellComplete of
// every command under root (root included) that has none.
func installValueCompletion(root *cli.Command) {
	var walk func(c *cli.Command)
	walk = func(c *cli.Command) {
		if c.ShellComplete == nil {
			c.ShellComplete = completeValues
		}
		for _, sub := range c.Commands {
			walk(sub)
		}
	}
	walk(root)
}

// completeValues is the shared ShellComplete. It never writes to stderr
// and never fails: a provider error or panic prints nothing.
func completeValues(ctx context.Context, cmd *cli.Command) {
	args := cmd.Args().Slice()
	if n := len(args); n > 0 && strings.HasPrefix(args[n-1], "-") {
		f := lookupFlag(cmd, args[n-1])
		if f == nil || !takesValue(f) {
			cli.DefaultCompleteWithFlags(ctx, cmd)
			return
		}
		// The trailing flag is either the word being completed (no
		// space typed yet) or the one before it; offering the longer
		// flags it prefixes (--source-tz → --source-tz-field) as well
		// lets the shell pick whichever matches.
		src := flagSource(cmd, f.Names()[0])
		writeCandidates(cmd, append(listValues(ctx, src, nil), longerFlags(cmd, f.Names()[0])...))
		return
	}
	if srcs, ok := positionalValues[cmd.FullName()]; ok {
		if i := len(args); i < len(srcs) {
			writeCandidates(cmd, listValues(ctx, srcs[i], args))
		}
		return
	}
	cli.DefaultCompleteWithFlags(ctx, cmd)
}

// listValues runs src's provider; any failure yields no candidate.
func listValues(ctx context.Context, src valueSource, args []string) (out []string) {
	if src.list == nil {
		return nil
	}
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	env := &completionEnv{ctx: ctx, args: args, pulse: completionPulse()}
	vs, err := src.list(env)
	if err != nil {
		return nil
	}
	return vs
}

// completionPulse returns a once-only builder of the Pulse instance a
// provider reads: the OS filesystem behind a read-only layer (completion
// never writes), no logger.
func completionPulse() func() (*pulse.Pulse, error) {
	var (
		p    *pulse.Pulse
		err  error
		done bool
	)
	return func() (*pulse.Pulse, error) {
		if !done {
			done = true
			p, err = pulse.New(pulse.Options{FS: afero.NewReadOnlyFs(afero.NewOsFs())})
		}
		return p, err
	}
}

func writeCandidates(cmd *cli.Command, vs []string) {
	vs = slices.DeleteFunc(slices.Clone(vs), func(v string) bool { return v == "" })
	slices.Sort(vs)
	var b strings.Builder
	for _, v := range slices.Compact(vs) {
		b.WriteString(v)
		b.WriteByte('\n')
	}
	_, _ = cmd.Root().Writer.Write([]byte(b.String()))
}

// commandFlags returns cmd's own flags then its ancestors' (persistent
// root flags included), nearest first.
func commandFlags(cmd *cli.Command) []cli.Flag {
	var out []cli.Flag
	for _, c := range cmd.Lineage() {
		out = append(out, c.Flags...)
	}
	return out
}

// lookupFlag finds the flag a "-x" / "--name" word names on cmd; a bare
// "-" or "--" (a flag still being typed) names none.
func lookupFlag(cmd *cli.Command, word string) cli.Flag {
	name := strings.TrimLeft(word, "-")
	if name == "" {
		return nil
	}
	for _, f := range commandFlags(cmd) {
		if slices.Contains(f.Names(), name) {
			return f
		}
	}
	return nil
}

func takesValue(f cli.Flag) bool {
	df, ok := f.(cli.DocGenerationFlag)
	return ok && df.TakesValue()
}

// longerFlags returns "--other" for every visible flag of cmd whose
// name strictly extends name.
func longerFlags(cmd *cli.Command, name string) []string {
	var out []string
	for _, f := range commandFlags(cmd) {
		if vf, ok := f.(cli.VisibleFlag); ok && !vf.IsVisible() {
			continue
		}
		n := f.Names()[0]
		if len(n) > len(name) && strings.HasPrefix(n, name) {
			out = append(out, "--"+n)
		}
	}
	return out
}

// flagSourceOK returns the valueSource for flag name on cmd: the
// nearest scoped override, else the by-name table. ok is false when the
// flag is unclassified.
func flagSourceOK(cmd *cli.Command, name string) (valueSource, bool) {
	for _, c := range cmd.Lineage() {
		if scope, ok := flagValuesIn[c.FullName()]; ok {
			if src, ok := scope[name]; ok {
				return src, true
			}
		}
	}
	src, ok := flagValues[name]
	return src, ok
}

// flagSource is flagSourceOK without the verdict (an unclassified flag
// completes nothing).
func flagSource(cmd *cli.Command, name string) valueSource {
	src, _ := flagSourceOK(cmd, name)
	return src
}

// CompletionFlagClass reports how completion classifies the value of
// the flag named name (its primary name) on cmd: "vocab", "file",
// "cohort", "field" or "free". ok is false for an unclassified flag.
// TestCompletion_TreeCoverage requires every value-taking flag of every
// leaf to be classified.
func CompletionFlagClass(cmd *cli.Command, name string) (class string, ok bool) {
	src, ok := flagSourceOK(cmd, name)
	if !ok {
		return "", false
	}
	return valueClassNames[src.class], true
}

// CompletionPositionalClasses reports, per leaf path ("pulse errors
// lookup"), the classes of the positionals completion knows.
func CompletionPositionalClasses() map[string][]string {
	out := make(map[string][]string, len(positionalValues))
	for leaf, srcs := range positionalValues {
		for _, s := range srcs {
			out[leaf] = append(out[leaf], valueClassNames[s.class])
		}
	}
	return out
}

// CompletionFlagScopes reports every scoped override: command path →
// the flag names it reclassifies.
func CompletionFlagScopes() map[string][]string {
	out := make(map[string][]string, len(flagValuesIn))
	for scope, flags := range flagValuesIn {
		for name := range flags {
			out[scope] = append(out[scope], name)
		}
		slices.Sort(out[scope])
	}
	return out
}
