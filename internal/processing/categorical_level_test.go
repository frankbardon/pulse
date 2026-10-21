package processing

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// categorical_level_test.go: the record-level half of U51 E5-S10 — a
// level read (StringValueChecked / LevelValueChecked) refuses a
// categorical code its dictionary has no entry for with E5-S8's
// ENCODING_INVALID, where the unchecked StringValue / LevelValue read it
// as absent (the same as a null, so every consumer dropped the row
// silently). The service-level arms are pinned by
// internal/service/categorical_level_test.go.

// TestCorruptLevel_CheckedReaders: both checked readers refuse a corrupt
// code, resolve an in-range one, and never judge a null — a null cell
// over a corrupt placeholder reads ("", false, nil).
//
// Falsified by deleting the encx.CategoricalIndexFault check in
// resolveLevel (the corrupt row then reads ("", false, nil)).
func TestCorruptLevel_CheckedReaders(t *testing.T) {
	for _, read := range []struct {
		name string
		fn   func(*Record, string) (string, bool, error)
	}{
		{"StringValueChecked", (*Record).StringValueChecked},
		{"LevelValueChecked", (*Record).LevelValueChecked},
	} {
		t.Run(read.name, func(t *testing.T) {
			if s, ok, err := read.fn(faultRecord(1, 1), "region"); err != nil || !ok || s != "south" {
				t.Fatalf("in range: %q %v %v", s, ok, err)
			}
			_, ok, err := read.fn(faultRecord(7, 1), "region")
			wantFault(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
			if ok {
				t.Fatal("a refused read reported ok")
			}
			null := faultRecord(7, 1)
			null.SetNull("region")
			if s, ok, err := read.fn(null, "region"); err != nil || ok || s != "" {
				t.Fatalf("null over a corrupt placeholder: %q %v %v", s, ok, err)
			}
			// A non-categorical field is never judged.
			if _, ok, err := read.fn(faultRecord(7, 1), "x"); err != nil || ok {
				t.Fatalf("numeric field: %v %v", ok, err)
			}
		})
	}
	// The unchecked forms keep their shape (tests and the extend view).
	if s, ok := faultRecord(7, 1).StringValue("region"); ok || s != "" {
		t.Fatalf("unchecked StringValue: %q %v", s, ok)
	}
}

// TestCorruptLevel_ReadFaultLatch: NoteReadFault keeps the first fault,
// TakeReadFault hands it over once, and ClearForRow drops it with the
// row — so a reused record never carries a stale fault to the next
// row's extension call.
//
// Falsified by deleting `r.aux.readFault = nil` in ClearForRow.
func TestCorruptLevel_ReadFaultLatch(t *testing.T) {
	r := faultRecord(7, 1)
	_, _, err := r.StringValueChecked("region")
	r.NoteReadFault(err)
	r.NoteReadFault(errSentinel)
	if got := r.TakeReadFault(); got != err {
		t.Fatalf("take = %v, want the first fault %v", got, err)
	}
	if got := r.TakeReadFault(); got != nil {
		t.Fatalf("second take = %v, want nil", got)
	}
	r.NoteReadFault(err)
	r.ClearForRow()
	if got := r.TakeReadFault(); got != nil {
		t.Fatalf("after ClearForRow: %v", got)
	}
}

var errSentinel = os.ErrInvalid

// TestCorruptLevel_ComparisonAndMemberKeys: a comparison attribute's
// table key_field, a centroid's by and the include-set label predicate
// refuse a corrupt key instead of reading it as an unmatched "" key /
// a null group / a non-member.
//
// Falsified by reverting comparisonCore.reference's key read to
// discard the error (the table case then counts the row unmatched).
func TestCorruptLevel_ComparisonAndMemberKeys(t *testing.T) {
	c := &comparisonCore{
		bound:    true,
		cfg:      vectors.ComparisonConfig{Reference: vectors.ComparisonReference{Form: vectors.ReferenceTableKeyField, KeyField: "region"}},
		profiles: map[string][]float64{"north": {1}},
	}
	if _, ok, err := c.reference(faultRecord(0, 1)); err != nil || !ok {
		t.Fatalf("clean key: %v %v", ok, err)
	}
	_, _, err := c.reference(faultRecord(7, 1))
	wantFault(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)

	f := &centroidFold{by: "region"}
	if k, ok, err := f.key(faultRecord(1, 1)); err != nil || !ok || k != "south" {
		t.Fatalf("clean centroid by: %q %v %v", k, ok, err)
	}
	_, _, err = f.key(faultRecord(7, 1))
	wantFault(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)

	set := newStringSet(1)
	set.Add("north")
	pred, err := BuildMemberSetPredicate(set, faultSchema(), "region")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := pred(faultRecord(0, 1)); err != nil || !ok {
		t.Fatalf("clean member: %v %v", ok, err)
	}
	_, err = pred(faultRecord(7, 1))
	wantFault(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
}

// TestCorruptLevel_FeatureLevelReads: every encoder and the stratified
// split refuse a corrupt level on the run — buffered (a MEDIAN keeps
// the rows) and streaming (a SUM) — while the clean records answer.
//
// Falsified by discarding the error of the stratify read in
// trainTestSplit.Compute (the buffered split then answers, the corrupt
// row stratified under "").
func TestCorruptLevel_FeatureLevelReads(t *testing.T) {
	feats := map[string]*types.Feature{
		"one_hot":     {Type: types.FEAT_ONE_HOT, Field: "region"},
		"frequency":   {Type: types.FEAT_FREQUENCY_ENCODE, Field: "region", Label: "f"},
		"target":      {Type: types.FEAT_TARGET_ENCODE, Field: "region", Label: "te", Params: json.RawMessage(`{"target":"x","smoothing":1.0}`)},
		"split_strat": {Type: types.FEAT_TRAIN_TEST_SPLIT, Label: "s", Params: json.RawMessage(`{"ratios":[0.5,0.5],"seed":7,"stratify":"region"}`)},
	}
	records := func(bad bool) []*Record {
		out := make([]*Record, 8)
		for i := range out {
			out[i] = faultRecord(float64(i%2), 1)
		}
		if bad {
			out[5] = faultRecord(7, 1)
		}
		return out
	}
	for name, feat := range feats {
		for _, agg := range []types.AggregationType{types.AGG_SUM, types.AGG_MEDIAN} {
			t.Run(name+"/"+string(agg), func(t *testing.T) {
				req := func() *types.Request {
					f := *feat
					return &types.Request{
						Features:     []*types.Feature{&f},
						Aggregations: []*types.Aggregation{{Type: agg, Field: "x", Label: "a"}},
					}
				}
				if _, err := NewProcessor(faultSchema()).Process(context.Background(), req(), NewSliceIterator(records(false))); err != nil {
					t.Fatalf("clean: %v", err)
				}
				_, err := NewProcessor(faultSchema()).Process(context.Background(), req(), NewSliceIterator(records(true)))
				wantFault(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
			})
		}
	}
}

// TestLevelReaders_Checked is the inventory gate of U51 E5-S10: no
// production file of the engine (internal/processing and its
// subpackages) nor the root adapter calls the UNCHECKED StringValue /
// LevelValue, which read a corrupt categorical code as absent. The only
// allowed callers are the unchecked forms' own definitions (record.go)
// and the extend views, which latch the fault (extensions_adapt.go:
// extRecord / featureRecordView StringValue override, not a call).
// A method VALUE named StringValue on another type (an extend.Record
// held by an extension) is the embedder's own read and out of scope.
//
// Falsified by restoring the unchecked read in attribute_compare.go's
// centroidFold.key (the gate names the file and line).
func TestLevelReaders_Checked(t *testing.T) {
	var files []string
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "../../extensions_adapt.go", "../../extensions_regression.go")
	allowed := map[string]bool{"record.go": true}
	fset := token.NewFileSet()
	for _, path := range files {
		if allowed[path] {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "StringValue" && sel.Sel.Name != "LevelValue") {
				return true
			}
			t.Errorf("%s: unchecked %s read; use %sChecked and propagate the error",
				fset.Position(call.Pos()), sel.Sel.Name, sel.Sel.Name)
			return true
		})
	}
}
