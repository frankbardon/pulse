package synth

import (
	"bytes"
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// VectorFieldFidelity is one row of FidelityReport.VectorFields: a
// vector field's per-element marginal comparison and its inter-element
// correlation comparison between the source (_synthetic=false) and
// synthetic (_synthetic=true) partitions of the output cohort.
//
// A vector is not driven through TEST_KS the way a scalar is — the
// question is the JOINT structure, and a per-element KS would say
// nothing about it — so this section compares moments, NaN rates and
// the correlation matrix directly, with the same pairwise-complete
// co-moment accumulator profile capture uses (coMoments), so both
// partitions and the captured profile are measured by one definition.
type VectorFieldFidelity struct {
	Field string `json:"field"`
	// SourceN / SyntheticN count each partition's NON-NULL vectors.
	SourceN    int `json:"source_n"`
	SyntheticN int `json:"synthetic_n"`
	// Elements is one entry per element, in element order.
	Elements []*VectorElementFidelity `json:"elements,omitempty"`
	// CorrelationPairs counts the element pairs measurable on BOTH
	// partitions; MeanCorrelationDelta / MaxCorrelationDelta summarise
	// abs(source rho − synthetic rho) over them, and MaxCorrelationPair
	// names the worst pair (1-based [a, b]).
	CorrelationPairs     int     `json:"correlation_pairs,omitempty"`
	MeanCorrelationDelta float64 `json:"mean_correlation_delta,omitempty"`
	MaxCorrelationDelta  float64 `json:"max_correlation_delta,omitempty"`
	MaxCorrelationPair   []int   `json:"max_correlation_pair,omitempty"`
	// CorrelationSkipped names why no correlation was compared (a dim
	// over maxVectorCorrelationDim), empty when it was.
	CorrelationSkipped string `json:"correlation_skipped,omitempty"`
	// Error is set when the synthetic partition has no non-null vector.
	Error string `json:"error,omitempty"`
}

// VectorElementFidelity is one element's marginal comparison. The
// synthetic figures and the deltas are meaningless when Error is set
// (the synthetic partition never carried a non-NaN value for it).
type VectorElementFidelity struct {
	Element          int     `json:"element"`
	Label            string  `json:"label,omitempty"`
	SourceMean       float64 `json:"source_mean"`
	SourceStd        float64 `json:"source_std"`
	SyntheticMean    float64 `json:"synthetic_mean,omitempty"`
	SyntheticStd     float64 `json:"synthetic_std,omitempty"`
	MeanDelta        float64 `json:"mean_delta,omitempty"`
	StdDelta         float64 `json:"std_delta,omitempty"`
	SourceNaNRate    float64 `json:"source_nan_rate"`
	SyntheticNaNRate float64 `json:"synthetic_nan_rate"`
	Error            string  `json:"error,omitempty"`
}

// computeVectorFieldFidelity compares field fi of mergedSchema across
// the two partitions in a single decode pass.
func computeVectorFieldFidelity(mergedSchema *encoding.Schema, records []byte, fi int) *VectorFieldFidelity {
	f := &mergedSchema.Fields[fi]
	meta, _ := mergedSchema.Vector(fi)
	entry := &VectorFieldFidelity{Field: f.Name}
	if meta.Dim == 0 {
		entry.Error = "vector field carries no dim in the output schema"
		return entry
	}
	// The two partitions reuse the profile accumulator verbatim; no
	// reservoir is kept, the report needs moments only.
	src, syn := newVecAcc(meta, false), newVecAcc(meta, false)

	rr := encx.NewRecordReader(bytes.NewReader(records), mergedSchema)
	values := make(map[string]float64, len(mergedSchema.Fields))
	nulls := make(map[string]bool, len(mergedSchema.Fields))
	wide := make(map[string]any, len(mergedSchema.Fields))
	for {
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			break
		}
		v, _ := wide[f.Name].([]float64)
		if values[SyntheticFieldName] != 0 {
			syn.observe(v, nulls[f.Name])
		} else {
			src.observe(v, nulls[f.Name])
		}
	}
	entry.SourceN, entry.SyntheticN = src.count, syn.count

	for k := 0; k < meta.Dim; k++ {
		el := &VectorElementFidelity{Element: k + 1}
		if k < len(meta.Labels) {
			el.Label = meta.Labels[k]
		}
		if src.count > 0 {
			el.SourceNaNRate = float64(src.nan[k]) / float64(src.count)
		}
		if np := numericProfileFrom(src.elems[k], false, false); np != nil {
			el.SourceMean, el.SourceStd = np.Mean, np.Std
		}
		if syn.count > 0 {
			el.SyntheticNaNRate = float64(syn.nan[k]) / float64(syn.count)
		}
		np := numericProfileFrom(syn.elems[k], false, false)
		if np == nil {
			el.Error = fmt.Sprintf("synthetic partition has no non-NaN observation of %s", vectorElementName(f.Name, k))
		} else {
			el.SyntheticMean, el.SyntheticStd = np.Mean, np.Std
			el.MeanDelta = math.Abs(el.SourceMean - el.SyntheticMean)
			el.StdDelta = math.Abs(el.SourceStd - el.SyntheticStd)
		}
		entry.Elements = append(entry.Elements, el)
	}
	if syn.count == 0 {
		entry.Error = fmt.Sprintf("synthetic partition has no non-null observations of %s", f.Name)
		return entry
	}
	if src.co == nil {
		entry.CorrelationSkipped = fmt.Sprintf("dim %d exceeds %d", meta.Dim, maxVectorCorrelationDim)
		return entry
	}
	sum := 0.0
	for i := 0; i < meta.Dim; i++ {
		for j := i + 1; j < meta.Dim; j++ {
			rs, _, okS := src.co.rho(i, j)
			rg, _, okG := syn.co.rho(i, j)
			if !okS || !okG {
				continue
			}
			d := math.Abs(rs - rg)
			entry.CorrelationPairs++
			sum += d
			if d > entry.MaxCorrelationDelta || entry.MaxCorrelationPair == nil {
				entry.MaxCorrelationDelta = d
				entry.MaxCorrelationPair = []int{i + 1, j + 1}
			}
		}
	}
	if entry.CorrelationPairs > 0 {
		entry.MeanCorrelationDelta = sum / float64(entry.CorrelationPairs)
	}
	return entry
}
