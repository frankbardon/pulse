package synth

import (
	"bytes"
	"math"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// This file puts an EFFECT SIZE beside every marginal p-value of the
// fidelity report (FR-17).
//
// A p-value answers "is there a difference at all", and on a fidelity
// report that is the wrong question asked at the wrong sample size: the
// report compares two partitions of thousands of rows each, where a
// difference no analysis would ever notice still rejects at p < 0.001.
// Read alone, every column of a large twin looks broken. The effect size
// answers the question a reader actually has — "how big is it" — on a
// scale that does not grow with n, and its magnitude label says, in
// words, that a significant difference is a very small one.
//
// The measures and bands are the repo's own (internal/descriptor
// conventions.go, Cohen 1988): Cohen's d for a numeric field, read on
// cohen1988_d (.2 / .5 / .8), and Cramér's V for a categorical one, read
// on cohen1988_w (.1 / .3 / .5). V takes w's bands legitimately here
// and nowhere wider: the χ² test runs the field against the two-level
// `_synthetic` column, so min(r, c) − 1 = 1 and V equals w. The region
// below "small" is "very small", never "negligible" — below Cohen's
// small is not the same as unimportant. internal/descriptor cannot be
// imported from this package (it imports synth), so the thresholds are
// restated below and pinned against the registry by
// TestSynthFidelityEffectBandsMatchConventions.
//
// The section is built to EXTEND: marginalEffectSize is the one switch a
// field type's arm is added to, so a scoring arm brings its effect size
// with it. u4, date (epoch days) and datetime (epoch seconds) take d;
// packed_bool takes V from its 2x2 χ²; a set option's V is computed
// directly (setOptionEffectSize, internal/synth/fidelity_marginal.go).

// Effect-size measures FieldEffectSize.Measure carries.
const (
	// EffectMeasureCohensD is the standardised mean difference
	// (synthetic − source) / pooled SD of a numeric field.
	EffectMeasureCohensD = "cohens_d"
	// EffectMeasureCramersV is Cramér's V of a categorical field against
	// the `_synthetic` partition, read from the χ² test's own details.
	EffectMeasureCramersV = "cramers_v"
)

// Convention IDs FieldEffectSize.Convention names: the effect-size
// convention whose bands label the value (internal/descriptor
// conventions.go).
const (
	effectConventionCohenD = "cohen1988_d"
	effectConventionCohenW = "cohen1988_w"
)

// effectBands are a convention's three benchmark thresholds and its four
// labels, lowest first.
type effectBands struct {
	convention string
	thresholds [3]float64
}

// effectLabels are Cohen's three benchmarks plus the region below
// "small" (descriptor's cohenLabels).
var effectLabels = [4]string{"very small", "small", "medium", "large"}

var (
	effectBandsD = effectBands{convention: effectConventionCohenD, thresholds: [3]float64{0.2, 0.5, 0.8}}
	effectBandsW = effectBands{convention: effectConventionCohenW, thresholds: [3]float64{0.1, 0.3, 0.5}}
)

// EffectBandThresholds returns the thresholds the report labels a
// convention's values with, and false for a convention it does not use.
// It exists for the parity gate against internal/descriptor's registry.
func EffectBandThresholds(convention string) ([]float64, []string, bool) {
	for _, b := range []effectBands{effectBandsD, effectBandsW} {
		if b.convention == convention {
			return b.thresholds[:], effectLabels[:], true
		}
	}
	return nil, nil, false
}

// label names |v|'s band.
func (b effectBands) label(v float64) string {
	a := math.Abs(v)
	i := 0
	for i < len(b.thresholds) && a >= b.thresholds[i] {
		i++
	}
	return effectLabels[i]
}

// FieldEffectSize is the effect size beside a FieldFidelity's p-value:
// how LARGE the source/synthetic difference is, on a scale that does
// not grow with the row count.
type FieldEffectSize struct {
	// Measure is EffectMeasureCohensD or EffectMeasureCramersV.
	Measure string `json:"measure"`
	// Value is the measure itself. Cohen's d is signed (synthetic minus
	// source, in pooled standard deviations); Cramér's V is in [0, 1].
	Value float64 `json:"value"`
	// Magnitude labels |Value| on Convention's bands: "very small",
	// "small", "medium" or "large". A rejected test with a "very small"
	// magnitude is a real but trivial difference — the large-n case.
	Magnitude string `json:"magnitude"`
	// Convention names the band set (cohen1988_d / cohen1988_w).
	Convention string `json:"convention"`
}

// marginalMoments are one numeric field's running moments in one
// partition, shifted by the first value seen so the sum of squares does
// not cancel catastrophically on a field far from zero.
type marginalMoments struct {
	n          int
	shift      float64
	sum, sumSq float64
}

func (m *marginalMoments) add(v float64) {
	if m.n == 0 {
		m.shift = v
	}
	d := v - m.shift
	m.n++
	m.sum += d
	m.sumSq += float64(d * d)
}

// meanVar returns the partition's mean and sample variance.
func (m *marginalMoments) meanVar() (mean, variance float64, ok bool) {
	if m.n < 2 {
		return 0, 0, false
	}
	n := float64(m.n)
	meanD := m.sum / n
	ss := m.sumSq - float64(m.sum*meanD)
	if ss < 0 {
		ss = 0
	}
	return m.shift + meanD, ss / (n - 1), true
}

// cohensD is (synthetic − source) / pooled SD; ok is false when either
// partition has fewer than two values or the pooled SD is zero.
func cohensD(src, syn *marginalMoments) (float64, bool) {
	mSrc, vSrc, ok1 := src.meanVar()
	mSyn, vSyn, ok2 := syn.meanVar()
	if !ok1 || !ok2 {
		return 0, false
	}
	nSrc, nSyn := float64(src.n), float64(syn.n)
	pooled := (float64((nSrc-1)*vSrc) + float64((nSyn-1)*vSyn)) / (nSrc + nSyn - 2)
	if !(pooled > 0) {
		return 0, false
	}
	d := (mSyn - mSrc) / math.Sqrt(pooled)
	if math.IsNaN(d) || math.IsInf(d, 0) {
		return 0, false
	}
	return d, true
}

// computeMarginalMoments decodes records once and accumulates every
// named field's moments per partition (index 0 source, 1 synthetic).
func computeMarginalMoments(mergedSchema *encoding.Schema, records []byte, fields []string) map[string]*[2]marginalMoments {
	out := make(map[string]*[2]marginalMoments, len(fields))
	for _, f := range fields {
		out[f] = &[2]marginalMoments{}
	}
	if len(fields) == 0 {
		return out
	}
	rr := encx.NewRecordReader(bytes.NewReader(records), mergedSchema)
	values := make(map[string]float64, len(mergedSchema.Fields))
	nulls := make(map[string]bool, len(mergedSchema.Fields))
	wide := make(map[string]any, len(mergedSchema.Fields))
	for {
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			break
		}
		part := 0
		if values[SyntheticFieldName] != 0 {
			part = 1
		}
		for _, f := range fields {
			if nulls[f] {
				continue
			}
			v := values[f]
			if math.IsNaN(v) {
				continue
			}
			out[f][part].add(v)
		}
	}
	return out
}

// marginalEffectSize is the per-type effect-size switch: the one place a
// new marginal scoring arm adds its measure. moments is nil for a field
// whose measure needs none.
func marginalEffectSize(f *encoding.Field, result *types.TestResult, moments *[2]marginalMoments) *FieldEffectSize {
	switch {
	case cohensDType(f.Type):
		if moments == nil {
			return nil
		}
		d, ok := cohensD(&moments[0], &moments[1])
		if !ok {
			return nil
		}
		return newFieldEffectSize(EffectMeasureCohensD, d, effectBandsD)
	case f.Type.IsCategorical() || f.Type == encoding.FieldTypePackedBool:
		v, ok := chiSqCramersV(result)
		if !ok {
			return nil
		}
		return newFieldEffectSize(EffectMeasureCramersV, v, effectBandsW)
	}
	return nil
}

func newFieldEffectSize(measure string, v float64, b effectBands) *FieldEffectSize {
	return &FieldEffectSize{Measure: measure, Value: v, Magnitude: b.label(v), Convention: b.convention}
}

// chiSqCramersV reads Cramér's V from a χ² test result's
// details.effect_size — the figure the same test computed beside its
// own p-value, never a second implementation.
func chiSqCramersV(result *types.TestResult) (float64, bool) {
	if result == nil || result.Details == nil {
		return 0, false
	}
	es, _ := result.Details["effect_size"].(map[string]any)
	v, ok := es[EffectMeasureCramersV].(float64)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// attachMarginalEffectSizes sets FieldFidelity.EffectSize on every entry
// whose test produced a result, decoding records at most once.
func attachMarginalEffectSizes(report *FidelityReport, mergedSchema *encoding.Schema, records []byte) {
	var numeric []string
	for _, e := range report.Fields {
		if e.Result == nil {
			continue
		}
		if f := mergedSchema.Field(e.Field); f != nil && cohensDType(f.Type) {
			numeric = append(numeric, e.Field)
		}
	}
	moments := computeMarginalMoments(mergedSchema, records, numeric)
	for _, e := range report.Fields {
		if e.Result == nil {
			continue
		}
		f := mergedSchema.Field(e.Field)
		if f == nil {
			continue
		}
		e.EffectSize = marginalEffectSize(f, e.Result, moments[e.Field])
	}
}
