package pulse

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/errors"
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
// written_with, features and behaviour (including the reserved limits
// and return sections) is refused with PULSE_FEATURE_PROFILE_INVALID.
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
)

// featureProfileFile is the decode target for a profile file. Features
// is a pointer so an absent or null "features" key is distinguishable
// from an empty array.
type featureProfileFile struct {
	Profile     string                   `json:"profile"`
	WrittenWith string                   `json:"written_with"`
	Features    *[]string                `json:"features"`
	Behaviour   *FeatureProfileBehaviour `json:"behaviour"`
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

	if err := validateFeatureProfileShape(fp, opts.FeatureProfileFile); err != nil {
		return nil, err
	}
	u := newFeatureUniverse(opts.Extensions, Version())
	if err := validateFeatureProfileNames(fp, u, opts.FeatureProfileFile); err != nil {
		return nil, err
	}
	if err := validateFeatureProfileDependencies(fp, u, opts.FeatureProfileFile); err != nil {
		return nil, err
	}
	return fp, nil
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
