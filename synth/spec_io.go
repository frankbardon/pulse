package synth

import (
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// WriteSpec writes spec to path as indented JSON.
//
// This is the library half of `synth from-profile --emit-spec`. It
// exists because SpecFromProfile derives a spec and generates from it
// in one breath, so until now nothing could print the thing that
// actually ran. That made the whole rule layer unreachable from the
// profile path — the only path the motivating use case takes — and it
// made authoring a rule guesswork, because writing
// {"when": "familiarity == 1", ...} requires knowing the field is
// called familiarity, that it is a u4 and that its floor is 1, and no
// command showed any of that.
//
// It is equally a DIAGNOSTIC. The emitted document is the only place a
// reader can see which captured models survived translation, which
// distribution each field reconstructed to, and which conditional
// pairs were retired — three findings this package has silently lost
// before and each of which leaves a plausible-looking cohort behind.
//
// Two properties are load-bearing rather than cosmetic:
//
//   - It marshals the spec VALUE, never a re-derivation from the
//     profile and never a summary. The contract is that feeding the
//     emitted file to `synth from-schema` at the same seed reproduces
//     the same cohort byte for byte
//     (TestEmitSpec_RoundTripsToByteIdenticalCohort), which a rendering
//     cannot satisfy.
//   - It is INDENTED. The real workflow is emit -> read -> write rules
//     -> apply, so a reader has to find a field name and its type in
//     the document by eye.
//
// Written through afero.Fs so the write path is hermetic in tests, per
// the repo-wide rule.
func WriteSpec(fs afero.Fs, spec *Spec, path string) error {
	if fs == nil {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "synth: fs is required")
	}
	if spec == nil {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "synth: spec is required")
	}
	if path == "" {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "synth: emit-spec path is required")
	}
	raw, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_VALIDATION, "marshalling synth spec")
	}
	raw = append(raw, '\n')
	if err := afero.WriteFile(fs, path, raw, 0o644); err != nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			fmt.Sprintf("writing synth spec to %q: %v", path, err),
			map[string]any{"path": path})
	}
	return nil
}

// ApplyRulesFile loads a standalone rules document from path, REPLACES
// spec.Rules with it, and validates the result against spec.
//
// The file format is the Spec.Rules array itself — a bare JSON array of
// rule objects, identical to the `rules` key of a spec. E1-S2 fixed
// that deliberately so this loader needs no translation layer and an
// author can move a rule between the two places by cut and paste.
//
// Merge semantics are REPLACE, not append. SpecFromProfile derives no
// rules, so on the path this exists for there is nothing to append to;
// inventing an append mode would only create an ordering question
// (does the file run before or after the spec's own rules?) that no
// caller has asked and that the declaration-order contract would then
// have to answer forever.
//
// Validation runs HERE rather than being left to the eventual
// Synth call, for two reasons. A profile-derived spec bypasses
// validateSpec by design, so on the from-profile path there is no
// earlier gate; and the refusal has to name the FILE. An analyst
// editing a rules file beside a spec beside a profile, told only that
// "rule 2 names field \"nosuchfield\"", does not know which of the
// three documents is wrong. Every refusal below therefore carries
// details["path"], and the rule-specific faults keep E1-S2's own code
// (PULSE_SYNTH_RULE_FIELD_UNKNOWN and the rest) rather than degrading
// to a bare parse error — `pulse errors lookup` prose is the point of
// that family.
//
// A refused file leaves spec.Rules exactly as it found it. A caller
// that logs and continues must not end up generating against half a
// rules document.
func ApplyRulesFile(fs afero.Fs, spec *Spec, path string) error {
	if fs == nil {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "synth: fs is required")
	}
	if spec == nil {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "synth: spec is required")
	}
	raw, err := afero.ReadFile(fs, path)
	if err != nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			fmt.Sprintf("reading rules file %q: %v", path, err),
			map[string]any{"path": path})
	}
	rules, err := ParseRules(raw)
	if err != nil {
		return withRulesFilePath(err, path)
	}

	// Validate against a COPY so a refusal cannot leave the caller's
	// spec carrying rules that were rejected.
	probe := *spec
	probe.Rules = rules
	if err := validateRules(&probe); err != nil {
		return withRulesFilePath(err, path)
	}

	spec.Rules = rules
	return nil
}

// ParseRules decodes a standalone rules document — the bare Spec.Rules
// array — WITHOUT validating it against any spec. Validation needs the
// field declarations, so it belongs to ApplyRulesFile; this is the
// decode step alone, exported for callers holding the bytes already.
//
// An empty array is accepted and means "no rules", which is a legal
// statement rather than a malformed document. A JSON object is refused:
// wrapping the array in {"rules": [...]} is the most likely author
// mistake and silently reading zero rules out of it would produce a run
// with every gate missing — the exact failure the eager-validation rule
// exists to remove.
func ParseRules(raw []byte) ([]RuleSpec, error) {
	var rules []RuleSpec
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION,
			fmt.Sprintf("parsing rules file: expected a JSON array of rule objects (the same shape as a spec's \"rules\" key): %v", err))
	}
	return rules, nil
}

// withRulesFilePath re-stamps a rule fault with the file it came from,
// preserving the code, the message and the rule index E1-S2 put on it.
//
// A new error is built rather than the existing Details map being
// mutated in place: NewCodedErrorWithDetails copies its input, so a
// caller holding the original map would otherwise see it change under
// them, and a fault that reached here through two paths would
// accumulate stamps.
func withRulesFilePath(err error, path string) error {
	ce, ok := err.(*errors.CodedError)
	if !ok {
		return err
	}
	details := make(map[string]any, len(ce.Details)+1)
	for k, v := range ce.Details {
		details[k] = v
	}
	details["path"] = path
	return &errors.CodedError{
		Code:    ce.Code,
		Message: fmt.Sprintf("rules file %q: %s", path, ce.Message),
		Details: details,
		Cause:   ce.Cause,
	}
}

// WriteRuleCandidates writes detected rule candidates to path as an
// indented JSON array — the standalone rules-file format, byte for byte
// the shape ApplyRulesFile / ParseRules consume.
//
// This is the library half of `profile create --suggest-rules`. The
// round trip is the contract and it is a GENERATION round trip, not a
// shape comparison: the written file, unmodified, is loaded by
// `synth from-profile --rules` and its rules fire
// (TestSuggestRules_WrittenFileGeneratesUnmodified). A test that merely
// unmarshals the file into []RuleSpec passes against a document whose
// predicates do not compile, whose targets name fields the derived spec
// does not declare, or whose `when` is silently never true — and all
// three are the silent-inertness class this effort exists to remove.
//
// An EMPTY candidate list writes `[]` rather than nothing. "Detection
// found no gating relationship" is a real answer and the analyst asked
// for the file; leaving a stale file from a previous run in its place,
// or no file at all, makes the run's own result unreadable. `[]` is a
// legal rules document meaning "no rules" (see ParseRules).
//
// Indented for the same reason WriteSpec is: the workflow is
// suggest -> read -> delete what you do not believe -> correct the
// `when` -> apply, and every step but the last is done by eye.
func WriteRuleCandidates(fs afero.Fs, candidates []RuleSpec, path string) error {
	if fs == nil {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "synth: fs is required")
	}
	if path == "" {
		return errors.NewCodedError(errors.SERVICE_VALIDATION, "synth: suggest-rules path is required")
	}
	if candidates == nil {
		candidates = []RuleSpec{}
	}
	raw, err := json.MarshalIndent(candidates, "", "  ")
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_VALIDATION, "marshalling rule candidates")
	}
	raw = append(raw, '\n')
	if err := afero.WriteFile(fs, path, raw, 0o644); err != nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			fmt.Sprintf("writing rule candidates to %q: %v", path, err),
			map[string]any{"path": path})
	}
	return nil
}
