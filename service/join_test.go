package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestJoin_InnerByteEqualToManual exercises a 1:1 inner join. Left
// has rows (id=1, score=10), (id=2, score=20). Right has rows (id=1,
// bonus=100), (id=2, bonus=200). Joined sum(score+bonus) per row.
func TestJoin_InnerByteEqualToManual(t *testing.T) {
	leftSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
	rightSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "bonus", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
	leftRecs := [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(20.0)},
	}
	rightRecs := [][]uint64{
		{1, math.Float64bits(100.0)},
		{2, math.Float64bits(200.0)},
	}

	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "left.pulse", writePulseFile(t, leftSchema, leftRecs), 0o644); err != nil {
		t.Fatalf("WriteFile left: %v", err)
	}
	if err := afero.WriteFile(cfg.Fs(), "right.pulse", writePulseFile(t, rightSchema, rightRecs), 0o644); err != nil {
		t.Fatalf("WriteFile right: %v", err)
	}
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "left.pulse"},
		Joins: []*types.JoinSpec{
			{
				Right: "right.pulse",
				Kind:  "inner",
				As:    "r_",
				On: []types.OnPair{
					{LeftField: "id", RightField: "id"},
				},
			},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "s"},
			{Type: types.AGG_SUM, Field: "r_bonus", Label: "b"},
		},
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process join: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatalf("expected one row of aggregates: %#v", resp)
	}
	if got := resp.Data[0]["s"].(float64); !floatClose(got, 30.0, 0.001) {
		t.Errorf("sum(score) = %v, want 30", got)
	}
	if got := resp.Data[0]["b"].(float64); !floatClose(got, 300.0, 0.001) {
		t.Errorf("sum(r_bonus) = %v, want 300", got)
	}
}

func TestJoin_UnmatchedLeftDropped(t *testing.T) {
	leftSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
	rightSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "bonus", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
	leftRecs := [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(20.0)},
		{3, math.Float64bits(30.0)},
	}
	rightRecs := [][]uint64{
		{1, math.Float64bits(100.0)},
	}

	cfg := fs.NewMemMap()
	afero.WriteFile(cfg.Fs(), "left.pulse", writePulseFile(t, leftSchema, leftRecs), 0o644)
	afero.WriteFile(cfg.Fs(), "right.pulse", writePulseFile(t, rightSchema, rightRecs), 0o644)
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "left.pulse"},
		Joins: []*types.JoinSpec{
			{Right: "right.pulse", On: []types.OnPair{{LeftField: "id", RightField: "id"}}, As: "r_"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := resp.Data[0]["n"].(float64); !floatClose(got, 1.0, 0.001) {
		t.Errorf("inner join count = %v, want 1 (only id=1 matched)", got)
	}
}

func TestJoin_KindOuterRejected(t *testing.T) {
	leftSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}
	cfg := fs.NewMemMap()
	afero.WriteFile(cfg.Fs(), "left.pulse", writePulseFile(t, leftSchema, [][]uint64{{1}}), 0o644)
	afero.WriteFile(cfg.Fs(), "right.pulse", writePulseFile(t, leftSchema, [][]uint64{{1}}), 0o644)
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "left.pulse"},
		Joins: []*types.JoinSpec{
			{Right: "right.pulse", Kind: "outer", On: []types.OnPair{{LeftField: "id", RightField: "id"}}, As: "r_"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "id"},
		},
	}
	_, err := svc.Process(context.Background(), req)
	if err == nil || !errors.HasCode(err, errors.PULSE_JOIN_KIND_NOT_IMPLEMENTED) {
		t.Fatalf("expected PULSE_JOIN_KIND_NOT_IMPLEMENTED, got %v", err)
	}
}

func TestJoin_TypeMismatchRejected(t *testing.T) {
	leftSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}
	rightSchema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeDecimal128, ByteOffset: 0, CsvColumnIdx: 0, Precision: 18, Scale: 2},
		},
	}
	cfg := fs.NewMemMap()
	afero.WriteFile(cfg.Fs(), "left.pulse", writePulseFile(t, leftSchema, [][]uint64{{1}}), 0o644)

	// Build a minimal right .pulse with a single decimal record.
	rightBytes := writePulseFile(t, rightSchema, [][]uint64{})
	afero.WriteFile(cfg.Fs(), "right.pulse", rightBytes, 0o644)
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "left.pulse"},
		Joins: []*types.JoinSpec{
			{Right: "right.pulse", On: []types.OnPair{{LeftField: "id", RightField: "id"}}, As: "r_"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "id"},
		},
	}
	_, err := svc.Process(context.Background(), req)
	if err == nil || !errors.HasCode(err, errors.PULSE_JOIN_TYPE_MISMATCH) {
		t.Fatalf("expected PULSE_JOIN_TYPE_MISMATCH, got %v", err)
	}
}

func TestJoin_TooManyRejected(t *testing.T) {
	cfg := fs.NewMemMap()
	schema := testSchema()
	afero.WriteFile(cfg.Fs(), "left.pulse", writePulseFile(t, schema, testRecords()), 0o644)
	afero.WriteFile(cfg.Fs(), "right.pulse", writePulseFile(t, schema, testRecords()), 0o644)
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "left.pulse"},
		Joins: []*types.JoinSpec{
			{Right: "right.pulse", On: []types.OnPair{{LeftField: "id", RightField: "id"}}},
			{Right: "right.pulse", On: []types.OnPair{{LeftField: "id", RightField: "id"}}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "id"},
		},
	}
	_, err := svc.Process(context.Background(), req)
	if err == nil || !errors.HasCode(err, errors.PULSE_JOIN_TOO_MANY) {
		t.Fatalf("expected PULSE_JOIN_TOO_MANY, got %v", err)
	}
}

func TestJoin_FieldCollisionRejected(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}
	cfg := fs.NewMemMap()
	afero.WriteFile(cfg.Fs(), "left.pulse", writePulseFile(t, schema, [][]uint64{{1}}), 0o644)
	afero.WriteFile(cfg.Fs(), "right.pulse", writePulseFile(t, schema, [][]uint64{{1}}), 0o644)
	svc := New(cfg)

	// Without As, right.id collides with left.id.
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "left.pulse"},
		Joins: []*types.JoinSpec{
			{Right: "right.pulse", On: []types.OnPair{{LeftField: "id", RightField: "id"}}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "id"},
		},
	}
	_, err := svc.Process(context.Background(), req)
	if err == nil || !errors.HasCode(err, errors.PULSE_JOIN_FIELD_COLLISION) {
		t.Fatalf("expected PULSE_JOIN_FIELD_COLLISION, got %v", err)
	}
}
