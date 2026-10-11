package processing

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// distanceOracle is one mv_distance.json case
// (scripts/reference/gen_distance.R).
type distanceOracle struct {
	Name         string                 `json:"name"`
	K            int                    `json:"k"`
	Kind         string                 `json:"kind"`
	Standardize  bool                   `json:"standardize"`
	Rows         [][]float64            `json:"rows"`
	Buckets      []float64              `json:"buckets"`
	Weights      []float64              `json:"weights"`
	Sizes        []float64              `json:"sizes"`
	Counts       []float64              `json:"counts"`
	Centroids    [][]float64            `json:"centroids"`
	CentroidsStd [][]float64            `json:"centroids_standardized"`
	PooledCov    [][]float64            `json:"pooled_covariance"`
	Distances    map[string][][]float64 `json:"distances"`
}

// distanceSchema is p f64 members x1..xp, a u8 bucket column g and a
// weight w.
func distanceSchema(p int) (*encoding.Schema, []string) {
	members := make([]string, p)
	fields := make([]encoding.Field, 0, p+2)
	for j := range p {
		members[j] = "x" + strconv.Itoa(j+1)
		fields = append(fields, encoding.Field{Name: members[j], Type: encoding.FieldTypeF64, Nullable: true})
	}
	fields = append(fields,
		encoding.Field{Name: "g", Type: encoding.FieldTypeU8, Nullable: true},
		encoding.Field{Name: "w", Type: encoding.FieldTypeF64})
	return &encoding.Schema{Fields: fields}, members
}

// runDistance folds rows into one MAT_DISTANCE slot built from spec
// (positions stamped in row order) and returns its result and
// components.
func runDistance(t *testing.T, schema *encoding.Schema, spec types.MatrixSpec, rows []map[string]float64, nulls []map[string]bool) (types.MatrixResult, types.MatrixComponents) {
	t.Helper()
	slots, err := buildMatrixSlotsFor(&types.Request{Matrices: []types.MatrixSpec{spec}}, schema, nil, FullComputePlan())
	if err != nil || len(slots) != 1 {
		t.Fatalf("buildMatrixSlotsFor: %d slots, %v", len(slots), err)
	}
	for i, vals := range rows {
		var nl map[string]bool
		if nulls != nil {
			nl = nulls[i]
		}
		rec := NewRecordWithNulls(schema, vals, nl)
		rec.SetMergePosition(0, i)
		if err := slots[0].UpdateRow(rec, ""); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	res, comps, err := finalizeMatrixSlots(slots)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	return res[0], comps[0]
}

func distNear(t *testing.T, where string, got, want float64) {
	t.Helper()
	if math.IsNaN(want) {
		if !math.IsNaN(got) {
			t.Errorf("%s = %v, want NaN", where, got)
		}
		return
	}
	if !(math.Abs(got-want) <= 1e-9*math.Max(1, math.Abs(want))) {
		t.Errorf("%s = %.17g, R = %.17g", where, got, want)
	}
}

func distNearRows(t *testing.T, where string, got *types.MatrixValues, want [][]float64) {
	t.Helper()
	if got == nil || len(got.Values) != len(want) {
		t.Fatalf("%s: %+v, want %d rows", where, got, len(want))
	}
	for i := range want {
		if len(got.Values[i]) != len(want[i]) {
			t.Fatalf("%s[%d]: %d cols, want %d", where, i, len(got.Values[i]), len(want[i]))
		}
		for j := range want[i] {
			distNear(t, fmt.Sprintf("%s[%d][%d]", where, i, j), got.Values[i][j], want[i][j])
		}
	}
}

// TestMatrixDistance_MatchesROracle pins every metric against R on
// every mv_distance.json case — the weighted.mean() centroids, the
// bucket masses and counts, the scale()d centroids under standardize,
// stats::dist / squared euclidean / cosine over them, and
// sqrt(mahalanobis()) over the w*-pooled within-bucket covariance —
// unweighted, under frequency weights (zero-weight rows included: they
// count, add no mass) and probability weights, with GROUP_CATEGORY as
// the between grouper and, on the k-means case, GROUP_KMEANS
// (reproducing R's partition, keys cluster_1..3).
func TestMatrixDistance_MatchesROracle(t *testing.T) {
	doc := loadReference[distanceOracle](t, "mv_distance")
	sawKMeans := false
	for _, c := range doc.Cases {
		for _, metric := range vectors.DistanceMetrics() {
			t.Run(c.Name+"/"+metric, func(t *testing.T) {
				p := len(c.Rows[0])
				schema, members := distanceSchema(p)
				rows := make([]map[string]float64, len(c.Rows))
				for i, r := range c.Rows {
					vals := map[string]float64{"g": c.Buckets[i], "w": 1}
					for j, v := range r {
						vals[members[j]] = v
					}
					if c.Weights != nil {
						vals["w"] = c.Weights[i]
					}
					rows[i] = vals
				}
				between := map[string]any{"type": "GROUP_CATEGORY", "field": "g"}
				labels := make([]string, c.K)
				for b := range labels {
					labels[b] = strconv.Itoa(b + 1)
				}
				if c.Name == "kmeans_blobs3" {
					sawKMeans = true
					between = map[string]any{"type": "GROUP_KMEANS", "fields": members, "params": map[string]any{"k": c.K, "seed": 7}}
					for b := range labels {
						labels[b] = "cluster_" + strconv.Itoa(b+1)
					}
				}
				raw, _ := json.Marshal(map[string]any{"between": between, "metric": metric, "standardize": c.Standardize})
				spec := types.MatrixSpec{Name: "d", Type: types.MAT_DISTANCE, Fields: members, Params: raw}
				switch c.Kind {
				case "frequency":
					spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
				case "probability":
					spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
				}
				res, comp := runDistance(t, schema, spec, rows, nil)
				if len(res.Warnings) != 0 {
					t.Errorf("warnings %+v", res.Warnings)
				}
				if fmt.Sprint(res.Primary.RowKeys) != fmt.Sprint(labels) || fmt.Sprint(res.Primary.ColumnKeys) != fmt.Sprint(labels) {
					t.Fatalf("keys %v / %v, want %v", res.Primary.RowKeys, res.Primary.ColumnKeys, labels)
				}
				if res.Primary.Kind != types.MatrixKindSquareSymmetric {
					t.Errorf("kind %q", res.Primary.Kind)
				}
				distNearRows(t, "primary", res.Primary, c.Distances[metric])
				for b := range labels {
					if res.Primary.Values[b][b] != 0 {
						t.Errorf("diagonal[%d] = %v, want exactly 0", b, res.Primary.Values[b][b])
					}
				}
				distNearRows(t, "centroids", res.Auxiliary["centroids"], c.Centroids)
				sizes, counts := res.Vectors["sizes"].([]float64), res.Vectors["counts"].([]int)
				for b := range labels {
					distNear(t, fmt.Sprintf("sizes[%d]", b), sizes[b], c.Sizes[b])
					if float64(counts[b]) != c.Counts[b] {
						t.Errorf("counts[%d] = %d, R = %v", b, counts[b], c.Counts[b])
					}
				}
				if c.Standardize {
					distNearRows(t, "centroids_standardized", res.Auxiliary["centroids_standardized"], c.CentroidsStd)
				} else if _, ok := res.Auxiliary["centroids_standardized"]; ok {
					t.Error("centroids_standardized without standardize")
				}
				if metric == vectors.DistanceMahalanobis {
					distNearRows(t, "pooled_covariance", res.Auxiliary["pooled_covariance"], c.PooledCov)
				} else if _, ok := res.Auxiliary["pooled_covariance"]; ok {
					t.Error("pooled_covariance off mahalanobis")
				}
				if comp.N != len(c.Rows) || comp.Operator["buckets"] != c.K || comp.Operator["n_null_key"] != 0 ||
					comp.Operator["metric"] != metric || comp.Operator["standardize"] != c.Standardize {
					t.Errorf("components %+v", comp)
				}
			})
		}
	}
	if !sawKMeans {
		t.Fatal("oracle has no k-means case")
	}
}

// distanceRows is a small two-member, three-bucket set with a null
// bucket key on row 3 and a null member on row 4.
func distanceRows() ([]map[string]float64, []map[string]bool) {
	rows := []map[string]float64{
		{"x1": 1, "x2": 2, "g": 1, "w": 1},
		{"x1": 3, "x2": 1, "g": 1, "w": 1},
		{"x1": 6, "x2": 8, "g": 2, "w": 1},
		{"x1": 100, "x2": 100, "g": 0, "w": 1},
		{"x1": 0, "x2": 50, "g": 3, "w": 1},
		{"x1": 7, "x2": 9, "g": 2, "w": 1},
		{"x1": -2, "x2": 4, "g": 3, "w": 1},
		{"x1": -1, "x2": 6, "g": 3, "w": 1},
	}
	nulls := make([]map[string]bool, len(rows))
	nulls[3] = map[string]bool{"g": true}
	nulls[4] = map[string]bool{"x1": true}
	return rows, nulls
}

func distanceSpec(params string) types.MatrixSpec {
	return types.MatrixSpec{Name: "d", Type: types.MAT_DISTANCE, Fields: []string{"x1", "x2"}, Params: json.RawMessage(params)}
}

// TestMatrixDistance_NullKeyExcludedAndCounted: a row whose bucket key
// is null joins no centroid and no floor count — it is n_null_key; a
// row with a null member is listwise-dropped (n_null). The centroids
// are the hand means over the remaining rows.
func TestMatrixDistance_NullKeyExcludedAndCounted(t *testing.T) {
	schema, _ := distanceSchema(2)
	rows, nulls := distanceRows()
	res, comp := runDistance(t, schema, distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "metric": "manhattan"}`), rows, nulls)
	if comp.Operator["n_null_key"] != 1 || comp.N != 6 || comp.NNull != 1 || comp.NListwiseDropped != 1 || comp.Operator["buckets"] != 3 {
		t.Fatalf("components %+v", comp)
	}
	want := [][]float64{{2, 1.5}, {6.5, 8.5}, {-1.5, 5}}
	distNearRows(t, "centroids", res.Auxiliary["centroids"], want)
	// manhattan(1, 2) = 4.5 + 7; (1, 3) = 3.5 + 3.5; (2, 3) = 8 + 3.5.
	distNearRows(t, "primary", res.Primary, [][]float64{{0, 11.5, 7}, {11.5, 0, 11.5}, {7, 11.5, 0}})
	if counts := res.Vectors["counts"].([]int); fmt.Sprint(counts) != "[2 2 2]" {
		t.Errorf("counts %v", counts)
	}
}

// TestMatrixDistance_UpperEncodingAndInclude: the spec's upper encoding
// writes the triangle; a between include list both filters the buckets
// (an excluded key counts as n_null_key) and orders them.
func TestMatrixDistance_UpperEncodingAndInclude(t *testing.T) {
	schema, _ := distanceSchema(2)
	rows, nulls := distanceRows()
	spec := distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g", "include": ["3", "1"]}}`)
	spec.Encoding = types.MatrixEncodingUpper
	res, comp := runDistance(t, schema, spec, rows, nulls)
	if fmt.Sprint(res.Primary.RowKeys) != "[3 1]" || res.Primary.Encoding != types.MatrixEncodingUpper {
		t.Fatalf("primary %+v", res.Primary)
	}
	if len(res.Primary.Values[0]) != 2 || len(res.Primary.Values[1]) != 1 || res.Primary.Values[1][0] != 0 {
		t.Errorf("upper layout %v", res.Primary.Values)
	}
	distNear(t, "d(3,1)", res.Primary.Values[0][1], math.Hypot(3.5, 3.5))
	if comp.Operator["n_null_key"] != 3 {
		t.Errorf("n_null_key = %v, want 3 (the null key and both bucket-2 rows)", comp.Operator["n_null_key"])
	}
}

// TestMatrixDistance_UndefinedFigures: a bucket whose rows carry no
// mass has a null centroid and null distances
// (PULSE_MATRIX_INSUFFICIENT_N scope "buckets"); cosine is null against
// a zero-length point; mahalanobis with no more rows than buckets is
// null (scope "pooled"), and over collinear members is null with
// PULSE_MATRIX_SINGULAR.
func TestMatrixDistance_UndefinedFigures(t *testing.T) {
	schema, _ := distanceSchema(2)
	wf := types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	t.Run("massless bucket", func(t *testing.T) {
		rows := []map[string]float64{
			{"x1": 1, "x2": 1, "g": 1, "w": 1}, {"x1": 2, "x2": 3, "g": 1, "w": 2},
			{"x1": 5, "x2": 5, "g": 2, "w": 0}, {"x1": 4, "x2": 0, "g": 3, "w": 1},
		}
		spec := distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}}`)
		spec.Weight = wf
		res, _ := runDistance(t, schema, spec, rows, nil)
		w := findDistanceWarning(res, errors.PULSE_MATRIX_INSUFFICIENT_N)
		if w == nil || w.Details["scope"] != "buckets" || fmt.Sprint(w.Details["buckets"]) != "[2]" {
			t.Fatalf("warning %+v", res.Warnings)
		}
		if !math.IsNaN(res.Primary.Values[0][1]) || !math.IsNaN(res.Primary.Values[1][1]) || math.IsNaN(res.Primary.Values[0][2]) {
			t.Errorf("primary %v: bucket 2 null, the rest defined", res.Primary.Values)
		}
	})
	t.Run("cosine zero length", func(t *testing.T) {
		rows := []map[string]float64{{"x1": 0, "x2": 0, "g": 1, "w": 1}, {"x1": 1, "x2": 1, "g": 2, "w": 1}, {"x1": 2, "x2": 0, "g": 3, "w": 1}}
		res, _ := runDistance(t, schema, distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "metric": "cosine"}`), rows, nil)
		v := res.Primary.Values
		if !math.IsNaN(v[0][1]) || !math.IsNaN(v[0][0]) || v[1][1] != 0 {
			t.Errorf("cosine %v", v)
		}
		distNear(t, "cos(2,3)", v[1][2], 1-1/math.Sqrt2)
	})
	t.Run("pooled undefined", func(t *testing.T) {
		rows := []map[string]float64{{"x1": 0, "x2": 1, "g": 1, "w": 1}, {"x1": 1, "x2": 1, "g": 2, "w": 1}}
		res, _ := runDistance(t, schema, distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "metric": "mahalanobis"}`), rows, nil)
		w := findDistanceWarning(res, errors.PULSE_MATRIX_INSUFFICIENT_N)
		if w == nil || w.Details["scope"] != "pooled" || !math.IsNaN(res.Primary.Values[0][1]) || !math.IsNaN(res.Primary.Values[0][0]) {
			t.Errorf("result %+v", res)
		}
	})
	t.Run("pooled singular", func(t *testing.T) {
		var rows []map[string]float64
		for i := range 12 {
			x := float64(i)
			rows = append(rows, map[string]float64{"x1": x, "x2": 2*x + 1, "g": float64(1 + i%3), "w": 1})
		}
		res, _ := runDistance(t, schema, distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "metric": "mahalanobis"}`), rows, nil)
		w := findDistanceWarning(res, errors.PULSE_MATRIX_SINGULAR)
		if w == nil || w.Details["reason"] != "pooled_covariance" || !math.IsNaN(res.Primary.Values[0][1]) {
			t.Errorf("result %+v", res)
		}
		if euclid, _ := runDistance(t, schema, distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}}`), rows, nil); len(euclid.Warnings) != 0 || math.IsNaN(euclid.Primary.Values[0][1]) {
			t.Errorf("euclidean on collinear members %+v", euclid)
		}
	})
}

func findDistanceWarning(res types.MatrixResult, code errors.Code) *types.ResponseWarning {
	for _, w := range res.Warnings {
		if w.Code == string(code) {
			return w
		}
	}
	return nil
}

// TestMatrixDistance_MergeEqualsSerial: two partitions cut at a merge
// block boundary, merged bucket by bucket, give the serial bits —
// matrix, centroids and components — and a buffered (GROUP_QUANTILE)
// between refuses to merge.
func TestMatrixDistance_MergeEqualsSerial(t *testing.T) {
	schema, _ := distanceSchema(2)
	spec := distanceSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "g"}, "metric": "mahalanobis", "standardize": true}`)
	const n = 3*4096 + 17
	recs := make([]*Record, n)
	for i := range recs {
		x := float64(i%97) + 1e3*float64(i%5)
		var nulls map[string]bool
		if i%50 == 7 {
			nulls = map[string]bool{"g": true} // null keys on both partitions
		}
		recs[i] = NewRecordWithNulls(schema, map[string]float64{"x1": x, "x2": math.Sin(float64(i)) * 7, "g": float64(1 + i%4), "w": 1}, nulls)
		recs[i].SetMergePosition(0, i)
	}
	build := func() []*matrixSlot {
		s, err := buildMatrixSlotsFor(&types.Request{Matrices: []types.MatrixSpec{spec}}, schema, nil, FullComputePlan())
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	serial := build()
	for _, r := range recs {
		if err := serial[0].UpdateRow(r, ""); err != nil {
			t.Fatal(err)
		}
	}
	a, b := build(), build()
	for i, r := range recs {
		part := a
		if i >= 2*4096 {
			part = b
		}
		if err := part[0].UpdateRow(r, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := mergeSlotSets(a, b); err != nil {
		t.Fatalf("merge: %v", err)
	}
	want, wc, err := finalizeMatrixSlots(serial)
	if err != nil {
		t.Fatal(err)
	}
	got, gc, err := finalizeMatrixSlots(a)
	if err != nil {
		t.Fatal(err)
	}
	wj, _ := json.Marshal(want)
	gj, _ := json.Marshal(got)
	if string(wj) != string(gj) {
		t.Errorf("merged %s\nserial %s", gj, wj)
	}
	if fmt.Sprint(wc) != fmt.Sprint(gc) {
		t.Errorf("components merged %+v serial %+v", gc, wc)
	}

	q := distanceSpec(`{"between": {"type": "GROUP_QUANTILE", "field": "x1"}}`)
	if q.Mergeable() || q.Streamable() {
		t.Fatal("a GROUP_QUANTILE between must be buffered and not mergeable")
	}
	qa, err := buildMatrixSlotsFor(&types.Request{Matrices: []types.MatrixSpec{q}}, schema, nil, FullComputePlan())
	if err != nil {
		t.Fatal(err)
	}
	qb, _ := buildMatrixSlotsFor(&types.Request{Matrices: []types.MatrixSpec{q}}, schema, nil, FullComputePlan())
	if err := mergeSlotSets(qa, qb); err == nil {
		t.Error("buffered MAT_DISTANCE slots merged")
	}
}

// TestMatrixDistance_ProjectionReadsBetween: projected decode keeps the
// columns a MAT_DISTANCE params.between reads — a field grouper's field,
// a k-means between's inline fields — beside the members; left out,
// every row would read a null key and land in no bucket.
func TestMatrixDistance_ProjectionReadsBetween(t *testing.T) {
	schema, _ := distanceSchema(3)
	for _, c := range []struct {
		params string
		want   []string
	}{
		{`{"between": {"type": "GROUP_CATEGORY", "field": "g"}}`, []string{"g"}},
		{`{"between": {"type": "GROUP_KMEANS", "fields": ["x3", "w"], "params": {"k": 2}}}`, []string{"x3", "w"}},
	} {
		got := NeededFields(&types.Request{Matrices: []types.MatrixSpec{distanceSpec(c.params)}}, schema, nil)
		for _, f := range append([]string{"x1", "x2"}, c.want...) {
			if !got.Has(f) {
				t.Errorf("%s: projection lacks %s", c.params, f)
			}
		}
	}
}
