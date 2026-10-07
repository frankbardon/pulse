package descriptor

import (
	"bytes"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// sizesCohort is a single-file cohort over sizesSchema carrying n
// (zeroed) records, so predict derives its record count from the file
// length.
func sizesCohort(t *testing.T, n int) []byte {
	t.Helper()
	data := buildTestPulseFile(t, sizesSchema(t))
	r := bytes.NewReader(data)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, make([]byte, n*s.RecordByteSize())...)
}

func sizesSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: makeDictionary(t, "N", "S", "E", "W")},
		{Name: "tier", Type: encoding.FieldTypeCategoricalU8, Dictionary: makeDictionary(t, "a", "b", "c")},
		{Name: "revenue", Type: encoding.FieldTypeF64},
		{Name: "cost", Type: encoding.FieldTypeF64},
	}}
}

// sizesRequest exercises every modelled section but crosstab.
func sizesRequest(ret *types.Return) *types.Request {
	return &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "revenue"}, {Type: types.AGG_AVERAGE, Field: "cost", Label: "avg_cost"}},
		Filterers:    []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "tier", Values: []string{"a"}}},
		Tests:        []*types.Test{{Type: types.TEST_T, Field: "revenue", SplitBy: "tier"}},
		Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "revenue", Predictors: []string{"cost"}}},
		Matrices:     []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Fields: []string{"revenue", "cost"}}},
		Return:       ret,
	}
}

// predictSizes predicts req and returns the result; a request carrying
// a `return` block must report its resolved plan.
func predictSizes(t *testing.T, data []byte, req *types.Request) *descriptor.PredictResult {
	t.Helper()
	res := predictFromBytes(data, req, nil).Data.(*descriptor.PredictResult)
	if req.Return != nil && res.Return == nil {
		t.Fatalf("no return plan reported for %+v", req.Return)
	}
	return res
}

func sizeOf(res *descriptor.PredictResult, section string) (descriptor.ResponseSectionSize, bool) {
	for _, s := range res.Sizes {
		if s.Section == section {
			return s, true
		}
	}
	return descriptor.ResponseSectionSize{}, false
}

// TestPredict_Sizes_NoReturnBlock: a request with no `return` block
// still reports every modelled section, shaped equal to full, and each
// section's full size matches the same request under `return:
// standard` — the selection never moves the unshaped figure.
func TestPredict_Sizes_NoReturnBlock(t *testing.T) {
	data := sizesCohort(t, 50)
	bare := predictSizes(t, data, sizesRequest(nil))
	if bare.Return != nil {
		t.Fatalf("no return block, plan reported: %+v", bare.Return)
	}
	want := []string{"data", "metadata", "tests", "regressions", "matrices", "components"}
	var got []string
	for _, s := range bare.Sizes {
		got = append(got, s.Section)
		if s.FullBytes <= 0 || s.ShapedBytes != s.FullBytes {
			t.Errorf("no return block %s: full = %d, shaped = %d", s.Section, s.FullBytes, s.ShapedBytes)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("sections = %v, want %v", got, want)
	}
	std := predictSizes(t, data, sizesRequest(&types.Return{Preset: types.ReturnPresetStandard}))
	if len(std.Sizes) != len(bare.Sizes) {
		t.Fatalf("standard sections = %d, bare = %d", len(std.Sizes), len(bare.Sizes))
	}
	for i, s := range std.Sizes {
		if b := bare.Sizes[i]; s.Section != b.Section || s.FullBytes != b.FullBytes || s.Basis != b.Basis {
			t.Errorf("standard %+v vs bare %+v", s, b)
		}
	}

	// A refused `return` block withholds the estimates.
	res := predictFromBytes(data, sizesRequest(&types.Return{Preset: "nope"}), nil).Data.(*descriptor.PredictResult)
	if res.Sizes != nil {
		t.Errorf("refused return block, sizes reported: %+v", res.Sizes)
	}
}

// TestPredict_ReturnSizes_ShapedNeverAboveFull: every section reports a
// positive full size and a shaped size no larger, under every preset and
// custom blocks; the full preset shapes nothing.
func TestPredict_ReturnSizes_ShapedNeverAboveFull(t *testing.T) {
	data := sizesCohort(t, 50)
	blocks := []*types.Return{
		{Preset: types.ReturnPresetFull},
		{Preset: types.ReturnPresetStandard},
		{Preset: types.ReturnPresetMinimal},
		{Include: []string{"data[*].region", "tests[*].p_value", "matrices[*].primary.values"}},
		{Exclude: []string{"components.aggregations[*].groups"}},
	}
	want := []string{"data", "metadata", "tests", "regressions", "matrices", "components"}
	for _, b := range blocks {
		rp := predictSizes(t, data, sizesRequest(b))
		var got []string
		for _, s := range rp.Sizes {
			got = append(got, s.Section)
			if s.FullBytes <= 0 || s.ShapedBytes < 0 || s.ShapedBytes > s.FullBytes {
				t.Errorf("%+v %s: full = %d, shaped = %d", b, s.Section, s.FullBytes, s.ShapedBytes)
			}
			if b.Preset == types.ReturnPresetFull && s.ShapedBytes != s.FullBytes {
				t.Errorf("full preset %s: shaped %d != full %d", s.Section, s.ShapedBytes, s.FullBytes)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%+v sections = %v, want %v", b, got, want)
		}
	}

	// A partial selection shrinks a section without zeroing it.
	rp := predictSizes(t, data, sizesRequest(&types.Return{Exclude: []string{"components.aggregations[*].groups"}}))
	if s, _ := sizeOf(rp, "components"); s.ShapedBytes == 0 || s.ShapedBytes >= s.FullBytes {
		t.Errorf("components under a nested exclude: %+v", s)
	}
	rp = predictSizes(t, data, sizesRequest(&types.Return{Include: []string{"data[*].region"}}))
	if s, _ := sizeOf(rp, "data"); s.ShapedBytes == 0 || s.ShapedBytes >= s.FullBytes {
		t.Errorf("data under a one-column include: %+v", s)
	}
}

// TestPredict_ReturnSizes_ExcludedReportZero: an excluded section, and
// one a preset omits, reports shaped 0 beside its full size.
func TestPredict_ReturnSizes_ExcludedReportZero(t *testing.T) {
	data := sizesCohort(t, 50)
	rp := predictSizes(t, data, sizesRequest(&types.Return{Exclude: []string{"data", "metadata", "matrices"}}))
	for _, s := range rp.Sizes {
		excluded := s.Section == "data" || s.Section == "metadata" || s.Section == "matrices"
		switch {
		case excluded && (s.ShapedBytes != 0 || s.FullBytes == 0):
			t.Errorf("excluded %s: %+v", s.Section, s)
		case !excluded && s.ShapedBytes != s.FullBytes:
			t.Errorf("kept %s: %+v", s.Section, s)
		}
	}
	rp = predictSizes(t, data, sizesRequest(&types.Return{Preset: types.ReturnPresetMinimal}))
	for _, sec := range []string{"metadata", "components"} {
		if s, ok := sizeOf(rp, sec); !ok || s.ShapedBytes != 0 {
			t.Errorf("minimal omits %s: %+v (present %v)", sec, s, ok)
		}
	}
}

// TestPredict_ReturnSizes_Basis: the row count's basis, the record-count
// clamp, and omission when the keys depend on the data.
func TestPredict_ReturnSizes_Basis(t *testing.T) {
	ret := &types.Return{Preset: types.ReturnPresetStandard}

	// Dictionary-grouped: an upper bound, clamped to the record count.
	many := predictSizes(t, sizesCohort(t, 50), sizesRequest(ret))
	few := predictSizes(t, sizesCohort(t, 2), sizesRequest(ret))
	dm, _ := sizeOf(many, "data")
	df, _ := sizeOf(few, "data")
	head := len64("data") + 4 + 2 // the key itself
	if dm.Basis != "upper_bound" || (df.FullBytes-head)*2 != dm.FullBytes-head {
		t.Errorf("4 buckets vs 2 records: 50 records %+v, 2 records %+v", dm, df)
	}

	// Ungrouped aggregation: one row, exact.
	one := predictSizes(t, sizesCohort(t, 50), &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "revenue"}}, Return: ret,
	})
	if s, ok := sizeOf(one, "data"); !ok || s.Basis != "exact" {
		t.Errorf("ungrouped data: %+v (present %v)", s, ok)
	}

	// Data-dependent keys: data, components and matrices are omitted.
	rng := sizesRequest(ret)
	rng.Groups = []*types.Group{{Type: types.GROUP_RANGE, Field: "revenue", Interval: 10}}
	rp := predictSizes(t, sizesCohort(t, 50), rng)
	for _, sec := range []string{"data", "components", "matrices"} {
		if s, ok := sizeOf(rp, sec); ok {
			t.Errorf("unknown-basis %s reported: %+v", sec, s)
		}
	}
	if _, ok := sizeOf(rp, "metadata"); !ok {
		t.Error("metadata must still be reported")
	}

	// Crosstab: cells from axis dictionaries; a range axis omits it.
	xt := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tier"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "revenue"},
		},
		Return: ret,
	}
	rp = predictSizes(t, sizesCohort(t, 50), xt)
	if s, ok := sizeOf(rp, "crosstab"); !ok || s.Basis != "upper_bound" || s.ShapedBytes != s.FullBytes {
		t.Errorf("crosstab: %+v (present %v)", s, ok)
	}
	if _, ok := sizeOf(rp, "data"); ok {
		t.Error("a crosstab writes no data rows")
	}
	xt.Crosstab.Columns = []*types.Group{{Type: types.GROUP_RANGE, Field: "cost", Interval: 5}}
	rp = predictSizes(t, sizesCohort(t, 50), xt)
	if s, ok := sizeOf(rp, "crosstab"); ok {
		t.Errorf("range-axis crosstab reported: %+v", s)
	}

	// Components that are not computed are not sized.
	off := true
	nc := sizesRequest(ret)
	nc.DisableComponents = &off
	if s, ok := sizeOf(predictSizes(t, sizesCohort(t, 50), nc), "components"); ok {
		t.Errorf("disabled components sized: %+v", s)
	}
}

// TestPredict_ReturnSizes_ArchiveRecordCount: a row-level request over a
// shard archive is sized by the cumulative record count.
func TestPredict_ReturnSizes_ArchiveRecordCount(t *testing.T) {
	data := buildShardArchiveBytes(t, twoShardInspectSchema(t), []struct {
		Name    string
		NRecord int
	}{{"p1.pulse", 4}, {"p2.pulse", 6}})
	req := func() *types.Request {
		return &types.Request{Return: &types.Return{Exclude: []string{"metadata"}}}
	}
	rp := predictSizes(t, data, req())
	s, ok := sizeOf(rp, "data")
	if !ok || s.Basis != "upper_bound" {
		t.Fatalf("archive row-level data: %+v (present %v)", s, ok)
	}
	// One row's bytes: braces + two columns.
	row := int64(2 + len("id") + 4 + 12 + len("score") + 4 + 12)
	if s.FullBytes != len64("data")+4+2+10*row {
		t.Errorf("archive data full = %d, want 10 rows of %d", s.FullBytes, row)
	}
}

func len64(s string) int64 { return int64(len(s)) }

// TestPredict_ReturnUnresolvedIncludes: Open includes predict cannot
// resolve are listed; a data column it can match is not; none, absent.
func TestPredict_ReturnUnresolvedIncludes(t *testing.T) {
	data := sizesCohort(t, 10)
	rp := predictSizes(t, data, sizesRequest(&types.Return{
		Include: []string{"tests[*].details.effect_size", "data[*].AGG_SUM_revenue", "matrices[*].auxiliary.n"},
	}))
	want := []string{"matrices[*].auxiliary.n", "tests[*].details.effect_size"}
	if !slices.Equal(rp.Return.UnresolvedIncludes, want) {
		t.Errorf("unresolved = %v, want %v", rp.Return.UnresolvedIncludes, want)
	}
	rp = predictSizes(t, data, sizesRequest(&types.Return{Include: []string{"data[*].AGG_SUM_revenue", "metadata"}}))
	if rp.Return.UnresolvedIncludes != nil {
		t.Errorf("resolvable includes listed: %v", rp.Return.UnresolvedIncludes)
	}
	// Under a crosstab the data column set is open: listed.
	xt := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tier"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "revenue"},
		},
		Return: &types.Return{Include: []string{"data[*].anything"}},
	}
	if rp := predictSizes(t, data, xt); !slices.Equal(rp.Return.UnresolvedIncludes, []string{"data[*].anything"}) {
		t.Errorf("crosstab data include: %v", rp.Return.UnresolvedIncludes)
	}
}
