// Package pulse is a high-performance, self-describing tabular data processing engine.
//
// Pulse ships as a CLI binary and as an embeddable Go library.
// The library is the primary deliverable; the CLI is a thin adapter over it.
package pulse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/imports"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/service"
	"github.com/frankbardon/pulse/internal/skills"
	"github.com/frankbardon/pulse/internal/template"
	"github.com/frankbardon/pulse/internal/temporal"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// MemberSet is a read-only set of field values consumed by
// FilterToFileBySetAndExpr as an include-set membership test. Build one
// with LoadMemberSetFromReader, which picks the fastest representation
// for the field's type (bitset for categorical, uint64 map for integer /
// date, string map for decimal / fallback). Only sets returned by
// LoadMemberSetFromReader are accepted by FilterToFileBySetAndExpr; any
// other implementation is refused when the membership predicate is built.
type MemberSet interface {
	// Len is the number of distinct members in the set.
	Len() int
	// Kind names the representation: "bitset", "uint64" or "string".
	Kind() string
}

// LoadMemberSetResult is the per-load report returned by
// LoadMemberSetFromReader. Lines is the total of non-blank lines
// processed; NotInDictionary counts categorical values absent from the
// field's dictionary (they can never match a record, so they are
// dropped); Invalid counts numeric lines that failed to parse on the
// integer path. Callers decide whether to surface each counter as a
// warning or a hard error.
type LoadMemberSetResult struct {
	Set             MemberSet
	Lines           int
	NotInDictionary int
	Invalid         int
}

// DateRangeSpec is the wire-level shape of a single labeled date range
// ({label, start, end}). It is the shared model authored inline on a
// GROUP_DATE_RANGES grouper or FILTER_DATE_RANGES filter and registered
// in an Extensions.RangeTables entry. Start / End are ISO date literals;
// nil / empty is an open bound; both bounds are inclusive.
type DateRangeSpec struct {
	Label string  `json:"label"`
	Start *string `json:"start,omitempty"`
	End   *string `json:"end,omitempty"`
}

// toEngineDateRanges converts root DateRangeSpecs to the engine's
// identical-layout type. A nil slice stays nil.
func toEngineDateRanges(in []DateRangeSpec) []processing.DateRangeSpec {
	if in == nil {
		return nil
	}
	out := make([]processing.DateRangeSpec, len(in))
	for i, r := range in {
		out[i] = processing.DateRangeSpec(r)
	}
	return out
}

// fromEngineDateRanges is the inverse of toEngineDateRanges; it always
// returns a fresh slice so callers never alias engine-owned state.
func fromEngineDateRanges(in []processing.DateRangeSpec) []DateRangeSpec {
	if in == nil {
		return nil
	}
	out := make([]DateRangeSpec, len(in))
	for i, r := range in {
		out[i] = DateRangeSpec(r)
	}
	return out
}

// LoadMemberSetFromReader reads newline-delimited values from r and
// returns the best MemberSet for the named field on schema (bitset for
// categorical, uint64 map for integer / date, string map for decimal /
// fallback). Lines are whitespace-trimmed, a leading UTF-8 BOM is
// stripped and blank lines are skipped. Float fields are rejected.
func LoadMemberSetFromReader(r io.Reader, schema *encoding.Schema, fieldName string) (LoadMemberSetResult, error) {
	res, err := processing.LoadMemberSetFromReader(r, schema, fieldName)
	if err != nil {
		return LoadMemberSetResult{}, err
	}
	return LoadMemberSetResult{
		Set:             res.Set,
		Lines:           res.Lines,
		NotInDictionary: res.NotInDictionary,
		Invalid:         res.Invalid,
	}, nil
}

// Type aliases re-exported from the types package so embedders can use
// pulse.Request instead of types.Request.
type (
	Request          = types.Request
	Response         = types.Response
	ComposedRequest  = types.ComposedRequest
	ComposedResponse = types.ComposedResponse

	// FacetRequest is the input to FacetSchema — multi-field, with
	// optional filters, percentiles, histograms, and additive
	// contribution counts.
	FacetRequest = types.FacetRequest
	// FacetResult is the response from FacetSchema.
	FacetResult = types.FacetResult
	// FacetField wraps either a discrete or numeric per-field summary.
	FacetField = types.FacetField
	// FacetDiscrete is the per-value count list for discrete fields.
	FacetDiscrete = types.FacetDiscrete
	// FacetNumeric is the streaming-stats summary for numeric fields.
	FacetNumeric = types.FacetNumeric
	// FacetValueCount is one (value, count) tuple inside FacetDiscrete.
	FacetValueCount = types.FacetValueCount
	// FacetHistogram is the fixed-width binning of a numeric field.
	FacetHistogram = types.FacetHistogram

	// LabelBinding pairs a categorical field with a label table for
	// output-time translation. See types.LabelBinding for semantics.
	LabelBinding = types.LabelBinding
	// LabelMode selects replace vs augment rendering for a binding.
	LabelMode = types.LabelMode
	// SampleRequest is the labelled variant of the Sample entry point.
	SampleRequest = types.SampleRequest

	// SynthSpec is the parsed synthesis request shape.
	SynthSpec = synth.Spec
	// SynthResult is the result of a successful Synth call.
	SynthResult = synth.Result
	// SynthOptions modulate the deterministic seed and other knobs.
	SynthOptions = synth.Options
	// Profile is the cohort statistical summary used by from-profile.
	Profile = synth.Profile
	// ProfileOptions modulate which statistics the profiler captures.
	ProfileOptions = synth.ProfileOptions

	// Example is the full record returned by ExampleGet — runnable
	// request JSON plus the indexed metadata.
	Example = examples.Example
	// ExampleSummary is the lightweight projection returned by
	// ExamplesSearch.
	ExampleSummary = examples.ExampleSummary
	// SkillMetadata is one skill-pack entry as Skills lists it — name,
	// description, kind and the frontmatter cross-references.
	SkillMetadata = skills.Metadata

	// ErrorMetadata is the depth-on-demand projection returned by
	// ErrorLookup, ErrorsByDomain, and ErrorsSearch. Carries the code,
	// domain, canonical Message, and materialised Fixup templates.
	ErrorMetadata = errors.LookupResult
	// ErrorFixup is one repair template attached to an error code.
	ErrorFixup = errors.Fixup
)

// LabelMode constants re-exported for caller ergonomics.
const (
	LabelModeReplace = types.LabelModeReplace
	LabelModeAugment = types.LabelModeAugment
)

// Record is a row of field→value data returned by Sample.
type Record = map[string]any

// Options configures a Pulse instance.
type Options struct {
	// DataDir is the base directory for cohort files.
	// Defaults to PULSE_DATA_DIR if empty and FS is not set.
	DataDir string

	// FS is an optional custom filesystem.
	// When set, DataDir is ignored for filesystem construction.
	FS afero.Fs

	// DisableDefaults turns off the smart-defaults pass that infers
	// operator Type from the named field's schema type when the caller
	// omits it. Defaults to false (defaults enabled). Predict still
	// computes and reports DefaultsApplied independently — this flag
	// governs only what the runtime mutates on the live request.
	DisableDefaults bool

	// DisableComponents suppresses the Response.Components block emitted
	// by every Process / Compose / ProcessChain / Facet response —
	// per-aggregator, per-grouper, per-filterer, crosstab, and run-wide
	// constituent metadata. Defaults to false (components emitted).
	//
	// When true, Response.Components stays nil and the wire form is
	// byte-identical to the pre-Components baseline; format_version is
	// not bumped. The gate sits at the processor's attach helpers and
	// the per-shard merge path, so the MetaAggregator.Components and
	// MetaGrouper.Components construction work is skipped entirely —
	// not built-then-discarded.
	//
	// Per-request override via Request.DisableComponents (*bool): nil
	// inherits this engine setting; explicit true / false wins.
	DisableComponents bool

	// DisableProjection turns off buffered-decode field projection.
	//
	// Projection is ON by default because it is output-transparent: the
	// runtime walks each request to compute the set of schema fields the
	// operators actually read (processing.NeededFields) and skips map
	// writes for fields outside that set. The result payload is identical
	// — only faster and lighter on per-record memory. Extension operators
	// without a registered FieldInputs hook widen the projection to the
	// full schema, so the default is always safe (worst case degenerates
	// to the full-decode behaviour).
	//
	// Set DisableProjection: true to force full-record decode (every
	// schema field populated in the record map regardless of what the
	// request references). Defaults to false (projection enabled).
	DisableProjection bool

	// DisableCrosstabFusion forces every crosstab request onto the
	// buffered path, skipping the fused in-decode streaming arm even
	// when the fusion gate (processing.CanFuseCrosstab) admits the
	// request. Output is identical either way — fusion is a peak-heap
	// optimisation — so this is a diagnostic / benchmarking knob, e.g.
	// to compare fused against buffered memory on the same request.
	// Defaults to false (fusion engages whenever the gate accepts).
	DisableCrosstabFusion bool

	// ImportsDir overrides the managed-imports directory. Defaults to
	// internal/imports.DefaultImportsDir (resolved relative to the Pulse fs
	// root). Honoured before the PULSE_IMPORTS_DIR env var.
	ImportsDir string

	// ImportTTL overrides the default TTL applied to managed imports
	// when the caller does not pass one. Zero falls back to the
	// PULSE_IMPORT_TTL env var, then to internal/imports.DefaultTTL. Negative
	// values pin imports (never expire) by default.
	ImportTTL time.Duration

	// ImportSourceFS is the afero.Fs used to read source files when
	// ImportFile / pulse_import receives an absolute path. Defaults to
	// afero.NewOsFs() on real OS installs, or to FS when FS is an
	// in-memory MemMapFs (so tests stay hermetic). Setting this
	// disables the default jail: the explicit fs IS the boundary.
	ImportSourceFS afero.Fs

	// ImportSourceJailRoot confines absolute source paths to a
	// directory tree. Empty string defaults to os.Getwd() at New
	// time — the natural sandbox for an MCP server or CLI invocation.
	// Ignored when ImportSourceFS is set explicitly.
	ImportSourceJailRoot string

	// Extensions registers domain-specific operators + expression
	// extensions that the runtime treats identically to built-ins.
	// Zero value disables the extension path entirely. See
	// extensions.go for the full surface.
	Extensions Extensions

	// ShardWorkers caps the per-shard parallel worker pool used when
	// Process operates on a shard archive. Zero means runtime.NumCPU();
	// 1 forces strictly serial execution (same byte-for-byte semantics
	// as the serial path). Negative values are rejected at New() time.
	//
	// The parallel reducer engages only when every operator in the
	// request is mergeable — built-ins per their type, an extension
	// aggregator per its AggregatorRegistration.Mergeable declaration.
	// Non-mergeable requests (percentile aggregators, window operators,
	// tier-2 tests, two-pass attributes combined with groupers, ...)
	// fall through to the serial shardIter path with no worker
	// spawning. Worker count is also capped at the shard count — no
	// point spawning more workers than shards.
	//
	// Order semantics: partials merge in shard insertion order (zip
	// central-directory order). Associative+commutative aggregators
	// (count, sum, min, max, null_count, frequency, distinct_count,
	// mode) produce byte-equal results vs the serial path; Welford
	// mean / variance / stddev drift within ULP on well-conditioned
	// inputs (parallel formula, see processing.MergeOnline docstrings).
	ShardWorkers int

	// DecodeWorkers caps the per-cohort parallel decode worker pool the
	// buffered Process path spawns when a single-file cohort is large
	// enough to benefit from segmenting record-decode across workers.
	// Zero means runtime.NumCPU() at dispatch time when the cohort
	// exceeds service.parallelDecodeRecordThreshold (currently 100_000
	// records); 1 forces strictly serial execution regardless of cohort
	// size. Negative values are rejected at New() time.
	//
	// Below the threshold the buffered Process path stays serial
	// regardless of this knob — worker spawn + merge overhead
	// dominates savings on small inputs. Sharded cohorts continue to
	// route through ShardWorkers; the two knobs are orthogonal (one
	// fans out across shards, the other fans out across record
	// segments within a single file or shard payload).
	DecodeWorkers int

	// DefaultTimeZone is the engine-wide IANA zone ("UTC" or an
	// Area/Location name such as "Europe/Berlin") that zone-capable
	// request slots inherit when neither the slot's `tz` nor the
	// request's `time_zone` names one. Empty means UTC. New() validates
	// the name and refuses an unknown one with PULSE_TIMEZONE_UNKNOWN; a
	// valid non-UTC zone is accepted here (whether a request may resolve
	// onto it is decided per request).
	DefaultTimeZone string

	// Strict promotes request-validation warnings into hard errors at
	// runtime. Today this covers the numeric-aggregation-on-categorical
	// check (PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL); future runtime
	// validations follow the same flag. Defaults to false — Process
	// runs the request and emits warnings through the descriptor
	// Envelope (visible via --json at the CLI boundary). Predict's
	// PredictOptions.Strict remains independently controllable.
	Strict bool

	// ProjectBufferedFields enables buffered-decode field projection.
	//
	// Deprecated: projection is on by default; retained for compat.
	// Setting true is a harmless no-op; use DisableProjection to opt out.
	ProjectBufferedFields bool

	// LabelTablesDir points at a directory of JSON files that the
	// engine auto-registers as LabelTables at pulse.New time. Empty
	// disables the loader (the only source of LabelTables is then
	// Options.Extensions.LabelTables, set programmatically).
	//
	// File format: each *.json file is a mapping of source value to
	// label string, or a wrapped object {"description": "...",
	// "rows": {"k": "v"}}. The filename without the .json extension
	// becomes the registered table name.
	//
	// Honoured after PULSE_LABEL_TABLES_DIR — programmatic value
	// wins. Tables loaded from disk merge into Options.Extensions.
	// LabelTables; collisions are rejected with
	// PULSE_EXTENSION_DUPLICATE.
	LabelTablesDir string

	// RangeTablesDir points at a directory of JSON files that the
	// engine auto-registers as RangeTables at pulse.New time. Empty
	// disables the loader (the only source of RangeTables is then
	// Options.Extensions.RangeTables, set programmatically).
	//
	// File format: each *.json file is a bare array of range objects
	// [{"label":"Q1","start":"2024-01-01","end":"2024-03-31"}, ...] or
	// a wrapped object {"description": "...", "ranges": [ ... ]}. The
	// filename without the .json extension becomes the registered
	// table name.
	//
	// Honoured after PULSE_RANGE_TABLES_DIR — the programmatic value
	// wins. A table name declared both programmatically and on disk is
	// a hard error at pulse.New; the registered ranges are then
	// validated via the shared range-compilation pass, surfacing the
	// matching PULSE_RANGE_* code on any structural failure.
	RangeTablesDir string

	// TemplateDirs is the ordered list of directory roots the engine
	// scans for request templates at pulse.New time. Empty disables the
	// loader entirely — no store is built, and template lookups answer
	// PULSE_TEMPLATE_NOT_FOUND.
	//
	// File format: every *.json file beneath a root is a template
	// document. A template's name is its path relative to its OWN root,
	// minus the .json extension, forward-slash separated — a file at
	// <root>/finance/revenue.json is named "finance/revenue" — so
	// subdirectories namespace for free and the root's own location never
	// leaks into the name.
	//
	// Roots are a precedence list and the FIRST root wins: the same name
	// under a later root is shadowed, not rejected, which is what lets a
	// site override directory sit ahead of a shipped default. A root that
	// does not exist is skipped (an absent optional layer is not a
	// fault); a root that exists but is not a directory is an error.
	//
	// Honoured before PULSE_TEMPLATES_DIR — the programmatic value wins,
	// and the env var (roots separated by os.PathListSeparator) is
	// consulted only when this slice is empty. The store is built
	// eagerly, so a malformed template fails pulse.New with the offending
	// file named.
	//
	// The roots stay live after startup: a lookup re-walks them when its
	// cached snapshot has aged past the store's rescan interval, so files
	// added, changed, and removed are picked up without restarting the
	// process. ReloadTemplates forces that walk immediately for callers
	// who need determinism rather than eventual visibility.
	//
	// Breakage after startup degrades per file rather than globally: a
	// template whose file becomes malformed keeps serving its last-good
	// parse, and the broken state surfaces on ListTemplates rather than
	// failing the catalog. That split is deliberate — at startup a broken
	// document is a deploy error, and afterwards it is almost always a
	// half-written editor save.
	TemplateDirs []string

	// EchoRequest causes execution paths and descriptor operations to
	// populate descriptor.Envelope.Request with the *normalized* request
	// that was executed — smart defaults resolved, per-stage forms
	// captured for ProcessChain. Off by default to keep wire size
	// unchanged for hot paths and existing callers. The streaming
	// process / compose paths skip the echo unconditionally (NDJSON
	// emit per row, no envelope construction). Predict / inspect /
	// facet / chain / join descriptor endpoints honor the flag via
	// PredictOptions and the equivalent option structs.
	//
	// Intended for automation callers that want to log or replay the
	// exact request the engine ran without re-deriving defaults
	// themselves.
	EchoRequest bool

	// AutoLabels are default LabelBindings the engine injects into every
	// read request (Process / Compose / Facet / Sample) before
	// validation, so registered label tables render display strings
	// without the caller specifying bindings per request. Each binding
	// is applied only when its Field is present and categorical in the
	// target cohort's schema and the caller has not already bound that
	// field — a default that does not fit a given cohort is silently
	// skipped, never an error. Bindings whose Table is not registered are
	// rejected at New time. Empty (the default) disables auto-binding
	// entirely; existing behaviour is unchanged.
	AutoLabels []LabelBinding

	// FeatureProfile declares the features this instance offers, as a
	// Go value. pulse.New validates it structurally and stores a copy on
	// the instance; the feature list is not applied yet, but its
	// Behaviour switches take effect (OR semantics: a profile switch set
	// true turns the matching Options switch on, and Options cannot turn
	// it back off). Nil means no profile — behaviour is unchanged.
	//
	// Mutually exclusive with FeatureProfileFile: setting both fails
	// New with PULSE_FEATURE_PROFILE_INVALID. pulse.New never reads a
	// profile from the environment.
	FeatureProfile *FeatureProfile

	// FeatureProfileFile names a JSON feature-profile file read through
	// the instance filesystem (Options.FS, or DataDir's OS-backed
	// filesystem — so a relative path resolves under the data root)
	// after extensions are probed and the filesystem is resolved. The
	// body is decoded strictly; a missing or unreadable file, malformed
	// JSON, an unknown key, an absent "features" array or a repeated
	// feature fails New with PULSE_FEATURE_PROFILE_INVALID. Empty means
	// no file. Mutually exclusive with FeatureProfile.
	FeatureProfileFile string

	// SetInferenceMinPct configures the delimited-cell heuristic used
	// when an importer must classify a column as set_* vs categorical.
	// Threshold is the minimum percentage of non-null sampled cells
	// that must contain the inferred delimiter for the column to be
	// classified as set_*; combined with two other gates (post-split
	// unique token count ≤ 64 + average post-split cardinality > 1)
	// the threshold trades off false-positive misclassifications
	// against missed multi-select columns. Zero falls back to the
	// package default of 30. Values > 100 clamp to 100. Per-import
	// overrides via the managed-import Spec still take precedence over
	// this default.
	SetInferenceMinPct int
}

// Pulse is the top-level library facade. It wraps the service layer and
// provides a clean API for embedding Pulse into Go programs.
type Pulse struct {
	svc     *service.Service
	fsys    afero.Fs
	imports *imports.Manager

	// templates is the request-template store built from
	// Options.TemplateDirs (or PULSE_TEMPLATES_DIR). Nil when no template
	// directories are configured — a nil *internal/template.Store is usable, so
	// call sites need no nil check.
	templates *template.Store

	// zones memoises zone loads for this instance; every request-time
	// zone resolution goes through zone(). defaultZone is
	// Options.DefaultTimeZone, validated at New (temporal.UTC when empty).
	zones       *temporal.Cache
	defaultZone *temporal.Zone

	// featureProfile is the validated feature profile from
	// Options.FeatureProfile or Options.FeatureProfileFile, nil when
	// none was given. Its behaviour switches are folded into the engine
	// at New; its feature list is resolved into the service's
	// InstanceSnapshot. Read it through FeatureProfile (a copy).
	featureProfile *FeatureProfile
}

// New creates a new Pulse instance with the given options.
func New(opts Options) (*Pulse, error) {
	if err := loadLabelTablesFromDir(&opts); err != nil {
		return nil, err
	}
	if err := loadRangeTablesFromDir(&opts); err != nil {
		return nil, err
	}
	// Built eagerly: a malformed template must fail startup with its path
	// named, not the first render that reaches it.
	templates, err := loadTemplateStore(&opts)
	if err != nil {
		return nil, err
	}
	universe, err := validateExtensionUniverse(opts.Extensions)
	if err != nil {
		return nil, err
	}
	if err := validateAutoLabels(opts.AutoLabels, opts.Extensions.LabelTables); err != nil {
		return nil, err
	}

	zones := &temporal.Cache{}
	defaultZone := temporal.UTC
	if opts.DefaultTimeZone != "" {
		defaultZone, err = zones.Load(opts.DefaultTimeZone)
		if err != nil {
			return nil, err
		}
	}

	var fsCfg *fs.Config

	if opts.FS != nil {
		// Custom FS provided: use it directly.
		fsCfg, err = fs.New(fs.WithFs(opts.FS))
		if err != nil {
			return nil, fmt.Errorf("pulse: configuring filesystem: %w", err)
		}
	} else if opts.DataDir != "" {
		// Explicit data directory.
		fsCfg, err = fs.New(fs.WithDataDir(opts.DataDir))
		if err != nil {
			return nil, fmt.Errorf("pulse: configuring filesystem: %w", err)
		}
	} else {
		// Fall back to environment defaults.
		fsCfg, err = fs.Default()
		if err != nil {
			return nil, fmt.Errorf("pulse: configuring filesystem: %w", err)
		}
	}

	// After extension probing and filesystem resolution: the file is
	// read through the instance Fs, and later validation classes need
	// the registered extension names.
	featureProfile, err := resolveFeatureProfile(opts, fsCfg.Fs())
	if err != nil {
		return nil, err
	}
	applyFeatureProfileBehaviour(&opts, featureProfile)
	// Resolved once: the enabled set + digest every scoped surface
	// reads. Hidden extension operators are dropped only now — their
	// DependsOn was validated above against ALL registrations.
	featureSet := resolveFeatureSet(universe, featureProfile, effectiveFeatureBehaviour(opts, featureProfile))
	visibleExt := withoutHiddenExtensions(opts.Extensions, featureSet)

	if opts.ShardWorkers < 0 {
		return nil, fmt.Errorf("pulse: ShardWorkers must be >= 0 (0 means runtime.NumCPU(), 1 forces serial)")
	}
	if opts.DecodeWorkers < 0 {
		return nil, fmt.Errorf("pulse: DecodeWorkers must be >= 0 (0 means runtime.NumCPU() above threshold, 1 forces serial)")
	}

	svc := service.New(fsCfg)
	svc.SetDisableDefaults(opts.DisableDefaults)
	svc.SetDisableComponents(opts.DisableComponents)
	svc.SetProjectBufferedFields(opts.ProjectBufferedFields || !opts.DisableProjection)
	svc.SetExtensions(buildRuntimeExtensions(visibleExt))
	extSnap := buildExtensionsSnapshot(visibleExt)
	if len(universe.skills) > 0 {
		if extSnap == nil {
			extSnap = &descx.ExtensionsSnapshot{}
		}
		// Every validated skill; the instance graph drops the ones whose
		// operator this profile hides (extendOntology).
		extSnap.Skills = universe.skills
	}
	svc.SetInstanceSnapshot(descx.NewInstanceSnapshot(extSnap, featureSet))
	svc.SetShardWorkers(opts.ShardWorkers)
	svc.SetDecodeWorkers(opts.DecodeWorkers)
	svc.SetStrict(opts.Strict)
	svc.SetAutoLabels(autoLabelPtrs(opts.AutoLabels))
	svc.SetEchoRequest(opts.EchoRequest)
	svc.SetDisableCrosstabFusion(opts.DisableCrosstabFusion)
	svc.SetTimeZones(opts.DefaultTimeZone, zones)

	importsMgr, err := imports.New(fsCfg.Fs(), imports.Options{
		ImportsDir:                opts.ImportsDir,
		DefaultTTL:                opts.ImportTTL,
		SourceFS:                  opts.ImportSourceFS,
		SourceJailRoot:            opts.ImportSourceJailRoot,
		DefaultSetInferenceMinPct: opts.SetInferenceMinPct,
	})
	if err != nil {
		return nil, fmt.Errorf("pulse: configuring imports manager: %w", err)
	}

	return &Pulse{
		svc:            svc,
		fsys:           fsCfg.Fs(),
		imports:        importsMgr,
		templates:      templates,
		zones:          zones,
		defaultZone:    defaultZone,
		featureProfile: featureProfile,
	}, nil
}

// zone resolves a zone name through this instance's cache. It returns a
// PULSE_TIMEZONE_UNKNOWN *errors.CodedError for an unknown name and the
// temporal.UTC sentinel for "UTC". Callers resolve the empty name to
// p.defaultZone themselves — zone("") is an unknown-zone error.
func (p *Pulse) zone(name string) (*temporal.Zone, error) {
	return p.zones.Load(name)
}

// validateAutoLabels checks each default binding's shape and that its
// table is registered. Field presence + categorical-ness are NOT checked
// here — those are per-cohort and handled at request time by skipping
// defaults that do not fit a given schema.
func validateAutoLabels(bindings []LabelBinding, tables map[string]LabelTable) error {
	for i := range bindings {
		b := bindings[i]
		if strings.TrimSpace(b.Field) == "" {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"auto_labels: Field is required",
				map[string]any{"index": i})
		}
		if strings.TrimSpace(b.Table) == "" {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"auto_labels: Table is required",
				map[string]any{"index": i, "field": b.Field})
		}
		mode := b.LabelModeOrDefault()
		if mode != LabelModeReplace && mode != LabelModeAugment {
			return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("auto_labels: Mode %q is invalid (expected %q or %q)", b.Mode, LabelModeReplace, LabelModeAugment),
				map[string]any{"index": i, "field": b.Field, "mode": string(b.Mode)})
		}
		if _, ok := tables[b.Table]; !ok {
			return errors.NewCodedErrorWithDetails(errors.PULSE_LABEL_TABLE_UNKNOWN,
				fmt.Sprintf("auto_labels: table %q not registered on Extensions.LabelTables", b.Table),
				map[string]any{"index": i, "field": b.Field, "table": b.Table})
		}
	}
	return nil
}

// autoLabelPtrs copies the value-typed Options.AutoLabels into the
// pointer slice the service layer stores.
func autoLabelPtrs(bindings []LabelBinding) []*types.LabelBinding {
	if len(bindings) == 0 {
		return nil
	}
	out := make([]*types.LabelBinding, len(bindings))
	for i := range bindings {
		cp := bindings[i]
		out[i] = &cp
	}
	return out
}

// Open reads a .pulse file and returns a Cohort with the parsed schema.
//
// Anchor syntax: a path of the form "archive.pulse#shard.pulse" opens
// the archive, locates the named shard inside it, and returns a single-
// shard cohort whose schema comes from the shard's own header (not the
// canonical schema in `_schema.pulse`). The returned Cohort has an
// empty Shards slice — anchor-resolved shards stand alone for the
// purposes of facade methods. Anchors require an archive backing the
// path; using `#` against a single-file `.pulse` raises
// PULSE_ARCHIVE_MAGIC_INVALID. A literal `#` in a filename is not
// supported in v1.
//
// Anchor parsing happens inside internal/service.Service.Open as well, so the
// other facade methods (Process, Sample, Facet, ...) that receive an
// anchored Cohort path resolve consistently.
func (p *Pulse) Open(ctx context.Context, path string) (*Cohort, error) {
	inner, err := p.svc.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	return &Cohort{inner: inner}, nil
}

// Process executes a single processing request against a cohort.
func (p *Pulse) Process(ctx context.Context, req *Request) (*Response, error) {
	resp, err := p.svc.Process(ctx, req)
	if err == nil && req != nil && req.Cohort != nil {
		p.touchManaged(ctx, resolveCohortPath(req.Cohort))
	}
	return resp, err
}

// Row is a single result row in a processing stream.
type Row = service.Row

// RowIter is a pull-based iterator over a processing result. Each call
// to Next returns the next row or (nil, false, nil) on exhaustion. Close
// releases underlying resources. Metadata returns the run metadata once
// available (always present after the iterator is drained).
type RowIter = service.RowIter

// ProcessStream executes a request and returns a pull-based row iterator
// over the result. Equivalent to Process for any request shape — same
// gates, same errors — but streaming consumers (HTTP responders, NDJSON
// writers, downstream pipelines) can drain rows one at a time without
// buffering the full result in their own memory.
//
// Predict's Streamable flag reports whether the underlying execution
// avoids buffering inside the engine; ProcessStream wraps the result
// regardless, so the API is stable for non-streamable requests too.
func (p *Pulse) ProcessStream(ctx context.Context, req *Request) (RowIter, error) {
	return p.svc.ProcessStream(ctx, req)
}

// Compose executes multiple requests against a cohort and returns a
// structured ComposedResponse carrying both per-request results and any
// composition-level overlay output.
//
// Returned shape:
//
//   - Responses []*Response — one entry per request in req.Requests,
//     in input order. Per-request errors surface as a Response with
//     non-empty Errors; the call returns a top-level error only on
//     orchestration failures (validation, cohort open, etc.).
//   - Overlays []OverlayLayer — one layer per OverlaySpec in
//     req.Overlays, in declaration order. The Compose-host overlay
//     fold (service.applyComposeOverlays) writes layers post-execution;
//     when req.Overlays is empty the slot is nil/omitempty.
//   - Overlays[i].Warnings []OverlayWarning — per-overlay diagnostics
//     emitted by the handler (cohesion failures, missing host
//     coordinates, threshold breaches). Empty when the layer produced
//     no diagnostics.
func (p *Pulse) Compose(ctx context.Context, req *ComposedRequest) (*ComposedResponse, error) {
	return p.svc.Compose(ctx, req)
}

// CountRecords returns the number of records in the cohort at path
// without decoding payload bytes. For single-file cohorts this is
// O(1) — bytes read is bounded by header + schema size, independent
// of the record count. For shard archives the cost is O(N shards)
// (zip central-directory walk + reserved `_schema.pulse` SHRD
// trailer). Anchor paths (`archive#shard.pulse`) report the named
// shard's count only.
//
// Use this when a planner needs to make a cardinality-dependent
// decision (sample injection, smaller-side hash join, batch
// sizing) without paying the per-row decode cost of a full Process.
func (p *Pulse) CountRecords(ctx context.Context, path string) (uint64, error) {
	count, err := p.svc.CountRecords(ctx, path)
	if err == nil {
		p.touchManaged(ctx, path)
	}
	return count, err
}

// ChainRequest re-exports types.ChainRequest so callers can use
// pulse.ChainRequest as the input to ProcessChain.
type ChainRequest = types.ChainRequest

// ChainStage re-exports types.ChainStage.
type ChainStage = types.ChainStage

// ChainResponse re-exports types.ChainResponse.
type ChainResponse = types.ChainResponse

// ProcessChain executes a source-rooted linear chain of Process
// requests. The first stage runs against the cohort identified by
// req.Cohort; each subsequent stage receives the previous stage's
// rows as its input. All stages must be mergeable per the v1 chain
// gate (an extension aggregator qualifies when its registration
// declares Mergeable); a non-mergeable stage surfaces
// PULSE_CHAIN_NOT_MERGEABLE with the offending stage index so the
// caller can fall back to per-stage Process calls.
//
// Why chain? Building a linear plan as N independent Process calls
// pays N file-open + schema-rebind + per-stage validate costs.
// ProcessChain collapses those into one open + one synth-schema
// per intermediate stage, keeping the streaming iterator stack alive
// across stages.
func (p *Pulse) ProcessChain(ctx context.Context, req *ChainRequest) (*ChainResponse, error) {
	resp, err := p.svc.ProcessChain(ctx, req)
	if err == nil && req != nil && req.Cohort != nil {
		p.touchManaged(ctx, resolveCohortPath(req.Cohort))
	}
	return resp, err
}

// ComposeOptions controls parallel execution. See internal/service.ComposeOptions.
type ComposeOptions = service.ComposeOptions

// ComposeParallel runs every request in req concurrently across a
// bounded worker pool and returns a structured ComposedResponse.
// Workers share the engine's read-only registries; each Process call
// constructs fresh stateful operators per request, so concurrent
// execution is safe.
//
// Defaults: MaxWorkers = runtime.GOMAXPROCS(0), no per-request timeout,
// FailFast = true (set FailFast=false to collect every request's outcome
// instead of cancelling siblings on first error).
//
// Returned shape mirrors Compose:
//
//   - Responses []*Response — one entry per request in req.Requests,
//     in input order (not completion order). Workers compose the slice
//     deterministically before returning.
//   - Overlays []OverlayLayer — one layer per OverlaySpec in
//     req.Overlays, in declaration order. The Compose-host overlay
//     fold runs after every per-request Process has settled, so
//     overlay handlers always observe the full Responses slice.
//   - Overlays[i].Warnings []OverlayWarning — per-overlay diagnostics
//     surfaced by distributeComposeWarnings (cohesion failures,
//     missing host coordinates, panel-target overflow). Empty when
//     the layer produced no diagnostics.
func (p *Pulse) ComposeParallel(ctx context.Context, req *ComposedRequest, opts ComposeOptions) (*ComposedResponse, error) {
	return p.svc.ComposeParallel(ctx, req, opts)
}

// Import converts tabular source data into a .pulse file.
// The job's FS field is set to the Pulse instance's filesystem if not already set.
func (p *Pulse) Import(ctx context.Context, job *pio.ImportJob) (*pio.ImportReport, error) {
	if job.FS == nil {
		job.FS = p.fsys
	}
	return job.Run(ctx)
}

// Export converts a .pulse file into tabular output.
// The job's FS field is set to the Pulse instance's filesystem if not already set.
//
// When job.Labels is non-empty, the facade builds the runtime label
// resolver from the Service's registered LabelTables and attaches it
// to job.LabelResolver before Run. The resolver applies replace /
// augment translation to categorical column values during export.
func (p *Pulse) Export(ctx context.Context, job *pio.ExportJob) (*pio.ExportReport, error) {
	if job.FS == nil {
		job.FS = p.fsys
	}
	if job.LabelResolver == nil && len(job.Labels) > 0 {
		r, err := p.newIOLabelResolver(job.Labels)
		if err != nil {
			return nil, err
		}
		job.LabelResolver = r
	}
	return job.Run(ctx)
}

// ExportTransfer compresses a cohort's exact bytes into a zstd transfer
// artifact (`.pulse.zst`) for moving it between machines. The job's FS
// field is set to the Pulse instance's filesystem if not already set.
//
// Compression is TRANSPORT-ONLY: the artifact is never opened as a
// cohort (every read surface refuses it with PULSE_COHORT_COMPRESSED),
// and ImportTransfer turns it back into a byte-identical `.pulse` at
// rest. Works for single-file cohorts (0x01 and 0x02) and whole shard
// archives alike, streaming in bounded memory.
func (p *Pulse) ExportTransfer(ctx context.Context, job *pio.TransferExportJob) (*pio.TransferReport, error) {
	if job.FS == nil {
		job.FS = p.fsys
	}
	return job.Run(ctx)
}

// ImportTransfer decompresses a transfer artifact produced by
// ExportTransfer into a byte-identical `.pulse` at rest (temp file,
// fsync, rename — a damaged artifact leaves nothing behind). The job's
// FS field is set to the Pulse instance's filesystem if not already set.
// The returned report's SHA256 matches the sender's.
func (p *Pulse) ImportTransfer(ctx context.Context, job *pio.TransferImportJob) (*pio.TransferReport, error) {
	if job.FS == nil {
		job.FS = p.fsys
	}
	return job.Run(ctx)
}

// Convert chains import and export with no intermediate file on disk.
// The job's FS field is set to the Pulse instance's filesystem if not already set.
//
// Labels apply to the export half only — see Export.
func (p *Pulse) Convert(ctx context.Context, job *pio.ConvertJob) (*pio.ConvertReport, error) {
	if job.FS == nil {
		job.FS = p.fsys
	}
	if job.LabelResolver == nil && len(job.Labels) > 0 {
		r, err := p.newIOLabelResolver(job.Labels)
		if err != nil {
			return nil, err
		}
		job.LabelResolver = r
	}
	return job.Run(ctx)
}

// Type aliases re-exported from the imports package so embedders can
// use pulse.ImportSpec instead of internal/imports.Spec.
type (
	ImportSpec   = imports.Spec
	ImportResult = imports.Result
	ImportEntry  = imports.Entry
	// ImportSidecar is the managed-import metadata document an
	// ImportEntry carries.
	ImportSidecar = imports.Sidecar
)

// ImportFile auto-detects the source format, converts the source into a
// managed .pulse file under the imports pool, and returns the resulting
// handle. Pulse-native sources pass through unchanged (no copy, no
// sidecar). Sliding-window TTL applies to managed handles — every
// subsequent Inspect / Predict / Process / Sample / Facet against
// the handle bumps the expiry forward.
func (p *Pulse) ImportFile(ctx context.Context, spec ImportSpec) (*ImportResult, error) {
	return p.imports.Open(ctx, spec)
}

// Drop removes a managed import handle (and its sidecar) from the pool.
// Returns PULSE_IMPORT_SOURCE_MISSING when the handle is unknown.
func (p *Pulse) Drop(ctx context.Context, handle string) error {
	return p.imports.Drop(ctx, handle)
}

// Imports returns a snapshot of the managed-imports pool. Sweep is not
// invoked; expired entries are flagged via Entry.Expired so callers can
// render them. Results are sorted by handle name.
func (p *Pulse) Imports(ctx context.Context) ([]ImportEntry, error) {
	return p.imports.List(ctx)
}

// SweepImports removes every expired managed handle and returns the
// list of swept handle names. Invoked opportunistically by ImportFile
// and exposed here for callers that want explicit control (CLI
// maintenance, periodic ticker, etc.).
func (p *Pulse) SweepImports(ctx context.Context) ([]string, error) {
	return p.imports.Sweep(ctx)
}

// ResolveImport returns the managed-pool path for a handle, or
// PULSE_IMPORT_SOURCE_MISSING when no such handle exists. Embedders
// who want to address a managed handle by name (instead of by path)
// run path through this resolver before passing it to Inspect/Process.
func (p *Pulse) ResolveImport(ctx context.Context, handle string) (string, error) {
	return p.imports.Resolve(ctx, handle)
}

// touchManaged is the hook each read-path facade method fires on the
// resolved cohort path. Best-effort: errors are intentionally swallowed
// — TTL slides are an optimisation, not a correctness path.
func (p *Pulse) touchManaged(ctx context.Context, path string) {
	if p.imports == nil || path == "" {
		return
	}
	_ = p.imports.Touch(ctx, path)
}

// Inspect reads a .pulse file header and schema, returning structured field information.
// It never reads record data.
//
// Callers that need the inspect WARNINGS (a truncated payload tail
// raises one) or dictionary options want InspectEnvelope — this
// wrapper keeps the result and discards the rest of the envelope.
func (p *Pulse) Inspect(ctx context.Context, path string) (*descriptor.InspectResult, error) {
	env, err := p.InspectEnvelope(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	if len(env.Errors) > 0 {
		return nil, fmt.Errorf("pulse: inspect: %s", env.Errors[0].Message)
	}
	result, ok := env.Data.(*descriptor.InspectResult)
	if !ok {
		return nil, fmt.Errorf("pulse: inspect returned unexpected type")
	}
	return result, nil
}

// InspectEnvelope is Inspect's envelope-returning sibling: same
// header-only read, same anchor resolution, same afero.Fs, but it
// returns the descriptor envelope WHOLE and accepts InspectOptions
// (nil means defaults).
//
// It exists because the two things a caller can need beyond the result
// are unreachable through Inspect: the InspectOptions.FullDict knob,
// and env.Warnings — a cohort whose payload length is not a whole
// multiple of the record stride reports its floored record count with
// an ENCODING_INVALID warning beside it, and Inspect drops that
// warning on the floor. `pulse cohort inspect --json` / `--full-dict`
// used to hand bytes from its own os.ReadFile to descriptor's
// byte-level inspect for exactly this reason, which cost it anchor resolution
// (archive.pulse#shard.pulse resolved in text mode and failed under
// --json) and the injected filesystem along with it.
//
// A returned error is a READ failure (missing file, unresolvable
// anchor). An envelope-level fault — a bad header, a corrupt archive —
// comes back as a non-nil envelope carrying env.Errors, so a --json
// caller can emit the coded envelope verbatim.
func (p *Pulse) InspectEnvelope(ctx context.Context, path string, opts *descriptor.InspectOptions) (*descriptor.Envelope, error) {
	readPath := path
	anchorEntry := ""
	if archivePath, entry, ok := service.SplitAnchorPath(path); ok {
		readPath = archivePath
		anchorEntry = entry
	}

	data, err := afero.ReadFile(p.fsys, readPath)
	if err != nil {
		return nil, fmt.Errorf("pulse: reading file for inspect: %w", err)
	}

	if anchorEntry != "" {
		shardBytes, aerr := extractShardBytes(data, anchorEntry)
		if aerr != nil {
			return nil, fmt.Errorf("pulse: inspect anchor: %w", aerr)
		}
		data = shardBytes
	}

	env, err := p.InspectBytes(ctx, data, opts)
	if err != nil {
		return nil, err
	}
	if len(env.Errors) == 0 {
		// TTL slide on a successful read only — an unreadable cohort is
		// not a use of the managed import.
		p.touchManaged(ctx, path)
	}
	return env, nil
}

// Predict validates a request against a .pulse file without executing it.
// It reads only the header and schema, never record data.
func (p *Pulse) Predict(ctx context.Context, req *Request) (*descriptor.PredictResult, error) {
	if req.Cohort == nil {
		return nil, fmt.Errorf("pulse: predict requires a cohort")
	}

	path := resolveCohortPath(req.Cohort)

	// Anchor syntax (`archive.pulse#shard.pulse`): resolve against the
	// named shard's standalone bytes so Predict validates against the
	// shard's own schema and record count, mirroring what
	// internal/service.Open(anchor) returns at runtime.
	readPath := path
	anchorEntry := ""
	if archivePath, entry, ok := service.SplitAnchorPath(path); ok {
		readPath = archivePath
		anchorEntry = entry
	}

	data, err := afero.ReadFile(p.fsys, readPath)
	if err != nil {
		return nil, fmt.Errorf("pulse: reading file for predict: %w", err)
	}

	if anchorEntry != "" {
		shardBytes, aerr := extractShardBytes(data, anchorEntry)
		if aerr != nil {
			return nil, fmt.Errorf("pulse: predict anchor: %w", aerr)
		}
		data = shardBytes
	}

	env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{
		Extensions:      p.svc.ExtensionsSnapshot(),
		Instance:        p.svc.InstanceSnapshot(),
		DefaultTimeZone: p.svc.DefaultTimeZone(),
		ZoneLoader:      p.svc.ZoneLoader(),
		DisableDefaults: p.svc.DefaultsDisabled(),
		SchemaLoader:    p.predictSchemaLoader(ctx),
	})
	if len(env.Errors) > 0 {
		// Return the result (which has Valid=false) rather than erroring.
		result, ok := env.Data.(*descriptor.PredictResult)
		if ok {
			p.touchManaged(ctx, path)
			return result, nil
		}
		return nil, fmt.Errorf("pulse: predict: %s", env.Errors[0].Message)
	}

	result, ok := env.Data.(*descriptor.PredictResult)
	if !ok {
		return nil, fmt.Errorf("pulse: predict returned unexpected type")
	}
	p.touchManaged(ctx, path)
	return result, nil
}

// InspectBytes inspects an in-memory .pulse cohort — a single file or a
// whole shard archive, detected by its leading magic bytes — and
// returns the descriptor envelope WHOLE, warnings included (a payload
// whose length is not a whole multiple of the record stride reports
// its floored record count beside an ENCODING_INVALID warning). It is
// the byte-level twin of InspectEnvelope for a caller that already
// holds the bytes: no filesystem read, no anchor resolution, no
// managed-import TTL slide. opts may be nil (defaults).
//
// Inspect has no request and no strict mode, so it reads nothing else
// from the instance's Options. A returned error is a cancelled ctx;
// every fault in the bytes themselves comes back as env.Errors so a
// --json caller can emit the coded envelope verbatim.
func (p *Pulse) InspectBytes(ctx context.Context, data []byte, opts *descriptor.InspectOptions) (*descriptor.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return descx.Inspect(bytes.NewReader(data), opts), nil
}

// PredictBytes validates req against an in-memory .pulse cohort — a
// single file or a whole shard archive, detected by its leading magic
// bytes — without executing it, and returns the descriptor envelope
// whole.
//
// Unlike Predict, every predict option comes from the instance: the
// extension snapshot (so an embedder-registered operator is KNOWN, not
// flagged unknown), Options.Strict (warnings promoted to errors) and
// Options.EchoRequest (envelope.Request carries the normalized
// request). req.Cohort is not read — the bytes ARE the cohort.
//
// A returned error is a nil req or a cancelled ctx; every validation
// fault comes back as env.Errors with PredictResult.Valid false.
func (p *Pulse) PredictBytes(ctx context.Context, data []byte, req *Request) (*descriptor.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req == nil {
		return nil, fmt.Errorf("pulse: predict requires a request")
	}
	return descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{
		Strict:          p.svc.Strict(),
		EchoRequest:     p.svc.EchoRequest(),
		Extensions:      p.svc.ExtensionsSnapshot(),
		Instance:        p.svc.InstanceSnapshot(),
		DefaultTimeZone: p.svc.DefaultTimeZone(),
		ZoneLoader:      p.svc.ZoneLoader(),
		DisableDefaults: p.svc.DefaultsDisabled(),
		SchemaLoader:    p.predictSchemaLoader(ctx),
	}), nil
}

// predictSchemaLoader reads a cohort's header + schema through the
// runtime's own opener (anchors and shard archives included), so
// predict validates a join against the joined schema the runtime
// builds. Only the header and schema of a single-file cohort are read.
func (p *Pulse) predictSchemaLoader(ctx context.Context) func(string) (*encoding.Schema, error) {
	return func(path string) (*encoding.Schema, error) {
		c, err := p.svc.Open(ctx, path)
		if err != nil {
			return nil, err
		}
		return c.Schema(), nil
	}
}

// Sample returns up to n rows from the cohort as maps of field name to value.
func (p *Pulse) Sample(ctx context.Context, path string, n int) ([]Record, error) {
	rows, err := p.svc.Sample(ctx, path, n)
	if err == nil {
		p.touchManaged(ctx, path)
	}
	return rows, err
}

// SampleResult bundles the rows and the resolver-side warnings from
// a labelled Sample call. Warnings carry PULSE_LABEL_COLLISION and
// PULSE_LABEL_LOOKUP_MISS records; an empty slice means the labels
// resolved cleanly (or no Labels were requested).
type SampleResult struct {
	Rows     []Record
	Warnings []SampleWarning
}

// SampleWarning is the envelope-ready projection of a single resolver
// warning. The shape mirrors descriptor.EnvelopeEntry so callers can
// fold it into a descriptor.Envelope at the CLI / MCP boundary.
type SampleWarning struct {
	Code    string
	Message string
	Details map[string]any
}

// SampleWithRequest is the labelled variant of Sample. The req struct
// carries the cohort path (via Cohort.Filename), the row cap (N), and
// LabelBinding entries that translate categorical values to display
// labels in the returned rows.
//
// When Labels is empty the call degenerates to the same shape as
// Sample, returning a SampleResult with no Warnings and no
// transformation applied to the rows.
func (p *Pulse) SampleWithRequest(ctx context.Context, req *SampleRequest) (*SampleResult, error) {
	if req == nil {
		return nil, fmt.Errorf("pulse: sample request is required")
	}
	if req.Cohort == nil || req.Cohort.Filename == "" {
		return nil, fmt.Errorf("pulse: sample request requires Cohort.Filename")
	}
	path := req.Cohort.Filename
	rows, warns, err := p.svc.SampleWithRequest(ctx, req, path)
	if err != nil {
		return nil, err
	}
	p.touchManaged(ctx, path)
	out := &SampleResult{Rows: rows}
	if len(warns) > 0 {
		out.Warnings = make([]SampleWarning, len(warns))
		for i, w := range warns {
			out.Warnings[i] = SampleWarning{
				Code:    string(w.Code),
				Message: w.Message,
				Details: w.Details,
			}
		}
	}
	return out, nil
}

// FilterToFile reads the .pulse cohort at src, evaluates filterExpr
// (FILTER_EXPRESSION semantics — same operators, identifiers, expr
// functions, and lookup tables available to pulse.Process with a
// FILTER_EXPRESSION filterer) against every record, and writes a new
// .pulse cohort at dst containing only matching records.
//
// Dispatch matches the rest of the facade. Single-file inputs produce
// single-file outputs whose header + schema bytes are copied byte-for-
// byte from src. Shard archives produce shard archives that preserve
// the per-shard layout: one input shard maps to one output shard at
// the same basename, in central-directory (insertion) order, with
// per-shard header + schema + categorical-dictionary bytes copied
// verbatim. Empty shards survive so shard_count metadata stays stable;
// the canonical `_schema.pulse` trailer's aggregate_record_count is
// refreshed to the surviving total. The anchor form
// `archive.pulse#shard.pulse` resolves a single shard and writes a
// single-file output.
//
// Returns the number of records written to dst (sum across shards for
// archive inputs).
func (p *Pulse) FilterToFile(ctx context.Context, src, dst, filterExpr string) (int64, error) {
	n, err := p.svc.FilterToFile(ctx, src, dst, filterExpr)
	if err == nil {
		p.touchManaged(ctx, src)
	}
	return n, err
}

// ResolveCanonicalSchema returns the canonical encoding.Schema for the
// cohort at src without streaming records. Resolves single-file,
// shard-archive, and `archive#shard` anchor inputs identically to the
// rest of the facade.
//
// Useful for callers that need to build an include-set before invoking
// FilterToFileBySetAndExpr: the set loader requires the schema to pick
// the best MemberSet impl (bitset / uint64 / string) for the field's
// type.
func (p *Pulse) ResolveCanonicalSchema(ctx context.Context, src string) (*encoding.Schema, error) {
	return p.svc.ResolveCanonicalSchema(ctx, src)
}

// FilterToFileBySetAndExpr applies an optional MemberSet membership
// test (record's value for includeField must be in set) combined with
// an optional FILTER_EXPRESSION (filterExpr) to every record in src,
// writing the survivors to dst. At least one of (set, filterExpr) must
// be supplied; if both are present they are AND-combined and the set
// is tested first so per-row work short-circuits on misses without
// paying the expr eval cost.
//
// Input-shape dispatch is identical to FilterToFile (single-file,
// shard archive, anchor). The set must be built against the same
// canonical schema as src.
//
// Returns the number of records written to dst (sum across shards for
// archive inputs).
func (p *Pulse) FilterToFileBySetAndExpr(ctx context.Context, src, dst, includeField string, set MemberSet, filterExpr string) (int64, error) {
	n, err := p.svc.FilterToFileBySetAndExpr(ctx, src, dst, includeField, set, filterExpr)
	if err == nil {
		p.touchManaged(ctx, src)
	}
	return n, err
}

// Synth materializes a synthetic .pulse file at output from spec. The
// generator is deterministic for a given (spec, opts.Seed) pair: same
// seed produces a byte-identical file.
//
// Setting opts.SourceCohort activates the tagged top-up path used by
// `synth from-profile`: output becomes source's real rows (tagged
// _synthetic=false) plus spec.RowCount newly generated rows (tagged
// _synthetic=true), output must be a path distinct from SourceCohort,
// and SourceCohort itself is never opened for write. Leaving
// SourceCohort empty (the default) is unchanged plain synthesis, used by
// `synth from-schema` and any caller that built the Spec directly.
//
// Setting opts.FidelityReportPath alongside opts.SourceCohort
// additionally writes a JSON fidelity report after generation
// completes: a per-field marginal comparison between the source and
// generated partitions of output, driven through TEST_KS (numeric
// fields) / TEST_CHISQ (categorical fields) against the tagged
// _synthetic column rather than new comparison math, plus — when spec
// carries any Correlations — a pairwise numeric-numeric correlation
// delta section comparing each pair's captured/target rho against its
// realized rho in the newly generated partition (synth.PairwiseFidelity),
// plus — when spec carries any CategoricalPairs / CategoricalNumericPairs
// — the categorical-categorical contingency-table delta and
// categorical-numeric conditional-mean/std delta sections
// (synth.CategoricalPairFidelity / synth.CategoricalNumericPairFidelity,
// E3-S4). opts.FidelityWarnings, when set, is copied verbatim onto the
// report's Warnings slot — the mechanism `synth from-profile` uses to
// surface Profile.Warnings (e.g. a thin numeric/categorical-pair
// warning) beside the pairwise deltas they qualify, across all three
// pair kinds. Ignored when SourceCohort is empty — the plain synthesis
// path has no _synthetic partition to compare against.
func (p *Pulse) Synth(_ context.Context, spec *SynthSpec, output string, opts SynthOptions) (*SynthResult, error) {
	res, err := synth.Synth(p.fsys, spec, output, opts)
	if err != nil {
		return nil, err
	}
	if opts.SourceCohort != "" && opts.FidelityReportPath != "" {
		// The report's warnings array carries BOTH channels: the ones
		// the caller supplied (capture-time, translation-time) and the
		// ones generation itself raised. See mergeFidelityWarnings for
		// why the fold lives here rather than at the CLI leaf.
		if err := writeSynthFidelityReport(p.fsys, output, opts.FidelityReportPath, spec,
			mergeFidelityWarnings(opts.FidelityWarnings, res.Warnings)); err != nil {
			return nil, err
		}
		res.FidelityReportPath = opts.FidelityReportPath
	}
	return res, nil
}

// Profile reads a .pulse file at path and returns a statistical summary
// suitable for from-profile synthesis. The profile retains no individual
// rows from the source data.
func (p *Pulse) Profile(_ context.Context, path string, opts ProfileOptions) (*Profile, error) {
	return synth.ProfileFile(p.fsys, path, opts)
}

// Facet returns distinct values for the named field in the cohort.
// Categorical fields short-circuit through the dictionary; numeric
// fields stream the file collecting distinct float values. For richer
// summaries (counts, null tallies, statistics, histograms, additive
// contributions) call FacetSchema instead.
func (p *Pulse) Facet(ctx context.Context, path string, field string) ([]string, error) {
	values, err := p.svc.Facet(ctx, path, field)
	if err == nil {
		p.touchManaged(ctx, path)
	}
	return values, err
}

// FacetSchema runs a multi-field rich facet against the cohort named in
// req.Cohort. Returns per-field summaries (discrete value counts or
// numeric statistics), with optional percentiles, fixed-width
// histograms, and "additive" contribution counts that report what each
// distinct value of an additive field would yield if it were added to
// the base filter.
//
// Streamability: requests with no NumericPercentiles run in a single
// pass; requests with percentiles buffer the requested numeric fields'
// non-null values and sort once before percentile interpolation.
func (p *Pulse) FacetSchema(ctx context.Context, req *FacetRequest) (*FacetResult, error) {
	if req == nil {
		return nil, fmt.Errorf("pulse: facet schema requires a request")
	}
	resp, err := p.svc.FacetSchema(ctx, req)
	if err == nil && req.Cohort != nil {
		p.touchManaged(ctx, resolveCohortPath(req.Cohort))
	}
	return resp, err
}

// LookupRequest re-exports types.LookupRequest so callers can use
// pulse.LookupRequest as the input to Lookup.
type LookupRequest = types.LookupRequest

// LookupResult re-exports types.LookupResult — the response from
// Lookup.
type LookupResult = types.LookupResult

// LookupMultiplicity re-exports types.LookupMultiplicity — the
// duplicate-key handling mode on LookupRequest (assert-unique by
// default; opt into "first" or "all" for a key that may match more
// than one row).
type LookupMultiplicity = types.LookupMultiplicity

// LookupKey re-exports types.LookupKey — one ordered key column/value
// pair in a composite LookupRequest.Keys tuple, so callers can build a
// composite key without importing the types package.
type LookupKey = types.LookupKey

// LookupMultiplicity mode constants, re-exported so callers can set
// LookupRequest.Multiplicity without importing the types package.
const (
	// LookupMultiplicityAssertUnique errors PULSE_LOOKUP_AMBIGUOUS when
	// the key matches more than one row. It is the zero-value default.
	LookupMultiplicityAssertUnique = types.LookupMultiplicityAssertUnique
	// LookupMultiplicityFirst returns the lowest-row-id match.
	LookupMultiplicityFirst = types.LookupMultiplicityFirst
	// LookupMultiplicityAll returns every match, ascending row-id order.
	LookupMultiplicityAll = types.LookupMultiplicityAll
)

// Lookup resolves a point lookup (single-key or composite, via
// req.Keys) against the cohort named in req.Cohort, using the prebuilt
// sidecar index (see BuildIndex). Returns PULSE_INDEX_MISSING when no
// sidecar index exists for the requested key fields, PULSE_LOOKUP_NOT_FOUND
// when the index exists but no record matches, or PULSE_LOOKUP_AMBIGUOUS
// when more than one record matches and req.Multiplicity is (or
// defaults to) LookupMultiplicityAssertUnique. See internal/service.Service.Lookup
// for the full algorithm.
func (p *Pulse) Lookup(ctx context.Context, req *LookupRequest) (*LookupResult, error) {
	if req == nil {
		return nil, fmt.Errorf("pulse: lookup requires a request")
	}
	resp, err := p.svc.Lookup(ctx, req)
	if err == nil && req.Cohort != nil {
		p.touchManaged(ctx, resolveCohortPath(req.Cohort))
	}
	return resp, err
}

// BuildIndexResult re-exports internal/service.BuildIndexResult — the outcome
// of a successful point-lookup sidecar index build: the derived
// sidecar path plus the in-memory SidecarIndex that was serialized
// there.
type BuildIndexResult = service.BuildIndexResult

// SidecarIndex is the in-memory point-lookup sidecar index carried by
// BuildIndexResult.Index: the source cohort's content fingerprint, the
// ordered key spec, the hash-bucket table and the source-stat snapshot
// (size + modification time) taken at build time.
type SidecarIndex = encx.Index

// SidecarIndexKeySpec is one ordered key column of a SidecarIndex
// (SidecarIndex.Keys): the column name and its field type.
type SidecarIndexKeySpec = encx.IndexKeySpec

// SidecarIndexBucket is one hash bucket of a SidecarIndex
// (SidecarIndex.Buckets): the entries whose key hashed to it.
type SidecarIndexBucket = encx.IndexBucket

// SidecarIndexEntry is one distinct key inside a SidecarIndexBucket:
// the key's raw on-wire bytes and the record IDs that carry it.
type SidecarIndexEntry = encx.IndexEntry

// CohortFingerprint is the SHA-256 content fingerprint of a cohort's
// bytes, as recorded in SidecarIndex.Fingerprint at build time and
// recomputed by VerifyIndex to decide freshness.
type CohortFingerprint = encx.Fingerprint

// BuildIndex builds a point-lookup sidecar index for the cohort at
// path over the ordered key columns named in keyFields (a single
// element is the degenerate single-key case; more than one produces a
// composite key, in column order). Delegates to internal/service.Service.BuildIndex
// for the full algorithm and error surface — see that method's doc
// comment for the scan/bucket/write contract, the
// PULSE_INDEX_UNSUPPORTED_SHARDED shard-archive rejection, and the
// PROCESSING_CONFIG disallowed-key-type rejection.
func (p *Pulse) BuildIndex(ctx context.Context, path string, keyFields []string) (*BuildIndexResult, error) {
	res, err := p.svc.BuildIndex(ctx, path, keyFields)
	if err == nil {
		p.touchManaged(ctx, path)
	}
	return res, err
}

// VerifyIndexResult re-exports internal/service.VerifyIndexResult — the outcome
// of a Service.VerifyIndex freshness check.
type VerifyIndexResult = service.VerifyIndexResult

// IndexFreshnessReason re-exports internal/service.IndexFreshnessReason — why
// VerifyIndex reached its Fresh/stale verdict
// ("stat_mismatch"/"fingerprint_match"/"fingerprint_mismatch").
type IndexFreshnessReason = service.IndexFreshnessReason

// VerifyIndex reports whether the sidecar point-lookup index built for
// keyFields against the cohort at path is still fresh, using the
// size+mtime fast-path before paying for a full content-hash recompute.
// Delegates to internal/service.Service.VerifyIndex — see that method's doc
// comment for the full fast-path decision tree. Returns
// PULSE_INDEX_MISSING when no sidecar exists for keyFields and
// PULSE_INDEX_UNSUPPORTED_SHARDED for shard archive cohorts.
func (p *Pulse) VerifyIndex(ctx context.Context, path string, keyFields []string) (*VerifyIndexResult, error) {
	res, err := p.svc.VerifyIndex(ctx, path, keyFields)
	if err == nil {
		p.touchManaged(ctx, path)
	}
	return res, err
}

// IndexInfo re-exports internal/service.IndexInfo — one entry in a
// Service.ListIndexes result: a sidecar's derived path, its ordered
// key column names, and its distinct-key / indexed-record summary.
type IndexInfo = service.IndexInfo

// ListIndexes enumerates every sidecar point-lookup index built
// against the cohort at path. Delegates to internal/service.Service.ListIndexes
// — see that method's doc comment for the directory-glob + sidecar-read
// discovery algorithm. Returns an empty (non-nil) slice, not an error,
// when no sidecar indexes have been built yet. Returns
// PULSE_INDEX_UNSUPPORTED_SHARDED for shard archive cohorts.
func (p *Pulse) ListIndexes(ctx context.Context, path string) ([]IndexInfo, error) {
	res, err := p.svc.ListIndexes(ctx, path)
	if err == nil {
		p.touchManaged(ctx, path)
	}
	return res, err
}

// DropIndex removes the sidecar point-lookup index built for keyFields
// against the cohort at path. Delegates to internal/service.Service.DropIndex —
// see that method's doc comment for the non-interactive
// (no-confirmation-prompt) contract. Returns PULSE_INDEX_MISSING when
// no sidecar exists at the derived path and
// PULSE_INDEX_UNSUPPORTED_SHARDED for shard archive cohorts.
func (p *Pulse) DropIndex(ctx context.Context, path string, keyFields []string) error {
	err := p.svc.DropIndex(ctx, path, keyFields)
	if err == nil {
		p.touchManaged(ctx, path)
	}
	return err
}

// WidenReport re-exports encoding.WidenReport — the outcome of a
// WidenSetField call: the field's identity, the rung it moved from and
// to, the number of records re-laid-out and the record stride on each
// side of the rewrite.
type WidenReport = encoding.WidenReport

// WidenSetField widens the set column named field in the cohort at path
// to the wider set rung named by targetType ("set_u16", "set_u32",
// "set_u64", "set_u128", "set_u256"), rewriting the cohort IN PLACE and
// returning a report of what moved.
//
// A widen changes the field's stride, so every record is re-laid-out
// and every field after the widened one moves. The rewrite is atomic —
// temp file beside the cohort, fsync, rename — so any failure leaves
// the original byte-identical; see internal/encoding.WidenSetFieldFile.
//
// targetType is a type NAME rather than an encoding.FieldType because
// this is the boundary where a caller-supplied string arrives (a CLI
// flag, an embedder's config), and the resolution rule belongs to the
// library: an unknown name is ENCODING_TYPE_MISMATCH, never a silent
// fallback to some default type. Callers holding an encoding.FieldType
// pass its String(); the name table round-trips by contract.
//
// Refusals are coded errors reusing existing codes — SERVICE_RESOURCE
// (no such cohort), SERVICE_VALIDATION (the path is a shard archive or
// an anchored shard within one), ENCODING_INVALID (no such field) and
// ENCODING_TYPE_MISMATCH (not a set, not wider, or already at the
// widest rung). See internal/service.Service.WidenSetField.
//
// Sidecars are not rebuilt: a widened cohort changes length, so the
// point-lookup index and the SPSS metadata sidecar invalidate
// themselves through their own fingerprints on the next read.
func (p *Pulse) WidenSetField(ctx context.Context, path, field, targetType string) (*WidenReport, error) {
	target, ok := encoding.ParseFieldType(targetType)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.ENCODING_TYPE_MISMATCH,
			fmt.Sprintf("widen: %q is not a known field type name", targetType),
			map[string]any{"target": targetType, "field": field, "cohort": path})
	}
	rep, err := p.svc.WidenSetField(ctx, path, field, target)
	if err != nil {
		return nil, err
	}
	p.touchManaged(ctx, path)
	return rep, nil
}

// ExamplesSearch returns summaries from the embedded request-example
// library matching the given filters. An empty filter is treated as
// "no constraint" for that dimension. Query is case-insensitive
// substring search across name, description, and operators; tags is
// ANDed; category is an exact match. Always returns a non-nil slice
// (possibly empty) for safe JSON marshaling. Under a feature profile an
// example that uses an operator the instance hides is not returned.
func (p *Pulse) ExamplesSearch(query string, tags []string, category string) []ExampleSummary {
	return p.svc.InstanceSnapshot().Discovery().ExamplesSearch(query, tags, category)
}

// ExampleGet returns the example whose _meta.name matches name. The
// returned Body is the request JSON with the _meta block stripped so
// it can be handed directly to Process / Predict. Under a feature
// profile an example that uses a hidden operator answers (nil, false),
// exactly as a name that does not exist.
func (p *Pulse) ExampleGet(name string) (*Example, bool) {
	return p.svc.InstanceSnapshot().Discovery().Example(name)
}

// ErrorLookup returns the metadata projection for a single error code.
// Case-sensitive exact match. Returns (ErrorMetadata{}, false) when
// the code is unknown.
//
// The manifest carries only the alphabetized code-name list; per-code
// Message + Fixup detail lives behind this facade so per-session
// bootstrap stays lean. Use ErrorsByDomain / ErrorsSearch to enumerate
// in bulk.
//
// The view is the instance's: under a feature profile a code every
// owning feature of which is hidden is not found, and a fixup naming a
// hidden feature is stripped from the result. With no profile it is
// the full registry.
func (p *Pulse) ErrorLookup(code string) (ErrorMetadata, bool) {
	return descx.ErrorLookup(p.svc.InstanceSnapshot(), code)
}

// ErrorsByDomain returns every code's metadata in the named domain
// (CLI, DATA, ENCODING, PROCESSING, PULSE, SERVICE). Match is
// case-insensitive. Returns a non-nil empty slice when nothing
// matches; results are sorted alphabetically by code. Instance-scoped
// as ErrorLookup: hidden codes are absent, hidden-naming fixups stripped.
func (p *Pulse) ErrorsByDomain(domain string) []ErrorMetadata {
	return descx.ErrorsByDomain(p.svc.InstanceSnapshot(), domain)
}

// ErrorsSearch returns codes whose Message or Fixup hints contain the
// query (case-insensitive substring). Results are ranked by match
// source: description hits before fixup hits before code-name hits;
// ties resolve alphabetically. Returns a non-nil empty slice when
// nothing matches. Instance-scoped as ErrorLookup; the query matches
// only the text the instance renders, never a stripped fixup.
func (p *Pulse) ErrorsSearch(query string) []ErrorMetadata {
	return descx.ErrorsSearch(p.svc.InstanceSnapshot(), query)
}

// Request-template type aliases, so embedders name the template document
// model through the facade rather than the internal template package.
type (
	// Template is one parsed request-template document (GetTemplate).
	Template = template.Template
	// TemplateSummary is one ListTemplates entry.
	TemplateSummary = template.Summary
	// RenderedTemplate is a RenderTemplate result: the substituted JSON
	// plus the decoded, validated request for the template's target.
	RenderedTemplate = template.Rendered
	// TemplateTarget names the request root a template renders into.
	TemplateTarget = template.Target
	// TemplateVariable is one declared template variable.
	TemplateVariable = template.Variable
	// TemplateVarType is a template variable's declared type.
	TemplateVarType = template.VarType
)

// TemplateTarget values — the closed set of request roots a template can
// render into. Each equals its internal counterpart, so a Template.Target,
// TemplateSummary.Target or RenderedTemplate.Target compares against these
// directly; no embedder needs to match on String().
const (
	// TemplateTargetRequest renders into Request (process / predict).
	TemplateTargetRequest TemplateTarget = template.TargetRequest
	// TemplateTargetComposed renders into ComposedRequest (Compose).
	TemplateTargetComposed TemplateTarget = template.TargetComposed
	// TemplateTargetChain renders into ChainRequest (ProcessChain).
	TemplateTargetChain TemplateTarget = template.TargetChain
	// TemplateTargetFacet renders into FacetRequest (the facet endpoints).
	TemplateTargetFacet TemplateTarget = template.TargetFacet
	// TemplateTargetSample renders into SampleRequest (record sampling).
	TemplateTargetSample TemplateTarget = template.TargetSample
)

// TemplateVarType values — the closed set of declared variable types. Each
// equals its internal counterpart, so a TemplateVariable.Type (or Items)
// compares against these directly.
const (
	// TemplateVarString accepts any JSON string.
	TemplateVarString TemplateVarType = template.VarString
	// TemplateVarNumber accepts any JSON number.
	TemplateVarNumber TemplateVarType = template.VarNumber
	// TemplateVarInteger accepts a JSON number with no fractional part.
	TemplateVarInteger TemplateVarType = template.VarInteger
	// TemplateVarBoolean accepts a JSON bool.
	TemplateVarBoolean TemplateVarType = template.VarBoolean
	// TemplateVarField accepts a JSON string naming a cohort field.
	TemplateVarField TemplateVarType = template.VarField
	// TemplateVarEnum accepts a JSON string from the declaration's Values.
	TemplateVarEnum TemplateVarType = template.VarEnum
	// TemplateVarList accepts a JSON array of the declaration's Items type.
	TemplateVarList TemplateVarType = template.VarList
	// TemplateVarDate accepts a JSON string parsing as an ISO date.
	TemplateVarDate TemplateVarType = template.VarDate
	// TemplateVarPeriod accepts a labeled-date-range object (ranges XOR table).
	TemplateVarPeriod TemplateVarType = template.VarPeriod
)

// ListTemplates returns one summary per registered request template,
// sorted by name so the order is deterministic across runs and platforms.
//
// Each Summary projects everything a caller needs to CHOOSE a template and
// build a form for it — name, description, target, declared variable names,
// and the source file it was loaded from — without carrying the body. The
// full declarations (types, defaults, enum values) come from GetTemplate.
//
// Shadows names the source paths of same-named templates the listed entry
// takes precedence over. Template directories are an ordered precedence
// list and the first root wins; the losing entries are reported here rather
// than discarded, which is what makes "why is my override not taking
// effect?" answerable from the listing alone. A shadowed entry deliberately
// gets no summary of its own — it is not renderable, and a listing whose
// entries cannot all be fetched would be a trap.
//
// Broken is how a post-startup breakage becomes visible. A template file
// that has stopped parsing since it was loaded keeps ANSWERING GetTemplate
// with its last-good copy, so nothing about fetching it would reveal the
// fault; the flag, with the fault text in Error, is what lets an operator
// find the bad file without rendering all fifty templates one at a time. A
// broken entry with an empty Target never parsed at all — the file was
// already malformed the first time the engine saw it — so it is listed to be
// SEEN rather than fetched, and asking for it by name returns
// PULSE_TEMPLATE_INVALID.
//
// Always returns a non-nil slice (possibly empty) for safe JSON marshaling.
// An engine with no template directories configured lists nothing; that is
// an ordinary deployment, not a fault.
func (p *Pulse) ListTemplates() []TemplateSummary {
	out := p.templates.List()
	if out == nil {
		return []TemplateSummary{}
	}
	return out
}

// ReloadTemplates rescans every configured template directory immediately
// and swaps in the result.
//
// It is an escape hatch, not the mechanism. Templates hot-reload on their
// own: a lookup whose cached snapshot has aged past the store's rescan
// interval re-walks the directories first, so a file dropped into a
// scanned directory becomes renderable, a changed file starts serving its
// new content, and a deleted one stops resolving — all without restarting
// the process. The interval is a package constant rather than an Option,
// because a dial nobody can set better than the store can is a permanent
// public surface bought for nothing.
//
// What the interval cannot give is determinism. A newly written file can
// be invisible for up to that interval, which is fine for an operator
// editing a directory and wrong for a deployment step that writes a
// template and must render it on the next line, or for a test that would
// otherwise have to sleep. ReloadTemplates covers exactly that case.
//
// A rescan is a directory walk plus one stat per candidate file; a file
// whose size and modification time both match the copy already parsed is
// carried over rather than re-read, so calling this on an unchanged
// directory costs syscalls and no JSON parsing.
//
// A file going bad after startup degrades PER FILE and does not come back
// from this call. A template that parsed once and whose file later becomes
// malformed keeps serving its last-good parse, every other template is
// untouched, and this returns nil: an error here would tell a caller its
// whole catalog failed over one half-written editor save. The broken state
// is observable through ListTemplates instead — Summary.Broken with the
// fault in Summary.Error — and through GetTemplate for a name that never
// parsed at all. Repairing the file clears it on the next rescan. An
// unreadable file degrades the same way; it is also one file.
//
// What this DOES return is a whole-walk fault: a configured root that
// exists but is not a directory, or a directory that cannot be walked.
// Those are misconfigurations rather than transient edits, and a failed
// walk leaves the previously loaded templates entirely in place.
//
// Startup keeps the opposite rule: pulse.New still fails outright on a
// malformed template, because at startup a broken document is a deploy
// error the operator should see immediately rather than a keystroke.
//
// An engine with no template directories configured has nothing to rescan
// and returns nil.
func (p *Pulse) ReloadTemplates() error {
	return p.templates.Reload()
}

// GetTemplate returns the parsed declaration registered under name —
// target, description, and the full Variable list — so a caller can build a
// form (or a prompt) from the declaration alone before rendering.
//
// Lookup is exact and case-sensitive, and the name is the derived one:
// a template's path relative to its own directory root, minus the .json
// extension, forward-slash separated. A file at <root>/finance/revenue.json
// is named "finance/revenue" — not "finance/revenue.json" and not the
// absolute path.
//
// An unregistered name — including every name on an engine with no template
// directories configured — is PULSE_TEMPLATE_NOT_FOUND carrying the
// requested name in its details. A name whose file has broken since it was
// loaded still resolves, to the last-good parse (see ReloadTemplates); only
// a name that has NEVER parsed is PULSE_TEMPLATE_INVALID here, naming the
// path so the operator knows which file to open.
//
// The returned template is the engine's own copy and must be treated as
// read-only; rendering never mutates it.
func (p *Pulse) GetTemplate(name string) (*Template, error) {
	return p.templates.Get(name)
}

// RenderTemplate resolves the named template and renders it against the
// supplied variable map, returning the substituted JSON plus the typed
// request it decoded into. It is the general form, covering all five
// targets; RenderTemplateRequest is the shorthand for the common one.
//
// Exactly one of Rendered's typed pointers is populated, selected by
// Rendered.Target — read that pointer (or Rendered.Typed()) and hand it to
// the matching execution method: Process, Compose, ProcessChain,
// FacetSchema, SampleWithRequest. There are deliberately no per-execution-
// mode convenience wrappers: N execution modes would mean N wrappers to
// keep in sync forever, for no capability gain.
//
// Rendered.JSON is the rendered body before decode, retained because
// re-marshaling the typed value would not reproduce it — every request
// struct is dense with omitempty, so a slot that rendered to an explicit
// zero would silently vanish from a round trip.
//
// Errors are the PULSE_TEMPLATE_* family, surfaced with their codes and
// details intact: PULSE_TEMPLATE_NOT_FOUND for an unknown name, then
// whatever the render raises — PULSE_TEMPLATE_VAR_MISSING /
// _VAR_UNKNOWN / _VAR_TYPE / _VAR_ENUM for the variable map,
// PULSE_TEMPLATE_UNRESOLVED for a marker with nothing to substitute, and
// PULSE_TEMPLATE_RENDER_INVALID when the substituted JSON does not fit the
// target request type.
//
// Rendering never opens a cohort: a template that renders is well-formed
// against the request SHAPE. Whether it is executable against a particular
// cohort stays Predict's question.
//
// On an engine whose feature profile hides a request slot (crosstab,
// joins, overlays), a rendered body that sets that slot is refused with
// the PULSE_TEMPLATE_RENDER_INVALID a key the target type does not
// declare gets — same message, details and document-order position. An
// operator name is not a slot: a hidden operator renders and fails at
// execution, exactly as a never-registered one does.
func (p *Pulse) RenderTemplate(name string, vars map[string]any) (*RenderedTemplate, error) {
	tmpl, err := p.templates.Get(name)
	if err != nil {
		return nil, err
	}
	inst := p.svc.InstanceSnapshot()
	if !inst.Scoped() {
		return template.Render(tmpl, vars)
	}
	return template.RenderWith(tmpl, vars, template.RenderOptions{
		WithheldSlots: func(root any) []string { return descx.HiddenSlotKeys(root, inst) },
	})
}

// RenderTemplateRequest renders the named template and returns the typed
// *Request directly. It is the 95% path — the caller hands the result
// straight to Process, Predict, or ProcessStream.
//
// It is RenderTemplate restricted to the "request" target. A template
// declaring any other target is PULSE_TEMPLATE_TARGET_UNKNOWN: the target
// is a valid one, just not one this method can return, so the message names
// both the target the template actually declares and RenderTemplate as the
// method that handles it. Every other fault is RenderTemplate's, unchanged.
func (p *Pulse) RenderTemplateRequest(name string, vars map[string]any) (*Request, error) {
	rendered, err := p.RenderTemplate(name, vars)
	if err != nil {
		return nil, err
	}
	if rendered.Target != template.TargetRequest {
		return nil, wrongTemplateTarget(name, rendered.Target)
	}
	return rendered.Request, nil
}

// wrongTemplateTarget builds the fault for RenderTemplateRequest called on
// a template whose target is not "request". The template is fine and so is
// the render — the call is the mismatch — so the message leads with the
// target the document declares and points at the method that returns it.
func wrongTemplateTarget(name string, target template.Target) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_TEMPLATE_TARGET_UNKNOWN,
		"template "+strconv.Quote(name)+" declares target "+strconv.Quote(target.String())+
			", so RenderTemplateRequest cannot return it: that method returns *types.Request and "+
			"only handles the \"request\" target. Call RenderTemplate("+strconv.Quote(name)+
			", vars) instead and read the Rendered."+renderedFieldFor(target)+" pointer (or "+
			"Rendered.Typed()), then hand it to the matching execution method.",
		map[string]any{
			errors.DetailTemplate: name,
			"target":              target.String(),
			"expected_target":     template.TargetRequest.String(),
		})
}

// renderedFieldFor names the internal/template.Rendered field a target populates, so
// the wrong-target message can tell the caller exactly which pointer to
// read rather than making them look it up.
func renderedFieldFor(target template.Target) string {
	switch target {
	case template.TargetComposed:
		return "Composed"
	case template.TargetChain:
		return "Chain"
	case template.TargetFacet:
		return "Facet"
	case template.TargetSample:
		return "Sample"
	default:
		return "Request"
	}
}

// Manifest returns the instance's self-description: only the features
// it offers. Without a feature profile that is the full registry plus
// the instance's extensions; with one, hidden operators, capabilities,
// I/O formats, commands and MCP tools are absent (a hidden capability's
// block is omitted) and no prose names them. FeatureSetDigest
// identifies the described set. The manifest is deterministic per
// instance and does not depend on cohort data or the filesystem;
// callers cache it keyed by (PulseVersion, FeatureSetDigest).
func (p *Pulse) Manifest(_ context.Context) *descriptor.Manifest {
	return descx.BuildManifestForInstance(p.svc.InstanceSnapshot())
}

// PayloadSchema returns the instance's payload JSON Schema (draft
// 2020-12) as raw JSON: only what it offers. The operator, overlay-kind
// and regression enums list only enabled names; a hidden request slot
// (crosstab, joins, overlays) is not a property; a hidden capability's
// root (compose, process_chain, facet, sample, lookup) is not an entry
// point; and no def reachable only through an omitted part remains.
// Request, Response and Envelope are always present and $id is
// unchanged. The root $comment carries the instance's
// feature_set_digest ("feature_set_digest: fs1:…"), equal to
// FeatureSetDigest and to the manifest's, so the two self-descriptions
// cache under one key. Without a feature profile the output is the
// published full-registry schema.
func (p *Pulse) PayloadSchema() ([]byte, error) {
	return descx.PayloadSchemaForInstance(p.svc.InstanceSnapshot())
}

// Fs returns the underlying afero.Fs. Embedders (e.g. the MCP server) need
// this to enumerate .pulse files; processing methods route through service
// and never expose the filesystem directly.
func (p *Pulse) Fs() afero.Fs {
	return p.fsys
}

// CreateShardArchive writes a fresh Pulse shard archive at archivePath
// containing the supplied single-file shardPaths. The first shard
// seeds the canonical schema; remaining shards are validated via
// structural cohesion + the append-only dictionary prefix rule. The
// archive is written atomically (temp file + rename) so partial
// writes never appear at archivePath. See internal/service.CreateShardArchive
// for the full error surface.
//
// Set-width auto-widen applies at CREATE exactly as it does at ADD: a
// set column whose merged dictionary outgrows its bitmask, or whose rung
// differs between two of the seeded shards, is promoted rather than
// refused, and the promotion is reported as a mandatory
// PULSE_SHARD_SET_WIDENED warning on the returned result. One rule, no
// asymmetry — the same two files must not produce an archive when
// passed together and an error when passed one after the other.
//
// CreateShardArchive returns a result rather than a bare error precisely
// so that warning has nowhere to be dropped.
//
// Grouped (format 0x02) shards are accepted: the archive keeps ONE
// parent-group layout (the first shard's), group dictionaries
// union-merge canonical-first, and any layout change a shard needs is
// reported as a mandatory PULSE_SHARD_GROUPS_REWRITTEN warning plus a
// GroupReconciliation on Regrouped — the same rule AddShard applies.
func (p *Pulse) CreateShardArchive(ctx context.Context, archivePath string, shardPaths []string) (*CreateShardArchiveResult, error) {
	return p.svc.CreateShardArchive(ctx, archivePath, shardPaths)
}

// CreateShardArchiveResult carries the outcome of CreateShardArchive:
// the archive's shard count, any set-field widenings the seed forced,
// and the non-fatal warnings (PULSE_SHARD_DESCRIPTION_DIVERGENCE,
// PULSE_SHARD_SET_WIDENED) to lift onto a --json envelope.
type CreateShardArchiveResult = service.CreateShardArchiveResult

// AddShard validates the incoming single-file shard against the
// archive's canonical schema and appends it. Dict growth that the
// incoming shard introduces is reflected in the rewritten
// `_schema.pulse` payload before the new shard payload is appended.
// v1 reads the whole archive into memory and writes it back via
// temp+fsync+rename — semantically equivalent to true in-place append
// and crash-safe at the canonical-path level.
//
// A merged dictionary that outgrows a set_* field's bitmask WIDENS the
// field across the canonical schema and every shard payload instead of
// refusing the add, and reports it as a mandatory PULSE_SHARD_SET_WIDENED
// warning on the returned result — the rewrite is far cheaper than the
// re-import it replaces but far more expensive than an append, and a
// caller must be able to tell which one it paid for. A union above the
// widest set rung stays fatal (PULSE_SHARD_DICT_WIDTH_OVERFLOW).
//
// AddShard returns a result rather than a bare error precisely so that
// warning has nowhere to be dropped.
//
// A grouped (format 0x02) shard is conformed to the archive's layout: an
// ungrouped archive stores it flattened, a grouped archive re-encodes it
// into its own groups and union-merges the group dictionaries (a union
// past the u32 index space is PULSE_SHARD_DICT_WIDTH_OVERFLOW). A
// constant group the shard disagrees with is promoted to indexed across
// the whole archive; a declared key it violates is refused
// (PULSE_GROUP_MEMBER_NOT_CONSTANT). Layout changes are reported as a
// mandatory PULSE_SHARD_GROUPS_REWRITTEN warning and on Regrouped.
func (p *Pulse) AddShard(ctx context.Context, archivePath, shardPath string) (*AddShardResult, error) {
	return p.svc.AddShard(ctx, archivePath, shardPath)
}

// AddShardResult carries the outcome of AddShard: the archive's new
// shard count, any set-field widenings the add forced, and the
// non-fatal warnings (PULSE_SHARD_DESCRIPTION_DIVERGENCE,
// PULSE_SHARD_SET_WIDENED) to lift onto a --json envelope.
type AddShardResult = service.AddShardResult

// SetWidening records one set field promoted to a wider rung during a
// CreateShardArchive or an AddShard, with the shard and record counts
// the rewrite cost. From == To marks the one-shard case: the arriving
// shard declared a narrower rung and was promoted to the archive's,
// leaving the archive itself untouched.
type SetWidening = service.SetWidening

// GroupReconciliation records one parent-group layout change a
// CreateShardArchive or AddShard made to fit a shard into the archive
// (reason incoming_flattened / incoming_regrouped / constant_promoted),
// with the shards and records the rewrite cost.
type GroupReconciliation = service.GroupReconciliation

// CohesionWarning is one non-fatal shard-archive diagnostic (for
// example PULSE_SHARD_DESCRIPTION_DIVERGENCE or PULSE_SHARD_SET_WIDENED),
// carried by AddShardResult.Warnings, CreateShardArchiveResult.Warnings
// and VerifyResult.Warnings. Code is the coded-error code, Details its
// structured payload.
type CohesionWarning = encx.CohesionWarning

// RemoveShard rewrites the archive omitting the named shard. The
// canonical schema is preserved (dictionary entries are never
// shrunk). Returns PULSE_SHARD_MISSING when the named shard is not in
// the archive.
func (p *Pulse) RemoveShard(ctx context.Context, archivePath, shardBasename string) error {
	return p.svc.RemoveShard(ctx, archivePath, shardBasename)
}

// ListShards returns the archive's shard manifest in central-
// directory order (which equals shard insertion order). Single-file
// cohorts return an empty slice.
func (p *Pulse) ListShards(ctx context.Context, archivePath string) ([]ShardEntry, error) {
	return p.svc.ListShards(ctx, archivePath)
}

// ExtractShard returns an io.ReadCloser over the named shard's
// standalone single-file `.pulse` bytes. Suitable for piping to
// `pulse inspect -` or writing back to disk.
func (p *Pulse) ExtractShard(ctx context.Context, archivePath, shardBasename string) (io.ReadCloser, error) {
	return p.svc.ExtractShard(ctx, archivePath, shardBasename)
}

// CompactShardArchive rewrites the archive to eliminate orphaned bytes
// from prior in-place mutations and refreshes the canonical metadata
// (aggregate_record_count + shard_count). v1 AddShard / RemoveShard
// already use temp+rename (no orphan bytes in v1 archives), so Compact
// primarily serves to refresh canonical metadata that may have drifted
// if the archive was edited outside Pulse. The whole-archive rewrite
// pattern is the explicit reclaim path per the design contract §7.1.
func (p *Pulse) CompactShardArchive(ctx context.Context, archivePath string) error {
	return p.svc.CompactShardArchive(ctx, archivePath)
}

// VerifyShardArchive opens the archive and re-validates every shard's
// header (magic + format_version), structural cohesion against the
// canonical schema, dictionary prefix rule, and cross-checks each
// shard's record count against the canonical aggregate. Returns a
// VerifyResult carrying any errors (PULSE_SHARD_HEADER_INVALID,
// PULSE_SHARD_SCHEMA_MISMATCH, PULSE_SHARD_DICT_DIVERGENCE) and any
// non-fatal warnings (PULSE_SHARD_DESCRIPTION_DIVERGENCE, aggregate
// drift). Returns a non-nil error only when the archive itself cannot
// be opened (archive corrupt, file missing, etc.); per-shard issues
// are reported through the result struct so the caller can render the
// full diagnosis.
func (p *Pulse) VerifyShardArchive(ctx context.Context, archivePath string) (*VerifyResult, error) {
	return p.svc.VerifyShardArchive(ctx, archivePath)
}

// VerifyResult carries the structured outcome of VerifyShardArchive.
// Errors aggregate every fatal cohesion failure discovered while
// walking the archive's shards; Warnings carry non-fatal divergences
// (per-field description drift, aggregate-record-count mismatch). An
// empty Errors slice means the archive is structurally sound.
type VerifyResult = service.VerifyResult

// GroupIndexHeadroom reports how much of one parent group's dictionary
// index space a grouped archive's canonical schema has consumed.
// VerifyShardArchive returns one per group in
// VerifyResult.GroupIndexHeadroom.
type GroupIndexHeadroom = encx.GroupIndexHeadroom

// SetWidthHeadroom reports how much of a set field's bitmask capacity
// the canonical dictionary has consumed, and which rung a widen would
// promote it to. VerifyShardArchive returns one per set field so an
// impending archive-wide widen is foreseeable rather than a surprise
// the next `shard add` bills for.
type SetWidthHeadroom = encoding.SetWidthHeadroom

// resolveCohortPath builds the file path from a Cohort specification.
func resolveCohortPath(c *types.Cohort) string {
	if c.DataDir != "" {
		return c.DataDir + "/" + c.Filename
	}
	return c.Filename
}

// extractShardBytes opens archiveBytes as a Pulse shard archive and
// returns the named entry's payload, suitable as standalone single-file
// .pulse input to internal/descriptor.Predict / internal/descriptor.Inspect.
func extractShardBytes(archiveBytes []byte, entryName string) ([]byte, error) {
	arch, err := encx.OpenArchive(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return nil, err
	}
	rc, err := arch.Open(entryName)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// ShardEntry is one shard inside a Pulse shard archive. Re-exported from
// service so embedders can address pulse.ShardEntry directly.
type ShardEntry = service.ShardEntry

// ShardInfo is one shard entry as surfaced by Inspect / Predict. Re-
// exported from descriptor so embedders consuming the no-execute
// surface can address pulse.ShardInfo directly. Mirrors ShardEntry's
// shape (filename + record count); the two types are parallel because
// descriptor/ cannot import internal/service/.
type ShardInfo = descriptor.ShardInfo

// Cohort represents an opened .pulse file with its parsed schema.
// It wraps the service-layer Cohort to provide a clean public API.
type Cohort struct {
	inner *service.Cohort
}

// Schema returns the cohort's schema.
func (c *Cohort) Schema() *encoding.Schema {
	return c.inner.Schema()
}

// Field returns a pointer to the named field, or nil if not found.
func (c *Cohort) Field(name string) *encoding.Field {
	return c.inner.Schema().Field(name)
}

// Categorical returns the dictionary for a named categorical field.
// Returns nil, false if the field is not found or is not categorical.
func (c *Cohort) Categorical(name string) (*encoding.Dictionary, bool) {
	return c.inner.Schema().Categorical(name)
}

// Shards returns the shard manifest for an archive-backed cohort. Empty
// for single-file cohorts. The returned slice is a defensive copy.
func (c *Cohort) Shards() []ShardEntry {
	return c.inner.Shards()
}

// RecordCount returns the number of records in the cohort. For
// single-file cohorts this is derived from the byte length of the
// record region divided by the per-record size implied by the schema.
// For archive-backed cohorts the caller should sum per-shard
// RecordCount values from Shards() — the underlying service Cohort
// errors on RecordCount for archives because the byte-region path
// doesn't apply across shards.
func (c *Cohort) RecordCount() (int64, error) {
	return c.inner.RecordCount()
}
