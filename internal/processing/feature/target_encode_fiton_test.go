package feature

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// teRow is one hand-built record for the fit_on / folds tests.
type teRow struct {
	cat   string // "" = null category
	y     float64
	yNull bool
	train bool // the fit_on verdict (column "train" = 1)
}

func teRecords(rows []teRow) []Record {
	out := make([]Record, len(rows))
	for i, r := range rows {
		rec := newFakeRecord()
		if r.cat == "" {
			rec.nulls["g"] = true
		} else {
			rec.str["g"] = r.cat
		}
		if r.yNull {
			rec.nulls["y"] = true
		} else {
			rec.num["y"] = r.y
		}
		if r.train {
			rec.num["train"] = 1
		} else {
			rec.num["train"] = 0
		}
		out[i] = rec
	}
	return out
}

// trainPred is the Go stand-in for the engine-compiled "train == 1".
func trainPred(r Record) (bool, error) {
	v, ok := r.NumericValue("train")
	return ok && v == 1, nil
}

// newBoundTE builds a FEAT_TARGET_ENCODE over g / y with params and,
// when params set fit_on, binds trainPred.
func newBoundTE(t *testing.T, params string) *targetEncode {
	t.Helper()
	c, err := newTargetEncode(&types.Feature{Type: types.FEAT_TARGET_ENCODE, Field: "g", Label: "te", Params: json.RawMessage(params)}, nil)
	if err != nil {
		t.Fatalf("factory %s: %v", params, err)
	}
	te := c.(*targetEncode)
	if te.FitOnExpression() != "" {
		te.BindFitOn(trainPred)
	}
	return te
}

// encodeBoth runs one fresh encoder buffered (Compute) and another
// streaming (PrePass / Finalize / EmitRow), asserts the two agree to the
// bit, and returns the buffered column.
func encodeBoth(t *testing.T, params string, recs []Record) Output {
	t.Helper()
	buf, err := newBoundTE(t, params).Compute(recs, "g")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	stream := streamCompute(t, newBoundTE(t, params), recs, "g")
	b, s := buf["te"], stream["te"]
	for i := range recs {
		bn, sn := b.Nulls[i], s.Nulls[i]
		if bn != sn || (!bn && math.Float64bits(b.Values[i]) != math.Float64bits(s.Values[i])) {
			t.Fatalf("%s row %d: buffered (%v, null %v) != streaming (%v, null %v)", params, i, b.Values[i], bn, s.Values[i], sn)
		}
	}
	return b
}

var fitOnRows = []teRow{
	{cat: "a", y: 1, train: true},
	{cat: "a", y: 3, train: true},
	{cat: "b", y: 10, train: true},
	{cat: "a", y: 100},
	{cat: "b", y: 200},
	{cat: "c", y: 50},
	{cat: "a", y: 5, train: true},
	{cat: "", y: 7, train: true}, // null category: no outcome fed, null out
	{cat: "b", yNull: true, train: true},
}

// TestTargetEncode_FitOn_TrainingMeansAppliedToAll: category and global
// means come from the fit_on rows only; every row is encoded from them,
// an unseen category (c: holdout only) with the TRAINING global mean.
func TestTargetEncode_FitOn_TrainingMeansAppliedToAll(t *testing.T) {
	out := encodeBoth(t, `{"target":"y","fit_on":"train == 1"}`, teRecords(fitOnRows))
	global := (1.0 + 3 + 10 + 5) / 4
	want := []float64{3, 3, 10, 3, 10, global, 3, 0, 10}
	for i, w := range want {
		if i == 7 {
			if !out.Nulls[i] {
				t.Errorf("row 7 (null category) = %v, want null", out.Values[i])
			}
			continue
		}
		if out.Nulls[i] || out.Values[i] != w {
			t.Errorf("row %d = %v (null %v), want %v", i, out.Values[i], out.Nulls[i], w)
		}
	}

	// Smoothing pulls toward the TRAINING global mean, unchanged formula.
	sm := encodeBoth(t, `{"target":"y","fit_on":"train == 1","smoothing":2}`, teRecords(fitOnRows))
	if w := (3*3 + 2*global) / (3 + 2); sm.Values[3] != w {
		t.Errorf("smoothed holdout a = %v, want %v", sm.Values[3], w)
	}
}

// TestTargetEncode_FitOn_HoldoutOutcomesNeverLeak is the leakage
// guarantee: perturbing every holdout row's outcome (and nulling one)
// leaves every row's encoding bit-identical, with and without folds. The
// same perturbation without fit_on DOES move the values, so the check is
// not vacuous.
func TestTargetEncode_FitOn_HoldoutOutcomesNeverLeak(t *testing.T) {
	perturbed := make([]teRow, len(fitOnRows))
	copy(perturbed, fitOnRows)
	for i := range perturbed {
		if !perturbed[i].train {
			perturbed[i].y = perturbed[i].y*-7.25 + 1e6
		}
	}
	perturbed[5].yNull = true

	for _, params := range []string{
		`{"target":"y","fit_on":"train == 1"}`,
		`{"target":"y","fit_on":"train == 1","smoothing":3}`,
		`{"target":"y","fit_on":"train == 1","folds":3,"rng_seed":11}`,
	} {
		a := encodeBoth(t, params, teRecords(fitOnRows))
		b := encodeBoth(t, params, teRecords(perturbed))
		for i := range fitOnRows {
			if a.Nulls[i] != b.Nulls[i] || math.Float64bits(a.Values[i]) != math.Float64bits(b.Values[i]) {
				t.Errorf("%s row %d: %v -> %v after perturbing holdout outcomes", params, i, a.Values[i], b.Values[i])
			}
		}
	}

	a := encodeBoth(t, `{"target":"y"}`, teRecords(fitOnRows))
	b := encodeBoth(t, `{"target":"y"}`, teRecords(perturbed))
	if a.Values[0] == b.Values[0] {
		t.Error("without fit_on the holdout outcomes should move the a encoding")
	}
}

// TestTargetEncode_Folds_OutOfFold checks the out-of-fold encoding
// against a brute-force oracle built from the dealt folds: a training
// row's value is the mean over training rows OUTSIDE its fold (unseen →
// that set's global mean, empty set → null); a holdout row gets the
// full-training value. Folds are balanced to within one row and the
// deal is deterministic in the seed.
func TestTargetEncode_Folds_OutOfFold(t *testing.T) {
	var rows []teRow
	cats := []string{"a", "b", "c"}
	for i := 0; i < 23; i++ {
		rows = append(rows, teRow{cat: cats[i%3], y: float64(i*i%17) + 0.5, train: i%4 != 3})
	}
	rows = append(rows, teRow{cat: "d", y: 9, train: true}) // only in one fold
	const k, seed = 4, 42
	recs := teRecords(rows)
	out := encodeBoth(t, `{"target":"y","fit_on":"train == 1","folds":4,"rng_seed":42}`, recs)

	// The deal: training rows in order, one fold each.
	d := newFoldDealer(k, seed)
	fold := make([]int, len(rows))
	sizes := make([]int, k)
	for i, r := range rows {
		fold[i] = -1
		if r.train {
			fold[i] = d.next()
			sizes[fold[i]]++
		}
	}
	for f := 1; f < k; f++ {
		if diff := sizes[f] - sizes[0]; diff > 1 || diff < -1 {
			t.Fatalf("unbalanced folds %v", sizes)
		}
	}
	mean := func(exclude int, cat string) (float64, bool) {
		var cs, gs float64
		var cn, gn int
		for i, r := range rows {
			if !r.train || (exclude >= 0 && fold[i] == exclude) {
				continue
			}
			gs += r.y
			gn++
			if r.cat == cat {
				cs += r.y
				cn++
			}
		}
		if gn == 0 {
			return 0, false
		}
		if cn == 0 {
			return gs / float64(gn), true
		}
		return cs / float64(cn), true
	}
	for i, r := range rows {
		want, ok := mean(fold[i], r.cat)
		if !ok {
			if !out.Nulls[i] {
				t.Errorf("row %d: %v, want null", i, out.Values[i])
			}
			continue
		}
		if out.Nulls[i] || math.Abs(out.Values[i]-want) > 1e-12 {
			t.Errorf("row %d (fold %d, %s): %v, want %v", i, fold[i], r.cat, out.Values[i], want)
		}
	}

	again := encodeBoth(t, `{"target":"y","fit_on":"train == 1","folds":4,"rng_seed":42}`, teRecords(rows))
	other := encodeBoth(t, `{"target":"y","fit_on":"train == 1","folds":4,"rng_seed":43}`, teRecords(rows))
	differs := false
	for i := range rows {
		if math.Float64bits(again.Values[i]) != math.Float64bits(out.Values[i]) {
			t.Fatalf("row %d not deterministic: %v vs %v", i, again.Values[i], out.Values[i])
		}
		if other.Values[i] != out.Values[i] {
			differs = true
		}
	}
	if !differs {
		t.Error("a different rng_seed dealt identical folds")
	}
}

// TestTargetEncode_FoldsWithoutFitOn: every row is a training row, so
// every row is encoded out-of-fold.
func TestTargetEncode_FoldsWithoutFitOn(t *testing.T) {
	rows := []teRow{{cat: "a", y: 1}, {cat: "a", y: 2}, {cat: "a", y: 4}, {cat: "a", y: 8}}
	out := encodeBoth(t, `{"target":"y","folds":4,"rng_seed":1}`, teRecords(rows))
	for i, r := range rows {
		if want := (15 - r.y) / 3; out.Values[i] != want {
			t.Errorf("row %d = %v, want leave-one-out %v", i, out.Values[i], want)
		}
	}
}

// TestTargetEncode_FitOn_Unbound: a fit_on the engine never bound is an
// internal fault on both drives, never an all-rows fit.
func TestTargetEncode_FitOn_Unbound(t *testing.T) {
	c, err := newTargetEncode(&types.Feature{Type: types.FEAT_TARGET_ENCODE, Field: "g", Params: json.RawMessage(`{"target":"y","fit_on":"train == 1"}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, cerr := c.Compute(teRecords(fitOnRows), "g")
	ce, ok := cerr.(*errors.CodedError)
	if !ok || ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("Compute = %v, want PROCESSING_INTERNAL", cerr)
	}
	if perr := c.(StreamingComputer).PrePass(teRecords(fitOnRows)[0], "g"); perr == nil {
		t.Fatal("PrePass accepted an unbound fit_on")
	}
}

// TestIsStreamable_TargetEncodeDerivedInputs: the encoder's pre-pass
// reads the raw record, so a fit_on (or target) over a column an earlier
// feature derives is not streamable and the streaming build refuses it.
func TestIsStreamable_TargetEncodeDerivedInputs(t *testing.T) {
	schema := makeCategoricalSchema(t, "g", "a", "b")
	schema.Fields = append(schema.Fields, encoding.Field{Name: "y", Type: encoding.FieldTypeF64}, encoding.Field{Name: "s", Type: encoding.FieldTypeU8})
	te := func(params string) []*types.Feature {
		return []*types.Feature{{Type: types.FEAT_TARGET_ENCODE, Field: "g", Params: json.RawMessage(params)}}
	}
	if !IsStreamable(te(`{"target":"y","fit_on":"s == 0","folds":2}`), schema) {
		t.Error("fit_on over a stored column should stream")
	}
	for _, params := range []string{`{"target":"y","fit_on":"split == 0"}`, `{"target":"LOG_y"}`} {
		if IsStreamable(te(params), schema) {
			t.Errorf("%s: streamable, want buffered", params)
		}
		if _, err := BuildStreaming(te(params), schema); err == nil {
			t.Errorf("%s: streaming build accepted it", params)
		}
	}
}
