package descriptor

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// validateFeatures validates each pre-filter feature operator against the
// schema and computes the post-feature column set. The set is union of
// schema field names and operator-emitted labels; downstream validators
// (filters, attributes, groupers, aggregators) test references against it.
//
// This file does not import processing/feature — that would couple
// validation to execution. Operator output naming is duplicated here as a
// pure function; the feature-engineering skill documents the contract and
// CI gates verify the operator set is documented.
func validateFeatures(env *Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) map[string]bool {
	cols := make(map[string]bool, len(schema.Fields))
	for _, f := range schema.Fields {
		cols[f.Name] = true
	}
	if len(req.Features) == 0 {
		return cols
	}

	splitSeenAt := -1
	for idx, feat := range req.Features {
		if !isKnownFeatureType(feat.Type) {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"unknown feature type: "+string(feat.Type),
				map[string]any{"feature": string(feat.Type)},
			)
			continue
		}

		// Field-presence and per-operator type checks.
		validateFeatureSpec(env, feat, schema, opts)

		// Detect target encoding without a preceding train/test split. The
		// canonical mitigation is to place FEAT_TRAIN_TEST_SPLIT before any
		// FEAT_TARGET_ENCODE; otherwise the encoder mixes statistics from
		// rows that will land in val/test, leaking the target.
		if feat.Type == types.FEAT_TARGET_ENCODE && splitSeenAt < 0 {
			entry := &EnvelopeEntry{
				Code:    string(errors.PULSE_FEAT_TARGET_LEAKAGE_RISK),
				Message: "FEAT_TARGET_ENCODE applied without a preceding FEAT_TRAIN_TEST_SPLIT; target information may leak from val/test rows into training features",
				Details: map[string]any{"feature": string(feat.Type), "feature_index": idx},
			}
			if opts.Strict {
				env.Errors = append(env.Errors, entry)
			} else {
				env.Warnings = append(env.Warnings, entry)
			}
		}
		if feat.Type == types.FEAT_TRAIN_TEST_SPLIT {
			splitSeenAt = idx
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
// field, type compatibility, params shape. Errors land in env via
// SERVICE_VALIDATION; warnings via PULSE_FIELD_DESCRIPTION_LOW_QUALITY are
// out of scope here.
func validateFeatureSpec(env *Envelope, feat *types.Feature, schema *encoding.Schema, opts *PredictOptions) {
	_ = opts // strict-mode handling reserved for future leakage warnings

	switch feat.Type {
	case types.FEAT_TRAIN_TEST_SPLIT:
		validateSplitParams(env, feat, schema)
		return
	}

	// All other operators require a field.
	if feat.Field == "" {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
			"feature "+string(feat.Type)+" requires a field",
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	f := schema.Field(feat.Field)
	if f == nil {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
			"feature references unknown field: "+feat.Field,
			map[string]any{"field": feat.Field, "feature": string(feat.Type)},
		)
		return
	}

	switch feat.Type {
	case types.FEAT_ONE_HOT, types.FEAT_FREQUENCY_ENCODE, types.FEAT_TARGET_ENCODE:
		if !f.Type.IsCategorical() {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"feature "+string(feat.Type)+" requires a categorical field",
				map[string]any{"field": feat.Field, "feature": string(feat.Type)},
			)
		}
		if feat.Type == types.FEAT_TARGET_ENCODE {
			validateTargetEncodeParams(env, feat, schema)
		}
	case types.FEAT_DATE_FEATURES:
		if f.Type != encoding.FieldTypeDate {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"feature FEAT_DATE_FEATURES requires a date field",
				map[string]any{"field": feat.Field, "feature": string(feat.Type)},
			)
		}
	case types.FEAT_BUCKETIZE:
		validateBucketizeParams(env, feat)
	}
}

func validateSplitParams(env *Envelope, feat *types.Feature, schema *encoding.Schema) {
	if len(feat.Params) == 0 {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
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
			string(errors.SERVICE_VALIDATION),
			"feature FEAT_TRAIN_TEST_SPLIT has malformed params: "+err.Error(),
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	if len(p.Ratios) < 2 || len(p.Ratios) > 3 {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
			"feature FEAT_TRAIN_TEST_SPLIT: 'ratios' must have 2 or 3 elements",
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	var sum float64
	for _, r := range p.Ratios {
		if r < 0 {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"feature FEAT_TRAIN_TEST_SPLIT: ratios must be non-negative",
				map[string]any{"feature": string(feat.Type)},
			)
			return
		}
		sum += r
	}
	if math.Abs(sum-1.0) > 1e-6 {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
			"feature FEAT_TRAIN_TEST_SPLIT: ratios must sum to 1.0",
			map[string]any{"feature": string(feat.Type), "sum": sum},
		)
	}
	if p.Stratify != "" {
		f := schema.Field(p.Stratify)
		if f == nil {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"feature FEAT_TRAIN_TEST_SPLIT: stratify references unknown field "+p.Stratify,
				map[string]any{"field": p.Stratify, "feature": string(feat.Type)},
			)
		} else if !f.Type.IsCategorical() {
			env.AddError(
				string(errors.SERVICE_VALIDATION),
				"feature FEAT_TRAIN_TEST_SPLIT: stratify field must be categorical",
				map[string]any{"field": p.Stratify, "feature": string(feat.Type)},
			)
		}
	}
}

func validateBucketizeParams(env *Envelope, feat *types.Feature) {
	if len(feat.Params) == 0 {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
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
			string(errors.SERVICE_VALIDATION),
			"feature FEAT_BUCKETIZE has malformed params: "+err.Error(),
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	if len(p.Boundaries) == 0 && p.Quantiles <= 0 {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
			"feature FEAT_BUCKETIZE: provide either 'boundaries' or 'quantiles'",
			map[string]any{"feature": string(feat.Type)},
		)
	}
	if len(p.Boundaries) > 0 && p.Quantiles > 0 {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
			"feature FEAT_BUCKETIZE: 'boundaries' and 'quantiles' are mutually exclusive",
			map[string]any{"feature": string(feat.Type)},
		)
	}
}

func validateTargetEncodeParams(env *Envelope, feat *types.Feature, schema *encoding.Schema) {
	if len(feat.Params) == 0 {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
			"feature FEAT_TARGET_ENCODE requires params with 'target'",
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	var p struct {
		Target    string  `json:"target"`
		Smoothing float64 `json:"smoothing"`
	}
	if err := json.Unmarshal(feat.Params, &p); err != nil {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
			"feature FEAT_TARGET_ENCODE has malformed params: "+err.Error(),
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	if p.Target == "" {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
			"feature FEAT_TARGET_ENCODE: 'target' is required in params",
			map[string]any{"feature": string(feat.Type)},
		)
		return
	}
	if p.Smoothing < 0 {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
			"feature FEAT_TARGET_ENCODE: 'smoothing' must be non-negative",
			map[string]any{"feature": string(feat.Type)},
		)
	}
	f := schema.Field(p.Target)
	if f == nil {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
			"feature FEAT_TARGET_ENCODE: target references unknown field "+p.Target,
			map[string]any{"field": p.Target, "feature": string(feat.Type)},
		)
	} else if f.Type.IsCategorical() {
		env.AddError(
			string(errors.SERVICE_VALIDATION),
			"feature FEAT_TARGET_ENCODE: target field must be numeric, got categorical",
			map[string]any{"field": p.Target, "feature": string(feat.Type)},
		)
	}
}

// featureOutputLabels returns the output column names a feature emits.
// Mirrors the operator implementations in processing/feature; CI does not
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
		return []string{
			prefix + "_year",
			prefix + "_month",
			prefix + "_day",
			prefix + "_dow",
			prefix + "_quarter",
		}
	case types.FEAT_ONE_HOT:
		prefix := feat.Label
		if prefix == "" {
			prefix = feat.Field
		}
		f := schema.Field(feat.Field)
		if f == nil || f.Dictionary == nil {
			return nil
		}
		values := f.Dictionary.Values()
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
