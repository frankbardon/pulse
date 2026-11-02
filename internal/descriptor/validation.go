package descriptor

import (
	_ "embed"
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
)

// validation.go is the read side of the validation registry
// (validation_registry.go): the raw record for docgen, and the
// instance's view of one operator for Explain's operator mode. Both
// render from the same record and the same StRD file, so the book and
// the engine surface cannot disagree.

// strdLRE is the NIST StRD record: a byte-identical copy of the root
// package's testdata/strd/lre.json, written by
// TestStRD_DigitsMatchRecord -update and held equal to it by
// TestStRD_DocgenCopyCurrent.
//
//go:embed strd_lre.json
var strdLRE []byte

// StRDResult is one dataset row of the StRD record. Dataset is
// "<family>/<set>" (lls, anova, univ).
type StRDResult struct {
	Dataset    string             `json:"dataset"`
	Difficulty string             `json:"difficulty"`
	Digits     map[string]float64 `json:"digits"`
	Min        *float64           `json:"min_digits"`
	NotOffered string             `json:"not_offered"`
}

// strdResults parses the embedded record once.
var strdResults = func() []StRDResult {
	s := string(strdLRE)
	if i := strings.LastIndex(s, "\n// golden-hash: "); i >= 0 {
		s = s[:i]
	}
	var doc struct {
		Results []StRDResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		panic("descriptor: strd_lre.json: " + err.Error())
	}
	return doc.Results
}()

// StRDResults returns the StRD record's rows in file order, as fresh
// copies.
func StRDResults() []StRDResult {
	out := make([]StRDResult, len(strdResults))
	for i, r := range strdResults {
		r.Digits = cloneDigits(r.Digits)
		if r.Min != nil {
			m := *r.Min
			r.Min = &m
		}
		out[i] = r
	}
	return out
}

func cloneDigits(m map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// strdFamilies maps a StRD family key to its title and the operators
// it checks, in the order the book lists them. figures, when set,
// restricts an operator to the figures it produces: the univariate
// suite certifies the mean (AGG_AVERAGE) and the sample standard
// deviation on one row, sd from AGG_STDDEV and welford_sd from
// AGG_WELFORD's stddev component (they differ on the offset sets:
// AGG_WELFORD accumulates about its first value).
var strdFamilies = map[string]struct {
	title   string
	ops     []string
	figures map[string][]string // operator → figure keys; nil = every figure
}{
	"lls":   {title: "linear regression", ops: []string{"REG_OLS"}},
	"anova": {title: "one-way ANOVA", ops: []string{"TEST_ANOVA_F"}},
	"univ": {title: "univariate summary statistics", ops: []string{"AGG_AVERAGE", "AGG_STDDEV", "AGG_WELFORD"},
		figures: map[string][]string{"AGG_AVERAGE": {"mean"}, "AGG_STDDEV": {"sd"}, "AGG_WELFORD": {"welford_sd"}}},
}

// StRDFamily returns the title of a StRD family key and the operators
// it checks; ok is false for an unknown key.
func StRDFamily(key string) (title string, ops []string, ok bool) {
	f, ok := strdFamilies[key]
	if !ok {
		return "", nil, false
	}
	return f.title, slices.Clone(f.ops), true
}

// ValidationRecordOf returns the registry record of a built-in operator
// as written (weighted checks included), a deep copy; ok is false for
// an operator the registry does not cover (an extension, or a
// descriptive operator outside its scope).
func ValidationRecordOf(name string) (ValidationRecord, bool) {
	v, ok := validationRegistry[name]
	if !ok {
		return ValidationRecord{}, false
	}
	out := ValidationRecord{Gap: v.Gap, PostTestOnly: v.PostTestOnly, WeightRefused: slices.Clone(v.WeightRefused)}
	for _, r := range v.Refs {
		r.Tests = slices.Clone(r.Tests)
		out.Refs = append(out.Refs, r)
	}
	return out, true
}

// ValidationOperators returns every operator the registry covers,
// sorted.
func ValidationOperators() []string {
	out := make([]string, 0, len(validationRegistry))
	for k := range validationRegistry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Validation returns name's validation evidence as this instance shows
// it: weighted references and the weight refusal are dropped when the
// instance does not offer weighting, and the refusal's reason is the
// one the engine's own refusal carries (OperatorWeightRefusal, or the
// shared post-test reason). Every prose string passes the instance's
// ProseScrub: a reference whose description names only what the
// instance hides (a capability it does not offer) is dropped, and a
// gap loses the sentences that name a hidden token. ok is false when
// the registry holds no record for name. The caller decides whether
// the instance offers name at all.
func (s *InstanceSnapshot) Validation(name string) (descriptor.OperatorValidation, bool) {
	v, ok := validationRegistry[name]
	if !ok {
		return descriptor.OperatorValidation{}, false
	}
	weightingOn := s.Enabled(FeatureWeighting)
	scrub := NewProseScrub(s)
	out := descriptor.OperatorValidation{References: []descriptor.ValidationReference{}, Gap: strings.TrimSpace(scrub.Text(v.Gap))}
	for _, r := range v.Refs {
		if r.Weighted && !weightingOn {
			continue
		}
		against := strings.TrimSpace(scrub.Text(r.Against))
		if against == "" {
			continue
		}
		out.References = append(out.References, descriptor.ValidationReference{
			Against: against, Tolerance: r.Tol, Tests: slices.Clone(r.Tests), Weighted: r.Weighted,
		})
	}
	if weightingOn && len(v.WeightRefused) > 0 {
		out.WeightRefusal = &descriptor.ValidationWeightRefusal{
			Code:   weightRefusalCode,
			Reason: ValidationWeightRefusalReason(name, v.PostTestOnly),
			Tests:  slices.Clone(v.WeightRefused),
		}
	}
	out.Certification = strdCertification(name)
	return out, true
}

// weightRefusalCode is the code a pinned weight refusal is raised with.
const weightRefusalCode = "PULSE_WEIGHT_UNSUPPORTED"

// ValidationWeightRefusalReason is the reason a pinned weight refusal
// states: the shared post-test reason for a post-test-only operator,
// else the operator's own permanent reason.
func ValidationWeightRefusalReason(name string, postTestOnly bool) string {
	if postTestOnly {
		return PostTestWeightRefusal()
	}
	reason, _ := OperatorWeightRefusal(name)
	return reason
}

// strdCertification returns the StRD rows checking name, each holding
// only the figures name produces; nil when no StRD family checks it.
func strdCertification(name string) []descriptor.ValidationCertification {
	var out []descriptor.ValidationCertification
	for _, r := range strdResults {
		key, set, _ := strings.Cut(r.Dataset, "/")
		fam, ok := strdFamilies[key]
		if !ok || !slices.Contains(fam.ops, name) || len(r.Digits) == 0 {
			continue
		}
		c := descriptor.ValidationCertification{Family: fam.title, Dataset: set, Difficulty: r.Difficulty, Digits: map[string]float64{}}
		keep := fam.figures[name]
		first := true
		for k, d := range r.Digits {
			if keep != nil && !slices.Contains(keep, k) {
				continue
			}
			c.Digits[k] = d
			if first || d < c.MinDigits {
				c.MinDigits = d
			}
			first = false
		}
		if len(c.Digits) == 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}
