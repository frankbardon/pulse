package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// ATTR_PROFILE_SIMILARITY / ATTR_VEC_DISTANCE end to end (U27 E3-S2):
// every execution mode under the default projected decode. A value
// reads only its members (and a key_field), so a column the projection
// dropped would read as missing and silently move every figure: the
// sums below are oracles over the generated rows.

// cmpOracleEuclid is √(Σ((x−r)/sd)² · p/p_obs) over the complete pairs
// (sd nil: unscaled); NaN with none.
func cmpOracleEuclid(x, r, sd []float64) float64 {
	n, s := 0, 0.0
	for i := range x {
		if math.IsNaN(x[i]) || math.IsNaN(r[i]) {
			continue
		}
		d := x[i] - r[i]
		if sd != nil {
			d /= sd[i]
		}
		s += d * d
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	return math.Sqrt(s * float64(len(x)) / float64(n))
}

func cmpOracleCorrelation(x, r []float64) float64 {
	var a, b []float64
	for i := range x {
		if !math.IsNaN(x[i]) {
			a, b = append(a, x[i]), append(b, r[i])
		}
	}
	if len(a) < 2 {
		return math.NaN()
	}
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma, mb = ma/float64(len(a)), mb/float64(len(b))
	var sab, saa, sbb float64
	for i := range a {
		sab += (a[i] - ma) * (b[i] - mb)
		saa += (a[i] - ma) * (a[i] - ma)
		sbb += (b[i] - mb) * (b[i] - mb)
	}
	return sab / math.Sqrt(saa*sbb)
}

// vexprListwiseSD is each battery member's ddof = 1 SD over the
// complete rows of [0, rows) — the fitted metrics' listwise fit.
func vexprListwiseSD(rows int) []float64 {
	var sum, sq [vexprMembers]float64
	n := 0
	for r := 0; r < rows; r++ {
		x := vexprRow(r)
		complete := true
		for _, v := range x {
			if math.IsNaN(v) {
				complete = false
			}
		}
		if !complete {
			continue
		}
		n++
		for j, v := range x {
			sum[j] += v
		}
	}
	for r := 0; r < rows; r++ {
		x := vexprRow(r)
		complete := true
		for _, v := range x {
			if math.IsNaN(v) {
				complete = false
			}
		}
		if !complete {
			continue
		}
		for j, v := range x {
			d := v - sum[j]/float64(n)
			sq[j] += d * d
		}
	}
	out := make([]float64, vexprMembers)
	for j := range out {
		out[j] = math.Sqrt(sq[j] / float64(n-1))
	}
	return out
}

var listwiseSDCache = map[int][]float64{}

func cachedListwiseSD(rows int) []float64 {
	if sd, ok := listwiseSDCache[rows]; ok {
		return sd
	}
	sd := vexprListwiseSD(rows)
	listwiseSDCache[rows] = sd
	return sd
}

type comparisonCase struct {
	name    string
	vecs    []types.VectorSpec
	attr    types.Attribute
	value   func(rows int, x []float64) float64
	metric  string // the kind default predict reports ("" = none)
	twoPass bool
}

func comparisonCases() []comparisonCase {
	ref := []float64{3, 4, 5, 6}
	return []comparisonCase{
		{name: "scale kind correlation default, literal",
			vecs:   []types.VectorSpec{{Name: "battery", Fields: []string{"b*"}, Kind: types.VectorKindScale}},
			attr:   types.Attribute{Type: types.ATTR_PROFILE_SIMILARITY, Vector: "battery", Params: json.RawMessage(`{"reference":[3,4,5,6],"min_valid":2}`)},
			value:  func(_ int, x []float64) float64 { return cmpOracleCorrelation(x, ref) },
			metric: "correlation"},
		{name: "inline labels, label map, euclidean",
			attr: types.Attribute{Type: types.ATTR_VEC_DISTANCE, Fields: []string{"b*"},
				Params: json.RawMessage(`{"metric":"euclidean","labels":["w","x","y","z"],"reference":{"z":6,"y":5,"x":4,"w":3}}`)},
			value: func(_ int, x []float64) float64 { return cmpOracleEuclid(x, ref, nil) }},
		{name: "measure kind std_euclidean default, two-pass fit",
			vecs:    []types.VectorSpec{{Name: "battery", Fields: []string{"b*"}, Labels: []string{"w", "x", "y", "z"}, Kind: types.VectorKindMeasure}},
			attr:    types.Attribute{Type: types.ATTR_VEC_DISTANCE, Vector: "battery", Params: json.RawMessage(`{"reference":{"w":3,"x":4,"y":5,"z":6}}`)},
			value:   func(rows int, x []float64) float64 { return cmpOracleEuclid(x, ref, cachedListwiseSD(rows)) },
			metric:  "std_euclidean",
			twoPass: true},
	}
}

func TestComparison_EveryModeUnderProjection(t *testing.T) {
	ctx := context.Background()
	for _, m := range vexprModes() {
		t.Run(m.name, func(t *testing.T) {
			p, cohort := m.open(t)
			for _, c := range comparisonCases() {
				t.Run(c.name, func(t *testing.T) {
					a := c.attr
					a.Label = "s"
					req := &types.Request{
						Cohort:     &types.Cohort{Filename: cohort},
						Vectors:    c.vecs,
						Attributes: []*types.Attribute{&a},
						Aggregations: []*types.Aggregation{
							{Type: types.AGG_SUM, Field: "s", Label: "sum_s"},
							{Type: types.AGG_COUNT, Field: "s", Label: "n_s"},
							{Type: types.AGG_SUM, Field: "qty", Label: "s_qty"},
						},
					}
					if m.buffered {
						req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "qty_pct"})
					}
					// Predict first: the runtime resolves smart defaults in
					// place, so a processed request carries the filled metric.
					res, err := p.Predict(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
					if !res.Valid {
						t.Fatalf("predict refuses a request Process runs")
					}
					var filled []string
					for _, d := range res.DefaultsApplied {
						if d.Category == "metric" {
							filled = append(filled, d.Type)
						}
					}
					if (c.metric == "") != (len(filled) == 0) || (c.metric != "" && filled[0] != c.metric) {
						t.Errorf("defaults_applied metric fills %v, want %q", filled, c.metric)
					}
					resp, err := p.Process(ctx, req)
					if err != nil {
						t.Fatalf("Process: %v", err)
					}
					wantSum, wantN := 0.0, 0
					for r := 0; r < m.rows; r++ {
						v := c.value(m.rows, vexprRow(r))
						if !math.IsNaN(v) {
							wantSum += v
							wantN++
						}
					}
					if wantN < m.rows/2 {
						t.Fatalf("vacuous oracle: %d of %d rows scored", wantN, m.rows)
					}
					if got, ok := vexprFloat(resp.Data[0]["sum_s"]); !ok || !vexprClose(got, wantSum) {
						t.Errorf("sum_s = %v, want %v", resp.Data[0]["sum_s"], wantSum)
					}
					if got, ok := vexprFloat(resp.Data[0]["n_s"]); !ok || got != float64(wantN) {
						t.Errorf("n_s = %v, want %d", resp.Data[0]["n_s"], wantN)
					}
					want := []types.AttributeComponents{{Label: "s", Type: a.Type, N: wantN, NNull: m.rows - wantN,
						Operator: map[string]any{"n_reference_missing": 0, "n_domain_invalid": 0}}}
					if resp.Components == nil || !reflect.DeepEqual(resp.Components.Attributes, want) {
						t.Errorf("Components.Attributes = %+v, want %+v", resp.Components, want)
					}
				})
			}
		})
	}
}

// refCohortModes opens the parity cohort (region categorical) with the
// reference tables, on every execution mode.
func refCohortModes(t *testing.T, tables map[string]types.ReferenceVectorTable) map[string]struct {
	p    *pulse.Pulse
	path string
	rows int
} {
	t.Helper()
	ext := pulse.Extensions{ReferenceVectors: tables}
	out := map[string]struct {
		p    *pulse.Pulse
		path string
		rows int
	}{}
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "c.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: ext})
	if err != nil {
		t.Fatal(err)
	}
	out["streaming"] = struct {
		p    *pulse.Pulse
		path string
		rows int
	}{p, "c.pulse", paritySmallRows}
	out["buffered"] = out["streaming"]

	dir := t.TempDir()
	writeParityCohort(t, afero.NewOsFs(), filepath.Join(dir, "large.pulse"), paritySchema(t), 0, parityLargeRows)
	lp, err := pulse.New(pulse.Options{DataDir: dir, DecodeWorkers: 4, Extensions: ext})
	if err != nil {
		t.Fatal(err)
	}
	out["parallel_decode"] = struct {
		p    *pulse.Pulse
		path string
		rows int
	}{lp, "large.pulse", parityLargeRows}

	sfs := afero.NewMemMapFs()
	shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
	for i, path := range shards {
		writeParityCohort(t, sfs, path, paritySchema(t), i*40, (i+1)*40)
	}
	sp, err := pulse.New(pulse.Options{FS: sfs, ShardWorkers: 3, Extensions: ext})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	out["shard_archive"] = struct {
		p    *pulse.Pulse
		path string
		rows int
	}{sp, "archive.pulse", 120}
	return out
}

// TestComparison_KeyFieldEveryMode: {table, key_field} reads each row's
// own profile by its region label, aligned BY LABEL (the table lists q
// before s); a region with no profile is null and counted in
// n_reference_missing — on every mode, the key column surviving the
// projection.
func TestComparison_KeyFieldEveryMode(t *testing.T) {
	ctx := context.Background()
	tables := map[string]types.ReferenceVectorTable{
		"targets": {Labels: []string{"q", "s"}, Profiles: map[string][]float64{"north": {2, 10}, "south": {6, 30}, "east": {1, 5}}},
	}
	regions := []string{"north", "south", "east", "west"}
	for name, m := range refCohortModes(t, tables) {
		t.Run(name, func(t *testing.T) {
			req := &types.Request{
				Cohort:  &types.Cohort{Filename: m.path},
				Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"score", "qty"}, Labels: []string{"s", "q"}}},
				Attributes: []*types.Attribute{{Type: types.ATTR_VEC_DISTANCE, Vector: "v", Label: "d",
					Params: json.RawMessage(`{"metric":"manhattan","reference":{"table":"targets","key_field":"region"}}`)}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "d", Label: "sum_d"},
					{Type: types.AGG_SUM, Field: "amount", Label: "a"},
				},
			}
			if name == "buffered" {
				req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "pct"})
			}
			resp, err := m.p.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			wantSum, n, missing := 0.0, 0, 0
			for r := 0; r < m.rows; r++ {
				prof, ok := tables["targets"].Profiles[regions[r%4]]
				if !ok {
					missing++
					continue
				}
				x := []float64{float64(r%50) + 0.5, float64(r % 13)}
				if r%7 == 3 {
					x[0] = math.NaN()
				}
				ref := []float64{prof[1], prof[0]} // by label: s, q
				d := 0.0
				obs := 0
				for i := range x {
					if !math.IsNaN(x[i]) {
						d += math.Abs(x[i] - ref[i])
						obs++
					}
				}
				wantSum += d * 2 / float64(obs)
				n++
			}
			if got, ok := vexprFloat(resp.Data[0]["sum_d"]); !ok || !vexprClose(got, wantSum) {
				t.Errorf("sum_d = %v, want %v", resp.Data[0]["sum_d"], wantSum)
			}
			want := []types.AttributeComponents{{Label: "d", Type: types.ATTR_VEC_DISTANCE, N: n, NNull: missing,
				Operator: map[string]any{"n_reference_missing": missing, "n_domain_invalid": 0}}}
			if resp.Components == nil || !reflect.DeepEqual(resp.Components.Attributes, want) {
				t.Errorf("Components.Attributes = %+v, want %+v", resp.Components, want)
			}
			if missing == 0 {
				t.Fatal("vacuous: no row without a profile")
			}
		})
	}
}

// TestComparison_RefusalsPredictRuntimeParity: a slot predict refuses is
// refused by Process with the same code, before any record — the
// direction split (with its pointer), the metric requirement, the
// literal / table label checks.
func TestComparison_RefusalsPredictRuntimeParity(t *testing.T) {
	ctx := context.Background()
	p, data := openReferenceCohort(t, map[string]types.ReferenceVectorTable{
		"t":     {Labels: []string{"q", "s"}, Profiles: map[string][]float64{"north": {1, 2}}},
		"other": {Labels: []string{"q", "x"}, Profiles: map[string][]float64{"north": {1, 2}}},
	})
	vec := []types.VectorSpec{{Name: "v", Fields: []string{"score", "qty"}, Labels: []string{"s", "q"}}}
	for _, c := range []struct {
		name   string
		typ    types.AttributeType
		params string
		code   perr.Code
		use    string
	}{
		{"distance on the similarity op", types.ATTR_PROFILE_SIMILARITY, `{"metric":"euclidean","reference":[1,2]}`, perr.PULSE_VECTOR_METRIC_UNKNOWN, "ATTR_VEC_DISTANCE"},
		{"similarity on the distance op", types.ATTR_VEC_DISTANCE, `{"metric":"correlation","reference":[1,2]}`, perr.PULSE_VECTOR_METRIC_UNKNOWN, "ATTR_PROFILE_SIMILARITY"},
		{"no kind no metric", types.ATTR_VEC_DISTANCE, `{"reference":[1,2]}`, perr.PULSE_VECTOR_METRIC_REQUIRED, ""},
		{"literal length", types.ATTR_VEC_DISTANCE, `{"metric":"euclidean","reference":[1,2,3]}`, perr.PULSE_VECTOR_REFERENCE_MISMATCH, ""},
		{"label map labels", types.ATTR_VEC_DISTANCE, `{"metric":"euclidean","reference":{"s":1,"x":2}}`, perr.PULSE_VECTOR_REFERENCE_MISMATCH, ""},
		{"unknown table", types.ATTR_VEC_DISTANCE, `{"metric":"euclidean","reference":{"table":"nope","key":"north"}}`, perr.PULSE_VECTOR_REFERENCE_UNKNOWN, ""},
		{"unknown key", types.ATTR_VEC_DISTANCE, `{"metric":"euclidean","reference":{"table":"t","key":"west"}}`, perr.PULSE_VECTOR_REFERENCE_UNKNOWN, ""},
		{"table labels", types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":{"table":"other","key_field":"region"}}`, perr.PULSE_VECTOR_REFERENCE_MISMATCH, ""},
		{"key_field numeric", types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":{"table":"t","key_field":"qty"}}`, perr.SERVICE_VALIDATION, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{
				Cohort:       &types.Cohort{Filename: "ref.pulse"},
				Vectors:      vec,
				Attributes:   []*types.Attribute{{Type: c.typ, Vector: "v", Label: "s", Params: json.RawMessage(c.params)}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s"}},
			}
			_, err := p.Process(ctx, req)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("Process: %v, want %s", err, c.code)
			}
			if use, _ := ce.Details["use"].(string); use != c.use {
				t.Errorf("Process details.use = %q, want %q", use, c.use)
			}
			env, _ := p.PredictBytes(ctx, data, req)
			if env == nil || len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) {
				t.Errorf("predict errors = %+v, want %s", env, c.code)
			}
		})
	}
}

// TestComparison_PredictReadings: predict reports the kind fill, the
// unsuited warning on an explicit poor fit, single-pass streamability
// for a row-local metric and the two-pass decline of a fitted one from
// crosstab fusion — each as the runtime behaves.
func TestComparison_PredictReadings(t *testing.T) {
	ctx := context.Background()
	p, _ := vexprModes()[0].open(t)
	scale := []types.VectorSpec{{Name: "battery", Fields: []string{"b*"}, Kind: types.VectorKindScale}}
	measure := []types.VectorSpec{{Name: "battery", Fields: []string{"b*"}, Kind: types.VectorKindMeasure}}
	build := func(vecs []types.VectorSpec, typ types.AttributeType, params string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "v.pulse"},
			Vectors:      vecs,
			Attributes:   []*types.Attribute{{Type: typ, Vector: "battery", Label: "s", Params: json.RawMessage(params)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s"}},
		}
	}
	// cosine on a scale battery is a poor fit: a warning on both paths,
	// naming the correlation default.
	req := build(scale, types.ATTR_PROFILE_SIMILARITY, `{"metric":"cosine","reference":[1,2,3,4]}`)
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	warned := func(ws []*types.ResponseWarning) bool {
		for _, w := range ws {
			if w.Code == string(perr.PULSE_VECTOR_METRIC_UNSUITED) && w.Details["suggested"] == "correlation" {
				return true
			}
		}
		return false
	}
	if !warned(resp.Warnings) {
		t.Errorf("runtime warnings %+v lack PULSE_VECTOR_METRIC_UNSUITED", resp.Warnings)
	}
	res, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || !res.Streamable {
		t.Errorf("predict valid %v streamable %v, want both", res.Valid, res.Streamable)
	}
	// Grouped, a fitted slot (the measure kind's std_euclidean default)
	// is two-pass and so runs buffered; the row-local scale default
	// streams grouped. Predict says so as the runtime routes it.
	for _, c := range []struct {
		vecs   []types.VectorSpec
		stream bool
	}{{measure, false}, {scale, true}} {
		req := build(c.vecs, types.ATTR_VEC_DISTANCE, `{"reference":[1,2,3,4]}`)
		req.Groups = []*types.Group{{Type: types.GROUP_RANGE, Field: "qty", Interval: 2}}
		res, err := p.Predict(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.Streamable != c.stream {
			t.Errorf("grouped %s: predict streamable %v, want %v", c.vecs[0].Kind, res.Streamable, c.stream)
		}
	}
	// The fitted default fits first: still streamable (two-pass drive),
	// but a crosstab over it declines fusion on both sides.
	for _, c := range []struct {
		vecs   []types.VectorSpec
		params string
		fuse   bool
	}{
		{measure, `{"reference":[1,2,3,4]}`, false},
		{scale, `{"reference":[1,2,3,4]}`, true},
	} {
		req := build(c.vecs, types.ATTR_VEC_DISTANCE, c.params)
		req.Aggregations = nil
		req.Crosstab = &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_RANGE, Field: "qty", Interval: 2}},
			Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "qty", Interval: 3}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "s"},
		}
		res, err := p.Predict(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.CrosstabFusable == nil || *res.CrosstabFusable != c.fuse {
			t.Errorf("%s: crosstab_fusable %v, want %v (%v)", c.params, res.CrosstabFusable, c.fuse, res.CrosstabFusionReasons)
		}
		if _, err := p.Process(ctx, req); err != nil {
			t.Fatalf("Process: %v", err)
		}
	}
}
