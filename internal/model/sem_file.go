package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// sem_file.go is the SEM fit file: the standalone, versioned JSON
// document a Compose SEM fit's `save` writes (FitSpec.Save on a fit
// whose request holds one MAT_SEM matrix) and a later request loads by
// name through its `models` list — the .pulse-sem.json suffix
// dispatches the kind (a `models` entry ending .pulse-model.json loads
// a regression model file). It holds a SEMFit's complete stored state —
// the model description and options, the member labels, the parsed
// model's variable orders, the solver stop and, per group, its level,
// counts and parameter matrices at the estimate — and nothing derived:
// the engine re-specifies the model from the description and builds
// each group's scorer from the stored matrices by the one arithmetic an
// in-compose bound fit uses, so a loaded fit scores bit-for-bit like
// the fit it was saved from.
//
// The conventions are the model file's (file.go) and the population
// file's (population.go): the codec lives here in the leaf, so the
// runtime and the no-execute predict path parse ONE format; floats
// round-trip bit-exactly (Float); the decode is version-first and
// strict; provenance is informational and never compared; the error
// family is PULSE_MODEL_SAVE_INVALID, _WRITE_FAILED, _OVERWRITTEN,
// _REF_INVALID, _FILE_UNREADABLE, _FILE_INVALID, _VERSION_UNSUPPORTED.

// SEMSuffix is the SEM fit file name suffix every SEM fit save path
// carries and a `models` entry loading one ends in. The label-, range-
// and population-directory loaders skip it (a SEM fit file beside a
// table is not a table).
const SEMSuffix = ".pulse-sem.json"

// SEMFormatVersion is the newest sem_format_version this build writes
// and reads. The version is a function of the content, never a choice:
// a fit grouped by a column its request computed (group_derived,
// group_levels) writes SEMFormatVersionDerivedGroup, else an ordinal
// fit (ordered, categories, per-group thresholds)
// SEMFormatVersionOrdinal, every other fit SEMFormatVersionML — byte
// for byte the document a build before the ordinal fields wrote — so a
// version-1 or -2 file stays readable forever and an older build
// refuses a newer file as an unsupported version rather than misreading
// it (a version-2 reader would take a derived group for a cohort field).
const SEMFormatVersion = SEMFormatVersionDerivedGroup

// The sem_format_version values: 1, a maximum-likelihood fit; 2, an
// ordinal (DWLS / WLSMV) fit; 3, a fit grouped by a computed column
// (either estimator: the ordinal fields optional).
const (
	SEMFormatVersionML           = 1
	SEMFormatVersionOrdinal      = 2
	SEMFormatVersionDerivedGroup = 3
)

// SEMFormatVersions are the sem_format_version values this build
// reads, oldest first.
func SEMFormatVersions() []int {
	return []int{SEMFormatVersionML, SEMFormatVersionOrdinal, SEMFormatVersionDerivedGroup}
}

// The SEMFile conventions spellings.
const (
	semConventionsLavaan = "lavaan"
	semConventionsSemopy = "semopy"
)

// SEMFile is one SEM fit file document: a SEMFit's stored state at
// full float64 precision plus informational provenance. No maps, so one
// fit encodes to one byte sequence.
type SEMFile struct {
	// SEMFormatVersion is the document version.
	SEMFormatVersion int `json:"sem_format_version"`
	// Name is the name the fit was saved under (informational: a load
	// binds the name the loading request gives it).
	Name string `json:"name"`
	// Operator is the battery the fit was taken from (MAT_SEM).
	Operator string `json:"operator"`
	// Model is the model description fitted.
	Model string `json:"model"`
	// Labels are the member labels in member order (what a consuming
	// slot aligns to, by label); Members the member columns.
	Labels  []string `json:"labels"`
	Members []string `json:"members"`
	// Observed and Inner are the parsed model's variable orders (the
	// rows and columns of every stored matrix); Latents the latent
	// variables in Inner order.
	Observed []string `json:"observed"`
	Inner    []string `json:"inner"`
	Latents  []string `json:"latents"`
	// Estimator is the fit's estimator; Conventions "lavaan" or
	// "semopy".
	Estimator   string `json:"estimator"`
	Conventions string `json:"conventions"`
	// Ordered are an ordinal fit's ordered variables (Observed order)
	// and Categories[k] the k-th one's category values ascending — a
	// version-2 file only, absent from a version-1 file.
	Ordered    []string  `json:"ordered,omitempty"`
	Categories [][]Float `json:"categories,omitempty"`
	// MeanStructure, Group, GroupReference, GroupEqual, GroupPartial,
	// Solver and Information are the fit's options.
	MeanStructure  bool     `json:"meanstructure"`
	Group          string   `json:"group,omitempty"`
	GroupReference string   `json:"group_reference,omitempty"`
	GroupEqual     []string `json:"group_equal,omitempty"`
	GroupPartial   []string `json:"group_partial,omitempty"`
	Solver         string   `json:"solver"`
	Information    string   `json:"information"`
	// GroupDerived reports a group the fit request computed and
	// GroupLevels its declared levels in order (absent: its observed
	// integral values) — a version-3 file only.
	GroupDerived bool                `json:"group_derived,omitempty"`
	GroupLevels  []SEMFileGroupLevel `json:"group_levels,omitempty"`
	// Basis is the weight basis (unweighted or frequency).
	Basis VectorBasis `json:"basis"`
	// N and SumWeights are the fit's listwise-complete rows and Σw.
	N          int64 `json:"n"`
	SumWeights Float `json:"sum_weights"`
	// DoF, NParams, Converged, Status and Iterations are the fit's
	// degrees of freedom, free parameter count and solver stop.
	DoF        Float  `json:"dof"`
	NParams    int    `json:"n_params"`
	Converged  bool   `json:"converged"`
	Status     string `json:"status"`
	Iterations int    `json:"iterations"`
	// NearestPDMinEigenvalue is set when the fit used the nearest
	// positive-definite substitute: the minimum eigenvalue it replaced.
	NearestPDMinEigenvalue *Float `json:"nearest_pd_min_eigenvalue,omitempty"`
	// Groups are the fit's groups in level order (one, level "",
	// ungrouped).
	Groups []SEMFileGroup `json:"groups"`
	// Provenance records where the fit came from. Informational only.
	Provenance Provenance `json:"provenance"`
}

// SEMFileGroupLevel is one declared level of a derived group
// (SEMGroupLevel).
type SEMFileGroupLevel struct {
	Value Float  `json:"value"`
	Label string `json:"label"`
}

// SEMFileGroup is one group's stored parameters (SEMGroup).
type SEMFileGroup struct {
	Level      string    `json:"level"`
	N          int64     `json:"n"`
	SumWeights Float     `json:"sum_weights"`
	Lambda     [][]Float `json:"lambda"`
	Beta       [][]Float `json:"beta"`
	Psi        [][]Float `json:"psi"`
	Theta      [][]Float `json:"theta"`
	Nu         []Float   `json:"nu"`
	Alpha      []Float   `json:"alpha"`
	SampleMean []Float   `json:"sample_mean"`
	// Thresholds are the group's raw thresholds per ordered variable (a
	// version-2 file only).
	Thresholds [][]Float `json:"thresholds,omitempty"`
}

// NewSEMFile builds the SEM fit file of a bound SEM fit under name. A
// malformed fit (one the decode would refuse) is an engine invariant
// violation (PROCESSING_INTERNAL): it is never saved.
func NewSEMFile(name string, f *SEMFit, prov Provenance) (*SEMFile, error) {
	if f == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("SEM fit %q has no state to save", name), map[string]any{"name": name})
	}
	version := SEMFormatVersionML
	switch {
	case f.GroupDerived:
		version = SEMFormatVersionDerivedGroup
	case f.Ordinal():
		version = SEMFormatVersionOrdinal
	}
	out := &SEMFile{
		SEMFormatVersion: version,
		Name:             name,
		Operator:         f.Operator,
		Model:            f.Model,
		Labels:           nonNil(f.Labels),
		Members:          nonNil(f.Members),
		Observed:         nonNil(f.Observed),
		Inner:            nonNil(f.Inner),
		Latents:          nonNil(f.Latents),
		Estimator:        f.Estimator,
		Conventions:      f.Conventions(),
		Ordered:          cloneStrings(f.Ordered),
		MeanStructure:    f.MeanStructure,
		Group:            f.Group,
		GroupReference:   f.GroupReference,
		GroupEqual:       cloneStrings(f.GroupEqual),
		GroupPartial:     cloneStrings(f.GroupPartial),
		Solver:           f.Solver,
		Information:      f.Information,
		GroupDerived:     f.GroupDerived,
		Basis:            f.Basis,
		N:                f.N,
		SumWeights:       Float(f.SumWeights),
		DoF:              Float(f.DoF),
		NParams:          f.NParams,
		Converged:        f.Converged,
		Status:           f.Status,
		Iterations:       f.Iterations,
		Groups:           make([]SEMFileGroup, len(f.Groups)),
		Provenance:       prov,
	}
	if f.NearestPD {
		v := Float(f.MinEigenvalue)
		out.NearestPDMinEigenvalue = &v
	}
	if f.Ordinal() {
		out.Categories = toFloatRows(f.Categories)
	}
	for _, l := range f.GroupLevels {
		out.GroupLevels = append(out.GroupLevels, SEMFileGroupLevel{Value: Float(l.Value), Label: l.Label})
	}
	for g, gr := range f.Groups {
		out.Groups[g] = SEMFileGroup{
			Level: gr.Level, N: gr.N, SumWeights: Float(gr.SumWeights),
			Lambda: toFloatRows(gr.Lambda), Beta: toFloatRows(gr.Beta), Psi: toFloatRows(gr.Psi), Theta: toFloatRows(gr.Theta),
			Nu: toFloatSlice(gr.Nu), Alpha: toFloatSlice(gr.Alpha), SampleMean: toFloatSlice(gr.SampleMean),
		}
		if f.Ordinal() {
			out.Groups[g].Thresholds = toFloatRows(gr.Thresholds)
		}
	}
	if what := out.check(); what != "" {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			fmt.Sprintf("SEM fit %q cannot be saved: it %s", name, what), map[string]any{"name": name})
	}
	return out, nil
}

// Encode writes f as the SEM fit file's bytes: indented JSON with a
// trailing newline (deterministic: no maps).
func (f *SEMFile) Encode() ([]byte, error) {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_INTERNAL, "encoding SEM fit file")
	}
	return append(b, '\n'), nil
}

// DecodeSEMFile parses a SEM fit file — DecodeFile's rule set. The
// version is read first: absent is PULSE_MODEL_FILE_INVALID reason
// version_missing, not an integer reason schema, any value outside
// SEMFormatVersions PULSE_MODEL_VERSION_UNSUPPORTED. The document is
// then decoded strictly — an unknown key, a wrong type or a structural
// fault (check) is PULSE_MODEL_FILE_INVALID reason schema; malformed
// JSON reason json. source names the file in messages and details.path.
func DecodeSEMFile(source string, data []byte) (*SEMFile, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, semFileInvalid(source, ReasonFileJSON, err.Error())
	}
	raw, ok := probe["sem_format_version"]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return nil, semFileInvalid(source, ReasonFileVersionMissing, "has no sem_format_version")
	}
	var version int
	if err := json.Unmarshal(raw, &version); err != nil {
		return nil, semFileInvalid(source, ReasonFileSchema, "sem_format_version is not an integer")
	}
	if !slices.Contains(SEMFormatVersions(), version) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_VERSION_UNSUPPORTED,
			fmt.Sprintf("SEM fit file %s has sem_format_version %d; this build reads version %d", source, version, SEMFormatVersion),
			map[string]any{"path": source, "version": version, "supported": SEMFormatVersions()})
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f SEMFile
	if err := dec.Decode(&f); err != nil {
		return nil, semFileInvalid(source, ReasonFileSchema, err.Error())
	}
	if what := f.check(); what != "" {
		return nil, semFileInvalid(source, ReasonFileSchema, what)
	}
	return &f, nil
}

// check is the structural rule; "" when the document is whole: what the
// engine's rebuild (the label alignment, the per-group scorer over
// Observed × Inner matrices, the level match) needs to hold, judged at
// the file so a bad file is the file's refusal, never an engine fault
// mid-request. Whether the model description re-specifies Observed /
// Inner / Latents is the engine's (CheckLoadedSEM), run at load by the
// runtime and predict alike.
func (f *SEMFile) check() string {
	if strings.TrimSpace(f.Model) == "" {
		return "has no model"
	}
	if what := distinctNames("label", f.Labels); what != "" {
		return what
	}
	if len(f.Members) != len(f.Labels) {
		return fmt.Sprintf("has %d members for %d labels", len(f.Members), len(f.Labels))
	}
	if what := distinctNames("observed variable", f.Observed); what != "" {
		return what
	}
	if what := distinctNames("inner variable", f.Inner); what != "" {
		return what
	}
	for _, l := range f.Latents {
		if !slices.Contains(f.Inner, l) {
			return fmt.Sprintf("lists latent %q outside inner", l)
		}
	}
	switch {
	case f.Conventions != semConventionsLavaan && f.Conventions != semConventionsSemopy:
		return fmt.Sprintf("has conventions %q; want lavaan or semopy", f.Conventions)
	case f.Basis != VectorBasisUnweighted && f.Basis != VectorBasisFrequency:
		return fmt.Sprintf("has basis %q; want unweighted or frequency", f.Basis)
	case f.N < 0 || f.NParams < 0 || f.Iterations < 0:
		return "has a negative count"
	case !finiteNonNegative(float64(f.SumWeights)):
		return "has a weight sum that is not finite and non-negative"
	case len(f.Groups) == 0:
		return "has no groups"
	case f.Group == "" && (len(f.Groups) != 1 || slices.ContainsFunc(f.Groups, func(g SEMFileGroup) bool { return g.Level != "" })):
		return "is ungrouped but does not hold exactly one group of level \"\""
	}
	nObs, nIn := len(f.Observed), len(f.Inner)
	for g, gr := range f.Groups {
		if f.Group != "" && slices.ContainsFunc(f.Groups[:g], func(o SEMFileGroup) bool { return o.Level == gr.Level }) {
			return fmt.Sprintf("lists group level %q twice", gr.Level)
		}
		if gr.N < 0 || !finiteNonNegative(float64(gr.SumWeights)) {
			return fmt.Sprintf("has a negative count or weight sum in group %d", g)
		}
		for _, m := range []struct {
			name string
			rows [][]Float
			r, c int
		}{{"lambda", gr.Lambda, nObs, nIn}, {"beta", gr.Beta, nIn, nIn}, {"psi", gr.Psi, nIn, nIn}, {"theta", gr.Theta, nObs, nObs}} {
			if len(m.rows) != m.r {
				return fmt.Sprintf("has a group %d %s of %d rows; the model needs %d", g, m.name, len(m.rows), m.r)
			}
			for _, row := range m.rows {
				if len(row) != m.c {
					return fmt.Sprintf("has a group %d %s row of %d entries; the model needs %d", g, m.name, len(row), m.c)
				}
			}
		}
		if len(gr.Nu) != nObs || len(gr.Alpha) != nIn || len(gr.SampleMean) != nObs {
			return fmt.Sprintf("has a group %d nu, alpha or sample_mean unlike the model's %d observed and %d inner variables", g, nObs, nIn)
		}
	}
	if what := f.checkDerived(); what != "" {
		return what
	}
	return f.checkOrdinal()
}

// semGroupValueMax bounds a derived group's level values (exact
// integers in a float64): vectors.SEMGroupValueMax.
const semGroupValueMax = 1 << 53

// checkDerived is the rule over the derived-group fields: present
// exactly on a version-3 file, which is a grouped fit; declared levels
// (when present) two at least, each an integral value |v| ≤ 2^53 with a
// non-blank label, no value and no label twice, and every group's level
// one of the labels — undeclared, every group's level an integer's
// canonical text (a scoring row's computed value matches it).
func (f *SEMFile) checkDerived() string {
	if f.SEMFormatVersion != SEMFormatVersionDerivedGroup {
		if f.GroupDerived || f.GroupLevels != nil {
			return fmt.Sprintf("has group_derived or group_levels under sem_format_version %d; a fit grouped by a computed column is version %d", f.SEMFormatVersion, SEMFormatVersionDerivedGroup)
		}
		return ""
	}
	if !f.GroupDerived || f.Group == "" {
		return fmt.Sprintf("is sem_format_version %d but no fit grouped by a computed column (group and group_derived)", SEMFormatVersionDerivedGroup)
	}
	if f.GroupLevels == nil {
		for _, g := range f.Groups {
			v, err := strconv.ParseInt(g.Level, 10, 64)
			if err != nil || strconv.FormatInt(v, 10) != g.Level || v > semGroupValueMax || v < -semGroupValueMax {
				return fmt.Sprintf("has group level %q, which is no integer level of computed group %q (declare named levels in group_levels)", g.Level, f.Group)
			}
		}
		return ""
	}
	if len(f.GroupLevels) < 2 {
		return "declares fewer than two group_levels"
	}
	labels := make([]string, len(f.GroupLevels))
	for i, l := range f.GroupLevels {
		v := float64(l.Value)
		if v != math.Trunc(v) || math.Abs(v) > semGroupValueMax {
			return fmt.Sprintf("has group_levels[%d] value %v, which is not an integer of magnitude at most 2^53", i, v)
		}
		if slices.ContainsFunc(f.GroupLevels[:i], func(o SEMFileGroupLevel) bool { return float64(o.Value) == v }) {
			return fmt.Sprintf("lists group_levels value %v twice", v)
		}
		labels[i] = l.Label
	}
	if what := distinctNames("group_levels label", labels); what != "" {
		return what
	}
	for _, g := range f.Groups {
		if !slices.Contains(labels, g.Level) {
			return fmt.Sprintf("has group level %q, which is none of its group_levels", g.Level)
		}
	}
	return ""
}

// checkOrdinal is the rule over the ordinal fields: present exactly on
// a version-2 file, the ordered variables distinct observed variables
// in Observed order, each with two or more ascending finite category
// values, and every group one finite threshold per category boundary.
func (f *SEMFile) checkOrdinal() string {
	// Version 2 is an ordinal fit; version 3 (a derived group) may be
	// one, signalled by its ordered variables.
	ordinal := f.SEMFormatVersion == SEMFormatVersionOrdinal || (f.SEMFormatVersion == SEMFormatVersionDerivedGroup && f.Ordered != nil)
	if !ordinal {
		if f.Ordered != nil || f.Categories != nil || slices.ContainsFunc(f.Groups, func(g SEMFileGroup) bool { return g.Thresholds != nil }) {
			return fmt.Sprintf("has ordered, categories or thresholds under sem_format_version %d; an ordinal fit is version %d (or %d, grouped by a computed column)", f.SEMFormatVersion, SEMFormatVersionOrdinal, SEMFormatVersionDerivedGroup)
		}
		return ""
	}
	if what := distinctNames("ordered variable", f.Ordered); what != "" {
		return what
	}
	last := -1
	for _, v := range f.Ordered {
		i := slices.Index(f.Observed, v)
		if i < 0 {
			return fmt.Sprintf("lists ordered variable %q outside observed", v)
		}
		if i < last {
			return "lists the ordered variables out of observed order"
		}
		last = i
	}
	if len(f.Categories) != len(f.Ordered) {
		return fmt.Sprintf("has %d category lists for %d ordered variables", len(f.Categories), len(f.Ordered))
	}
	for k, cats := range f.Categories {
		if len(cats) < 2 {
			return fmt.Sprintf("has fewer than two categories for ordered variable %q", f.Ordered[k])
		}
		for i, c := range cats {
			if !finite(float64(c)) || (i > 0 && !(c > cats[i-1])) {
				return fmt.Sprintf("has categories of ordered variable %q that are not finite and ascending", f.Ordered[k])
			}
		}
	}
	for g, gr := range f.Groups {
		if len(gr.Thresholds) != len(f.Ordered) {
			return fmt.Sprintf("has %d threshold lists in group %d for %d ordered variables", len(gr.Thresholds), g, len(f.Ordered))
		}
		for k, th := range gr.Thresholds {
			if len(th) != len(f.Categories[k])-1 {
				return fmt.Sprintf("has %d thresholds of ordered variable %q in group %d for its %d categories", len(th), f.Ordered[k], g, len(f.Categories[k]))
			}
			for _, t := range th {
				if !finite(float64(t)) {
					return fmt.Sprintf("has a non-finite threshold of ordered variable %q in group %d", f.Ordered[k], g)
				}
			}
		}
	}
	return ""
}

// distinctNames is the rule over a name list: non-empty, no blank, no
// repeat; "" when whole.
func distinctNames(what string, names []string) string {
	if len(names) == 0 {
		return fmt.Sprintf("has no %ss", what)
	}
	for i, n := range names {
		if strings.TrimSpace(n) == "" {
			return fmt.Sprintf("has a blank %s at %d", what, i)
		}
		if slices.Contains(names[:i], n) {
			return fmt.Sprintf("lists %s %q twice", what, n)
		}
	}
	return ""
}

// SEMFit rebuilds the saved fit under name: every stored figure copied,
// none recomputed, so a slot binding it scores exactly as one binding
// the fit it was saved from.
func (f *SEMFile) SEMFit(name string) *SEMFit {
	out := &SEMFit{
		Name:           name,
		Operator:       f.Operator,
		Model:          f.Model,
		Labels:         append([]string(nil), f.Labels...),
		Members:        append([]string(nil), f.Members...),
		Observed:       append([]string(nil), f.Observed...),
		Inner:          append([]string(nil), f.Inner...),
		Latents:        append([]string(nil), f.Latents...),
		Estimator:      f.Estimator,
		Lavaan:         f.Conventions == semConventionsLavaan,
		Ordered:        cloneStrings(f.Ordered),
		MeanStructure:  f.MeanStructure,
		Group:          f.Group,
		GroupReference: f.GroupReference,
		GroupEqual:     cloneStrings(f.GroupEqual),
		GroupPartial:   cloneStrings(f.GroupPartial),
		Solver:         f.Solver,
		Information:    f.Information,
		GroupDerived:   f.GroupDerived,
		Basis:          f.Basis,
		N:              f.N,
		SumWeights:     float64(f.SumWeights),
		DoF:            float64(f.DoF),
		NParams:        f.NParams,
		Converged:      f.Converged,
		Status:         f.Status,
		Iterations:     f.Iterations,
		Groups:         make([]SEMGroup, len(f.Groups)),
	}
	if f.NearestPDMinEigenvalue != nil {
		out.NearestPD, out.MinEigenvalue = true, float64(*f.NearestPDMinEigenvalue)
	}
	if f.Categories != nil {
		out.Categories = fromFloatRows(f.Categories)
	}
	for _, l := range f.GroupLevels {
		out.GroupLevels = append(out.GroupLevels, SEMGroupLevel{Value: float64(l.Value), Label: l.Label})
	}
	for g, gr := range f.Groups {
		out.Groups[g] = SEMGroup{
			Level: gr.Level, N: gr.N, SumWeights: float64(gr.SumWeights),
			Lambda: fromFloatRows(gr.Lambda), Beta: fromFloatRows(gr.Beta), Psi: fromFloatRows(gr.Psi), Theta: fromFloatRows(gr.Theta),
			Nu: fromFloatSlice(gr.Nu), Alpha: fromFloatSlice(gr.Alpha), SampleMean: fromFloatSlice(gr.SampleMean),
		}
		if gr.Thresholds != nil {
			out.Groups[g].Thresholds = fromFloatRows(gr.Thresholds)
		}
	}
	return out
}

// CheckSEMSavePath is the rule over a SEM fit's `save` path —
// CheckSavePath's, with SEMSuffix: non-blank, a file name ending
// .pulse-sem.json with a stem before it, never above the filesystem
// root. A violation is PULSE_MODEL_SAVE_INVALID with details.path and
// details.reason.
func CheckSEMSavePath(p string) *errors.CodedError {
	if reason, what := checkFilePath(p, SEMSuffix); reason != "" {
		return saveInvalid(p, reason, what)
	}
	return nil
}

// SEMOverwritten is the PULSE_MODEL_OVERWRITTEN warning of a SEM fit's
// save that replaced an existing file at path.
func SEMOverwritten(p, name string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_OVERWRITTEN,
		fmt.Sprintf("SEM fit %q overwrote the existing SEM fit file %s", name, p),
		map[string]any{"path": p, "name": name})
}

// SEMWriteFailed is the PULSE_MODEL_WRITE_FAILED refusal of a SEM fit's
// save of fit name to path, failed at stage. The target is left as it
// was.
func SEMWriteFailed(p, name, stage string, cause error) *errors.CodedError {
	msg := fmt.Sprintf("saving SEM fit %q to %s failed at %s", name, p, stage)
	if cause != nil {
		msg += ": " + cause.Error()
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_WRITE_FAILED, msg,
		map[string]any{"path": p, "name": name, "stage": stage})
}

// SEMRebuildFunc re-specifies a loaded SEM fit's model from its
// description and options and reports whether it reproduces the stored
// variable orders (nil when it does) — the engine's rebuild, handed in
// so this package stays a leaf.
type SEMRebuildFunc func(f *SEMFit) error

// CheckLoadedSEM refuses a loaded SEM fit file (LoadFiles' result)
// whose model description does not rebuild to its stored orders under
// rebuild: PULSE_MODEL_FILE_INVALID reason schema with details.model,
// name and path — nothing could score it. The runtime and predict run
// it right after the load (beside CheckLoadedTypes), so both refuse the
// same file the same way. Nil l or rebuild checks nothing.
func CheckLoadedSEM(refs Refs, l *Loaded, rebuild SEMRebuildFunc) *errors.CodedError {
	if l == nil || rebuild == nil {
		return nil
	}
	for j, f := range l.SEMs {
		if f == nil {
			continue
		}
		r := refs.Models[j]
		if err := rebuild(f.SEMFit(r.Name)); err != nil {
			return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_FILE_INVALID,
				fmt.Sprintf("models[%d]: SEM fit file %s is not a valid SEM fit file: its model does not rebuild: %v", j, r.Path, err),
				map[string]any{"model": j, "name": r.Name, "path": r.Path, "reason": ReasonFileSchema})
		}
	}
	return nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func semFileInvalid(source, reason, what string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_FILE_INVALID,
		fmt.Sprintf("SEM fit file %s is not a valid SEM fit file: %s", source, what),
		map[string]any{"path": source, "reason": reason})
}

func toFloatRows(m [][]float64) [][]Float {
	out := make([][]Float, len(m))
	for i, r := range m {
		out[i] = toFloatSlice(r)
	}
	return out
}

func fromFloatRows(m [][]Float) [][]float64 {
	out := make([][]float64, len(m))
	for i, r := range m {
		out[i] = fromFloatSlice(r)
	}
	return out
}

// isSEMPath reports whether p names a SEM fit file (its base ends in
// SEMSuffix) — the `models` entry's kind dispatch.
func isSEMPath(p string) bool {
	return strings.HasSuffix(filepath.ToSlash(p), SEMSuffix)
}
