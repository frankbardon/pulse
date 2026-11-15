package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// file.go is the model file: the standalone, versioned JSON document a
// `save` writes (RegressionSpec.Save, FitSpec.Save) and a later request
// loads. The codec lives here, in the leaf, so the runtime and the
// no-execute predict path parse ONE format with ONE rule set; the
// atomic write (afero) is the service's.
//
// Floats round-trip bit-exactly: a finite float64 is written in Go's
// shortest round-trip form (negative zero as -0, subnormals exact); an
// undefined figure (NaN) is JSON null and reads back NaN, never 0; ±Inf
// is the JSON string "+Inf" / "-Inf". NaN payload bits are not kept —
// every NaN in a fit means "undefined".

// negativeBinomial is the REG_GLM family whose fits carry a theta
// (internal/reglink.NegativeBinomial; the leaf imports no rule package).
const negativeBinomial = "negative_binomial"

// FileSuffix is the model file name suffix every save path carries. The
// label- and range-table directory loaders skip it (a model file beside
// a table is not a table).
const FileSuffix = ".pulse-model.json"

// FileFormatVersion is the one model_format_version this build writes
// and reads. What a file carries beyond the plain fit is not encoded in
// the version but listed in its `features` (FileFeatures): the writer
// lists exactly the sections present, and a reader judges the document
// by that list. A version-1 file of an earlier build (a plain fit, no
// features key) reads unchanged; the retired versions 2 to 6 — a
// version ladder that encoded feature combinations — are refused
// PULSE_MODEL_VERSION_UNSUPPORTED with a re-save message, never aliased.
const FileFormatVersion = 1

// FileFormatVersions are the model_format_version values this build
// reads.
func FileFormatVersions() []int { return []int{FileFormatVersion} }

// The model file features: each names one optional section of the
// document, listed in `features` exactly when the section is present.
const (
	// FeatureRobustCovariance: `covariance`, the robust estimator the
	// standard errors, intervals, df and vcov came from.
	FeatureRobustCovariance = "robust_covariance"
	// FeatureOutputs: `outputs`, an extension or model regression's
	// RegressionResult.Outputs (its coefficients named by its model).
	FeatureOutputs = "outputs"
	// FeatureDispersion: `dispersion`, the estimated φ̂ beside a
	// classical vcov.
	FeatureDispersion = "dispersion"
	// FeatureTheta: `theta`, a REG_GLM negative_binomial fit's θ.
	FeatureTheta = "theta"
	// FeatureSweepAxes: `sweep_axes`, a sweep-bound fit's (`from_sweep`)
	// winning slot's axis values, each kept as raw JSON — what
	// sweep_axis() reads off the loaded model.
	FeatureSweepAxes = "sweep_axes"
	// FeatureAdstock: `adstock`, a media-mix fit's per-channel adstock
	// records (Adstock, AdstockLineage) — what response() reads off the
	// loaded model.
	FeatureAdstock = "adstock"
	// FeatureSaturation: `saturation`, a REG_MMM_BAYES fit's curve
	// record — its params.saturation when not the default Hill curve —
	// what response() evaluates on the loaded model.
	FeatureSaturation = "saturation"
)

// fileFeature is one optional model-file section: the registry entry a
// new section adds (beside its File field and the NewFile line that
// fills it). The writer derives `features` from present, in registry
// order; check requires present exactly when the name is listed, then
// applies rule.
type fileFeature struct {
	// name is the `features` entry.
	name string
	// key is the document key the section occupies.
	key string
	// present reports whether f carries the section.
	present func(f *File) bool
	// rule is the section's own structural rule, applied when present;
	// "" when whole. Nil for none.
	rule func(f *File) string
}

// fileFeatures is the registry, in the order the writer lists features.
var fileFeatures = []fileFeature{
	{
		name: FeatureRobustCovariance, key: "covariance",
		present: func(f *File) bool { return f.Covariance != nil },
		rule: func(f *File) string {
			c := f.Covariance
			robust := &types.RegressionCovariance{Type: c.Type}
			if !slices.Contains(types.AllCovarianceTypes(), c.Type) || !robust.Robust() {
				return fmt.Sprintf("covariance type %q is not a robust estimator", c.Type)
			}
			return ""
		},
	},
	{
		name: FeatureOutputs, key: "outputs",
		present: func(f *File) bool { return f.Outputs != nil },
		rule: func(f *File) string {
			if BuiltinRegression(f.Type) {
				return fmt.Sprintf("carries outputs, which only an extension or model regression's fit has, but its type is the built-in %s", f.Type)
			}
			return ""
		},
	},
	{
		name: FeatureDispersion, key: "dispersion",
		present: func(f *File) bool { return f.Dispersion != 0 },
		rule: func(f *File) string {
			switch {
			case !BuiltinRegression(f.Type):
				return fmt.Sprintf("carries a dispersion, which only a built-in fit has, but its type is %s", f.Type)
			case f.Vcov == nil:
				return "carries a dispersion without a vcov (the dispersion rides a classical vcov)"
			case f.Covariance != nil:
				return "carries both a dispersion and a robust covariance (the dispersion rides a classical vcov)"
			}
			return ""
		},
	},
	{
		name: FeatureTheta, key: "theta",
		present: func(f *File) bool { return f.Theta != 0 },
		rule: func(f *File) string {
			switch {
			case !BuiltinRegression(f.Type) || f.Family != negativeBinomial:
				return fmt.Sprintf("carries a theta, which only a REG_GLM negative_binomial fit has, but its family is %s", strconv.Quote(f.Family))
			case !(f.Theta > 0 && !math.IsInf(float64(f.Theta), 1)):
				return "has a theta that is not a finite number > 0"
			}
			return ""
		},
	},
	{
		name: FeatureSweepAxes, key: "sweep_axes",
		present: func(f *File) bool { return f.SweepAxes != nil },
		rule: func(f *File) string {
			if len(f.SweepAxes) == 0 {
				return "has an empty sweep_axes (a sweep has at least one axis)"
			}
			return ""
		},
	},
	{
		name: FeatureAdstock, key: "adstock",
		present: func(f *File) bool { return f.Adstock != nil },
		rule:    adstockRule,
	},
	{
		name: FeatureSaturation, key: "saturation",
		present: func(f *File) bool { return f.Saturation != "" },
		rule:    saturationRule,
	},
}

// FileFeatures are the model-file features this build reads and writes,
// in the order the writer lists them.
func FileFeatures() []string {
	out := make([]string, len(fileFeatures))
	for i, ft := range fileFeatures {
		out[i] = ft.name
	}
	return out
}

// knownFeature reports whether name is a registered feature.
func knownFeature(name string) bool {
	return slices.ContainsFunc(fileFeatures, func(ft fileFeature) bool { return ft.name == name })
}

// presentFeatures is f's features list: every registered section f
// carries, in registry order; nil for none (a plain fit's file then has
// no features key, byte-identical to a version-1 file of earlier builds).
func (f *File) presentFeatures() []string {
	var out []string
	for _, ft := range fileFeatures {
		if ft.present(f) {
			out = append(out, ft.name)
		}
	}
	return out
}

// The PULSE_MODEL_SAVE_INVALID reasons.
const (
	ReasonSavePathEmpty   = "path_empty"
	ReasonSaveSuffix      = "suffix"
	ReasonSaveEscapesRoot = "escapes_root"
	ReasonSaveNameMissing = "name_missing"
)

// The PULSE_MODEL_FILE_INVALID reasons.
const (
	ReasonFileJSON           = "json"
	ReasonFileVersionMissing = "version_missing"
	ReasonFileSchema         = "schema"
)

// File is one model file document (model_format_version 1 plus its
// `features`): the fitted regression's scoring figures at full float64
// precision plus informational provenance. Maps are keyed like
// RegressionResult's (InterceptKey included); a term absent from a map
// has no figure.
type File struct {
	// ModelFormatVersion is always FileFormatVersion (1).
	ModelFormatVersion int `json:"model_format_version"`
	// Features lists the optional sections the document carries
	// (FileFeatures order on write; absent for none). A listed feature's
	// section is required, an unlisted one's absent.
	Features []string `json:"features,omitempty"`
	// Name is the name the model was saved under (informational: a load
	// binds the name the loading request gives it).
	Name string `json:"name"`
	// Type is the regression operator that produced the fit.
	Type types.RegressionType `json:"type"`
	// Family and Link are the GLM error family and link.
	Family string `json:"family,omitempty"`
	Link   string `json:"link,omitempty"`
	// Penalty, Selection and Resample echo the fit's modifiers.
	Penalty   string `json:"penalty,omitempty"`
	Selection string `json:"selection,omitempty"`
	Resample  string `json:"resample,omitempty"`
	// Target is the fit's response field.
	Target string `json:"target"`
	// Predictors are the predictor columns the fit uses, in fit order —
	// under Selection the retained ones only.
	Predictors []string `json:"predictors"`
	// Terms are the predictor columns the fit was offered, in spec
	// order (equal to Predictors without Selection).
	Terms []string `json:"terms"`
	// Coefficients, StdErrors, CILower and CIUpper are the fit's
	// per-term figures.
	Coefficients map[string]Float `json:"coefficients"`
	StdErrors    map[string]Float `json:"std_errors,omitempty"`
	CILower      map[string]Float `json:"ci_lower,omitempty"`
	CIUpper      map[string]Float `json:"ci_upper,omitempty"`
	// DF is the intervals' Student-t degrees of freedom (absent: Wald;
	// null: undefined).
	DF Float `json:"df,omitempty"`
	// ConfLevel is the intervals' coverage (absent: no intervals).
	ConfLevel Float `json:"conf_level,omitempty"`
	// Vcov is the coefficient covariance when the fit computed one.
	Vcov *FileMatrix `json:"vcov,omitempty"`
	// Dispersion (feature dispersion, beside a classical vcov) is the
	// fit's estimated dispersion φ̂ — RegressionResult.Dispersion,
	// present beside a classical vcov whose φ is estimated (REG_OLS σ̂²,
	// REG_GLM gamma's Pearson χ²/df, a fixed-θ negative binomial's under
	// dispersion "estimated"); null when undefined (df ≤ 0).
	Dispersion Float `json:"dispersion,omitempty"`
	// Theta (feature theta) is a REG_GLM
	// negative_binomial fit's θ — RegressionResult.Theta, its estimate or
	// the fixed θ: the variance μ + μ²/θ its residual kinds and influence
	// figures read. Finite and > 0.
	Theta Float `json:"theta,omitempty"`
	// Covariance (feature robust_covariance) names the robust
	// estimator the standard errors, intervals, df and vcov came from —
	// RegressionResult.Covariance: type, and the cluster field and its
	// level count or the order_by field and resolved lag. Absent: the
	// fit's classical covariance.
	Covariance *types.RegressionCovarianceResult `json:"covariance,omitempty"`
	// Outputs (feature outputs) are an extension regression's
	// RegressionResult.Outputs, each value in its wire JSON form (a
	// non-finite float null): what the registration's Scorer reads
	// beside the coefficients.
	Outputs map[string]json.RawMessage `json:"outputs,omitempty"`
	// SweepAxes (feature sweep_axes) are a sweep-bound fit's
	// (FitSpec.FromSweep) winning slot's axis values (RankEntry.Axes),
	// each in its JSON form — a number, string or bool reads back
	// exactly. Present: the loaded model is sweep-bound and
	// sweep_axis() reads these; absent: it is not (not_sweep_bound).
	SweepAxes map[string]json.RawMessage `json:"sweep_axes,omitempty"`
	// Adstock (feature adstock) is a media-mix fit's adstock record per
	// channel (Adstock): the kernel the channel's spend went through,
	// written by lineage (AdstockLineage) or declared by an inline
	// model. File.Bind restores it as Bound.Adstocks.
	Adstock map[string]Adstock `json:"adstock,omitempty"`
	// Saturation (feature saturation) is a REG_MMM_BAYES fit's curve
	// record (Bound.Saturation): logistic or michaelis_menten, absent
	// for the default Hill curve (a file may spell hill out). File.Bind
	// restores it.
	Saturation string `json:"saturation,omitempty"`
	// NObs, SumWeights and NEff are the fit's sample sizes (the last
	// two present on a weighted fit only).
	NObs       int   `json:"n_obs"`
	SumWeights Float `json:"sum_weights,omitempty"`
	NEff       Float `json:"n_eff,omitempty"`
	// Provenance records where the fit came from. Informational only:
	// a load never compares it with anything.
	Provenance Provenance `json:"provenance"`
}

// Provenance is a model file's informational origin.
type Provenance struct {
	// Cohort is the training request's cohort path.
	Cohort string `json:"cohort,omitempty"`
	// Fingerprint is "sha256:<hex>" of the training cohort file's bytes
	// at save time (absent when it could not be computed).
	Fingerprint string `json:"fingerprint,omitempty"`
	// RequestDigest is the training request's canonical hash
	// (types.Request.Hash).
	RequestDigest string `json:"request_digest,omitempty"`
}

// FileMatrix is a model file's matrix (the vcov): MatrixValues with
// bit-exact floats.
type FileMatrix struct {
	Kind       types.MatrixKind     `json:"kind"`
	Encoding   types.MatrixEncoding `json:"encoding"`
	RowKeys    []string             `json:"row_keys"`
	ColumnKeys []string             `json:"column_keys"`
	Labels     []string             `json:"labels,omitempty"`
	Values     [][]Float            `json:"values"`
}

// Float is a model-file float64: shortest round-trip form when finite,
// null for NaN, "+Inf" / "-Inf" for the infinities.
type Float float64

// The JSON spellings of the infinities.
const (
	posInf = "+Inf"
	negInf = "-Inf"
)

// MarshalJSON writes f bit-exactly (see Float).
func (f Float) MarshalJSON() ([]byte, error) {
	v := float64(f)
	switch {
	case math.IsNaN(v):
		return []byte("null"), nil
	case math.IsInf(v, 1):
		return json.Marshal(posInf)
	case math.IsInf(v, -1):
		return json.Marshal(negInf)
	}
	return json.Marshal(v)
}

// UnmarshalJSON reads what MarshalJSON writes: null is NaN.
func (f *Float) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if string(b) == "null" {
		*f = Float(math.NaN())
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		switch s {
		case posInf:
			*f = Float(math.Inf(1))
		case negInf:
			*f = Float(math.Inf(-1))
		default:
			return fmt.Errorf("float string %q is not %q or %q", s, posInf, negInf)
		}
		return nil
	}
	v, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return fmt.Errorf("float %s: %w", b, err)
	}
	*f = Float(v)
	return nil
}

// NewFile builds the model file of a finished fit: spec is the
// regression as the fit ran it, res its full-precision result (never a
// `return`-rounded one). It holds what Bind needs to rebuild the same
// Bound — under Selection the retained predictors, with the candidate
// set as Terms.
func NewFile(name string, spec *types.RegressionSpec, res *types.RegressionResult, prov Provenance) (*File, error) {
	// Bind is the gate: a result it cannot bind is not saved.
	b, err := Bind(name, spec, res)
	if err != nil {
		return nil, err
	}
	f := &File{
		ModelFormatVersion: FileFormatVersion,
		Name:               name,
		Type:               res.Type,
		Family:             res.Family,
		Link:               res.Link,
		Penalty:            res.Penalty,
		Selection:          spec.Selection,
		Resample:           res.Resample,
		Target:             b.Target,
		Predictors:         nonNil(b.Predictors),
		Terms:              nonNil(b.Terms),
		Coefficients:       toFloats(res.Coefficients),
		StdErrors:          toFloats(res.StdErrors),
		CILower:            toFloats(res.CILower),
		CIUpper:            toFloats(res.CIUpper),
		DF:                 Float(res.DF),
		ConfLevel:          Float(res.ConfLevel),
		NObs:               res.NObs,
		SumWeights:         Float(res.SumWeights),
		NEff:               Float(res.NEff),
		Provenance:         prov,
	}
	if f.Coefficients == nil {
		f.Coefficients = map[string]Float{}
	}
	if res.Vcov != nil {
		f.Vcov = toFileMatrix(res.Vcov)
	}
	if res.Dispersion != 0 && BuiltinRegression(res.Type) && res.Covariance == nil {
		// The engine sets it beside a classical vcov only.
		f.Dispersion = Float(res.Dispersion)
	}
	if res.Covariance != nil {
		c := *res.Covariance
		if c.Lags != nil {
			lags := *c.Lags
			c.Lags = &lags
		}
		f.Covariance = &c
	}
	if res.Theta != 0 && BuiltinRegression(res.Type) {
		// A negative binomial fit's θ (a robust fit keeps its covariance
		// echo beside it; a fixed-θ fit its estimated dispersion).
		f.Theta = Float(res.Theta)
	}
	if !BuiltinRegression(res.Type) {
		outs, err := encodeOutputs(res.Outputs)
		if err != nil {
			return nil, err
		}
		f.Outputs = outs
	}
	f.Saturation = b.Saturation
	// The list is derived from the sections, never set by hand: the
	// writer lists exactly what is present.
	f.Features = f.presentFeatures()
	return f, nil
}

// SetSweepAxes records a sweep-bound fit's winning axis values
// (RankEntry.Axes) as the file's sweep_axes section, each value in its
// JSON form (types.MarshalFinite), and re-derives the features list. A
// nil or empty axes leaves the file as is: only a sweep-bound fit
// carries the section.
func (f *File) SetSweepAxes(axes map[string]any) error {
	if len(axes) == 0 {
		return nil
	}
	out := make(map[string]json.RawMessage, len(axes))
	for k, v := range axes {
		b, err := types.MarshalFinite(v)
		if err != nil {
			return errors.WrapCodedError(err, errors.PROCESSING_INTERNAL, "encoding model sweep axis "+k)
		}
		out[k] = json.RawMessage(b)
	}
	f.SweepAxes = out
	f.Features = f.presentFeatures()
	return nil
}

// SetAdstock records a media-mix fit's adstock records (AdstockLineage)
// as the file's adstock section and re-derives the features list. A nil
// or empty map leaves the file as is: only a fit with a record carries
// the section.
func (f *File) SetAdstock(records map[string]Adstock) {
	if len(records) == 0 {
		return
	}
	f.Adstock = cloneAdstocks(records)
	f.Features = f.presentFeatures()
}

// sweepAxisValues reads the sweep_axes section back into the values
// sweep_axis() reads: a number as a json.Number holding its exact
// literal (SweepAxis normalizes it to float64, as it does the
// expansion's), a string or bool as is. Nil for none; the file decode
// already proved each value valid JSON.
func (f *File) sweepAxisValues() map[string]any {
	if f.SweepAxes == nil {
		return nil
	}
	out := make(map[string]any, len(f.SweepAxes))
	for k, b := range f.SweepAxes {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var v any
		_ = dec.Decode(&v)
		out[k] = v
	}
	return out
}

// Encode writes f as the model file's bytes: indented JSON (map keys
// sorted, so a file is deterministic) with a trailing newline.
func (f *File) Encode() ([]byte, error) {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_INTERNAL, "encoding model file")
	}
	return append(b, '\n'), nil
}

// DecodeFile parses a model file. The version is read first: absent or
// not an integer is PULSE_MODEL_FILE_INVALID (reason version_missing /
// schema), any value but 1 PULSE_MODEL_VERSION_UNSUPPORTED (the retired
// 2 to 6 with a re-save message). The `features` list is read next: not
// a list of strings is reason schema, a name this build does not know
// PULSE_MODEL_VERSION_UNSUPPORTED with details.unknown_features — before
// the strict decode, so the unknown section's key is not misjudged. The
// document is then decoded strictly — an unknown key, a wrong type or a
// structural fault (no type or target, a predictor without a
// coefficient, a predictor outside terms, a malformed vcov, a listed
// feature without its section or a section whose feature is unlisted, a
// section breaking its rule) is PULSE_MODEL_FILE_INVALID reason schema;
// malformed JSON reason json. source names the file in messages and
// details.path.
func DecodeFile(source string, data []byte) (*File, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fileInvalid(source, ReasonFileJSON, err.Error())
	}
	raw, ok := probe["model_format_version"]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return nil, fileInvalid(source, ReasonFileVersionMissing, "has no model_format_version")
	}
	var version int
	if err := json.Unmarshal(raw, &version); err != nil {
		return nil, fileInvalid(source, ReasonFileSchema, "model_format_version is not an integer")
	}
	if version != FileFormatVersion {
		msg := fmt.Sprintf("model file %s has model_format_version %d; this build reads version %d only", source, version, FileFormatVersion)
		if version >= retiredVersionMin && version <= retiredVersionMax {
			msg = fmt.Sprintf("model file %s has model_format_version %d, a retired version this build no longer reads (it reads version %d plus a features list); re-save the fit with this build (rerun it with `save`)", source, version, FileFormatVersion)
		}
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_VERSION_UNSUPPORTED, msg,
			map[string]any{"path": source, "version": version, "supported": FileFormatVersions()})
	}
	if raw, ok := probe["features"]; ok {
		var names []string
		if err := json.Unmarshal(raw, &names); err != nil {
			return nil, fileInvalid(source, ReasonFileSchema, "features is not a list of strings")
		}
		var unknown []string
		for _, n := range names {
			if !knownFeature(n) && !slices.Contains(unknown, n) {
				unknown = append(unknown, n)
			}
		}
		if len(unknown) > 0 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_VERSION_UNSUPPORTED,
				fmt.Sprintf("model file %s lists feature(s) %s this build does not read (it reads %s); load it with the Pulse version that wrote it", source, strings.Join(unknown, ", "), strings.Join(FileFeatures(), ", ")),
				map[string]any{"path": source, "version": version, "supported": FileFormatVersions(),
					"unknown_features": unknown, "supported_features": FileFeatures()})
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f File
	if err := dec.Decode(&f); err != nil {
		return nil, fileInvalid(source, ReasonFileSchema, err.Error())
	}
	if err := f.check(); err != "" {
		return nil, fileInvalid(source, ReasonFileSchema, err)
	}
	return &f, nil
}

// The retired model_format_version ladder (versions 2 to 6), refused
// with a re-save message.
const (
	retiredVersionMin = 2
	retiredVersionMax = 6
)

// check is the structural rule; "" when the document is whole. The shape
// is judged by the features list: a listed feature's section is
// required, an unlisted one's absent, and each present section obeys
// its own rule.
func (f *File) check() string {
	switch {
	case f.Type == "":
		return "has no type"
	case f.Target == "":
		return "has no target"
	case f.Coefficients == nil:
		return "has no coefficients"
	}
	for i, n := range f.Features {
		if !knownFeature(n) {
			return fmt.Sprintf("lists unknown feature %q", n)
		}
		if slices.Contains(f.Features[:i], n) {
			return fmt.Sprintf("lists feature %q twice", n)
		}
	}
	for _, ft := range fileFeatures {
		listed, has := slices.Contains(f.Features, ft.name), ft.present(f)
		switch {
		case listed && !has:
			return fmt.Sprintf("lists feature %s without its %s section", ft.name, ft.key)
		case !listed && has:
			return fmt.Sprintf("carries %s without listing feature %s", ft.key, ft.name)
		case has && ft.rule != nil:
			if msg := ft.rule(f); msg != "" {
				return msg
			}
		}
	}
	if f.Family == negativeBinomial && f.Theta == 0 {
		return "is a negative_binomial fit without its theta (feature theta)"
	}
	// An extension regression's coefficients are named by its model: no
	// intercept, nor one per predictor, is required.
	builtin := BuiltinRegression(f.Type)
	if _, ok := f.Coefficients[InterceptKey]; !ok && builtin {
		return fmt.Sprintf("has no %q coefficient", InterceptKey)
	}
	if !builtin && len(f.Coefficients) == 0 {
		return "has no coefficient (every fit reports at least one)"
	}
	for i, p := range f.Predictors {
		if slices.Contains(f.Predictors[:i], p) {
			return fmt.Sprintf("lists predictor %q twice", p)
		}
		if _, ok := f.Coefficients[p]; !ok && builtin {
			return fmt.Sprintf("has no coefficient for predictor %q", p)
		}
		if !slices.Contains(f.Terms, p) {
			return fmt.Sprintf("predictor %q is not one of terms", p)
		}
	}
	if f.Selection == "" && !slices.Equal(f.Predictors, f.Terms) {
		return "terms differ from predictors without a selection"
	}
	if m := f.Vcov; m != nil {
		if len(m.Values) != len(m.RowKeys) {
			return "vcov has a row count unlike its row_keys"
		}
		for _, row := range m.Values {
			if len(row) != len(m.ColumnKeys) {
				return "vcov has a row length unlike its column_keys"
			}
		}
	}
	return ""
}

// Bind rebuilds the Bound the saved fit had, under name: the same
// predictors, terms and figures, bit for bit. Its Result carries the
// saved figures only (no R² or deviance). A file carrying sweep_axes
// binds sweep-bound (SweepBound + SweepAxes), so sweep_axis() reads the
// saved winner's axes; one carrying adstock binds its records
// (Bound.Adstocks), and one carrying saturation its curve record
// (Bound.Saturation; "hill" binds as the default "").
func (f *File) Bind(name string) (*Bound, error) {
	spec := &types.RegressionSpec{
		Type:       f.Type,
		Target:     f.Target,
		Predictors: append([]string(nil), f.Terms...),
		Family:     f.Family,
		Link:       f.Link,
		Penalty:    f.Penalty,
		Selection:  f.Selection,
		Resample:   f.Resample,
	}
	b, err := Bind(name, spec, f.Result())
	if err != nil {
		return nil, err
	}
	if f.SweepAxes != nil {
		b.SweepBound, b.SweepAxes = true, f.sweepAxisValues()
	}
	b.Adstocks = cloneAdstocks(f.Adstock)
	if f.Saturation != MMMSaturationHill {
		b.Saturation = f.Saturation
	}
	return b, nil
}

// Result is the RegressionResult the file's figures describe.
func (f *File) Result() *types.RegressionResult {
	res := &types.RegressionResult{
		Name:         f.Name,
		Type:         f.Type,
		Family:       f.Family,
		Link:         f.Link,
		Penalty:      f.Penalty,
		Selection:    f.Selection,
		Resample:     f.Resample,
		Coefficients: fromFloats(f.Coefficients),
		StdErrors:    fromFloats(f.StdErrors),
		CILower:      fromFloats(f.CILower),
		CIUpper:      fromFloats(f.CIUpper),
		DF:           float64(f.DF),
		ConfLevel:    float64(f.ConfLevel),
		NObs:         f.NObs,
		SumWeights:   float64(f.SumWeights),
		NEff:         float64(f.NEff),
		Dispersion:   float64(f.Dispersion),
		Theta:        float64(f.Theta),
	}
	if f.Selection != "" {
		res.SelectedFeatures = append([]string(nil), f.Predictors...)
	}
	if f.Vcov != nil {
		res.Vcov = fromFileMatrix(f.Vcov)
	}
	if f.Covariance != nil {
		c := *f.Covariance
		if c.Lags != nil {
			lags := *c.Lags
			c.Lags = &lags
		}
		res.Covariance = &c
	}
	res.Outputs = decodeOutputs(f.Outputs)
	return res
}

// CheckSavePath is the rule over a `save` path, shared by the runtime and
// predict: non-blank, a file name ending in FileSuffix with a stem
// before it, and never above the filesystem root (no leading ".." once
// cleaned). A violation is PULSE_MODEL_SAVE_INVALID with details.path
// and details.reason. Nil for a valid path.
func CheckSavePath(p string) *errors.CodedError {
	if reason, what := checkModelPath(p); reason != "" {
		return saveInvalid(p, reason, what)
	}
	return nil
}

// checkModelPath is the one model-file path rule, shared by a `save`
// (CheckSavePath) and a `models` entry (CheckModelRefs): the violated
// reason and its phrase, or "" for a valid path.
func checkModelPath(p string) (reason, what string) { return checkFilePath(p, FileSuffix) }

// checkFilePath is the saved-file path rule over suffix — the model
// file's (FileSuffix) and the population file's (PopulationSuffix).
func checkFilePath(p, suffix string) (reason, what string) {
	if strings.TrimSpace(p) == "" {
		return ReasonSavePathEmpty, "is blank"
	}
	slashed := filepath.ToSlash(p)
	base := path.Base(slashed)
	if !strings.HasSuffix(base, suffix) || strings.HasSuffix(slashed, "/") {
		return ReasonSaveSuffix, fmt.Sprintf("does not name a %s file", suffix)
	}
	if base == suffix {
		return ReasonSaveNameMissing, fmt.Sprintf("has no file name before %s", suffix)
	}
	if c := path.Clean(slashed); c == ".." || strings.HasPrefix(c, "../") {
		return ReasonSaveEscapesRoot, "climbs above the filesystem root"
	}
	return "", ""
}

// cleanModelPath is p as the filesystem sees it — the key two `save`
// paths are compared by.
func cleanModelPath(p string) string { return path.Clean(filepath.ToSlash(p)) }

// CheckRegressionSaves applies CheckSavePath to every RegressionSpec
// that sets Save, in order; the first fault wins and gains
// details.regression (the 0-based spec index).
func CheckRegressionSaves(regs []*types.RegressionSpec) *errors.CodedError {
	for i, r := range regs {
		if r == nil || r.Save == "" {
			continue
		}
		if cerr := CheckSavePath(r.Save); cerr != nil {
			cerr.Details["regression"] = i
			return cerr
		}
	}
	return nil
}

// Overwritten is the PULSE_MODEL_OVERWRITTEN warning: a save replaced
// an existing file at path.
func Overwritten(p, name string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_OVERWRITTEN,
		fmt.Sprintf("model %q overwrote the existing model file %s", name, p),
		map[string]any{"path": p, "name": name})
}

// WriteFailed is the PULSE_MODEL_WRITE_FAILED refusal: the save of
// model name to path failed at stage (mkdir, create, write, sync,
// close, rename, stat). The target is left as it was.
func WriteFailed(p, name, stage string, cause error) *errors.CodedError {
	msg := fmt.Sprintf("saving model %q to %s failed at %s", name, p, stage)
	if cause != nil {
		msg += ": " + cause.Error()
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_WRITE_FAILED, msg,
		map[string]any{"path": p, "name": name, "stage": stage})
}

func saveInvalid(p, reason, what string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_SAVE_INVALID,
		fmt.Sprintf("save path %q %s", p, what),
		map[string]any{"path": p, "reason": reason})
}

func fileInvalid(source, reason, what string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_FILE_INVALID,
		fmt.Sprintf("model file %s is not a valid model file: %s", source, what),
		map[string]any{"path": source, "reason": reason})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return append([]string(nil), s...)
}

func toFloats(m map[string]float64) map[string]Float {
	if m == nil {
		return nil
	}
	out := make(map[string]Float, len(m))
	for k, v := range m {
		out[k] = Float(v)
	}
	return out
}

func fromFloats(m map[string]Float) map[string]float64 {
	if m == nil {
		return nil
	}
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = float64(v)
	}
	return out
}

func toFileMatrix(m *types.MatrixValues) *FileMatrix {
	out := &FileMatrix{
		Kind: m.Kind, Encoding: m.Encoding,
		RowKeys: nonNil(m.RowKeys), ColumnKeys: nonNil(m.ColumnKeys),
		Labels: append([]string(nil), m.Labels...),
		Values: make([][]Float, len(m.Values)),
	}
	for i, row := range m.Values {
		out.Values[i] = make([]Float, len(row))
		for j, v := range row {
			out.Values[i][j] = Float(v)
		}
	}
	return out
}

func fromFileMatrix(m *FileMatrix) *types.MatrixValues {
	out := &types.MatrixValues{
		Kind: m.Kind, Encoding: m.Encoding,
		RowKeys: append([]string(nil), m.RowKeys...), ColumnKeys: append([]string(nil), m.ColumnKeys...),
		Labels: append([]string(nil), m.Labels...),
		Values: make([][]float64, len(m.Values)),
	}
	for i, row := range m.Values {
		out.Values[i] = make([]float64, len(row))
		for j, v := range row {
			out.Values[i][j] = float64(v)
		}
	}
	return out
}

// encodeOutputs is an extension regression's Outputs as the model file
// keeps them: each value in its wire JSON form (types.MarshalFinite — a
// non-finite float null). Nil for none.
func encodeOutputs(outs map[string]any) (map[string]json.RawMessage, error) {
	if len(outs) == 0 {
		return nil, nil
	}
	out := make(map[string]json.RawMessage, len(outs))
	for k, v := range outs {
		b, err := types.MarshalFinite(v)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.PROCESSING_INTERNAL, "encoding model output "+k)
		}
		out[k] = json.RawMessage(b)
	}
	return out, nil
}

// decodeOutputs reads encodeOutputs' form back into plain JSON values
// (float64, string, bool, []any, map[string]any, nil). Nil for none;
// the file decode already proved each value valid JSON.
func decodeOutputs(raw map[string]json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]any, len(raw))
	for k, b := range raw {
		var v any
		_ = json.Unmarshal(b, &v)
		out[k] = v
	}
	return out
}

// NormalizeOutputs is outs in the form a model file reads back
// (encodeOutputs then decodeOutputs): what an extension regression's
// Scorer sees, for a fit in the request and the same fit reloaded from
// its file alike, so the two score identically.
func NormalizeOutputs(outs map[string]any) (map[string]any, error) {
	raw, err := encodeOutputs(outs)
	if err != nil {
		return nil, err
	}
	return decodeOutputs(raw), nil
}
