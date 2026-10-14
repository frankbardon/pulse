package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// population.go is the population file: the standalone, versioned JSON
// document a Compose vector fit's `save` writes (FitSpec.Save) and a
// later request loads by name (Request.Populations). It holds a
// VectorFit's complete stored state — member labels, weight basis,
// counts, Σw / Σw², the raw mean and the raw co-moment M2 — and nothing
// derived: every block a consumer reads (covariance, correlation,
// Cholesky factor, eigen-decomposition, loadings, rotation) is
// recomputed from these figures at score time, so one population
// serves every fitted vector operator and a loaded fit scores bit-for-
// bit like the fit it was saved from (on one architecture; a
// decomposition recomputed on another is tolerance-equal).
//
// The conventions are the model file's (file.go): the codec lives here
// in the leaf, so the runtime and the no-execute predict path parse ONE
// format; floats round-trip bit-exactly (Float: shortest form, NaN as
// null, ±Inf as "+Inf" / "-Inf"); the decode is version-first and
// strict; provenance is informational and never compared. The error
// family is the model file's too — PULSE_MODEL_SAVE_INVALID,
// _WRITE_FAILED, _OVERWRITTEN, _REF_INVALID, _FILE_UNREADABLE,
// _FILE_INVALID, _VERSION_UNSUPPORTED, _NAME_COLLISION — each refusal
// naming the population entry (details.population) or the fit
// (details.fit) it belongs to.

// PopulationSuffix is the population file name suffix every vector-fit
// save path and every `populations` entry carries. The label- and
// range-table directory loaders skip it (a population file beside a
// table is not a table).
const PopulationSuffix = ".pulse-population.json"

// PopulationFormatVersion is the newest population_format_version this
// build writes and reads.
const PopulationFormatVersion = 1

// PopulationFormatVersions are the population_format_version values
// this build reads, oldest first.
func PopulationFormatVersions() []int { return []int{PopulationFormatVersion} }

// PopulationFile is one population file document: a vector fit's
// stored state at full float64 precision, in axis (Labels) order, plus
// informational provenance.
type PopulationFile struct {
	// PopulationFormatVersion is the document version.
	PopulationFormatVersion int `json:"population_format_version"`
	// Name is the name the population was saved under (informational: a
	// load binds the name the loading request gives it).
	Name string `json:"name"`
	// Operator is the battery the fit was taken from — the fit
	// request's MAT_* type or fitted attribute type.
	Operator string `json:"operator,omitempty"`
	// Labels are the member labels in axis order: what a consuming slot
	// aligns to, by label. Members are the fit's member columns.
	Labels  []string `json:"labels"`
	Members []string `json:"members"`
	// Basis is the weight basis: unweighted, frequency or probability.
	Basis VectorBasis `json:"basis"`
	// N, NMass and NWeightInvalid are the fit's row counts
	// (VectorFit).
	N              int64 `json:"n"`
	NMass          int64 `json:"n_mass"`
	NWeightInvalid int64 `json:"n_weight_invalid"`
	// SumWeights and SumWeightsSq are Σw and Σw².
	SumWeights   Float `json:"sum_weights"`
	SumWeightsSq Float `json:"sum_weights_sq"`
	// Mean is the stored (weighted) mean; M2 the raw co-moment, a full
	// symmetric p × p matrix — both axis order.
	Mean []Float   `json:"mean"`
	M2   [][]Float `json:"m2"`
	// Provenance records where the fit came from. Informational only:
	// a load never compares it with anything.
	Provenance Provenance `json:"provenance"`
}

// NewPopulationFile builds the population file of a closed vector fit
// under name. A malformed fit (one the decode would refuse) is an
// engine invariant violation (PROCESSING_INTERNAL): it is never saved.
func NewPopulationFile(name string, v *VectorFit, prov Provenance) (*PopulationFile, error) {
	if v == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("vector fit %q has no state to save", name), map[string]any{"name": name})
	}
	f := &PopulationFile{
		PopulationFormatVersion: PopulationFormatVersion,
		Name:                    name,
		Operator:                v.Operator,
		Labels:                  nonNil(v.Labels),
		Members:                 nonNil(v.Members),
		Basis:                   v.Basis,
		N:                       v.N,
		NMass:                   v.NMass,
		NWeightInvalid:          v.NWeightInvalid,
		SumWeights:              Float(v.SumWeights),
		SumWeightsSq:            Float(v.SumWeightsSq),
		Mean:                    toFloatSlice(v.Mean),
		M2:                      make([][]Float, len(v.M2)),
		Provenance:              prov,
	}
	for i, row := range v.M2 {
		f.M2[i] = toFloatSlice(row)
	}
	if what := f.check(); what != "" {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("vector fit %q cannot be saved: it %s", name, what), map[string]any{"name": name})
	}
	return f, nil
}

// Encode writes f as the population file's bytes: indented JSON with a
// trailing newline (deterministic: no maps).
func (f *PopulationFile) Encode() ([]byte, error) {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_INTERNAL, "encoding population file")
	}
	return append(b, '\n'), nil
}

// DecodePopulationFile parses a population file — DecodeFile's rule
// set. The version is read first: absent is PULSE_MODEL_FILE_INVALID
// reason version_missing, not an integer reason schema, any value
// outside PopulationFormatVersions PULSE_MODEL_VERSION_UNSUPPORTED. The
// document is then decoded strictly — an unknown key, a wrong type or a
// structural fault (no labels, a blank or repeated label, members
// unlike the labels, an unknown basis, a negative count, n_mass above
// n, a weight sum that is not finite and non-negative, a mean or m2
// whose shape is not the labels', an m2 that is not symmetric) is
// PULSE_MODEL_FILE_INVALID reason schema; malformed JSON reason json.
// source names the file in messages and details.path.
func DecodePopulationFile(source string, data []byte) (*PopulationFile, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, populationInvalid(source, ReasonFileJSON, err.Error())
	}
	raw, ok := probe["population_format_version"]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return nil, populationInvalid(source, ReasonFileVersionMissing, "has no population_format_version")
	}
	var version int
	if err := json.Unmarshal(raw, &version); err != nil {
		return nil, populationInvalid(source, ReasonFileSchema, "population_format_version is not an integer")
	}
	if !slices.Contains(PopulationFormatVersions(), version) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_VERSION_UNSUPPORTED,
			fmt.Sprintf("population file %s has population_format_version %d; this build reads version %d", source, version, PopulationFormatVersion),
			map[string]any{"path": source, "version": version, "supported": PopulationFormatVersions()})
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f PopulationFile
	if err := dec.Decode(&f); err != nil {
		return nil, populationInvalid(source, ReasonFileSchema, err.Error())
	}
	if what := f.check(); what != "" {
		return nil, populationInvalid(source, ReasonFileSchema, what)
	}
	return &f, nil
}

// check is the structural rule; "" when the document is whole. It is
// what the engine's rebuild (linalg.NewCoMomentFromListwise, the label
// alignment) needs to hold, judged at the file so a bad file is the
// file's refusal, never an engine fault mid-request.
func (f *PopulationFile) check() string {
	p := len(f.Labels)
	if p == 0 {
		return "has no labels"
	}
	for i, l := range f.Labels {
		if strings.TrimSpace(l) == "" {
			return fmt.Sprintf("has a blank label at %d", i)
		}
		if slices.Contains(f.Labels[:i], l) {
			return fmt.Sprintf("lists label %q twice", l)
		}
	}
	switch {
	case len(f.Members) != p:
		return fmt.Sprintf("has %d members for %d labels", len(f.Members), p)
	case !slices.Contains([]VectorBasis{VectorBasisUnweighted, VectorBasisFrequency, VectorBasisProbability}, f.Basis):
		return fmt.Sprintf("has basis %q; want unweighted, frequency or probability", f.Basis)
	case f.N < 0 || f.NMass < 0 || f.NWeightInvalid < 0:
		return "has a negative count"
	case f.NMass > f.N:
		return "has n_mass above n"
	case !finiteNonNegative(float64(f.SumWeights)) || !finiteNonNegative(float64(f.SumWeightsSq)):
		return "has a weight sum that is not finite and non-negative"
	case len(f.Mean) != p:
		return fmt.Sprintf("has %d means for %d labels", len(f.Mean), p)
	case len(f.M2) != p:
		return fmt.Sprintf("has %d m2 rows for %d labels", len(f.M2), p)
	}
	for i, row := range f.M2 {
		if len(row) != p {
			return fmt.Sprintf("has an m2 row %d of %d entries for %d labels", i, len(row), p)
		}
	}
	for i := range f.M2 {
		for j := i + 1; j < p; j++ {
			if math.Float64bits(float64(f.M2[i][j])) != math.Float64bits(float64(f.M2[j][i])) {
				return fmt.Sprintf("has an m2 that is not symmetric at (%d, %d)", i, j)
			}
		}
	}
	return ""
}

// VectorFit rebuilds the saved fit under name: every stored figure
// copied, none recomputed, so a slot binding it scores exactly as one
// binding the fit it was saved from.
func (f *PopulationFile) VectorFit(name string) *VectorFit {
	v := &VectorFit{
		Name:           name,
		Labels:         append([]string(nil), f.Labels...),
		Members:        append([]string(nil), f.Members...),
		Basis:          f.Basis,
		N:              f.N,
		NMass:          f.NMass,
		NWeightInvalid: f.NWeightInvalid,
		SumWeights:     float64(f.SumWeights),
		SumWeightsSq:   float64(f.SumWeightsSq),
		Mean:           fromFloatSlice(f.Mean),
		M2:             make([][]float64, len(f.M2)),
		Operator:       f.Operator,
	}
	for i, row := range f.M2 {
		v.M2[i] = fromFloatSlice(row)
	}
	return v
}

// CheckPopulationSavePath is the rule over a vector fit's `save` path —
// CheckSavePath's, with PopulationSuffix: non-blank, a file name ending
// .pulse-population.json with a stem before it, never above the
// filesystem root. A violation is PULSE_MODEL_SAVE_INVALID with
// details.path and details.reason.
func CheckPopulationSavePath(p string) *errors.CodedError {
	if reason, what := checkFilePath(p, PopulationSuffix); reason != "" {
		return saveInvalid(p, reason, what)
	}
	return nil
}

// PopulationOverwritten is the PULSE_MODEL_OVERWRITTEN warning of a
// vector fit's save that replaced an existing file at path.
func PopulationOverwritten(p, name string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_OVERWRITTEN,
		fmt.Sprintf("population %q overwrote the existing population file %s", name, p),
		map[string]any{"path": p, "name": name})
}

// PopulationWriteFailed is the PULSE_MODEL_WRITE_FAILED refusal of a
// vector fit's save of population name to path, failed at stage. The
// target is left as it was.
func PopulationWriteFailed(p, name, stage string, cause error) *errors.CodedError {
	msg := fmt.Sprintf("saving population %q to %s failed at %s", name, p, stage)
	if cause != nil {
		msg += ": " + cause.Error()
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_WRITE_FAILED, msg,
		map[string]any{"path": p, "name": name, "stage": stage})
}

func populationInvalid(source, reason, what string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_FILE_INVALID,
		fmt.Sprintf("population file %s is not a valid population file: %s", source, what),
		map[string]any{"path": source, "reason": reason})
}

func finiteNonNegative(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

func toFloatSlice(s []float64) []Float {
	out := make([]Float, len(s))
	for i, v := range s {
		out[i] = Float(v)
	}
	return out
}

func fromFloatSlice(s []Float) []float64 {
	out := make([]float64, len(s))
	for i, v := range s {
		out[i] = float64(v)
	}
	return out
}
