package descriptor

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/targetencode"
	"github.com/frankbardon/pulse/types"
)

// validateFeatures validates each pre-filter feature operator against the
// schema and computes the post-feature column set. The set is union of
// schema field names and operator-emitted labels; downstream validators
// (filters, attributes, groupers, aggregators) test references against it.
//
// This file does not import internal/processing/feature — that would couple
// validation to execution. Operator output naming is duplicated here as a
// pure function; the feature-engineering skill documents the contract and
// CI gates verify the operator set is documented.
func validateFeatures(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) map[string]bool {
	cols := make(map[string]bool, len(schema.Fields))
	for _, f := range schema.Fields {
		cols[f.Name] = true
	}
	if len(req.Features) == 0 {
		return cols
	}

	for idx, feat := range req.Features {
		// A type the instance hides is unknown here, exactly as a
		// never-registered one — the runtime's PROCESSING_CONFIG refusal.
		if !isKnownFeatureType(opRoute(opts.instance(), feat.Type)) && !isExtensionFeatureType(opts, feat.Type) {
			env.AddError(
				string(errors.PROCESSING_CONFIG),
				"unknown feature type: "+string(feat.Type),
				map[string]any{"feature": string(feat.Type)},
			)
			continue
		}

		// Field-presence and per-operator type checks. cols carries the
		// schema's fields plus any earlier feature's outputs so chained
		// features (e.g. BUCKETIZE on LOG's output) validate cleanly.
		validateFeatureSpec(env, feat, schema, cols, opts)

		// A target encoding without params.fit_on leaks: features run
		// before filters, so each encoded value averages every record's
		// outcome — the validation / test records' included, and (without
		// folds) the record's own. A preceding train/test split changes no
		// value; only fit_on silences the warning.
		if feat.Type == types.FEAT_TARGET_ENCODE {
			if w := targetLeakageWarning(idx, feat, opts.instance()); w != nil {
				entry := &descriptor.EnvelopeEntry{Code: string(w.Code), Message: w.Message, Details: w.Details}
				if opts.Strict {
					env.Errors = append(env.Errors, entry)
				} else {
					env.Warnings = append(env.Warnings, entry)
				}
			}
		}

		// Add output columns to the projected set even if validation found
		// problems — downstream errors are more useful when they don't
		// cascade from missing labels.
		for _, label := range featureOutputLabels(feat, schema) {
			cols[label] = true
		}
	}
	return cols
}

// TargetLeakageWarnings is the PULSE_FEAT_TARGET_LEAKAGE_RISK rule the
// runtime repeats (#309): one coded warning per FEAT_TARGET_ENCODE in
// req.Features (after item expansion) that sets no params.fit_on, in
// request order, each carrying predict's message and details
// {feature, feature_index} (+ folds when set). It reads the request
// alone, never the cohort. A type the instance hides yields none —
// predict refuses it as unknown and skips the warning, so the runtime
// does too. Under Options.Strict the runtime refuses the first entry
// before any record is read (predict reports every entry as an error);
// otherwise each rides Response.Warnings.
func TargetLeakageWarnings(req *types.Request, inst *InstanceSnapshot) []*errors.CodedError {
	if req == nil || inst.Hidden(string(types.FEAT_TARGET_ENCODE)) {
		return nil
	}
	var out []*errors.CodedError
	for idx, feat := range req.Features {
		if feat != nil && feat.Type == types.FEAT_TARGET_ENCODE {
			if w := targetLeakageWarning(idx, feat, inst); w != nil {
				out = append(out, w)
			}
		}
	}
	return out
}

// targetLeakageWarning is the one PULSE_FEAT_TARGET_LEAKAGE_RISK entry
// predict and the runtime both raise for the FEAT_TARGET_ENCODE at
// features[idx], decided from its params alone: nil when params.fit_on
// names the training rows (only they feed the means); with folds but
// no fit_on, the reworded entry (no row sees its own outcome, every row
// still sees the holdout's); otherwise the full one. Params that do not
// decode leave fit_on and folds unset — their refusal is the params
// check's.
func targetLeakageWarning(idx int, feat *types.Feature, inst *InstanceSnapshot) *errors.CodedError {
	var p targetencode.Params
	if len(feat.Params) > 0 {
		_ = json.Unmarshal(feat.Params, &p)
	}
	if strings.TrimSpace(p.FitOn) != "" {
		return nil
	}
	details := map[string]any{"feature": string(types.FEAT_TARGET_ENCODE), "feature_index": idx}
	if p.Folds > 0 {
		details["folds"] = p.Folds
		return errors.NewCodedErrorWithDetails(errors.PULSE_FEAT_TARGET_LEAKAGE_RISK,
			"FEAT_TARGET_ENCODE with folds but no params.fit_on encodes each record out-of-fold, so no record's own outcome enters its value, "+
				"but every value still averages the validation/test records' outcomes; set params.fit_on to the training rows "+
				"(e.g. \"split == 0\" after "+trainTestSplitName(inst)+") to keep them out",
			details)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_FEAT_TARGET_LEAKAGE_RISK,
		"FEAT_TARGET_ENCODE without params.fit_on averages every record's outcome, the record's own and validation/test records' included; "+
			"a preceding "+trainTestSplitName(inst)+" does not change that — set params.fit_on to the training rows (e.g. \"split == 0\"), "+
			"optionally with folds for out-of-fold training values",
		details)
}

// isKnownFeatureType reports whether the type is in types.AllFeatureTypes().
func isKnownFeatureType(t types.FeatureType) bool {
	for _, ft := range types.AllFeatureTypes() {
		if ft == t {
			return true
		}
	}
	return false
}

// validateFeatureSpec performs per-operator structural checks: required
// field, type compatibility, params shape. cols includes the schema's
// fields plus any earlier feature's projected outputs so chained
// features can reference upstream derived columns. Operators with type
// constraints (categorical, date) are only enforced when the field
// resolves to a schema entry; chained operators that consume an
// earlier feature's output are trusted because runtime behavior is
// determined by the producing operator.
func validateFeatureSpec(env *descriptor.Envelope, feat *types.Feature, schema *encoding.Schema, cols map[string]bool, opts *PredictOptions) {
	_ = opts // strict-mode handling reserved for future leakage warnings

	switch feat.Type {
	case types.FEAT_TRAIN_TEST_SPLIT:
		validateSplitParams(env, feat, schema)
		return
	}

	// All other operators require a field.
	if feat.Field == "" {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature "+string(feat.Type)+" requires a field",
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	f := schema.Field(feat.Field)
	if f == nil {
		if cols[feat.Field] {
			// Reference to an earlier feature's output. Operator-specific
			// type checks below depend on the schema-level type, which we
			// don't have for derived columns; trust the producing
			// operator to emit a compatible value.
			if feat.Type == types.FEAT_TARGET_ENCODE {
				validateTargetEncodeParams(env, feat, schema, nil)
			}
			if feat.Type == types.FEAT_BUCKETIZE {
				validateBucketizeParams(env, feat)
			}
			if feat.Type == types.FEAT_POLY {
				validatePolyParams(env, feat)
			}
			return
		}
		// Unknown: the field-reference rule's refusal (FieldRefRefusals).
		return
	}

	// A vector field is the field-reference walk's refusal
	// (vectorScalarRefusals); the type checks below would only repeat it.
	// The accepted-types table's refusals are the runtime
	// constructors' word for word (fieldaccept.Refusal never refuses a
	// vector).
	switch feat.Type {
	case types.FEAT_ONE_HOT, types.FEAT_FREQUENCY_ENCODE:
		if cerr := fieldaccept.Refusal(string(feat.Type), fieldaccept.SlotField, feat.Field, f.Type); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}
	case types.FEAT_TARGET_ENCODE:
		validateTargetEncodeParams(env, feat, schema, f)
	case types.FEAT_DATE_FEATURES:
		if cerr := fieldaccept.Refusal(string(feat.Type), fieldaccept.SlotField, feat.Field, f.Type); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}
	case types.FEAT_LOG, types.FEAT_SQRT:
		// The accepted-types table's refusal, the runtime's (newLog /
		// newSqrt) word for word.
		if cerr := fieldaccept.Refusal(string(feat.Type), fieldaccept.SlotField, feat.Field, f.Type); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
		}
	case types.FEAT_BUCKETIZE:
		// newBucketize judges the type before its params.
		if cerr := fieldaccept.Refusal(string(feat.Type), fieldaccept.SlotField, feat.Field, f.Type); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
			return
		}
		validateBucketizeParams(env, feat)
	case types.FEAT_POLY:
		// The accepted-types table's refusal, the runtime's (newPoly)
		// word for word, judged before the params; a vector is
		// vectorScalarRefusals'.
		if cerr := fieldaccept.Refusal(string(feat.Type), fieldaccept.SlotField, feat.Field, f.Type); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
			return
		}
		validatePolyParams(env, feat)
	}
}

func validateSplitParams(env *descriptor.Envelope, feat *types.Feature, schema *encoding.Schema) {
	if len(feat.Params) == 0 {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_TRAIN_TEST_SPLIT requires params with 'ratios'",
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	var p struct {
		Ratios   []float64 `json:"ratios"`
		Stratify string    `json:"stratify"`
	}
	if err := json.Unmarshal(feat.Params, &p); err != nil {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_TRAIN_TEST_SPLIT has malformed params: "+err.Error(),
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	if len(p.Ratios) < 2 || len(p.Ratios) > 3 {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_TRAIN_TEST_SPLIT: 'ratios' must have 2 or 3 elements",
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	var sum float64
	for _, r := range p.Ratios {
		if r < 0 {
			env.AddError(
				string(errors.PROCESSING_CONFIG),
				"feature FEAT_TRAIN_TEST_SPLIT: ratios must be non-negative",
				map[string]any{"feature": string(feat.Type)},
			)
			return
		}
		sum += r
	}
	if math.Abs(sum-1.0) > 1e-6 {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_TRAIN_TEST_SPLIT: ratios must sum to 1.0",
			map[string]any{"feature": string(feat.Type), "sum": sum},
		)
		return
	}
	// An unknown name is FieldRefRefusals' refusal; a non-categorical
	// stratify column the accepted-types table's.
	if cerr := fieldaccept.Check(string(feat.Type), fieldaccept.SlotStratify, p.Stratify, schema); cerr != nil {
		env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
	}
}

func validateBucketizeParams(env *descriptor.Envelope, feat *types.Feature) {
	if len(feat.Params) == 0 {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_BUCKETIZE requires 'boundaries' or 'quantiles' in params",
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	var p struct {
		Boundaries []float64 `json:"boundaries"`
		Quantiles  int       `json:"quantiles"`
	}
	if err := json.Unmarshal(feat.Params, &p); err != nil {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_BUCKETIZE has malformed params: "+err.Error(),
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	if len(p.Boundaries) == 0 && p.Quantiles <= 0 {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_BUCKETIZE: provide either 'boundaries' or 'quantiles'",
			map[string]any{"feature": string(feat.Type)},
		)
	}
	if len(p.Boundaries) > 0 && p.Quantiles > 0 {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_BUCKETIZE: 'boundaries' and 'quantiles' are mutually exclusive",
			map[string]any{"feature": string(feat.Type)},
		)
	}
}

// validatePolyParams checks FEAT_POLY's Degree param: required, ≥2, ≤10.
// Mirrors internal/processing/feature/poly.go's factory checks so predict can
// surface the same diagnostic without executing.
func validatePolyParams(env *descriptor.Envelope, feat *types.Feature) {
	if len(feat.Params) == 0 {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_POLY requires 'degree' in params",
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	var p struct {
		Degree int `json:"degree"`
	}
	if err := json.Unmarshal(feat.Params, &p); err != nil {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_POLY has malformed params: "+err.Error(),
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	if p.Degree < 2 {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_POLY requires Degree >= 2; use the original column for the linear term",
			map[string]any{"feature": string(feat.Type), "degree": p.Degree},
		)
	}
	if p.Degree > 10 {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_POLY Degree capped at 10; consider standardizing inputs first or using an orthogonal basis",
			map[string]any{"feature": string(feat.Type), "degree": p.Degree},
		)
	}
}

// validateTargetEncodeParams reports FEAT_TARGET_ENCODE's refusals in
// the constructor's order, stopping at the first: its params, then the
// encoded field's type (field is nil for a derived column, which has no
// schema type), then the target's.
func validateTargetEncodeParams(env *descriptor.Envelope, feat *types.Feature, schema *encoding.Schema, field *encoding.Field) {
	if len(feat.Params) == 0 {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_TARGET_ENCODE requires params with 'target'",
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	var p targetencode.Params
	if err := json.Unmarshal(feat.Params, &p); err != nil {
		env.AddError(
			string(errors.PROCESSING_CONFIG),
			"feature FEAT_TARGET_ENCODE has malformed params: "+err.Error(),
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	if ce := targetencode.Check(p); ce != nil {
		env.AddError(string(ce.Code), ce.Message, map[string]any{"feature": string(feat.Type)})
		return
	}
	if field != nil {
		if cerr := fieldaccept.Refusal(string(feat.Type), fieldaccept.SlotField, feat.Field, field.Type); cerr != nil {
			env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
			return
		}
	}
	// An unknown name is FieldRefRefusals' refusal; a target the
	// accepted-types table refuses (a categorical's index or a set's
	// bitmask is no numeric outcome) the runtime constructor's.
	if cerr := fieldaccept.Check(string(feat.Type), fieldaccept.SlotTarget, p.Target, schema); cerr != nil {
		env.AddError(string(cerr.Code), cerr.Message, cerr.Details)
	}
}

// featureOutputLabels returns the output column names a feature emits.
// Mirrors the operator implementations in internal/processing/feature; CI does not
// statically verify the agreement, but the integration tests round-trip
// each operator end to end.
func featureOutputLabels(feat *types.Feature, schema *encoding.Schema) []string {
	switch feat.Type {
	case types.FEAT_LOG:
		return []string{singleLabel(feat, "LOG_")}
	case types.FEAT_SQRT:
		return []string{singleLabel(feat, "SQRT_")}
	case types.FEAT_BUCKETIZE:
		return []string{singleLabel(feat, "BUCKET_")}
	case types.FEAT_FREQUENCY_ENCODE:
		return []string{singleLabel(feat, "FREQ_")}
	case types.FEAT_TARGET_ENCODE:
		return []string{singleLabel(feat, "TARGET_")}
	case types.FEAT_TRAIN_TEST_SPLIT:
		label := feat.Label
		if label == "" {
			label = "split"
		}
		return []string{label}
	case types.FEAT_DATE_FEATURES:
		prefix := feat.Label
		if prefix == "" {
			prefix = feat.Field
		}
		out := []string{
			prefix + "_year",
			prefix + "_month",
			prefix + "_day",
			prefix + "_dow",
			prefix + "_quarter",
		}
		// A `datetime` also carries the wall-clock hour.
		if schema != nil {
			if f := schema.Field(feat.Field); f != nil && f.Type == encoding.FieldTypeDateTime {
				out = append(out, prefix+"_hour")
			}
		}
		return out
	case types.FEAT_POLY:
		prefix := feat.Label
		if prefix == "" {
			prefix = feat.Field + "_poly"
		}
		var p struct {
			Degree int `json:"degree"`
		}
		if len(feat.Params) > 0 {
			_ = json.Unmarshal(feat.Params, &p)
		}
		if p.Degree < 2 {
			// Predict surfaces a validation error elsewhere; emit no
			// columns rather than guess.
			return nil
		}
		if p.Degree > 10 {
			p.Degree = 10
		}
		out := make([]string, 0, p.Degree-1)
		for k := 2; k <= p.Degree; k++ {
			out = append(out, prefix+"_"+strconv.Itoa(k))
		}
		return out
	case types.FEAT_ONE_HOT:
		prefix := feat.Label
		if prefix == "" {
			prefix = feat.Field
		}
		// The runtime's level list (newOneHot): dictionary labels, or a
		// packed_bool's "0" / "1".
		values := fieldaccept.Levels(schema.Field(feat.Field))
		if values == nil {
			return nil
		}
		out := make([]string, len(values))
		for i, v := range values {
			out[i] = prefix + "_" + strings.ReplaceAll(v, " ", "_")
		}
		return out
	}
	return nil
}

func singleLabel(feat *types.Feature, prefix string) string {
	if feat.Label != "" {
		return feat.Label
	}
	return prefix + feat.Field
}

// trainTestSplitName names the split operator in the target-leakage
// warning, or describes it when the instance hides FEAT_TRAIN_TEST_SPLIT
// so the warning never advertises an operator it does not offer.
func trainTestSplitName(inst *InstanceSnapshot) string {
	if inst.Hidden(string(types.FEAT_TRAIN_TEST_SPLIT)) {
		return "train/test split"
	}
	return string(types.FEAT_TRAIN_TEST_SPLIT)
}
