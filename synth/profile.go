package synth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// conditionalJointCap bounds the number of row-aligned numeric
// snapshots ProfileOptions.IncludeConditional retains for pairwise
// reconstruction. Mirrors the existing 10000-sample reservoir cap used
// elsewhere in this file (percentiles, the plain Pairwise correlation
// stats) so profile size stays bounded regardless of source cohort
// size.
const conditionalJointCap = 10000

// MinPairObservations is the minimum number of co-occurring, non-null
// observations a captured pair needs before its reconstruction
// parameters are trusted. This threshold and the warning mechanism
// below are deliberately generic across pair *kind* — numeric-numeric
// is the only kind this story populates, but categorical and set_*
// pairs (later epics) reuse the same threshold and the same
// thinPairWarning helper rather than inventing their own. Below the
// threshold the profile still ships the pair — a thin pair is never a
// refusal — but records a warning on Profile.Warnings so a caller
// downstream of profile capture knows the figure may be unstable.
const MinPairObservations = 30

// thinPairWarning returns a warning string when n falls below
// threshold, or "" when the pair has enough support. kind names the
// pair type in the message (e.g. "numeric" today; "categorical" / "set"
// once later stories call this same helper) so every warning this
// mechanism produces reads the same shape regardless of which capture
// path produced it.
func thinPairWarning(kind, a, b string, n, threshold int) string {
	if n >= threshold {
		return ""
	}
	return fmt.Sprintf(
		"thin %s pair %s x %s: only %d supporting observation(s) (below %d) — reconstructed correlation may be unstable",
		kind, a, b, n, threshold)
}

// ProfileOptions modulates how Profile summarizes a cohort.
type ProfileOptions struct {
	// TopK is the number of top categorical values to capture per
	// categorical field. Defaults to 32 when zero.
	TopK int
	// IncludeStats turns on percentile / stdev / kurtosis collection.
	// When false, only mean / min / max / null-rate are recorded for
	// numeric fields. Defaults to true.
	IncludeStats bool
	// IncludeCorrelations enables pairwise Pearson correlation capture
	// between numeric fields. Off by default to keep profile size bounded.
	IncludeCorrelations bool
	// CorrelationTopK caps the number of strongest |rho| pairs retained.
	// Defaults to 16 when IncludeCorrelations or IncludeConditional is
	// true.
	CorrelationTopK int
	// IncludeConditional enables capture of the joint reconstruction
	// structure (`profile create --conditional`): for numeric-numeric
	// field pairs, a row-aligned correlation + observation count
	// (Profile.Conditional.NumericPairs) that SpecFromProfile uses to
	// drive the exact conditional-Gaussian reconstruction in
	// synth/copula.go, in place of the plain (and, for cohorts with
	// nulls, only approximately aligned) Pairwise correlation stats.
	// Off by default; the profile document's `conditional` key is
	// entirely absent (omitempty) when this is false, and every
	// pre-existing profile document — which has no such key — remains
	// valid SpecFromProfile input either way.
	IncludeConditional bool
	// SampleLimit caps the number of records ingested for the profile.
	// Zero = unlimited.
	SampleLimit int
}

// Profile is a serialization-friendly statistical summary of a cohort.
// It contains everything needed to drive synth from-profile without
// retaining any individual rows from the source data.
type Profile struct {
	RowCount int               `json:"row_count"`
	Fields   []FieldProfile    `json:"fields"`
	Pairwise []CorrelationStat `json:"pairwise,omitempty"`
	// Conditional carries joint-reconstruction structure captured only
	// when ProfileOptions.IncludeConditional was set. Additive and
	// omitempty: absent on every profile document captured without
	// --conditional, including every pre-existing one, and
	// SpecFromProfile treats a nil Conditional exactly as it always
	// has (falling back to Pairwise).
	Conditional *ConditionalProfile `json:"conditional,omitempty"`
	Warnings    []string            `json:"warnings,omitempty"`
	Meta        map[string]any      `json:"meta,omitempty"`
}

// ConditionalProfile carries the joint reconstruction structure
// `profile create --conditional` captures beyond independent per-field
// marginals. v1 (this story) covers numeric-numeric pairs only;
// categorical and set_* pairs are later-epic additions to this same
// struct, not a new top-level section, so the same nil-check keeps
// gating all of them.
type ConditionalProfile struct {
	// NumericPairs lists the numeric-numeric pairs captured: the
	// row-aligned Pearson correlation and the count of rows where both
	// fields were simultaneously non-null (N) — the input
	// thinPairWarning checks against MinPairObservations. Capped at
	// ProfileOptions.CorrelationTopK strongest |rho| pairs, same
	// convention as Profile.Pairwise.
	NumericPairs []NumericPairProfile `json:"numeric_pairs,omitempty"`
}

// NumericPairProfile is one captured numeric-numeric pair's
// reconstruction input.
type NumericPairProfile struct {
	A   string  `json:"a"`
	B   string  `json:"b"`
	Rho float64 `json:"rho"`
	// N is the number of rows in the capture where both A and B were
	// simultaneously non-null — the pair's true co-occurrence count,
	// unlike Profile.Pairwise's Rho (computed from two independently
	// capped reservoirs that can drift out of alignment once either
	// field has nulls).
	N int `json:"n"`
}

// FieldProfile holds per-field summary statistics. Exactly one of
// Numeric, Categorical, or Date is populated based on the field's type.
type FieldProfile struct {
	Name        string              `json:"name"`
	Type        string              `json:"type"`
	Description string              `json:"description,omitempty"`
	NullRate    float64             `json:"null_rate"`
	Numeric     *NumericProfile     `json:"numeric,omitempty"`
	Categorical *CategoricalProfile `json:"categorical,omitempty"`
	Date        *DateProfile        `json:"date,omitempty"`
	// Precision/Scale carry decimal128 metadata so synth-from-profile can
	// reconstruct the original field shape.
	Precision uint8 `json:"precision,omitempty"`
	Scale     uint8 `json:"scale,omitempty"`
}

// NumericProfile is the detail block for numeric fields.
type NumericProfile struct {
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Mean float64 `json:"mean"`
	Std  float64 `json:"std"`
	// Percentiles holds {p1, p5, p25, p50, p75, p95, p99} when
	// IncludeStats was on; nil otherwise.
	Percentiles []float64 `json:"percentiles,omitempty"`
}

// CategoricalProfile holds the top-K observed values and the total
// distinct count.
type CategoricalProfile struct {
	Cardinality int           `json:"cardinality"`
	Top         []CategoryHit `json:"top"`
}

// CategoryHit records a categorical value with its observed weight.
type CategoryHit struct {
	Value  string  `json:"value"`
	Weight float64 `json:"weight"`
}

// DateProfile holds the (start, end) range of date values plus a weekday
// histogram for Mode-A reconstruction.
type DateProfile struct {
	Start    string `json:"start"`
	End      string `json:"end"`
	Weekdays [7]int `json:"weekdays"`
}

// CorrelationStat is a captured pairwise correlation entry.
type CorrelationStat struct {
	A   string  `json:"a"`
	B   string  `json:"b"`
	Rho float64 `json:"rho"`
}

// internal accumulators used by profileRecords. Lifted to package scope
// so computeCorrelations can take them by name.
type numAcc struct {
	count       int
	nulls       int
	sum, sumSq  float64
	min, max    float64
	samples     []float64
	reservoirOn bool
}
type catAcc struct {
	count int
	nulls int
	hist  map[string]int
}
type dateAcc struct {
	count    int
	nulls    int
	minDays  int64
	maxDays  int64
	weekdays [7]int
}

// ProfileBytes summarizes a .pulse file given its raw bytes.
func ProfileBytes(data []byte, opts ProfileOptions) (*Profile, error) {
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		return nil, err
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		return nil, err
	}
	return profileRecords(schema, r, opts)
}

// ProfileFile reads a .pulse file from fs and produces a Profile.
func ProfileFile(fs afero.Fs, path string, opts ProfileOptions) (*Profile, error) {
	data, err := afero.ReadFile(fs, path)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, "reading cohort for profile")
	}
	return ProfileBytes(data, opts)
}

// MarshalJSON serializes the profile.
func (p *Profile) MarshalJSON() ([]byte, error) {
	type alias Profile
	return json.Marshal((*alias)(p))
}

func profileRecords(schema *encoding.Schema, r io.Reader, opts ProfileOptions) (*Profile, error) {
	if opts.TopK == 0 {
		opts.TopK = 32
	}
	if (opts.IncludeCorrelations || opts.IncludeConditional) && opts.CorrelationTopK == 0 {
		opts.CorrelationTopK = 16
	}
	// Default IncludeStats to true if the caller didn't set it explicitly.
	includeStats := opts.IncludeStats || (!opts.IncludeStats && !opts.IncludeCorrelations)

	rr := encoding.NewRecordReader(r, schema)

	numAccs := make(map[string]*numAcc)
	catAccs := make(map[string]*catAcc)
	dateAccs := make(map[string]*dateAcc)
	warnings := []string{}

	// jointFieldNames, jointRows and jointNulls back
	// ProfileOptions.IncludeConditional only: a row-aligned snapshot of
	// every numeric field's (value, isNull) on each captured row, used
	// to compute a numeric-numeric pair's TRUE co-occurrence Rho/N —
	// unlike na.samples below (per-field, filled independently), this
	// stays aligned across fields even when some rows carry nulls.
	var jointFieldNames []string
	var jointRows [][]float64
	var jointNulls [][]bool

	for _, f := range schema.Fields {
		switch {
		case f.Type == encoding.FieldTypeDate:
			dateAccs[f.Name] = &dateAcc{minDays: math.MaxInt64, maxDays: math.MinInt64}
		case f.Type.IsCategorical():
			catAccs[f.Name] = &catAcc{hist: make(map[string]int)}
		default:
			numAccs[f.Name] = &numAcc{
				min: math.Inf(1), max: math.Inf(-1),
				reservoirOn: includeStats,
			}
			if opts.IncludeConditional {
				jointFieldNames = append(jointFieldNames, f.Name)
			}
		}
	}

	values := make(map[string]float64, len(schema.Fields))
	nulls := make(map[string]bool, len(schema.Fields))
	wide := make(map[string]any, len(schema.Fields))

	rowCount := 0
	for {
		err := rr.ReadRecordWithWide(values, nulls, wide)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		rowCount++
		if opts.SampleLimit > 0 && rowCount > opts.SampleLimit {
			break
		}
		for _, f := range schema.Fields {
			isNull := nulls[f.Name]
			switch {
			case f.Type == encoding.FieldTypeDate:
				da := dateAccs[f.Name]
				if isNull {
					da.nulls++
					continue
				}
				da.count++
				days := int64(values[f.Name])
				if days < da.minDays {
					da.minDays = days
				}
				if days > da.maxDays {
					da.maxDays = days
				}
				wd := dayOfWeek(days)
				da.weekdays[wd]++
			case f.Type.IsCategorical():
				ca := catAccs[f.Name]
				if isNull {
					ca.nulls++
					continue
				}
				id := uint32(values[f.Name])
				if f.Dictionary != nil {
					name := f.Dictionary.Resolve(id)
					if name == "" {
						continue
					}
					ca.hist[name]++
					ca.count++
				}
			default:
				na := numAccs[f.Name]
				if na == nil {
					continue
				}
				if isNull {
					na.nulls++
					continue
				}
				v := values[f.Name]
				na.count++
				na.sum += v
				na.sumSq += v * v
				if v < na.min {
					na.min = v
				}
				if v > na.max {
					na.max = v
				}
				if na.reservoirOn && len(na.samples) < 10000 {
					na.samples = append(na.samples, v)
				}
			}
		}
		if len(jointFieldNames) >= 2 && len(jointRows) < conditionalJointCap {
			rowVals := make([]float64, len(jointFieldNames))
			rowNulls := make([]bool, len(jointFieldNames))
			for idx, name := range jointFieldNames {
				rowNulls[idx] = nulls[name]
				if !rowNulls[idx] {
					rowVals[idx] = values[name]
				}
			}
			jointRows = append(jointRows, rowVals)
			jointNulls = append(jointNulls, rowNulls)
		}
	}

	// Build the field profiles in declaration order.
	pf := &Profile{RowCount: rowCount}
	for _, f := range schema.Fields {
		fp := FieldProfile{
			Name:        f.Name,
			Type:        f.Type.String(),
			Description: f.Description,
			Precision:   f.Precision,
			Scale:       f.Scale,
		}
		switch {
		case f.Type == encoding.FieldTypeDate:
			da := dateAccs[f.Name]
			total := da.count + da.nulls
			if total > 0 {
				fp.NullRate = float64(da.nulls) / float64(total)
			}
			if da.count > 0 {
				fp.Date = &DateProfile{
					Start:    daysToISO(da.minDays),
					End:      daysToISO(da.maxDays),
					Weekdays: da.weekdays,
				}
			}
		case f.Type.IsCategorical():
			ca := catAccs[f.Name]
			total := ca.count + ca.nulls
			if total > 0 {
				fp.NullRate = float64(ca.nulls) / float64(total)
			}
			if ca.count > 0 {
				fp.Categorical = &CategoricalProfile{
					Cardinality: len(ca.hist),
					Top:         topNCategorical(ca.hist, opts.TopK),
				}
			}
		default:
			na := numAccs[f.Name]
			if na == nil {
				break
			}
			total := na.count + na.nulls
			if total > 0 {
				fp.NullRate = float64(na.nulls) / float64(total)
			}
			if na.count > 0 {
				mean := na.sum / float64(na.count)
				var variance float64
				if na.count > 1 {
					variance = (na.sumSq - mean*na.sum) / float64(na.count-1)
				}
				if variance < 0 {
					variance = 0
				}
				num := &NumericProfile{
					Min:  na.min,
					Max:  na.max,
					Mean: mean,
					Std:  math.Sqrt(variance),
				}
				if includeStats && len(na.samples) > 0 {
					num.Percentiles = computePercentiles(na.samples,
						[]float64{0.01, 0.05, 0.25, 0.5, 0.75, 0.95, 0.99})
				}
				fp.Numeric = num
			}
		}
		pf.Fields = append(pf.Fields, fp)
	}

	if opts.IncludeCorrelations {
		pf.Pairwise = computeCorrelations(pf, numAccs, opts.CorrelationTopK)
	}

	if opts.IncludeConditional {
		pf.Conditional = computeConditionalNumericPairs(
			jointFieldNames, jointRows, jointNulls, opts.CorrelationTopK, &warnings)
	}

	if len(warnings) > 0 {
		pf.Warnings = warnings
	}

	return pf, nil
}

func dayOfWeek(daysSinceEpoch int64) int {
	// 1970-01-01 was a Thursday (weekday index 4 with Sunday=0).
	wd := (int(daysSinceEpoch%7) + 4) % 7
	if wd < 0 {
		wd += 7
	}
	return wd
}

func daysToISO(days int64) string {
	t := time.Unix(days*86400, 0).UTC()
	return t.Format("2006-01-02")
}

func topNCategorical(hist map[string]int, n int) []CategoryHit {
	type pair struct {
		k string
		v int
	}
	pairs := make([]pair, 0, len(hist))
	for k, v := range hist {
		pairs = append(pairs, pair{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].v != pairs[j].v {
			return pairs[i].v > pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	total := 0
	for _, p := range pairs {
		total += p.v
	}
	out := make([]CategoryHit, len(pairs))
	for i, p := range pairs {
		w := 0.0
		if total > 0 {
			w = float64(p.v) / float64(total)
		}
		out[i] = CategoryHit{Value: p.k, Weight: w}
	}
	return out
}

func computePercentiles(samples []float64, qs []float64) []float64 {
	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sort.Float64s(sorted)
	out := make([]float64, len(qs))
	for i, q := range qs {
		if len(sorted) == 0 {
			out[i] = 0
			continue
		}
		idx := q * float64(len(sorted)-1)
		lo := int(math.Floor(idx))
		hi := int(math.Ceil(idx))
		if lo == hi {
			out[i] = sorted[lo]
			continue
		}
		frac := idx - float64(lo)
		out[i] = sorted[lo]*(1-frac) + sorted[hi]*frac
	}
	return out
}

// computeCorrelations returns up to topK strongest absolute Pearson
// correlations among numeric fields whose accumulator captured samples.
func computeCorrelations(pf *Profile, numAccs map[string]*numAcc, topK int) []CorrelationStat {
	// Collect sample arrays for fields that retained them.
	type kept struct {
		name    string
		samples []float64
		mean    float64
	}
	var fields []kept
	for _, fp := range pf.Fields {
		if fp.Numeric == nil {
			continue
		}
		na, ok := numAccs[fp.Name]
		if !ok || len(na.samples) == 0 {
			continue
		}
		mean := na.sum / float64(na.count)
		fields = append(fields, kept{name: fp.Name, samples: na.samples, mean: mean})
	}
	if len(fields) < 2 {
		return nil
	}

	pairs := make([]CorrelationStat, 0, len(fields)*(len(fields)-1)/2)
	for i := 0; i < len(fields); i++ {
		for j := i + 1; j < len(fields); j++ {
			rho := pearson(fields[i].samples, fields[j].samples)
			if math.IsNaN(rho) {
				continue
			}
			pairs = append(pairs, CorrelationStat{
				A: fields[i].name, B: fields[j].name, Rho: rho,
			})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return math.Abs(pairs[i].Rho) > math.Abs(pairs[j].Rho)
	})
	if len(pairs) > topK {
		pairs = pairs[:topK]
	}
	return pairs
}

// pearson returns the Pearson correlation of two equal-length samples.
// When the lengths differ we truncate to min(len(a), len(b)) — the
// reservoir cap keeps both bounded so this is a no-op in practice.
func pearson(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n < 2 {
		return math.NaN()
	}
	var sumA, sumB float64
	for i := 0; i < n; i++ {
		sumA += a[i]
		sumB += b[i]
	}
	mA := sumA / float64(n)
	mB := sumB / float64(n)
	var num, dA, dB float64
	for i := 0; i < n; i++ {
		da := a[i] - mA
		db := b[i] - mB
		num += da * db
		dA += da * da
		dB += db * db
	}
	if dA == 0 || dB == 0 {
		return math.NaN()
	}
	return num / math.Sqrt(dA*dB)
}

// computeConditionalNumericPairs derives (rho, n) for every
// numeric-numeric pair from row-aligned joint samples — rows[r][i] /
// nulls[r][i] is field fields[i]'s value/null-flag on captured row r,
// exactly as jointRows/jointNulls were built during the streaming pass.
// Only rows where BOTH fields of a pair were simultaneously non-null
// count toward that pair's N and its Rho — the co-occurrence discipline
// Profile.Pairwise's computeCorrelations cannot guarantee once any
// field carries nulls, since its per-field reservoirs are filled
// independently. Returns nil when no field pair produced a defined
// correlation (fewer than two co-occurring observations). Pairs below
// MinPairObservations still ship — appended as a warning to *warnings,
// never dropped.
func computeConditionalNumericPairs(fields []string, rows [][]float64, nulls [][]bool, topK int, warnings *[]string) *ConditionalProfile {
	n := len(fields)
	if n < 2 {
		return nil
	}
	pairs := make([]NumericPairProfile, 0, n*(n-1)/2)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			var a, b []float64
			for r := range rows {
				if nulls[r][i] || nulls[r][j] {
					continue
				}
				a = append(a, rows[r][i])
				b = append(b, rows[r][j])
			}
			rho := pearson(a, b)
			if math.IsNaN(rho) {
				continue
			}
			pairs = append(pairs, NumericPairProfile{A: fields[i], B: fields[j], Rho: rho, N: len(a)})
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	sort.Slice(pairs, func(i, j int) bool {
		return math.Abs(pairs[i].Rho) > math.Abs(pairs[j].Rho)
	})
	if len(pairs) > topK {
		pairs = pairs[:topK]
	}
	for _, p := range pairs {
		if w := thinPairWarning("numeric", p.A, p.B, p.N, MinPairObservations); w != "" {
			*warnings = append(*warnings, w)
		}
	}
	return &ConditionalProfile{NumericPairs: pairs}
}

// SpecFromProfile builds a Spec the synth pipeline can execute. Numeric
// fields are reconstructed as normal distributions (mean, std clipped
// at min/max), categorical fields as weighted_categorical, and date
// fields as uniform_date over the observed range.
func SpecFromProfile(p *Profile, rowCount int) *Spec {
	s := &Spec{RowCount: rowCount}
	for _, fp := range p.Fields {
		fs := FieldSpec{
			Name:        fp.Name,
			Type:        fp.Type,
			Description: fp.Description,
			NullRate:    fp.NullRate,
			Precision:   fp.Precision,
			Scale:       fp.Scale,
		}
		switch {
		case fp.Date != nil:
			fs.Distribution = DistUniformDate
			fs.Params = map[string]any{
				"start": fp.Date.Start, "end": fp.Date.End,
			}
		case fp.Categorical != nil:
			vals := make([]any, len(fp.Categorical.Top))
			weights := make([]any, len(fp.Categorical.Top))
			for i, c := range fp.Categorical.Top {
				vals[i] = c.Value
				weights[i] = c.Weight
			}
			fs.Distribution = DistWeightedCategorical
			fs.Params = map[string]any{"values": vals, "weights": weights}
		case fp.Numeric != nil:
			fs.Distribution = DistNormal
			fs.Params = map[string]any{
				"mean": fp.Numeric.Mean,
				"std":  math.Max(fp.Numeric.Std, 1e-9),
				"min":  fp.Numeric.Min,
				"max":  fp.Numeric.Max,
			}
		default:
			// Field type the profiler couldn't summarize. Use a constant
			// of 0 so synth still produces a record-shaped output.
			fs.Distribution = DistConstant
			fs.Params = map[string]any{"value": float64(0)}
		}
		s.Fields = append(s.Fields, fs)
	}
	// Drop correlations whose endpoints are not numeric in the
	// reconstructed spec (the copula only operates on numeric fields).
	//
	// Prefer the row-aligned p.Conditional.NumericPairs (captured only
	// when --conditional was set, with an accurate co-occurrence N)
	// over the plain p.Pairwise stats; fall back to Pairwise when no
	// Conditional section is present so a profile captured without
	// --conditional — including every document from before this
	// section existed — still reconstructs its best-effort correlation
	// exactly as before. Both funnel into the same Spec.Correlations
	// shape: synth/copula.go's conditional-Gaussian reconstruction
	// applies identically either way, so this choice affects only
	// which Rho/pair-set gets used, never how it is reconstructed.
	typeOf := make(map[string]string, len(s.Fields))
	for _, fs := range s.Fields {
		typeOf[fs.Name] = fs.Type
	}
	addCorrelation := func(a, b string, rho float64) {
		if !isNumericFieldType(typeOf[a]) || !isNumericFieldType(typeOf[b]) {
			return
		}
		s.Correlations = append(s.Correlations, CorrelationSpec{A: a, B: b, Correlation: rho})
	}
	if p.Conditional != nil && len(p.Conditional.NumericPairs) > 0 {
		for _, c := range p.Conditional.NumericPairs {
			addCorrelation(c.A, c.B, c.Rho)
		}
	} else {
		for _, c := range p.Pairwise {
			addCorrelation(c.A, c.B, c.Rho)
		}
	}
	return s
}
