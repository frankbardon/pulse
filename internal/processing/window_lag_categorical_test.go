package processing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// window_lag_categorical_test.go: WIN_LAG / WIN_LEAD over a categorical
// field (mmm-optimize-depth E4-S1, #581).

// numericLagDigestRequests are numeric WIN_LAG / WIN_LEAD requests over
// every shape a lag runs in: record rows (offset, default, lead),
// windows-first grouped and crosstab, and a lag over output rows.
func numericLagDigestRequests() []*types.Request {
	lag := wfLagXWindow
	lead := func() *types.Window {
		return &types.Window{Type: types.WIN_LEAD, Field: "x", Label: "lead_x", PartitionBy: []string{"region"},
			OrderBy: []types.OrderKey{{Field: "d"}}, Params: []byte(`{"offset": 2, "default": -1}`)}
	}
	return []*types.Request{
		{Windows: []*types.Window{lag(), lead()}},
		{Windows: []*types.Window{{Type: types.WIN_LAG, Field: "d", Label: "lag_d", OrderBy: []types.OrderKey{{Field: "d"}}, Params: []byte(`{"offset": 3}`)}}},
		{Windows: []*types.Window{lag()}, Groups: []*types.Group{wfQuarterGroup()},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "lag_x", Label: "s"}}},
		{Windows: []*types.Window{lead()}, Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{wfQuarterGroup()}, Columns: []*types.Group{wfRegionGroup()},
			Cell: &types.Aggregation{Type: types.AGG_SUM, Field: "lead_x", Label: "s"}}},
		{Groups: []*types.Group{wfQuarterGroup()}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
			Windows: []*types.Window{{Type: types.WIN_LAG, Field: "s", Label: "lag_s", OrderBy: []types.OrderKey{{Field: "s"}}}}},
	}
}

// numericLagDigest pins every numeric WIN_LAG / WIN_LEAD response the
// categorical lift must leave byte-identical: the digest was taken on
// the code before the lift.
const numericLagDigest = "c47f4d3edf5bbc4bcac4f87629d94169c592bee2844f5d12e4f5e836e3d134fa"

// TestWinLagLead_NumericDigestUnchanged: lifting the categorical refusal
// changes no numeric lag or lead — data, crosstab and Components of
// every shape hash to the pre-lift digest.
func TestWinLagLead_NumericDigestUnchanged(t *testing.T) {
	t.Parallel()

	h := sha256.New()
	for _, req := range numericLagDigestRequests() {
		resp := wfRun(t, req, false)
		b, err := json.Marshal([]any{resp.Data, resp.Crosstab, resp.Components})
		if err != nil {
			t.Fatal(err)
		}
		h.Write(b)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != numericLagDigest {
		t.Fatalf("numeric lag / lead digest = %s, want %s", got, numericLagDigest)
	}
}

// wcSchema is wfSchema plus, for the twin, lag_region: a stored nullable
// categorical on region's dictionary holding WIN_LAG(region) ordered by
// d (the previous record's region; null for the first).
func wcSchema(twin bool) *encoding.Schema {
	s := wfSchema(false)
	if twin {
		s.Fields = append(s.Fields, encoding.Field{Name: "lag_region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: s.Field("region").Dictionary})
	}
	return s
}

func wcRecords(twin bool) []*Record {
	s := wcSchema(twin)
	recs := make([]*Record, wfN)
	for i := range recs {
		vals := map[string]float64{"d": wfDay(i), "region": wfRegion(i), "x": wfX(i)}
		nulls := map[string]bool{}
		if twin {
			if i == 0 {
				nulls["lag_region"] = true
			} else {
				vals["lag_region"] = wfRegion(i - 1)
			}
		}
		recs[i] = NewRecordWithNulls(s, vals, nulls)
	}
	return recs
}

func wcLag(params string) *types.Window {
	w := &types.Window{Type: types.WIN_LAG, Field: "region", Label: "lag_region", OrderBy: []types.OrderKey{{Field: "d"}}}
	if params != "" {
		w.Params = []byte(params)
	}
	return w
}

// wcRun runs req over the source records, or — twin — req without its
// windows over the twin cohort.
func wcRun(req *types.Request, twin bool) (*types.Response, error) {
	r := *req
	if twin {
		r.Windows = nil
	}
	p := NewProcessor(wcSchema(twin))
	if r.Crosstab != nil {
		return p.RunCrosstab(context.Background(), &r, wcRecords(twin))
	}
	return p.processRecords(context.Background(), p.prepare(&r), wcRecords(twin))
}

// TestWindowsFirst_CategoryGroupOnCategoricalWindow: grouping by a
// lagged category — GROUP_CATEGORY on a WIN_LAG of a categorical, a
// request group or a crosstab axis — keys by the source dictionary's
// labels, and every figure (data, crosstab, Components) equals the same
// request over a cohort storing the lag as a categorical column.
func TestWindowsFirst_CategoryGroupOnCategoricalWindow(t *testing.T) {
	t.Parallel()

	for name, req := range map[string]func() *types.Request{
		"grouped": func() *types.Request {
			return &types.Request{Windows: []*types.Window{wcLag("")}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "lag_region"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}}
		},
		"crosstab": func() *types.Request {
			return &types.Request{Windows: []*types.Window{wcLag("")}, Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "lag_region"}},
				Columns: []*types.Group{wfRegionGroup()},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"},
			}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := wcRun(req(), false)
			if err != nil {
				t.Fatal(err)
			}
			want, err := wcRun(req(), true)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Data, want.Data) || !reflect.DeepEqual(got.Crosstab, want.Crosstab) {
				t.Fatalf("data\n got %v %+v\nwant %v %+v", got.Data, got.Crosstab, want.Data, want.Crosstab)
			}
			gb, _ := json.Marshal(got.Components)
			wb, _ := json.Marshal(want.Components)
			if string(gb) != string(wb) {
				t.Fatalf("components\n got %s\nwant %s", gb, wb)
			}
			if !strings.Contains(string(gb), `"key":"a"`) {
				t.Fatalf("components key no label: %s", gb)
			}
		})
	}
}

// TestWinLag_CategoricalRecordRows: over the record rows a lagged (or
// led) category is its source's label; a missing neighbour is null, or
// params.default — a dictionary label — where given.
func TestWinLag_CategoricalRecordRows(t *testing.T) {
	t.Parallel()

	lead := &types.Window{Type: types.WIN_LEAD, Field: "region", Label: "next", OrderBy: []types.OrderKey{{Field: "d"}}, Params: []byte(`{"default": "c"}`)}
	resp, err := wcRun(&types.Request{Windows: []*types.Window{wcLag(""), lead}}, false)
	if err != nil {
		t.Fatal(err)
	}
	rows := resp.Data
	labels := []string{"a", "b", "c"}
	for i, row := range rows {
		if i == 0 {
			if v := row["lag_region"]; v != nil {
				t.Fatalf("row 0 lag_region = %v, want null", v)
			}
		} else if v := row["lag_region"]; v != labels[int(wfRegion(i-1))] {
			t.Fatalf("row %d lag_region = %v, want %s", i, v, labels[int(wfRegion(i-1))])
		}
		want := "c"
		if i+1 < wfN {
			want = labels[int(wfRegion(i+1))]
		}
		if v := row["next"]; v != want {
			t.Fatalf("row %d next = %v, want %s", i, v, want)
		}
	}
}

// TestWinLag_CategoricalRefusals: the engine refuses what predict
// refuses (descriptor's TestWindowsFirst_CategoricalWindowGroupPredicted)
// before any row — an aggregation or a value window reading a lagged
// category (PULSE_FIELD_TYPE_UNSUPPORTED), a default that is no
// dictionary label (PULSE_WINDOW_INVALID default_not_a_label) — and
// still refuses a set or datetime source.
func TestWinLag_CategoricalRefusals(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		req          *types.Request
		code         errors.Code
		reason, slot string
	}{
		"aggregated": {req: &types.Request{Windows: []*types.Window{wcLag("")}, Groups: []*types.Group{wfRegionGroup()},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "lag_region", Label: "s"}}},
			code: errors.PULSE_FIELD_TYPE_UNSUPPORTED, reason: "categorical_window_output"},
		"crosstab cell": {req: &types.Request{Windows: []*types.Window{wcLag("")}, Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "lag_region"}}, Columns: []*types.Group{wfRegionGroup()},
			Cell: &types.Aggregation{Type: types.AGG_DISTINCT_COUNT, Field: "lag_region", Label: "n"}}},
			code: errors.PULSE_FIELD_TYPE_UNSUPPORTED, reason: "categorical_window_output"},
		"value window": {req: &types.Request{Windows: []*types.Window{wcLag(""),
			{Type: types.WIN_PCT_CHANGE, Field: "lag_region", Label: "p", OrderBy: []types.OrderKey{{Field: "d"}}}}},
			code: errors.PULSE_FIELD_TYPE_UNSUPPORTED},
		"unknown default": {req: &types.Request{Windows: []*types.Window{wcLag(`{"default": "z"}`)}},
			code: errors.PULSE_WINDOW_INVALID, reason: "default_not_a_label"},
		"numeric default": {req: &types.Request{Windows: []*types.Window{wcLag(`{"default": 1}`)}},
			code: errors.PULSE_WINDOW_INVALID, reason: "default_not_a_label"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := wcRun(c.req, false)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
			if c.reason != "" && ce.Details["reason"] != c.reason {
				t.Fatalf("details %v, want reason %s", ce.Details, c.reason)
			}
		})
	}
	for _, ft := range []encoding.FieldType{encoding.FieldTypeSetU8, encoding.FieldTypeDateTime, encoding.FieldTypeDecimal128} {
		if fieldaccept.Accepts(string(types.WIN_LAG), fieldaccept.SlotField, ft) || fieldaccept.Accepts(string(types.WIN_LEAD), fieldaccept.SlotField, ft) {
			t.Fatalf("copy windows accept %s", ft)
		}
	}
}

// TestDeriveRecords_CategoricalLagEmitsCodes: materialize's derive pass
// emits a lagged category as its source dictionary's codes (nil for a
// missing neighbour), the form the sink writes as the inherited
// categorical column.
func TestDeriveRecords_CategoricalLagEmitsCodes(t *testing.T) {
	t.Parallel()

	s := wcSchema(false)
	req := &types.Request{Windows: []*types.Window{wcLag("")}}
	var got []any
	err := NewProcessor(s).DeriveRecords(context.Background(), req, NewSliceIterator(wcRecords(false)),
		[]DerivedColumn{{Name: "lag_region", Window: true, Dictionary: s.Field("region").Dictionary}},
		func(_ int64, values []any) error { got = append(got, values[0]); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		var want any
		if i > 0 {
			want = wfRegion(i - 1)
		}
		if v != want {
			t.Fatalf("record %d = %v, want %v", i, v, want)
		}
	}
}
