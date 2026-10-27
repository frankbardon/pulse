package pulse_test

import (
	"bytes"
	"context"
	stderrors "errors"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// extend_record_level_test.go pins U51 E5-S10 for embedders: an
// extension that reads a categorical label through extend.Record's
// StringValue — whose signature has no error — never sees a corrupt
// code as a silent null. The view answers ("", false), latches the
// fault on the engine row, and the adapter returns ENCODING_INVALID
// (reason categorical_index_out_of_range) in place of the call's result:
// a filter, an aggregator or a feature fails the run; a tier-1 test
// fails its slot alone (PULSE_SLOT_FAILED). A window reads rendered rows,
// which E5-S8's record rows already refuse (it does not depend on the
// latch); it is pinned here so the family stays covered.

const (
	lvlFilter = types.FiltererType("FILTER_LVLX_NORTH")
	lvlAgg    = types.AggregationType("AGG_LVLX_LABELLED")
	lvlTest   = types.TestType("TEST_LVLX_LABELLED")
	lvlWindow = types.WindowType("WIN_LVLX_POS")
	lvlFeat   = types.FeatureType("FEAT_LVLX_IS_NORTH")
)

type lvlNorth struct{}

func (lvlNorth) Build(spec *types.Filterer, _ *encoding.Schema) (extend.FilterFunc, error) {
	field := spec.Field
	return func(rec extend.Record) (bool, error) {
		s, ok := rec.StringValue(field)
		return ok && s == "north", nil
	}, nil
}

// lvlLabelled counts the rows whose field has a label.
type lvlLabelled struct{}

func (lvlLabelled) Aggregate(rows extend.Rows, field string) (float64, error) {
	n := 0.0
	for i := 0; i < rows.Len(); i++ {
		if _, ok := rows.At(i).StringValue(field); ok {
			n++
		}
	}
	return n, nil
}

type lvlRowTest struct {
	field string
	n     *float64
}

func (t lvlRowTest) UpdateRow(rec extend.Record) error {
	if _, ok := rec.StringValue(t.field); ok {
		*t.n++
	}
	return nil
}

func (t lvlRowTest) Finalize() (*types.TestResult, error) {
	return &types.TestResult{Statistic: *t.n, PValue: 1, Alpha: 0.05}, nil
}

type lvlWin struct{}

func (lvlWin) Compute(rows []map[string]any, partitions [][]int, label string) error {
	for _, part := range partitions {
		for pos, i := range part {
			rows[i][label] = float64(pos)
		}
	}
	return nil
}

// lvlIsNorth is a buffered feature: 1 for a north row, 0 otherwise.
type lvlIsNorth struct{}

func (lvlIsNorth) Compute(rows extend.Rows, field string) (map[string]extend.FeatureOutput, error) {
	out := make([]float64, rows.Len())
	for i := range out {
		if s, ok := rows.At(i).StringValue(field); ok && s == "north" {
			out[i] = 1
		}
	}
	return map[string]extend.FeatureOutput{"is_north": {Values: out}}, nil
}

func lvlExtensions() pulse.Extensions {
	return pulse.Extensions{
		Filterers: []pulse.FiltererRegistration{{Name: lvlFilter, Factory: func() extend.FiltererBuilder { return lvlNorth{} }}},
		Aggregators: []pulse.AggregatorRegistration{{Name: lvlAgg,
			Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) { return lvlLabelled{}, nil }}},
		Tests: []pulse.TestRegistration{{Name: lvlTest, Tier: pulse.TestTierRow, Streamable: true,
			RowFactory: func(spec *types.Test, _ *encoding.Schema) (extend.RowTest, error) {
				return lvlRowTest{field: spec.Field, n: new(float64)}, nil
			}}},
		Windows: []pulse.WindowRegistration{{Name: lvlWindow,
			Factory: func(*types.Window, extend.WindowOptions) (extend.WindowComputer, error) { return lvlWin{}, nil }}},
		Features: []pulse.FeatureRegistration{{Name: lvlFeat,
			Factory: func(*types.Feature, *encoding.Schema) (extend.FeatureComputer, error) { return lvlIsNorth{}, nil }}},
	}
}

// lvlCohort writes rows records of {region categorical_u8 {north,
// south}, x f64} through the encoder, then patches record bad's region
// code to 7 (bad < 0: no patch).
func lvlCohort(t *testing.T, fsys afero.Fs, path string, rows, bad int) {
	t.Helper()
	dict := encoding.NewDictionary()
	_, _ = dict.Add("north")
	_, _ = dict.Add("south")
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: dict},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 1},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rows; i++ {
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, uint64(i%2)); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(float64(i)+0.5)); err != nil {
			t.Fatal(err)
		}
	}
	b := buf.Bytes()
	if bad >= 0 {
		stride := schema.RecordByteSize()
		b[len(b)-rows*stride+bad*stride] = 7
	}
	if err := afero.WriteFile(fsys, path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func wantLevelRefusal(t *testing.T, err error) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID ||
		ce.Details["field"] != "region" || ce.Details["reason"] != "categorical_index_out_of_range" {
		t.Fatalf("err = %v, want ENCODING_INVALID on region (categorical_index_out_of_range)", err)
	}
}

// TestExtendRecord_StringValueRefusesCorruptCode: every extension
// category that reads StringValue refuses the corrupt row (the clean
// twin answers); the tier-1 test fails its slot alone.
//
// Falsified by deleting the NoteReadFault call in extRecord.StringValue
// (every case then answers, the corrupt row read as a null), and by
// replacing readFault's taken fault with err in fltCore's closure (the
// filter case answers).
func TestExtendRecord_StringValueRefusesCorruptCode(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	lvlCohort(t, fsys, "clean.pulse", 32, -1)
	lvlCohort(t, fsys, "bad.pulse", 32, 5)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: lvlExtensions()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	count := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}
	fatal := map[string]func(string) *types.Request{
		"filter_streaming": func(path string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path}, Aggregations: count,
				Filterers: []*types.Filterer{{Type: lvlFilter, Field: "region"}}}
		},
		"filter_buffered": func(path string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path},
				Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "x", Label: "m"}},
				Filterers:    []*types.Filterer{{Type: lvlFilter, Field: "region"}}}
		},
		"aggregator_buffered": func(path string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path},
				Aggregations: []*types.Aggregation{{Type: lvlAgg, Field: "region", Label: "l"}}}
		},
		"feature_buffered": func(path string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path}, Aggregations: count,
				Features: []*types.Feature{{Type: lvlFeat, Field: "region"}}}
		},
		"window_rows": func(path string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path},
				Windows: []*types.Window{{Type: lvlWindow, Field: "x", Label: "pos", OrderBy: []types.OrderKey{{Field: "x"}}}}}
		},
	}
	for name, req := range fatal {
		t.Run(name, func(t *testing.T) {
			if _, err := p.Process(ctx, req("clean.pulse")); err != nil {
				t.Fatalf("clean: %v", err)
			}
			_, err := p.Process(ctx, req("bad.pulse"))
			wantLevelRefusal(t, err)
		})
	}
	t.Run("row_test_fails_alone", func(t *testing.T) {
		req := func(path string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: path}, Aggregations: count,
				Tests: []*types.Test{{Type: lvlTest, Field: "region", Label: "lt"}}}
		}
		clean, err := p.Process(ctx, req("clean.pulse"))
		if err != nil || len(clean.Tests) != 1 || clean.Tests[0].Error != nil || clean.Tests[0].Statistic != 32 {
			t.Fatalf("clean: %v %+v", err, clean)
		}
		resp, err := p.Process(ctx, req("bad.pulse"))
		if err != nil {
			t.Fatalf("the test's corrupt read failed the run, want the slot alone: %v", err)
		}
		se := resp.Tests[0].Error
		if se == nil || se.Code != string(errors.ENCODING_INVALID) || se.Details["reason"] != "categorical_index_out_of_range" {
			t.Fatalf("slot error = %+v, want ENCODING_INVALID", se)
		}
		if len(resp.Data) != 1 || resp.Data[0]["n"] == nil {
			t.Fatalf("the aggregation beside the failed test did not survive: %v", resp.Data)
		}
	})
}
