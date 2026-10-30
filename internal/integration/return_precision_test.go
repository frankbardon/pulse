package integration_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// sigDigits counts the significant digits of a JSON number literal.
func sigDigits(lit string) int {
	mant, _, _ := strings.Cut(strings.ToLower(lit), "e")
	mant = strings.TrimLeft(strings.ReplaceAll(strings.TrimPrefix(mant, "-"), ".", ""), "0")
	return len(mant)
}

// TestReturn_PrecisionProcess: a real Process under return.precision
// rounds measure columns on the wire, writes count columns (AGG_COUNT —
// a count carried as a float64) and a decimal128 sum exact, echoes the
// precision on the marker (alone and with a preset), and leaves the Go
// values identical to the unshaped run.
func TestReturn_PrecisionProcess(t *testing.T) {
	t.Parallel()

	p, _, cohort := acceptanceCohort(t)
	mk := func(ret *types.Return) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_AVERAGE, Field: "x", Label: "m"},
				{Type: types.AGG_COUNT, Field: "x", Label: "n"},
				{Type: types.AGG_SUM, Field: "t_decimal128", Label: "d"},
			},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Return: ret,
		}
	}
	base, baseJSON := processJSON(t, p, mk(nil))
	for name, ret := range map[string]*types.Return{
		"precision only":  {Precision: 1},
		"standard + prec": {Preset: types.ReturnPresetStandard, Precision: 1},
		"minimal + prec":  {Preset: types.ReturnPresetMinimal, Precision: 1},
		"include + prec":  {Include: []string{"data"}, Precision: 1},
	} {
		t.Run(name, func(t *testing.T) {
			resp, b := processJSON(t, p, mk(ret))
			if resp.Returned == nil || resp.Returned.Precision != 1 {
				t.Fatalf("returned = %+v, want precision 1", resp.Returned)
			}
			if !reflect.DeepEqual(resp.Data, base.Data) {
				t.Errorf("Go data changed under precision:\n got %v\nwant %v", resp.Data, base.Data)
			}
			var doc, baseDoc struct {
				Data []map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(baseJSON, &baseDoc); err != nil {
				t.Fatal(err)
			}
			if len(doc.Data) == 0 || len(doc.Data) != len(baseDoc.Data) {
				t.Fatalf("data rows = %d, want %d", len(doc.Data), len(baseDoc.Data))
			}
			wider := false
			for i, row := range doc.Data {
				if sigDigits(string(baseDoc.Data[i]["m"])) > 1 {
					wider = true
				}
				if got := sigDigits(string(row["m"])); got > 1 {
					t.Errorf("row %d m = %s: %d significant digits, want <= 1", i, row["m"], got)
				}
				if string(row["n"]) != string(baseDoc.Data[i]["n"]) {
					t.Errorf("row %d count n = %s, want exact %s", i, row["n"], baseDoc.Data[i]["n"])
				}
				if string(row["d"]) != string(baseDoc.Data[i]["d"]) || !strings.HasPrefix(string(row["d"]), `"`) {
					t.Errorf("row %d decimal d = %s, want exact %s", i, row["d"], baseDoc.Data[i]["d"])
				}
			}
			if !wider {
				t.Fatalf("baseline means already have <= 1 digit: the gate is vacuous")
			}
		})
	}
}

// TestReturn_PrecisionJoinedDefaultCount (#214): a slot whose omitted
// aggregation Type resolves to a count-semantic default only on the
// JOINED schema (r_cat exists after the join alone; categorical →
// AGG_MODE_COUNT) keeps its count column exact under return.precision,
// on Process and on a Compose slot — and a chain stage's defaulted count
// does too. The facade resolves the plan's exact set from the request
// after the service ran; the join path defaults a shallow clone whose
// *Aggregation pointers are the caller's, so the defaulted Type reaches
// that plan. A deep copy in the join path would round the count.
func TestReturn_PrecisionJoinedDefaultCount(t *testing.T) {
	t.Parallel()

	fs, cohort := zoneCohort(t)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mk := func(withCohort, join bool, prec int) *types.Request {
		r := &types.Request{
			Aggregations: []*types.Aggregation{{Field: "cat", Label: "mc"}, {Type: types.AGG_AVERAGE, Field: "n", Label: "m"}},
			Return:       &types.Return{Precision: prec},
		}
		if prec == 0 {
			r.Return = nil
		}
		if join {
			r.Joins = []*types.JoinSpec{{Right: cohort, As: "r_", On: []types.OnPair{{LeftField: "n", RightField: "n"}}}}
			r.Aggregations[0].Field = "r_cat"
		}
		if withCohort {
			r.Cohort = &types.Cohort{Filename: cohort}
		}
		return r
	}
	// run returns the wire data[0] of one run and the request it ran.
	type run func(prec int) (map[string]json.RawMessage, *types.Request)
	wire := func(t *testing.T, v any) map[string]json.RawMessage {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Data []map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		if len(doc.Data) != 1 {
			t.Fatalf("data rows = %d, want 1: %s", len(doc.Data), b)
		}
		return doc.Data[0]
	}
	runs := map[string]run{
		"process join": func(prec int) (map[string]json.RawMessage, *types.Request) {
			req := mk(true, true, prec)
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			return wire(t, resp), req
		},
		"compose slot join": func(prec int) (map[string]json.RawMessage, *types.Request) {
			req := mk(true, true, prec)
			out, err := p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{req}})
			if err != nil {
				t.Fatal(err)
			}
			return wire(t, out.Responses[0]), req
		},
		"chain stage": func(prec int) (map[string]json.RawMessage, *types.Request) {
			req := mk(false, false, prec)
			out, err := p.ProcessChain(ctx, &types.ChainRequest{Cohort: &types.Cohort{Filename: cohort},
				Stages: []*types.ChainStage{{Name: "s0", Request: req}}})
			if err != nil {
				t.Fatal(err)
			}
			return wire(t, out.Stages[0]), req
		},
	}
	for name, r := range runs {
		t.Run(name, func(t *testing.T) {
			base, _ := r(0)
			row, req := r(1)
			if req.Aggregations[0].Type != types.AGG_MODE_COUNT {
				t.Errorf("defaulted type = %q, want AGG_MODE_COUNT", req.Aggregations[0].Type)
			}
			if sigDigits(string(base["mc"])) < 2 {
				t.Fatalf("baseline count %s has < 2 significant digits: the gate is vacuous", base["mc"])
			}
			if string(row["mc"]) != string(base["mc"]) {
				t.Errorf("defaulted count mc = %s under precision 1, want exact %s", row["mc"], base["mc"])
			}
			if got := sigDigits(string(row["m"])); got > 1 || sigDigits(string(base["m"])) <= 1 {
				t.Errorf("measure m = %s (baseline %s): precision did not apply", row["m"], base["m"])
			}
		})
	}
}
