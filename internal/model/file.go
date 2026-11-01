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

// FileSuffix is the model file name suffix every save path carries. The
// label- and range-table directory loaders skip it (a model file beside
// a table is not a table).
const FileSuffix = ".pulse-model.json"

// FileFormatVersion is the newest model_format_version this build
// writes. The version is a function of the content, never a flag (the
// .pulse header's rule): a built-in fit carrying its estimated
// `dispersion` (a classical vcov fit whose φ is estimated: REG_OLS,
// REG_GLM gamma — RegressionResult.Dispersion) writes 4 — version 1
// plus `dispersion`, what a bound fit's influence figures read (U65
// E1-S2); an extension regression's fit (a type outside
// types.AllRegressionTypes) writes 3 — version 2 plus the `outputs` of
// the fit, its coefficients named by the model rather than intercept +
// predictors; a built-in fit whose standard errors came from a robust
// RegressionSpec.Covariance writes 2 — version 1 plus the `covariance`
// echo — and every other fit writes 1, byte-identical to the files
// earlier builds wrote. All stay readable (FileFormatVersions).
const FileFormatVersion = 4

// fileFormatV1 is the version a built-in fit without a robust
// covariance or a dispersion writes; fileFormatV2 a built-in fit with a
// robust covariance; fileFormatV3 an extension regression's fit.
const (
	fileFormatV1 = 1
	fileFormatV2 = 2
	fileFormatV3 = 3
)

// FileFormatVersions are the model_format_version values this build
// reads, oldest first.
func FileFormatVersions() []int {
	return []int{fileFormatV1, fileFormatV2, fileFormatV3, FileFormatVersion}
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

// File is one model file document (model_format_version 1 to 4): the
// fitted regression's scoring figures at full float64 precision plus
// informational provenance. Maps are keyed like RegressionResult's
// (InterceptKey included); a term absent from a map has no figure.
type File struct {
	// ModelFormatVersion is 3 for an extension regression's fit, else 2
	// when Covariance is set, else 4 when Dispersion is, else 1.
	ModelFormatVersion int `json:"model_format_version"`
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
	// Dispersion (version 4 only, where it is required) is the fit's
	// estimated dispersion φ̂ — RegressionResult.Dispersion, present
	// beside a classical vcov whose φ is estimated (REG_OLS σ̂², REG_GLM
	// gamma's Pearson χ²/df); null when undefined (df ≤ 0).
	Dispersion Float `json:"dispersion,omitempty"`
	// Covariance (required on version 2, optional on version 3, absent
	// on version 1) names the robust
	// estimator the standard errors, intervals, df and vcov came from —
	// RegressionResult.Covariance: type, and the cluster field and its
	// level count or the order_by field and resolved lag. Absent: the
	// fit's classical covariance.
	Covariance *types.RegressionCovarianceResult `json:"covariance,omitempty"`
	// Outputs (version 3 only) are an extension regression's
	// RegressionResult.Outputs, each value in its wire JSON form (a
	// non-finite float null): what the registration's Scorer reads
	// beside the coefficients.
	Outputs map[string]json.RawMessage `json:"outputs,omitempty"`
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
		ModelFormatVersion: fileFormatV1,
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
		f.ModelFormatVersion = FileFormatVersion
	}
	if res.Covariance != nil {
		c := *res.Covariance
		if c.Lags != nil {
			lags := *c.Lags
			c.Lags = &lags
		}
		f.Covariance = &c
		f.ModelFormatVersion = fileFormatV2
	}
	if !BuiltinRegression(res.Type) {
		f.ModelFormatVersion = fileFormatV3
		outs, err := encodeOutputs(res.Outputs)
		if err != nil {
			return nil, err
		}
		f.Outputs = outs
	}
	return f, nil
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
// schema), any value outside FileFormatVersions
// PULSE_MODEL_VERSION_UNSUPPORTED. The document is then decoded strictly
// — an unknown key, a wrong type or a structural fault (no type or
// target, a predictor without a coefficient, a predictor outside terms,
// a malformed vcov, a covariance on a version 1 file or none on a
// version 2 one, a covariance type outside the robust estimators) is
// PULSE_MODEL_FILE_INVALID reason schema; malformed JSON reason json.
// source names the file in messages and details.path.
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
	if !slices.Contains(FileFormatVersions(), version) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_VERSION_UNSUPPORTED,
			fmt.Sprintf("model file %s has model_format_version %d; this build reads versions %d to %d", source, version, fileFormatV1, FileFormatVersion),
			map[string]any{"path": source, "version": version, "supported": FileFormatVersions()})
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

// check is the structural rule; "" when the document is whole.
func (f *File) check() string {
	switch {
	case f.Type == "":
		return "has no type"
	case f.Target == "":
		return "has no target"
	case f.Coefficients == nil:
		return "has no coefficients"
	case f.ModelFormatVersion == fileFormatV1 && f.Covariance != nil:
		return "carries a covariance, which model_format_version 1 does not have"
	case f.ModelFormatVersion == fileFormatV2 && f.Covariance == nil:
		return "is model_format_version 2 without a covariance"
	case f.ModelFormatVersion != fileFormatV3 && f.Outputs != nil:
		return fmt.Sprintf("carries outputs, which model_format_version %d does not have", f.ModelFormatVersion)
	case f.ModelFormatVersion == fileFormatV3 && BuiltinRegression(f.Type):
		return fmt.Sprintf("is model_format_version 3, which only an extension or model regression's fit writes, but its type is the built-in %s", f.Type)
	case f.ModelFormatVersion != fileFormatV3 && !BuiltinRegression(f.Type):
		return fmt.Sprintf("has type %s, which names its own coefficients; only model_format_version 3 carries an extension or model regression's fit", f.Type)
	case f.ModelFormatVersion == FileFormatVersion && f.Dispersion == 0:
		return "is model_format_version 4 without a dispersion"
	case f.ModelFormatVersion == FileFormatVersion && f.Vcov == nil:
		return "is model_format_version 4 without a vcov (the dispersion rides a classical vcov)"
	case f.ModelFormatVersion != FileFormatVersion && f.Dispersion != 0:
		return fmt.Sprintf("carries a dispersion, which model_format_version %d does not have", f.ModelFormatVersion)
	case f.ModelFormatVersion == FileFormatVersion && f.Covariance != nil:
		return "carries a covariance, which model_format_version 4 does not have"
	}
	if c := f.Covariance; c != nil {
		robust := &types.RegressionCovariance{Type: c.Type}
		if !slices.Contains(types.AllCovarianceTypes(), c.Type) || !robust.Robust() {
			return fmt.Sprintf("covariance type %q is not a robust estimator", c.Type)
		}
	}
	// An extension regression's coefficients are named by its model
	// (version 3): no intercept, nor one per predictor, is required.
	builtin := f.ModelFormatVersion != fileFormatV3
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
// saved figures only (no R² or deviance).
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
	return Bind(name, spec, f.Result())
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
