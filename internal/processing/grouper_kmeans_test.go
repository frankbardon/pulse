package processing

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// kmSchema: two numeric members, a nullable third, a frequency weight
// and a categorical row axis.
func kmSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	for _, l := range []string{"a", "b"} {
		_, _ = dict.Add(l)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "z", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "w", Type: encoding.FieldTypeU32},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
	}}
}

// kmBlobs returns rows of blobs[i] points around centre i (well
// separated), sizes as given, in a seeded shuffled order.
func kmBlobs(sizes []int, seed uint64) [][2]float64 {
	r := rand.New(rand.NewPCG(seed, 7))
	var out [][2]float64
	for i, n := range sizes {
		cx, cy := float64(i*40), float64((i%3)*25)
		for range n {
			out = append(out, [2]float64{cx + float64(r.IntN(100))/50, cy + float64(r.IntN(100))/50})
		}
	}
	r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func kmRecords(schema *encoding.Schema, pts [][2]float64) []*Record {
	recs := make([]*Record, len(pts))
	for i, p := range pts {
		recs[i] = NewRecord(schema, map[string]float64{"x": p[0], "y": p[1], "z": 1, "w": 1, "g": float64(i % 2)})
	}
	return recs
}

func kmRequest(params string) *types.Request {
	return &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_KMEANS, Fields: []string{"x", "y"}, Params: json.RawMessage(params)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
	}
}

func kmRun(t *testing.T, req *types.Request, recs []*Record) *types.Response {
	t.Helper()
	resp, err := NewProcessor(kmSchema()).Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp
}

// TestKMeans_InvariantUnderRowOrder: the same multiset of rows in any
// order gives byte-identical Data and Components (the labels are a
// function of the member tuples alone).
func TestKMeans_InvariantUnderRowOrder(t *testing.T) {
	pts := kmBlobs([]int{40, 25, 12}, 1)
	base := kmRun(t, kmRequest(`{"k":3,"seed":11}`), kmRecords(kmSchema(), pts))
	want, _ := json.Marshal(base)
	for s := uint64(2); s < 6; s++ {
		perm := append([][2]float64(nil), pts...)
		rand.New(rand.NewPCG(s, s)).Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
		got, _ := json.Marshal(kmRun(t, kmRequest(`{"k":3,"seed":11}`), kmRecords(kmSchema(), perm)))
		if string(got) != string(want) {
			t.Fatalf("shuffle %d changed the response:\n got  %s\n want %s", s, got, want)
		}
	}
	if base.Data[0]["n"] != int64(40) && base.Data[0]["n"] != 40.0 && fmt.Sprint(base.Data[0]["n"]) != "40" {
		t.Fatalf("cluster_1 = %v rows, want the 40-row blob", base.Data[0]["n"])
	}
}

// TestKMeans_K12EmitsSizeOrder: k = 12 emits cluster_1..cluster_12 in
// that order (never alphabetical: cluster_10 after cluster_9) with
// strictly descending sizes.
func TestKMeans_K12EmitsSizeOrder(t *testing.T) {
	sizes := make([]int, 12)
	for i := range sizes {
		sizes[i] = 5 + i
	}
	resp := kmRun(t, kmRequest(`{"k":12}`), kmRecords(kmSchema(), kmBlobs(sizes, 3)))
	if len(resp.Data) != 12 {
		t.Fatalf("%d rows, want 12", len(resp.Data))
	}
	prev := math.Inf(1)
	for i, row := range resp.Data {
		if got, want := row["GROUP_KMEANS_x"], "cluster_"+strconv.Itoa(i+1); got != want {
			t.Fatalf("row %d key = %v, want %s", i, got, want)
		}
		n, _ := strconv.ParseFloat(fmt.Sprint(row["n"]), 64)
		if n >= prev {
			t.Fatalf("row %d size %v not below %v", i, n, prev)
		}
		prev = n
	}
}

// TestKMeans_LloydFixedPoint: every clustered point is nearest its own
// centroid and every centroid is its cluster's mean (clustering space).
func TestKMeans_LloydFixedPoint(t *testing.T) {
	pts := kmBlobs([]int{30, 30, 30}, 9)
	// Blur the blobs so the partition is not trivially separable.
	for i := range pts {
		pts[i][0] /= 8
	}
	resp := kmRun(t, kmRequest(`{"k":3,"standardize":false}`), kmRecords(kmSchema(), pts))
	op := resp.Components.Groupers[0].Operator
	if op["converged"] != true {
		t.Fatalf("not converged: %v", op)
	}
	buckets := op["buckets"].([]map[string]any)
	cents := make([][]float64, len(buckets))
	for i, b := range buckets {
		cents[i] = b["centroid_standardized"].([]float64)
	}
	sums := make([][2]float64, len(cents))
	counts := make([]float64, len(cents))
	for _, p := range pts {
		c := nearestCentroid([]float64{p[0], p[1]}, cents)
		sums[c][0] += p[0]
		sums[c][1] += p[1]
		counts[c]++
	}
	for c := range cents {
		for j := range 2 {
			if m := sums[c][j] / counts[c]; math.Abs(m-cents[c][j]) > 1e-9 {
				t.Fatalf("cluster %d member %d: mean %v, centroid %v", c, j, m, cents[c][j])
			}
		}
		if counts[c] != buckets[c]["size"].(float64) {
			t.Fatalf("cluster %d: %v nearest points, size %v", c, counts[c], buckets[c]["size"])
		}
	}
}

// TestKMeans_NullMemberToNNull: a row null on a member joins no
// cluster and is counted in n_null.
func TestKMeans_NullMemberToNNull(t *testing.T) {
	schema := kmSchema()
	recs := kmRecords(schema, kmBlobs([]int{10, 10}, 4))
	recs = append(recs, NewRecordWithNulls(schema, map[string]float64{"x": 1}, map[string]bool{"z": true}))
	req := kmRequest(`{"k":2}`)
	req.Groups[0].Fields = []string{"x", "y", "z"}
	resp := kmRun(t, req, recs)
	g := resp.Components.Groupers[0]
	if g.TotalN != 20 || g.NNull != 1 {
		t.Fatalf("total_n %d n_null %d, want 20 / 1", g.TotalN, g.NNull)
	}
}

// TestKMeans_KAboveDistinctRefused: k above the distinct member tuples
// is refused (PROCESSING_CONFIG, reason k_exceeds_distinct_points).
func TestKMeans_KAboveDistinctRefused(t *testing.T) {
	schema := kmSchema()
	recs := kmRecords(schema, [][2]float64{{1, 1}, {1, 1}, {2, 2}, {2, 2}})
	_, err := NewProcessor(schema).Process(context.Background(), kmRequest(`{"k":3}`), NewSliceIterator(recs))
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PROCESSING_CONFIG || ce.Details["reason"] != "k_exceeds_distinct_points" || ce.Details["distinct_points"] != 2 {
		t.Fatalf("err = %v, want PROCESSING_CONFIG k_exceeds_distinct_points over 2 distinct points", err)
	}
	if _, err := NewProcessor(schema).Process(context.Background(), kmRequest(`{"k":2}`), NewSliceIterator(recs)); err != nil {
		t.Fatalf("k = distinct points refused: %v", err)
	}
}

// TestKMeans_FrequencyWeightIsReplication: a row of frequency weight w
// clusters exactly as w copies of the row (one canonical point of mass
// w either way): identical labels and components.
func TestKMeans_FrequencyWeightIsReplication(t *testing.T) {
	schema := kmSchema()
	pts := kmBlobs([]int{15, 9, 6}, 5)
	var weighted, replicated []*Record
	for i, p := range pts {
		w := float64(1 + i%3)
		weighted = append(weighted, NewRecord(schema, map[string]float64{"x": p[0], "y": p[1], "w": w}))
		for range int(w) {
			replicated = append(replicated, NewRecord(schema, map[string]float64{"x": p[0], "y": p[1], "w": 1}))
		}
	}
	req := func(weight bool) *types.Request {
		r := &types.Request{Groups: []*types.Group{{Type: types.GROUP_KMEANS, Fields: []string{"x", "y"}, Params: json.RawMessage(`{"k":3}`)}}}
		if weight {
			r.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
		}
		r.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}
		return r
	}
	a := kmRun(t, req(true), weighted).Components.Groupers[0].Operator
	b := kmRun(t, req(false), replicated).Components.Groupers[0].Operator
	for _, key := range []string{"total_ss", "within_ss", "between_ss", "best_init"} {
		if !reflect.DeepEqual(a[key], b[key]) {
			t.Errorf("%s: weighted %v, replicated %v", key, a[key], b[key])
		}
	}
	ab, bb := a["buckets"].([]map[string]any), b["buckets"].([]map[string]any)
	for i := range ab {
		for _, key := range []string{"size", "within_ss", "centroid", "centroid_standardized"} {
			if !reflect.DeepEqual(ab[i][key], bb[i][key]) {
				t.Errorf("bucket %d %s: weighted %v, replicated %v", i, key, ab[i][key], bb[i][key])
			}
		}
	}
}

// TestKMeans_NotConvergedWarns: a best start capped at max_iter raises
// PULSE_MATRIX_NOT_CONVERGED (solver kmeans_lloyd) and still groups.
func TestKMeans_NotConvergedWarns(t *testing.T) {
	resp := kmRun(t, kmRequest(`{"k":3,"max_iter":1}`), kmRecords(kmSchema(), kmBlobs([]int{20, 20, 20}, 6)))
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(errors.PULSE_MATRIX_NOT_CONVERGED) ||
		resp.Warnings[0].Details["solver"] != "kmeans_lloyd" {
		t.Fatalf("warnings = %+v, want one PULSE_MATRIX_NOT_CONVERGED kmeans_lloyd", resp.Warnings)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("%d rows, want 3", len(resp.Data))
	}
	if ok := kmRun(t, kmRequest(`{"k":3}`), kmRecords(kmSchema(), kmBlobs([]int{20, 20, 20}, 6))); len(ok.Warnings) != 0 {
		t.Fatalf("converged run warned: %+v", ok.Warnings)
	}
}

// TestKMeans_CrosstabFitsTheWholeSet: as a crosstab column axis the
// clusters are fitted once over every filtered row — never refitted
// inside each row bucket — and emitted in size order.
func TestKMeans_CrosstabFitsTheWholeSet(t *testing.T) {
	schema := kmSchema()
	pts := kmBlobs([]int{30, 18, 9}, 8)
	// The row axis splits the blobs (g = a: the first blob only), so a
	// per-row-bucket refit would cut that blob into three clusters.
	recs := kmRecords(schema, pts)
	for i, p := range pts {
		g := 1.0
		if p[0] < 20 {
			g = 0
		}
		recs[i] = NewRecord(schema, map[string]float64{"x": p[0], "y": p[1], "z": 1, "w": 1, "g": g})
	}
	grouped := kmRun(t, kmRequest(`{"k":3}`), kmRecords(schema, pts))
	req := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Columns: []*types.Group{{Type: types.GROUP_KMEANS, Fields: []string{"x", "y"}, Params: json.RawMessage(`{"k":3}`)}},
		Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x"},
	}}
	resp, err := NewProcessor(schema).RunCrosstab(context.Background(), req, recs)
	if err != nil {
		t.Fatalf("RunCrosstab: %v", err)
	}
	m := resp.Crosstab.Matrix
	if got := fmt.Sprint(m.ColumnKeys); got != "[[cluster_1] [cluster_2] [cluster_3]]" {
		t.Fatalf("column keys %s, want size order", got)
	}
	for c := range 3 {
		var sum float64
		for r := range m.Cells {
			sum += m.Cells[r][c].Scalar()
		}
		want, _ := strconv.ParseFloat(fmt.Sprint(grouped.Data[c]["n"]), 64)
		if sum != want {
			t.Fatalf("column cluster_%d holds %v rows across row buckets, the whole-set fit %v", c+1, sum, want)
		}
	}
}

// TestKMeans_PrepareResolvesNamedVectorAndKeyColumn: a named vector
// resolves onto the slot copy and the key column is GROUP_KMEANS_<vector>.
func TestKMeans_PrepareResolvesNamedVectorAndKeyColumn(t *testing.T) {
	req := kmRequest(`{"k":2}`)
	req.Vectors = []types.VectorSpec{{Name: "xy", Fields: []string{"x", "y"}}}
	req.Groups[0].Fields, req.Groups[0].Vector = nil, "xy"
	resp := kmRun(t, req, kmRecords(kmSchema(), kmBlobs([]int{8, 5}, 2)))
	if _, ok := resp.Data[0]["GROUP_KMEANS_xy"]; !ok {
		t.Fatalf("row %v lacks the GROUP_KMEANS_xy key column", resp.Data[0])
	}
	if req.Groups[0].Field != "" || req.Groups[0].Vector != "xy" {
		t.Fatal("prepare mutated the caller's request")
	}
}

// kmeansOracle is one mv_kmeans.json case (scripts/reference/gen_kmeans.R).
type kmeansOracle struct {
	Name          string      `json:"name"`
	Kind          string      `json:"kind"`
	K             int         `json:"k"`
	Standardize   bool        `json:"standardize"`
	Weight        string      `json:"weight"`
	Rows          [][]float64 `json:"rows"`
	Weights       []float64   `json:"weights"`
	Sizes         []float64   `json:"sizes"`
	WithinSS      []float64   `json:"within_ss"`
	TotalSS       float64     `json:"total_ss"`
	BetweenSS     float64     `json:"between_ss"`
	CentroidsStd  [][]float64 `json:"centroids_standardized"`
	Centroids     [][]float64 `json:"centroids"`
	Clusters      []float64   `json:"clusters"`
	RBestWithinSS *float64    `json:"r_best_within_ss"`
}

func kmClose(got, want float64) bool {
	return math.Abs(got-want) <= 1e-9*math.Max(1, math.Max(math.Abs(got), math.Abs(want)))
}

// TestKMeans_MatchesROracle: on separable blobs every seed reaches the
// partition stats::kmeans reaches from fixed starts — sizes, within-SS,
// total / between SS, both centroid forms and every row's cluster
// number — unweighted, unstandardized and frequency-weighted (R on the
// rep()-expanded rows); on attitude (not separable) the within-SS is no
// worse than R's best of 200 starts.
func TestKMeans_MatchesROracle(t *testing.T) {
	doc := loadReference[kmeansOracle](t, "mv_kmeans")
	kinds := map[string]bool{}
	for _, c := range doc.Cases {
		kinds[c.Kind+"/"+c.Weight] = true
		t.Run(c.Name, func(t *testing.T) {
			p := len(c.Rows[0])
			fields := make([]encoding.Field, 0, p+1)
			members := make([]string, p)
			for j := range p {
				members[j] = "x" + strconv.Itoa(j+1)
				fields = append(fields, encoding.Field{Name: members[j], Type: encoding.FieldTypeF64})
			}
			fields = append(fields, encoding.Field{Name: "w", Type: encoding.FieldTypeU32})
			schema := &encoding.Schema{Fields: fields}
			recs := make([]*Record, len(c.Rows))
			for i, row := range c.Rows {
				vals := map[string]float64{"w": 1}
				for j, v := range row {
					vals[members[j]] = v
				}
				if c.Weights != nil {
					vals["w"] = c.Weights[i]
				}
				recs[i] = NewRecord(schema, vals)
			}
			nInit := 10
			if c.RBestWithinSS != nil {
				nInit = 200 // R's nstart: Lloyd from k-means++ starts, as many as R tried
			}
			for _, seed := range []int{0, 3, 99} {
				grp := &types.Group{Type: types.GROUP_KMEANS, Field: "cl", Fields: members,
					Params: json.RawMessage(fmt.Sprintf(`{"k":%d,"seed":%d,"n_init":%d,"standardize":%v}`, c.K, seed, nInit, c.Standardize))}
				if c.Weights != nil {
					grp.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
				}
				g, err := newKMeansGrouper(grp, schema)
				if err != nil {
					t.Fatal(err)
				}
				buckets, err := g.Group(recs, "")
				if err != nil {
					t.Fatal(err)
				}
				op, _ := g.(MetaGrouper).Components()
				if c.RBestWithinSS != nil {
					if w := op["within_ss"].(float64); w > *c.RBestWithinSS*(1+1e-9) {
						t.Fatalf("seed %d: within-SS %.17g above R's best %.17g", seed, w, *c.RBestWithinSS)
					}
					if !kmClose(op["total_ss"].(float64), c.TotalSS) {
						t.Fatalf("total_ss %v, R %v", op["total_ss"], c.TotalSS)
					}
					continue
				}
				label := map[*Record]int{}
				for key, rs := range buckets {
					n, _ := strconv.Atoi(key[len(kmeansLabelPrefix):])
					for _, r := range rs {
						label[r] = n
					}
				}
				for i, r := range recs {
					if label[r] != int(c.Clusters[i]) {
						t.Fatalf("seed %d row %d: cluster %d, R %v", seed, i, label[r], c.Clusters[i])
					}
				}
				if !kmClose(op["total_ss"].(float64), c.TotalSS) || !kmClose(op["between_ss"].(float64), c.BetweenSS) {
					t.Fatalf("seed %d: total %v between %v, R %v %v", seed, op["total_ss"], op["between_ss"], c.TotalSS, c.BetweenSS)
				}
				for b, bucket := range op["buckets"].([]map[string]any) {
					if !kmClose(bucket["size"].(float64), c.Sizes[b]) || !kmClose(bucket["within_ss"].(float64), c.WithinSS[b]) {
						t.Fatalf("seed %d cluster %d: size %v within %v, R %v %v", seed, b+1, bucket["size"], bucket["within_ss"], c.Sizes[b], c.WithinSS[b])
					}
					for j := range p {
						if !kmClose(bucket["centroid"].([]float64)[j], c.Centroids[b][j]) ||
							!kmClose(bucket["centroid_standardized"].([]float64)[j], c.CentroidsStd[b][j]) {
							t.Fatalf("seed %d cluster %d member %d: centroid %v / %v, R %v / %v", seed, b+1, j,
								bucket["centroid"], bucket["centroid_standardized"], c.Centroids[b], c.CentroidsStd[b])
						}
					}
				}
			}
		})
	}
	for _, k := range []string{"separable/none", "separable/frequency", "best_of_starts/none"} {
		if !kinds[k] {
			t.Fatalf("vacuous oracle: no %s case (%v)", k, kinds)
		}
	}
}
