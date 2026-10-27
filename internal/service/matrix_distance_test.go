package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// matrix_distance_test.go is MAT_DISTANCE end to end over the grouped
// matrix cohort (g.pulse: categorical region a..d, a set column, x1..x3,
// a weight w): GROUP_CATEGORY and GROUP_KMEANS as the between grouper,
// predict against the run, the streaming and buffered arms, projected
// decode reaching the between columns, and the weight-kind refusal of a
// k-means between. The numbers are pinned against R in
// internal/processing (TestMatrixDistance_MatchesROracle).

func distanceReq(spec types.MatrixSpec) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: "g.pulse"}, Matrices: []types.MatrixSpec{spec}}
}

func distanceSvcSpec(params string) types.MatrixSpec {
	return types.MatrixSpec{Name: "d", Type: types.MAT_DISTANCE, Fields: []string{"x1", "x3"}, Params: json.RawMessage(params)}
}

// TestMatrixDistance_PredictMatchesRuntime: with GROUP_CATEGORY (the
// region dictionary bounds k) and GROUP_KMEANS (k from params.k) as
// between, predict's shape, empty axis, streamability and mergeability
// agree with the run, and the run's buckets are the grouper's own keys
// in its emission order — which also proves projected decode read the
// between columns (a missing region column would put every row in no
// bucket).
func TestMatrixDistance_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	cfg := groupedMatrixCohort(t, 2*linalg.MergeBlockSize+31)
	data, err := afero.ReadFile(cfg.Fs(), "g.pulse")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name       string
		params     string
		keys       []string
		streamable bool
	}{
		{"category", `{"between": {"type": "GROUP_CATEGORY", "field": "region"}}`, []string{"a", "b", "c", "d"}, true},
		{"kmeans", `{"between": {"type": "GROUP_KMEANS", "fields": ["x3", "w"], "params": {"k": 3}}, "metric": "mahalanobis"}`, []string{"cluster_1", "cluster_2", "cluster_3"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := distanceReq(distanceSvcSpec(c.params))
			env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			if len(env.Errors) != 0 {
				t.Fatalf("predict errors %v", env.Errors)
			}
			mp := env.Data.(*descriptor.PredictResult).Matrices[0]
			k := len(c.keys)
			if mp.Shape != [2]int{k, k} || len(mp.AxisKeys) != 0 || mp.Streamable != c.streamable || mp.Mergeable != c.streamable {
				t.Errorf("predict %+v", mp)
			}
			res := processMatrices(t, cfg, req)
			m := res.Matrices[0]
			if !reflect.DeepEqual(m.Primary.RowKeys, c.keys) || !reflect.DeepEqual(m.Primary.ColumnKeys, c.keys) {
				t.Fatalf("runtime keys %v / %v, want %v", m.Primary.RowKeys, m.Primary.ColumnKeys, c.keys)
			}
			if got := [2]int{len(m.Primary.RowKeys), len(m.Primary.ColumnKeys)}; got != mp.Shape {
				t.Errorf("runtime shape %v, predict %v", got, mp.Shape)
			}
			comp := res.Components.Matrices[0]
			if comp.Operator["buckets"] != k || comp.Operator["n_null_key"] != 0 {
				t.Errorf("components %+v", comp)
			}
		})
	}
}

// TestMatrixDistance_StreamingEqualsBuffered: a streamable between
// (GROUP_CATEGORY) gives the same bits on the streaming arm and the
// buffered arm (an AGG_MEDIAN beside it), weighted and standardized.
func TestMatrixDistance_StreamingEqualsBuffered(t *testing.T) {
	t.Parallel()

	cfg := groupedMatrixCohort(t, 2*linalg.MergeBlockSize+31)
	spec := distanceSvcSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "region"}, "standardize": true, "metric": "mahalanobis"}`)
	spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	streamReq := distanceReq(spec)
	bufReq := distanceReq(spec)
	bufReq.Aggregations = []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x3", Label: "med"}}
	schema := groupedMatrixSchema(t)
	if !processing.CanStreamRequest(streamReq, schema) || processing.CanStreamRequest(bufReq, schema) {
		t.Fatal("arms not exercised")
	}
	a := mustJSON(t, processMatrices(t, cfg, streamReq).Matrices)
	b := mustJSON(t, processMatrices(t, cfg, bufReq).Matrices)
	if a != b {
		t.Errorf("streamed %s\nbuffered %s", a, b)
	}

	// A buffered between routes the whole request buffered.
	km := distanceReq(distanceSvcSpec(`{"between": {"type": "GROUP_KMEANS", "fields": ["x2"], "params": {"k": 2}}}`))
	if processing.CanStreamRequest(km, schema) {
		t.Error("a GROUP_KMEANS between streams")
	}
}

// TestMatrixDistance_KMeansBetweenFrequencyOnly: a GROUP_KMEANS between
// refuses a probability weight (PULSE_WEIGHT_UNSUPPORTED naming the
// between), at predict and at run time alike; under a frequency weight
// it runs.
func TestMatrixDistance_KMeansBetweenFrequencyOnly(t *testing.T) {
	t.Parallel()

	cfg := groupedMatrixCohort(t, 300)
	spec := distanceSvcSpec(`{"between": {"type": "GROUP_KMEANS", "fields": ["x2"], "params": {"k": 2}}}`)
	spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	_, err := New(cfg).Process(context.Background(), distanceReq(spec))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED || ce.Details["between"] != "GROUP_KMEANS" {
		t.Fatalf("probability-weighted k-means between: %v", err)
	}
	cat := distanceSvcSpec(`{"between": {"type": "GROUP_CATEGORY", "field": "region"}}`)
	cat.Weight = spec.Weight
	if _, err := New(cfg).Process(context.Background(), distanceReq(cat)); err != nil {
		t.Errorf("probability-weighted category between refused: %v", err)
	}
}

// TestMatrixDistance_VectorBetweenIsReferenced: a vector named only by a
// GROUP_KMEANS between is referenced (no PULSE_VECTOR_UNREFERENCED) and
// resolves to the same clusters as its inline fields; rows the buffered
// between keys to no cluster are counted in n_null_key.
func TestMatrixDistance_VectorBetweenIsReferenced(t *testing.T) {
	t.Parallel()

	cfg := groupedMatrixCohort(t, 300)
	data, err := afero.ReadFile(cfg.Fs(), "g.pulse")
	if err != nil {
		t.Fatal(err)
	}
	named := distanceReq(distanceSvcSpec(`{"between": {"type": "GROUP_KMEANS", "vector": "seg", "params": {"k": 2}}}`))
	named.Vectors = []types.VectorSpec{{Name: "seg", Fields: []string{"x2", "w"}}}
	env := descx.Predict(bytes.NewReader(data), named, &descx.PredictOptions{})
	if len(env.Errors) != 0 {
		t.Fatalf("predict errors %v", env.Errors)
	}
	for _, w := range env.Warnings {
		if w.Code == string(errors.PULSE_VECTOR_UNREFERENCED) {
			t.Errorf("vector named by the between reported unreferenced: %v", w)
		}
	}
	inline := distanceReq(distanceSvcSpec(`{"between": {"type": "GROUP_KMEANS", "fields": ["x2", "w"], "params": {"k": 2}}}`))
	a, b := processMatrices(t, cfg, named).Matrices[0], processMatrices(t, cfg, inline).Matrices[0]
	if mustJSON(t, a) != mustJSON(t, b) {
		t.Errorf("named %s\ninline %s", mustJSON(t, a), mustJSON(t, b))
	}
	if math.IsNaN(a.Primary.Values[0][1]) {
		t.Error("undefined cluster distance")
	}
	// x2 carries nulls: the buffered k-means between places those rows in
	// no cluster, and they are counted, not folded.
	if n, _ := processMatrices(t, cfg, inline).Components.Matrices[0].Operator["n_null_key"].(int); n <= 0 {
		t.Errorf("buffered n_null_key = %d, want the rows with a null x2", n)
	}
}
