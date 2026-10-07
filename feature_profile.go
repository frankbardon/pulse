package pulse

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// FeatureProfile declares the set of features a Pulse instance offers.
// It mirrors the JSON profile file accepted through
// Options.FeatureProfileFile key for key, so a Go value and a file are
// interchangeable.
//
// pulse.New validates the profile and stores it on the instance. The
// feature list is not applied yet — no surface is hidden or filtered by
// it — but the Behaviour switches take effect immediately.
//
// The JSON form is decoded strictly: a key outside profile,
// written_with, features, behaviour and return (including the reserved
// limits section) is refused with PULSE_FEATURE_PROFILE_INVALID.
type FeatureProfile struct {
	// Profile is a free-form label naming the profile, e.g.
	// "self-serve". Informational only.
	Profile string `json:"profile,omitempty"`

	// WrittenWith records the Pulse version the profile was authored
	// against. Informational only: it is never compared and an
	// unparseable value is not an error.
	WrittenWith string `json:"written_with,omitempty"`

	// Features lists the feature names the instance offers. It is
	// REQUIRED: a nil slice (or an absent / null "features" key in the
	// file) is refused with PULSE_FEATURE_PROFILE_INVALID. An empty,
	// non-nil slice is valid and offers no optional feature. A name may
	// appear at most once.
	Features []string `json:"features"`

	// Behaviour carries engine switches the profile turns on. Nil
	// leaves every switch to Options.
	Behaviour *FeatureProfileBehaviour `json:"behaviour,omitempty"`

	// Return is the instance `return` default the profile supplies: the
	// response selection a request without its own `return` block is
	// shaped by. Options.DefaultReturn wins over it; a request block
	// replaces either entirely. Nil leaves the library default `full`.
	// pulse.New (and CheckFeatureProfile) resolve it against the
	// profile's own feature set after the dependency class: a bad
	// preset, precision or path syntax, or a path the profile's
	// instance does not have — one a hidden feature owns included — is
	// PULSE_FEATURE_PROFILE_INVALID reason "invalid_return".
	Return *types.Return `json:"return,omitempty"`
}

// FeatureProfileBehaviour holds the engine switches a FeatureProfile
// can set. Each switch combines with the matching Options field by OR:
// true in the profile turns the switch on, and Options cannot force it
// back off. False (the default) defers to Options.
type FeatureProfileBehaviour struct {
	// DisableDefaults ORs into Options.DisableDefaults.
	DisableDefaults bool `json:"disable_defaults,omitempty"`

	// DisableComponents ORs into Options.DisableComponents.
	DisableComponents bool `json:"disable_components,omitempty"`

	// DisableProjection ORs into Options.DisableProjection.
	DisableProjection bool `json:"disable_projection,omitempty"`

	// DisableCohortScan has no effect on the engine pulse.New builds:
	// the cohort resource scan belongs to the MCP server. It is stored
	// on the instance and ORed into the MCP adapter's own setting by
	// gosdk.Register (and so by mcpserve and `pulse mcp`): true skips
	// the startup scan even when Config.DisableCohortScan is false.
	DisableCohortScan bool `json:"disable_cohort_scan,omitempty"`
}

// Reasons carried under the "reason" detail of a
// PULSE_FEATURE_PROFILE_INVALID error.
const (
	featureProfileReasonBothSet         = "both_options_set"
	featureProfileReasonFileUnreadable  = "file_unreadable"
	featureProfileReasonMalformedJSON   = "malformed_json"
	featureProfileReasonUnknownKey      = "unknown_key"
	featureProfileReasonMissingFeatures = "missing_features"
	featureProfileReasonDuplicate       = "duplicate_feature"
	featureProfileReasonInvalidReturn   = "invalid_return"
)

// featureProfileFile is the decode target for a profile file. Features
// is a pointer so an absent or null "features" key is distinguishable
// from an empty array.
type featureProfileFile struct {
	Profile     string                   `json:"profile"`
	WrittenWith string                   `json:"written_with"`
	Features    *[]string                `json:"features"`
	Behaviour   *FeatureProfileBehaviour `json:"behaviour"`
	Return      *types.Return            `json:"return"`
}

// resolveFeatureProfile turns the two Options profile fields into one
// validated, caller-independent profile. It returns nil, nil when
// neither is set. fsys is the instance filesystem — FeatureProfileFile
// is read through it, never through the OS directly.
//
// Validation runs in classes and stops at the first failing class,
// reporting every instance of that class: structural faults
// (PULSE_FEATURE_PROFILE_INVALID), then names that do not resolve
// against the built-in table plus the registered extension operators
// (PULSE_FEATURE_PROFILE_UNKNOWN), then unmet dependency groups
// (PULSE_FEATURE_PROFILE_DEPENDENCY). An extension omitted from the
// profile is not an error.
func resolveFeatureProfile(opts Options, fsys afero.Fs) (*FeatureProfile, error) {
	if opts.FeatureProfile != nil && opts.FeatureProfileFile != "" {
		return nil, featureProfileInvalid(featureProfileReasonBothSet,
			"feature profile: set exactly one of Options.FeatureProfile and Options.FeatureProfileFile, not both",
			map[string]any{"path": opts.FeatureProfileFile})
	}

	var fp *FeatureProfile
	switch {
	case opts.FeatureProfile != nil:
		fp = cloneFeatureProfile(opts.FeatureProfile)
	case opts.FeatureProfileFile != "":
		var err error
		fp, err = loadFeatureProfileFile(fsys, opts.FeatureProfileFile)
		if err != nil {
			return nil, err
		}
	default:
		return nil, nil
	}

	u := newFeatureUniverse(opts.Extensions, Version())
	if err := validateFeatureProfile(fp, u, opts.FeatureProfileFile); err != nil {
		return nil, err
	}
	if err := validateOptionsAgainstFeatureProfile(opts, fp); err != nil {
		return nil, err
	}
	return fp, nil
}

// validateOptionsAgainstFeatureProfile refuses an Options value that
// needs a feature the (already valid) profile omits — it would act on a
// surface the instance hides. Today two: Options.DefaultWeight needs
// capability:weighting (.claude/reference/weighting.md, Feature
// profile); a hidden weighting refuses every request `weight`, so an
// instance default would weight requests that could not opt out — and
// Options.DefaultMultiplicity needs capability:multiplicity for the
// same reason (every `multiplicity` block is refused). It is the
// dependency class: PULSE_FEATURE_PROFILE_DEPENDENCY with one `unmet`
// entry {option, requires_any_of} per unmet option and an `options`
// list, in that order.
func validateOptionsAgainstFeatureProfile(opts Options, fp *FeatureProfile) error {
	checks := []struct {
		option  string
		set     bool
		feature string
	}{
		{"Options.DefaultWeight", opts.DefaultWeight != nil, descx.FeatureWeighting},
		{"Options.DefaultMultiplicity", opts.DefaultMultiplicity != nil, descx.FeatureMultiplicity},
	}
	var unmet []map[string]any
	var options, parts []string
	for _, c := range checks {
		if !c.set || slices.Contains(fp.Features, c.feature) {
			continue
		}
		unmet = append(unmet, map[string]any{"option": c.option, "requires_any_of": []string{c.feature}})
		options = append(options, c.option)
		parts = append(parts, c.option+" requires one of ["+c.feature+"]")
	}
	if len(unmet) == 0 {
		return nil
	}
	details := map[string]any{
		"unmet":   unmet,
		"options": options,
	}
	if opts.FeatureProfileFile != "" {
		details["path"] = opts.FeatureProfileFile
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_FEATURE_PROFILE_DEPENDENCY,
		"feature profile: unmet dependencies: "+strings.Join(parts, "; "),
		details)
}

// validateFeatureProfile runs the three validation classes in order —
// structural (INVALID), name resolution (UNKNOWN), dependencies
// (DEPENDENCY) — and stops at the first failing class. It is the one
// path shared by pulse.New and CheckFeatureProfile.
func validateFeatureProfile(fp *FeatureProfile, u featureUniverse, path string) error {
	if err := validateFeatureProfileShape(fp, path); err != nil {
		return err
	}
	if err := validateFeatureProfileNames(fp, u, path); err != nil {
		return err
	}
	if err := validateFeatureProfileDependencies(fp, u, path); err != nil {
		return err
	}
	return validateFeatureProfileReturn(fp, u, path)
}

// validateFeatureProfileReturn resolves the profile's `return` section
// against the instance the profile itself scopes (its resolved feature
// set), so a path a hidden feature owns is refused exactly like a
// nonexistent one. It runs after the dependency class because it needs
// a valid feature set. Any resolver refusal is re-raised as
// PULSE_FEATURE_PROFILE_INVALID reason "invalid_return", carrying the
// resolver's code under "return_code" and its details under "return".
func validateFeatureProfileReturn(fp *FeatureProfile, u featureUniverse, path string) error {
	if fp.Return == nil {
		return nil
	}
	snap := descx.NewInstanceSnapshot(nil, resolveFeatureSet(u, fp, descx.FeatureBehaviour{}))
	err := descx.ValidateDefaultReturn(fp.Return, snap)
	if err == nil {
		return nil
	}
	details := map[string]any{}
	if path != "" {
		details["path"] = path
	}
	msg := err.Error()
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		details["return_code"] = string(ce.Code)
		details["return"] = ce.Details
		msg = ce.Message
	}
	return featureProfileInvalid(featureProfileReasonInvalidReturn, "feature profile: return: "+msg, details)
}

// loadFeatureProfileFile reads and strictly decodes a profile file
// through the instance filesystem.
func loadFeatureProfileFile(fsys afero.Fs, path string) (*FeatureProfile, error) {
	raw, err := afero.ReadFile(fsys, path)
	if err != nil {
		return nil, featureProfileInvalid(featureProfileReasonFileUnreadable,
			fmt.Sprintf("feature profile: cannot read %q: %v", path, err),
			map[string]any{"path": path})
	}
	return decodeFeatureProfile(raw, path)
}

// ParseFeatureProfile strictly decodes the JSON form of a feature
// profile — the same decode, with the same refusals, that pulse.New
// applies to Options.FeatureProfileFile. Every refusal is
// PULSE_FEATURE_PROFILE_INVALID with a "reason" detail: malformed_json
// (including trailing data after the object), unknown_key, or
// missing_features (an absent or null "features" key).
//
// It only decodes: feature names and dependencies are validated when
// the result is handed to pulse.New through Options.FeatureProfile.
//
// It exists for entry points that read a profile from somewhere other
// than the instance filesystem. Options.FeatureProfileFile stays
// relative to the instance afero Fs (hermetic, and inside DataDir when
// DataDir is set); a caller holding a host OS path — the MCP server's
// --feature-profile flag and PULSE_FEATURE_PROFILE — reads the bytes
// itself and passes the parsed value through Options.FeatureProfile.
func ParseFeatureProfile(data []byte) (*FeatureProfile, error) {
	return decodeFeatureProfile(data, "")
}

// decodeFeatureProfile is the one strict decode shared by the file arm
// and ParseFeatureProfile. path, when non-empty, names the source in
// the message and the "path" detail.
func decodeFeatureProfile(raw []byte, path string) (*FeatureProfile, error) {
	details := map[string]any{}
	prefix := "feature profile: "
	if path != "" {
		details["path"] = path
		prefix = fmt.Sprintf("feature profile: %q: ", path)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc *featureProfileFile
	if err := dec.Decode(&doc); err != nil {
		reason := featureProfileReasonMalformedJSON
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			reason = featureProfileReasonUnknownKey
		}
		return nil, featureProfileInvalid(reason, prefix+err.Error(), details)
	}
	if err := dec.Decode(new(json.RawMessage)); !stderrors.Is(err, io.EOF) {
		return nil, featureProfileInvalid(featureProfileReasonMalformedJSON,
			prefix+"trailing data after the profile object", details)
	}
	if doc == nil || doc.Features == nil {
		return nil, featureProfileInvalid(featureProfileReasonMissingFeatures,
			prefix+"\"features\" is required (an empty array is allowed)", details)
	}

	return &FeatureProfile{
		Profile:     doc.Profile,
		WrittenWith: doc.WrittenWith,
		Features:    append([]string{}, (*doc.Features)...),
		Behaviour:   doc.Behaviour,
		Return:      doc.Return,
	}, nil
}

// validateFeatureProfileShape runs the structural class shared by the
// Go-value and file forms: features present, no name repeated. path is
// echoed into details when the profile came from a file.
func validateFeatureProfileShape(fp *FeatureProfile, path string) error {
	details := map[string]any{}
	if path != "" {
		details["path"] = path
	}
	if fp.Features == nil {
		return featureProfileInvalid(featureProfileReasonMissingFeatures,
			"feature profile: Features is required (an empty, non-nil slice is allowed)",
			details)
	}

	seen := make(map[string]int, len(fp.Features))
	for _, name := range fp.Features {
		seen[name]++
	}
	var dups []string
	for name, n := range seen {
		if n > 1 {
			dups = append(dups, name)
		}
	}
	if len(dups) > 0 {
		sort.Strings(dups)
		details["duplicates"] = dups
		return featureProfileInvalid(featureProfileReasonDuplicate,
			fmt.Sprintf("feature profile: features listed more than once: %s", strings.Join(dups, ", ")),
			details)
	}
	return nil
}

// cloneFeatureProfile copies a caller-owned profile so later mutation
// by the caller cannot change what the instance validated and stored.
func cloneFeatureProfile(in *FeatureProfile) *FeatureProfile {
	out := *in
	if in.Features != nil {
		out.Features = append([]string{}, in.Features...)
	}
	if in.Behaviour != nil {
		b := *in.Behaviour
		out.Behaviour = &b
	}
	if in.Return != nil {
		r := *in.Return
		r.Include = append([]string(nil), in.Return.Include...)
		r.Exclude = append([]string(nil), in.Return.Exclude...)
		out.Return = &r
	}
	return &out
}

// applyFeatureProfileBehaviour ORs the profile's behaviour switches
// into opts. A profile can turn a switch on; it never turns one off.
func applyFeatureProfileBehaviour(opts *Options, fp *FeatureProfile) {
	if fp == nil || fp.Behaviour == nil {
		return
	}
	b := fp.Behaviour
	opts.DisableDefaults = opts.DisableDefaults || b.DisableDefaults
	opts.DisableComponents = opts.DisableComponents || b.DisableComponents
	if b.DisableProjection {
		// The deprecated ProjectBufferedFields knob would otherwise
		// turn projection back on (New ORs it against
		// !DisableProjection), letting Options override the profile.
		opts.DisableProjection = true
		opts.ProjectBufferedFields = false
	}
}

func featureProfileInvalid(reason, msg string, details map[string]any) error {
	d := make(map[string]any, len(details)+1)
	for k, v := range details {
		d[k] = v
	}
	d["reason"] = reason
	return errors.NewCodedErrorWithDetails(errors.PULSE_FEATURE_PROFILE_INVALID, msg, d)
}
