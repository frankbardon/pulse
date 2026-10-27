package service

import (
	"archive/zip"
	"bytes"
	"context"
	stderrors "errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// categorical_index_test.go pins U51 E5-S8: a categorical cell whose
// stored index addresses no entry of its (widened) dictionary — or a set
// cell selecting a bit past its dictionary — is refused with a coded
// ENCODING_INVALID on every arm, never minted as a numeric-key group,
// an "" label or a member-dropping set key. Every fixture is written
// through the encoder (writeSinglePulseNulls: WriteHeader / WriteSchema
// / WriteFieldValue) with VALID codes, then one byte of one record is
// patched — the shape a torn write or a bit flip leaves behind.

const (
	ciRegionOff = 0 // categorical_u8 {north, south}
	ciTagsOff   = 1 // set_u8 {VISA, MC, AMEX}
	ciSegOff    = 2 // nullable categorical_u8, EMPTY dictionary, always null
	ciStride    = 12
)

func ciDict(entries ...string) *encoding.Dictionary {
	d := encoding.NewDictionary()
	for _, e := range entries {
		_, _ = d.Add(e)
	}
	return d
}

// ciSchema: region, tags, seg (nullable, every row null over the
// placeholder 0 — an index its empty dictionary does not hold, which
// must NOT be refused: a null's placeholder is not data) and x.
func ciSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: ciRegionOff, Dictionary: ciDict("north", "south")},
		{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: ciTagsOff, Dictionary: ciDict("VISA", "MC", "AMEX")},
		{Name: "seg", Type: encoding.FieldTypeCategoricalU8, ByteOffset: ciSegOff, Nullable: true, Dictionary: encoding.NewDictionary()},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 3},
	}}
}

// ciFile writes rows valid records through the encoder, then patches
// byte off of record bad to v (bad < 0: no patch).
func ciFile(t *testing.T, rows, bad, off int, v byte) []byte {
	t.Helper()
	schema := ciSchema()
	if got := schema.RecordByteSize(); got != ciStride {
		t.Fatalf("stride %d, want %d", got, ciStride)
	}
	recs := make([][]uint64, rows)
	nulls := make([][]bool, rows)
	for i := range recs {
		tags := uint64(1)
		if i%3 == 0 {
			tags |= 2
		}
		recs[i] = []uint64{uint64(i % 2), tags, 0, math.Float64bits(float64(i%17) + 0.5)}
		nulls[i] = []bool{false, false, true, false}
	}
	b := writeSinglePulseNulls(t, schema, recs, nulls)
	if bad >= 0 {
		at := len(b) - rows*ciStride + bad*ciStride + off
		b[at] = v
	}
	return b
}

// ciArchive is a two-shard archive whose SECOND shard carries the patch.
func ciArchive(t *testing.T, rows, bad, off int, v byte) []byte {
	t.Helper()
	var doc bytes.Buffer
	if err := encx.WriteSchemaDoc(&doc, ciSchema(), uint64(2*rows), 2); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range []struct {
		name    string
		payload []byte
	}{
		{encx.ReservedSchemaName, doc.Bytes()},
		{"s0.pulse", ciFile(t, rows, -1, 0, 0)},
		{"s1.pulse", ciFile(t, rows, bad, off, v)},
	} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ciEnv writes the cohorts to a temp OS directory (mappable, so the
// parallel decode arm engages) and returns a service over it.
func ciEnv(t *testing.T, files map[string][]byte) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	for name, b := range files {
		if err := afero.WriteFile(osFs, filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	l := limits.Defaults()
	l.MaxEstimatedMemory = 1 << 50
	svc.SetLimits(l)
	return svc, dir
}

// wantDictionaryRefusal asserts err is the coded E5-S8 refusal.
func wantDictionaryRefusal(t *testing.T, err error, field, reason string) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("err = %v, want a coded ENCODING_INVALID refusal", err)
	}
	if ce.Code != errors.ENCODING_INVALID || ce.Details["field"] != field || ce.Details["reason"] != reason {
		t.Fatalf("err = %s %v, want ENCODING_INVALID on %q (%s)", ce.Code, ce.Details, field, reason)
	}
}

func ciGrouped(path string, grp types.GroupType, field string, agg types.AggregationType) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: path},
		Groups:       []*types.Group{{Type: grp, Field: field}},
		Aggregations: []*types.Aggregation{{Type: agg, Field: "x", Label: "a"}},
	}
}

// TestCorruptCategorical_RefusedOnEveryArm: GROUP_CATEGORY over a
// corrupt region index (and GROUP_SET_VALUE / GROUP_SET_PER_ELEMENT over
// a set bit past the dictionary) refuses with ENCODING_INVALID on the
// streaming, buffered, parallel-decode, shard-parallel, serial-archive
// and fused-crosstab arms — the arm each case names is asserted from the
// run's ExecInfo, so no case silently compares serial with serial. The
// clean twin of every case succeeds, and the always-null seg column
// (placeholder 0 over an empty dictionary) never trips the refusal.
//
// Falsified by deleting the encx.CategoricalIndexFault check in
// categoryGrouper.KeyFor (every categorical case then answers with a
// "7" group) and the encx.SetMemberFault check in setValueGrouper.KeyFor
// / setPerElementGrouper.KeysForRow (the set cases then answer).
func TestCorruptCategorical_RefusedOnEveryArm(t *testing.T) {
	t.Parallel()

	const small = 64
	big := int(parallelDecodeRecordThreshold) + 1024
	files := map[string][]byte{
		"cat.pulse":        ciFile(t, small, 5, ciRegionOff, 7),
		"cat_clean.pulse":  ciFile(t, small, -1, 0, 0),
		"set.pulse":        ciFile(t, small, 5, ciTagsOff, 0x40),
		"big.pulse":        ciFile(t, big, big-3, ciRegionOff, 9),
		"big_clean.pulse":  ciFile(t, big, -1, 0, 0),
		"arch.pulse":       ciArchive(t, small, 7, ciRegionOff, 200),
		"arch_set.pulse":   ciArchive(t, small, 7, ciTagsOff, 0x80),
		"arch_clean.pulse": ciArchive(t, small, -1, 0, 0),
	}
	svc, dir := ciEnv(t, files)
	p := func(name string) string { return filepath.Join(dir, name) }
	crosstab := func(path string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: path}, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 5}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x"},
		}}
	}
	cases := []struct {
		name          string
		bad, clean    string
		req           func(string) *types.Request
		shard, decode int
		arm           observe.Arm
		field, reason string
	}{
		{"streaming", "cat.pulse", "cat_clean.pulse",
			func(s string) *types.Request { return ciGrouped(s, types.GROUP_CATEGORY, "region", types.AGG_SUM) },
			1, 1, observe.ArmStreaming, "region", encx.ReasonCategoricalIndexOutOfRange},
		{"buffered", "cat.pulse", "cat_clean.pulse",
			func(s string) *types.Request { return ciGrouped(s, types.GROUP_CATEGORY, "region", types.AGG_MEDIAN) },
			1, 1, observe.ArmBuffered, "region", encx.ReasonCategoricalIndexOutOfRange},
		{"parallel_decode", "big.pulse", "big_clean.pulse",
			func(s string) *types.Request { return ciGrouped(s, types.GROUP_CATEGORY, "region", types.AGG_SUM) },
			1, 4, observe.ArmParallelDecode, "region", encx.ReasonCategoricalIndexOutOfRange},
		{"shard_parallel", "arch.pulse", "arch_clean.pulse",
			func(s string) *types.Request { return ciGrouped(s, types.GROUP_CATEGORY, "region", types.AGG_SUM) },
			2, 1, observe.ArmShardParallel, "region", encx.ReasonCategoricalIndexOutOfRange},
		{"archive_serial", "arch.pulse", "arch_clean.pulse",
			func(s string) *types.Request { return ciGrouped(s, types.GROUP_CATEGORY, "region", types.AGG_SUM) },
			1, 1, observe.ArmStreaming, "region", encx.ReasonCategoricalIndexOutOfRange},
		{"fused_crosstab", "cat.pulse", "cat_clean.pulse", crosstab,
			1, 1, observe.ArmFusedCrosstab, "region", encx.ReasonCategoricalIndexOutOfRange},
		{"set_value_streaming", "set.pulse", "cat_clean.pulse",
			func(s string) *types.Request { return ciGrouped(s, types.GROUP_SET_VALUE, "tags", types.AGG_SUM) },
			1, 1, observe.ArmStreaming, "tags", encx.ReasonSetMemberOutOfRange},
		{"set_per_element_buffered", "set.pulse", "cat_clean.pulse",
			func(s string) *types.Request {
				return ciGrouped(s, types.GROUP_SET_PER_ELEMENT, "tags", types.AGG_MEDIAN)
			},
			1, 1, observe.ArmBuffered, "tags", encx.ReasonSetMemberOutOfRange},
		{"set_value_shard_parallel", "arch_set.pulse", "arch_clean.pulse",
			func(s string) *types.Request { return ciGrouped(s, types.GROUP_SET_VALUE, "tags", types.AGG_SUM) },
			2, 1, observe.ArmShardParallel, "tags", encx.ReasonSetMemberOutOfRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc.SetShardWorkers(tc.shard)
			svc.SetDecodeWorkers(tc.decode)
			defer svc.SetShardWorkers(0)
			defer svc.SetDecodeWorkers(0)
			run := func(path string) (*types.Response, observe.Arm, error) {
				ei := NewExecInfo(time.Now())
				resp, err := svc.Process(WithExecInfo(context.Background(), ei), tc.req(p(path)))
				return resp, ei.Snapshot().Arm, err
			}
			resp, arm, err := run(tc.clean)
			if err != nil {
				t.Fatalf("clean twin: %v", err)
			}
			if arm != tc.arm {
				t.Fatalf("clean twin ran arm %q, want %q", arm, tc.arm)
			}
			if resp.Crosstab == nil && len(resp.Data) == 0 {
				t.Fatal("clean twin produced no groups")
			}
			_, arm, err = run(tc.bad)
			wantDictionaryRefusal(t, err, tc.field, tc.reason)
			// The serial arms record their plan when the run finishes,
			// so a refused serial run reports none; dispatch depends on
			// the request and the worker knobs only, which the clean
			// twin has just pinned.
			if arm != tc.arm && arm != "" {
				t.Fatalf("corrupt run ran arm %q, want %q", arm, tc.arm)
			}
		})
	}

	// The always-null seg column carries placeholder 0 over an EMPTY
	// dictionary on every row: grouping by it is legal (every row null).
	t.Run("null_placeholder_is_not_data", func(t *testing.T) {
		for _, path := range []string{"cat_clean.pulse", "arch_clean.pulse"} {
			if _, err := svc.Process(context.Background(), ciGrouped(p(path), types.GROUP_CATEGORY, "seg", types.AGG_SUM)); err != nil {
				t.Fatalf("%s: grouping by an always-null categorical: %v", path, err)
			}
		}
	})
}

// TestCorruptCategorical_ChainAndJoin: a chain whose stage 0 groups by
// the corrupt column, and a join keyed on it (probe side and build
// side), refuse with the same code.
//
// Falsified by deleting the encx.CategoricalIndexFault check in
// joinKeyOf (both join cases then answer, keying the corrupt row "").
func TestCorruptCategorical_ChainAndJoin(t *testing.T) {
	t.Parallel()

	svc, dir := ciEnv(t, map[string][]byte{
		"cat.pulse":   ciFile(t, 64, 5, ciRegionOff, 7),
		"clean.pulse": ciFile(t, 64, -1, 0, 0),
	})
	p := func(name string) string { return filepath.Join(dir, name) }
	ctx := context.Background()

	chain := func(path string) *types.ChainRequest {
		return &types.ChainRequest{
			Cohort: &types.Cohort{Filename: p(path)},
			Stages: []*types.ChainStage{{Request: ciGrouped(p(path), types.GROUP_CATEGORY, "region", types.AGG_SUM)}},
		}
	}
	if _, err := svc.ProcessChain(ctx, chain("clean.pulse")); err != nil {
		t.Fatalf("clean chain: %v", err)
	}
	_, err := svc.ProcessChain(ctx, chain("cat.pulse"))
	wantDictionaryRefusal(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)

	join := func(left, right string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: p(left)},
			Joins: []*types.JoinSpec{{Right: p(right), Kind: "inner", As: "r_",
				On: []types.OnPair{{LeftField: "region", RightField: "region"}}}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		}
	}
	if _, err := svc.Process(ctx, join("clean.pulse", "clean.pulse")); err != nil {
		t.Fatalf("clean join: %v", err)
	}
	for name, sides := range map[string][2]string{
		"probe": {"cat.pulse", "clean.pulse"},
		"build": {"clean.pulse", "cat.pulse"},
	} {
		t.Run("join_"+name, func(t *testing.T) {
			_, err := svc.Process(ctx, join(sides[0], sides[1]))
			wantDictionaryRefusal(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
		})
	}
}

// TestCorruptCategorical_RenderedOutputsRefuse: every consumer that
// renders or evaluates the label — a formula attribute (streaming and
// buffered), record rows (a window-only request), Sample, the rich
// facet, and the public cohort reader's value API — refuses rather than
// emitting "" or the numeric index.
//
// Falsified by: deleting noteAllValuesFault in AllValues' categorical
// branch (record rows / Sample answer with region ""), the
// CategoricalIndexFault check in exprValue's slot branch (the streaming
// formula answers), and the check in categoricalAccumulator.update (the
// facet answers with value "7").
func TestCorruptCategorical_RenderedOutputsRefuse(t *testing.T) {
	t.Parallel()

	svc, dir := ciEnv(t, map[string][]byte{
		"cat.pulse":   ciFile(t, 64, 5, ciRegionOff, 7),
		"clean.pulse": ciFile(t, 64, -1, 0, 0),
	})
	p := func(name string) string { return filepath.Join(dir, name) }
	ctx := context.Background()
	formula := func(agg types.AggregationType) func(string) *types.Request {
		return func(path string) *types.Request {
			return &types.Request{
				Cohort:       &types.Cohort{Filename: p(path)},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: `region == "north" ? 1 : 0`, Label: "is_north"}},
				Aggregations: []*types.Aggregation{{Type: agg, Field: "is_north", Label: "a"}},
			}
		}
	}
	rows := func(path string) *types.Request {
		return &types.Request{
			Cohort:  &types.Cohort{Filename: p(path)},
			Windows: []*types.Window{{Type: types.WIN_LAG, Field: "x", Label: "prev", OrderBy: []types.OrderKey{{Field: "x"}}}},
		}
	}
	for name, mk := range map[string]func(string) *types.Request{
		"formula_streaming": formula(types.AGG_SUM),
		"formula_buffered":  formula(types.AGG_MEDIAN),
		"record_rows":       rows,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.Process(ctx, mk("clean.pulse")); err != nil {
				t.Fatalf("clean: %v", err)
			}
			_, err := svc.Process(ctx, mk("cat.pulse"))
			wantDictionaryRefusal(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
		})
	}
	t.Run("sample", func(t *testing.T) {
		if _, err := svc.Sample(ctx, p("clean.pulse"), 64); err != nil {
			t.Fatalf("clean: %v", err)
		}
		_, err := svc.Sample(ctx, p("cat.pulse"), 64)
		wantDictionaryRefusal(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
	})
	t.Run("facet", func(t *testing.T) {
		req := func(path string) *types.FacetRequest {
			return &types.FacetRequest{Cohort: &types.Cohort{Filename: p(path)}, Fields: []string{"region"}}
		}
		if _, err := svc.FacetSchema(ctx, req("clean.pulse")); err != nil {
			t.Fatalf("clean: %v", err)
		}
		_, err := svc.FacetSchema(ctx, req("cat.pulse"))
		wantDictionaryRefusal(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
	})
}

// TestCorruptCategorical_CohortReaderRefuses: the public record reader
// (Cohort.Reader) refuses a corrupt categorical code and a set bit past
// the dictionary with the shared refusal, reads the corrupt archive
// shard's clean neighbours, and returns nil for the null seg cell whose
// placeholder addresses no entry.
//
// Falsified by deleting the encx.SetMemberFault check on the narrow set
// rungs in exactValue (the set row then reads as ["VISA"]).
func TestCorruptCategorical_CohortReaderRefuses(t *testing.T) {
	t.Parallel()

	svc, dir := ciEnv(t, map[string][]byte{
		"cat.pulse":  ciFile(t, 8, 5, ciRegionOff, 7),
		"set.pulse":  ciFile(t, 8, 5, ciTagsOff, 0x40),
		"arch.pulse": ciArchive(t, 8, 3, ciRegionOff, 200),
	})
	for _, tc := range []struct {
		file          string
		bad           int64
		field, reason string
	}{
		{"cat.pulse", 5, "region", encx.ReasonCategoricalIndexOutOfRange},
		{"set.pulse", 5, "tags", encx.ReasonSetMemberOutOfRange},
		{"arch.pulse", 8 + 3, "region", encx.ReasonCategoricalIndexOutOfRange},
	} {
		t.Run(tc.file, func(t *testing.T) {
			rr, err := mustOpen(t, svc, filepath.Join(dir, tc.file)).OpenRecordReader()
			if err != nil {
				t.Fatal(err)
			}
			defer rr.Close()
			for i := int64(0); i < rr.Len(); i++ {
				row, err := rr.RecordAt(i)
				if i == tc.bad {
					wantDictionaryRefusal(t, err, tc.field, tc.reason)
					continue
				}
				if err != nil {
					t.Fatalf("clean row %d: %v", i, err)
				}
				if row[2] != nil {
					t.Fatalf("row %d: null seg read as %v", i, row[2])
				}
			}
		})
	}
}
