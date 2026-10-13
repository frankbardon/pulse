package synth

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
)

// maxVectorCorrelationDim is the widest vector whose inter-element
// correlation profile capture measures (and whose elements keep a
// percentile / shape reservoir). The co-moment accumulator is
// dim·(dim−1)/2 pairs updated per row, so a 768-dim embedding would be
// ~295k pair updates a row and a 10,000-sample reservoir per element
// would be ~60 MB. Above the cap the correlation is ABANDONED, never
// truncated (a partial matrix would present an arbitrary subset as the
// structure), the elements keep their moments, range and level
// histogram, and a warning names the field — generation then draws its
// elements independent and says so. A package constant tuned in code,
// like maxDiscreteLevels, not a ProfileOptions field.
const maxVectorCorrelationDim = 64

// VectorProfile is a vector field's capture (FieldProfile.Vector,
// additive + omitempty): the schema's own vector metadata plus one
// marginal per element and the pairwise-complete inter-element
// correlation. It is always present on a vector field — even an
// all-null one — because the dim is what lets SpecFromProfile rebuild
// the column at all.
type VectorProfile struct {
	Dim      int      `json:"dim"`
	Kind     string   `json:"kind,omitempty"`
	ScaleMin *float64 `json:"scale_min,omitempty"`
	ScaleMax *float64 `json:"scale_max,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	// N is the number of NON-NULL vectors the elements were measured
	// over (FieldProfile.NullRate carries the null-vector rate).
	N int `json:"n"`
	// Elements is one entry per element in element order; absent when
	// no non-null vector was observed.
	Elements []VectorElementProfile `json:"elements,omitempty"`
	// Correlations lists every MEASURED element pair (1-based a < b):
	// Pearson over the rows where both elements are present (non-null
	// vector, neither element NaN). A pair that could not be measured —
	// fewer than 3 co-present rows, or no variance on either side — is
	// ABSENT, and generation completes it as independent with a counted
	// warning, the same assume-and-record policy the scalar matrix uses.
	Correlations []VectorCorrelationStat `json:"correlations,omitempty"`
}

// VectorElementProfile is one element's marginal: the share of non-null
// vectors on which it is NaN, and the scalar numeric summary over the
// rest — the very NumericProfile a scalar column gets, so `discrete` (an
// all-integer element with at most maxDiscreteLevels levels), `shape`
// (--fit-shape) and the plain moments mean what they mean there.
type VectorElementProfile struct {
	NaNRate float64         `json:"nan_rate"`
	Numeric *NumericProfile `json:"numeric,omitempty"`
}

// VectorCorrelationStat is one measured element pair: 1-based elements
// A < B, Pearson Rho over N co-present rows.
type VectorCorrelationStat struct {
	A   int     `json:"a"`
	B   int     `json:"b"`
	Rho float64 `json:"rho"`
	N   int     `json:"n"`
}

// coMoments accumulates pairwise-complete co-moments over the elements
// of one vector column — Welford's online update per pair, so a NaN on
// either side drops that pair's observation only. Shared by profile
// capture and the fidelity report's vector arm, so the source and the
// generated side are measured by one definition.
//
// Every product feeding an add is wrapped float64(...) — the FMA rule
// (internal/synth/moments.go) that keeps a profile document identical
// across architectures.
type coMoments struct {
	dim                   int
	n                     []int
	mx, my, m2x, m2y, cxy []float64
}

func newCoMoments(dim int) *coMoments {
	p := dim * (dim - 1) / 2
	return &coMoments{
		dim: dim, n: make([]int, p),
		mx: make([]float64, p), my: make([]float64, p),
		m2x: make([]float64, p), m2y: make([]float64, p), cxy: make([]float64, p),
	}
}

// pairIndex maps 0-based i < j onto the packed upper triangle.
func (c *coMoments) pairIndex(i, j int) int {
	return i*c.dim - i*(i+1)/2 + (j - i - 1)
}

func (c *coMoments) observe(v []float64) {
	p := 0
	for i := 0; i < c.dim; i++ {
		x := v[i]
		if math.IsNaN(x) {
			p += c.dim - i - 1
			continue
		}
		for j := i + 1; j < c.dim; j, p = j+1, p+1 {
			y := v[j]
			if math.IsNaN(y) {
				continue
			}
			c.n[p]++
			n := float64(c.n[p])
			dx := x - c.mx[p]
			c.mx[p] += dx / n
			dy := y - c.my[p]
			c.my[p] += dy / n
			c.m2x[p] += float64(dx * (x - c.mx[p]))
			c.m2y[p] += float64(dy * (y - c.my[p]))
			c.cxy[p] += float64(dx * (y - c.my[p]))
		}
	}
}

// rho returns pair (i, j)'s Pearson correlation and support; ok is false
// for an unmeasurable pair (fewer than minResidualPairObservations
// co-present rows — two points always correlate at ±1 — or a constant
// side).
func (c *coMoments) rho(i, j int) (r float64, n int, ok bool) {
	p := c.pairIndex(i, j)
	n = c.n[p]
	if n < minResidualPairObservations || c.m2x[p] <= 0 || c.m2y[p] <= 0 {
		return 0, n, false
	}
	r = c.cxy[p] / math.Sqrt(float64(c.m2x[p]*c.m2y[p]))
	return math.Max(-1, math.Min(1, r)), n, true
}

// vecAcc accumulates one vector column during the profiling scan.
type vecAcc struct {
	meta  encoding.VectorField
	count int // non-null vectors
	nulls int
	elems []*numAcc
	nan   []int
	co    *coMoments // nil above maxVectorCorrelationDim
}

func newVecAcc(meta encoding.VectorField, reservoir bool) *vecAcc {
	a := &vecAcc{meta: meta, elems: make([]*numAcc, meta.Dim), nan: make([]int, meta.Dim)}
	keep := reservoir && meta.Dim <= maxVectorCorrelationDim
	for k := range a.elems {
		a.elems[k] = &numAcc{
			min: math.Inf(1), max: math.Inf(-1),
			reservoirOn: keep,
			// Level capture is on for every element and abandoned the
			// first time the element shows a non-integer value (or more
			// than maxDiscreteLevels levels): an all-integer element — a
			// scale or binary battery — reconstructs from its histogram,
			// exactly as a small-integer scalar does.
			levels: make(map[int64]int, 8),
		}
	}
	if meta.Dim <= maxVectorCorrelationDim {
		a.co = newCoMoments(meta.Dim)
	}
	return a
}

// observe folds one row's vector cell. v is nil for a null vector.
func (a *vecAcc) observe(v []float64, isNull bool) {
	if isNull || len(v) != a.meta.Dim {
		a.nulls++
		return
	}
	a.count++
	for k, x := range v {
		if math.IsNaN(x) {
			a.nan[k]++
			continue
		}
		na := a.elems[k]
		na.count++
		na.sum += x
		na.sumSq += float64(x * x)
		na.min = math.Min(na.min, x)
		na.max = math.Max(na.max, x)
		if na.reservoirOn && len(na.samples) < 10000 {
			na.samples = append(na.samples, x)
		}
		if na.levels != nil {
			switch {
			case x != math.Trunc(x) || math.Abs(x) > 1<<53:
				na.levels, na.levelsOver = nil, true
			default:
				key := int64(x)
				if _, seen := na.levels[key]; seen {
					na.levels[key]++
				} else if len(na.levels) >= maxDiscreteLevels {
					na.levels, na.levelsOver = nil, true
				} else {
					na.levels[key] = 1
				}
			}
		}
	}
	if a.co != nil {
		a.co.observe(v)
	}
}

// finish builds the document section and the field's null rate.
func (a *vecAcc) finish(field string, includeStats, fitShape bool, warnings *[]string) (*VectorProfile, float64) {
	vp := &VectorProfile{Dim: a.meta.Dim, N: a.count}
	if a.meta.Kind != encoding.VectorKindNone {
		vp.Kind = a.meta.Kind.String()
	}
	if a.meta.HasScale {
		lo, hi := a.meta.ScaleMin, a.meta.ScaleMax
		vp.ScaleMin, vp.ScaleMax = &lo, &hi
	}
	if len(a.meta.Labels) > 0 {
		vp.Labels = append([]string(nil), a.meta.Labels...)
	}
	nullRate := 0.0
	if total := a.count + a.nulls; total > 0 {
		nullRate = float64(a.nulls) / float64(total)
	}
	if a.count == 0 {
		return vp, nullRate
	}
	vp.Elements = make([]VectorElementProfile, a.meta.Dim)
	for k, na := range a.elems {
		vp.Elements[k].NaNRate = float64(a.nan[k]) / float64(a.count)
		vp.Elements[k].Numeric = numericProfileFrom(na, includeStats, fitShape)
	}
	if a.co == nil {
		*warnings = append(*warnings, fmt.Sprintf(
			"vector field %q: dim %d exceeds %d, so its inter-element correlation was not captured "+
				"(abandoned, never truncated) and generation draws its elements independent",
			field, a.meta.Dim, maxVectorCorrelationDim))
		return vp, nullRate
	}
	thin, unmeasured := 0, 0
	for i := 0; i < a.meta.Dim; i++ {
		for j := i + 1; j < a.meta.Dim; j++ {
			r, n, ok := a.co.rho(i, j)
			if !ok {
				unmeasured++
				continue
			}
			if n < MinPairObservations {
				thin++
			}
			vp.Correlations = append(vp.Correlations, VectorCorrelationStat{A: i + 1, B: j + 1, Rho: r, N: n})
		}
	}
	pairs := a.meta.Dim * (a.meta.Dim - 1) / 2
	if thin > 0 {
		*warnings = append(*warnings, fmt.Sprintf(
			"vector field %q: %d of %d element pair correlation(s) rest on fewer than %d co-present rows",
			field, thin, pairs, MinPairObservations))
	}
	if unmeasured > 0 {
		*warnings = append(*warnings, fmt.Sprintf(
			"vector field %q: %d of %d element pair correlation(s) could not be measured (fewer than %d co-present rows, or a constant element) and are absent from the profile",
			field, unmeasured, pairs, minResidualPairObservations))
	}
	return vp, nullRate
}

// vectorFieldSpecFromProfile rebuilds a vector FieldSpec from its
// capture: one element family per element (the scalar reconstruction
// rules — `discrete` for a histogram, `mixture` for a captured shape,
// else a `normal` clamped to the observed range), the captured NaN rate
// per element and the measured correlations. An all-null capture
// carries only the shape and draws nothing visible (null_rate 1).
func vectorFieldSpecFromProfile(fs FieldSpec, vp *VectorProfile) FieldSpec {
	if vp == nil {
		return fs
	}
	v := &VectorSpec{Dim: vp.Dim, Kind: vp.Kind, Labels: append([]string(nil), vp.Labels...)}
	if len(v.Labels) == 0 {
		v.Labels = nil
	}
	if vp.ScaleMin != nil && vp.ScaleMax != nil {
		lo, hi := *vp.ScaleMin, *vp.ScaleMax
		v.ScaleMin, v.ScaleMax = &lo, &hi
	}
	fs.Vector = v
	if len(vp.Elements) != vp.Dim {
		fs.Distribution = DistNormal
		fs.Params = map[string]any{"mean": 0.0, "std": 1.0}
		return fs
	}
	v.Elements = make([]VectorElementSpec, vp.Dim)
	for k, e := range vp.Elements {
		v.Elements[k] = vectorElementSpecFromProfile(e)
	}
	for _, c := range vp.Correlations {
		v.Correlations = append(v.Correlations, VectorCorrelationSpec{A: c.A, B: c.B, Correlation: c.Rho})
	}
	return fs
}

func vectorElementSpecFromProfile(e VectorElementProfile) VectorElementSpec {
	out := VectorElementSpec{}
	if e.NaNRate > 0 {
		r := e.NaNRate
		out.NaNRate = &r
	}
	np := e.Numeric
	switch {
	case np == nil:
		// Every observation of this element was NaN: its NaN rate is 1
		// and the family is a placeholder nothing ever writes.
		out.Distribution = DistNormal
		out.Params = map[string]any{"mean": 0.0, "std": 1.0}
	case np.Discrete != nil:
		vals := make([]any, len(np.Discrete.Levels))
		weights := make([]any, len(np.Discrete.Levels))
		for i, lv := range np.Discrete.Levels {
			vals[i] = lv.Value
			weights[i] = float64(lv.Count)
		}
		out.Distribution = DistDiscrete
		out.Params = map[string]any{"values": vals, "weights": weights}
	case np.Shape != nil:
		means := make([]any, len(np.Shape.Means))
		stds := make([]any, len(np.Shape.Stds))
		weights := make([]any, len(np.Shape.Weights))
		for i := range np.Shape.Means {
			means[i], stds[i], weights[i] = np.Shape.Means[i], np.Shape.Stds[i], np.Shape.Weights[i]
		}
		out.Distribution = DistMixture
		out.Params = map[string]any{"means": means, "stds": stds, "weights": weights}
	default:
		out.Distribution = DistNormal
		out.Params = map[string]any{
			"mean": np.Mean, "std": math.Max(np.Std, 1e-9), "min": np.Min, "max": np.Max,
		}
	}
	return out
}
