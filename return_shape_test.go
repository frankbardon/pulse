package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// returnCorpus is the broad fixture corpus of the response-shaping
// identity gate: one request per result family — grouped rows,
// crosstab with overlays, tier-1 tests, regressions, matrices — each
// with Components on (the engine default).
func returnCorpus(cohort string) map[string]func() *types.Request {
	c := func() *types.Cohort { return &types.Cohort{Filename: cohort} }
	return map[string]func() *types.Request{
		"grouped": func() *types.Request {
			return &types.Request{
				Cohort: c(),
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_AVERAGE, Field: "x", Label: "m"},
					{Type: types.AGG_COUNT, Field: "x", Label: "n"},
				},
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			}
		},
		"crosstab-overlays": func() *types.Request {
			return &types.Request{
				Cohort: c(),
				Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "subj"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"},
					Shape:   types.CrosstabShapeMatrix,
					Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
				},
				Overlays: []types.OverlaySpec{
					{Name: "chi", Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix},
				},
			}
		},
		"tests": func() *types.Request {
			return &types.Request{
				Cohort:       c(),
				Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "x", Label: "m"}},
				Tests: []*types.Test{
					{Type: types.TEST_T, Field: "x", SplitBy: "g", Label: "t"},
					{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Label: "r"},
				},
			}
		},
		"regressions": func() *types.Request {
			return &types.Request{
				Cohort:       c(),
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}},
			}
		},
		"matrices": func() *types.Request {
			return &types.Request{
				Cohort:       c(),
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
				Matrices:     []types.MatrixSpec{{Type: types.MAT_CORRELATION, Name: "xy", Fields: []string{"x", "y"}}},
			}
		},
	}
}

func processJSON(t *testing.T, p *pulse.Pulse, req *types.Request) (*types.Response, []byte) {
	t.Helper()
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return resp, b
}

// TestReturnFullIsIdentity: a request without `return`, with an empty
// block and with preset `full` produce byte-identical responses over
// the whole corpus, with no `returned` marker; a non-identity block over
// the same request does change the bytes (the gate is not vacuous).
func TestReturnFullIsIdentity(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	for name, mk := range returnCorpus(cohort) {
		t.Run(name, func(t *testing.T) {
			_, baseline := processJSON(t, p, mk())
			if bytes.Contains(baseline, []byte(`"returned"`)) {
				t.Fatalf("baseline carries a returned marker: %s", baseline)
			}
			if !bytes.Contains(baseline, []byte(`"components"`)) {
				t.Fatalf("baseline carries no components: %s", baseline)
			}
			for vname, ret := range map[string]*types.Return{
				"preset full":                 {Preset: types.ReturnPresetFull},
				"empty block":                 {},
				"full plus a covered include": {Preset: types.ReturnPresetFull, Include: []string{"metadata"}},
			} {
				r := mk()
				r.Return = ret
				_, got := processJSON(t, p, r)
				if !bytes.Equal(got, baseline) {
					t.Errorf("%s differs from the no-return baseline:\n got %s\nwant %s", vname, got, baseline)
				}
			}
			r := mk()
			r.Return = &types.Return{Exclude: []string{"metadata"}}
			_, shaped := processJSON(t, p, r)
			if bytes.Equal(shaped, baseline) || !bytes.Contains(shaped, []byte(`"returned"`)) {
				t.Errorf("an exclude left the response unshaped: %s", shaped)
			}
		})
	}
}

// topKeys decodes b's top-level object keys, sorted.
func topKeys(t *testing.T, b []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestReturn_ProcessPrunesGoAndWire: an exclude prunes the Go value
// (nil slot, zero leaf, deleted data column) and the wire (absent,
// never null), and the plain json.Marshal of the response is shaped.
func TestReturn_ProcessPrunesGoAndWire(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	req := returnCorpus(cohort)["grouped"]()
	req.Return = &types.Return{Exclude: []string{"components", "metadata.total_rows", "data[*].m"}}
	resp, b := processJSON(t, p, req)

	if resp.Components != nil {
		t.Errorf("excluded components = %+v; want nil in Go", resp.Components)
	}
	if resp.Metadata == nil || resp.Metadata.TotalRows != 0 || resp.Metadata.FilteredRows == 0 {
		t.Errorf("metadata = %+v; want total_rows zeroed, filtered_rows kept", resp.Metadata)
	}
	for _, row := range resp.Data {
		if _, ok := row["m"]; ok {
			t.Errorf("excluded data column m still in Go row %v", row)
		}
		if _, ok := row["n"]; !ok {
			t.Errorf("kept data column n missing from Go row %v", row)
		}
	}
	s := string(b)
	for _, absent := range []string{`"components"`, `"total_rows"`, `"m":`} {
		if strings.Contains(s, absent) {
			t.Errorf("wire carries excluded %s: %s", absent, s)
		}
	}
	if !strings.Contains(s, `"filtered_rows"`) {
		t.Errorf("wire lost kept filtered_rows: %s", s)
	}
	if resp.Returned == nil || resp.Returned.Preset != "custom" || !strings.HasPrefix(resp.Returned.Digest, "rp1:") {
		t.Fatalf("returned marker = %+v; want preset custom with an rp1 digest", resp.Returned)
	}
	pr, err := p.Predict(context.Background(), req)
	if err != nil || pr.Return == nil {
		t.Fatalf("predict: %v %+v", err, pr)
	}
	if pr.Return.Digest != resp.Returned.Digest {
		t.Errorf("runtime digest %s != predict digest %s", resp.Returned.Digest, pr.Return.Digest)
	}
}

// TestReturn_IncludeOnlyAllowlist: include without a preset is an
// allowlist; the marker reads "custom" and is emitted though not
// selected; warnings stay selectable.
func TestReturn_IncludeOnlyAllowlist(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	req := returnCorpus(cohort)["tests"]()
	req.Return = &types.Return{Include: []string{"tests[*].p_value"}}
	resp, b := processJSON(t, p, req)
	if got, want := strings.Join(topKeys(t, b), ","), "returned,tests"; got != want {
		t.Errorf("top-level keys = %s; want %s (%s)", got, want, b)
	}
	if resp.Data != nil || resp.Metadata != nil || resp.Components != nil {
		t.Errorf("unselected slots survive in Go: data=%v metadata=%v components=%v", resp.Data, resp.Metadata, resp.Components)
	}
	var wire struct {
		Tests []map[string]json.RawMessage `json:"tests"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	for _, tr := range wire.Tests {
		if len(tr) != 1 || tr["p_value"] == nil {
			t.Errorf("test entry = %v; want only p_value", tr)
		}
	}
	if resp.Returned == nil || resp.Returned.Preset != "custom" {
		t.Errorf("returned = %+v; want preset custom", resp.Returned)
	}
}

// TestReturn_MarkerNotExcludable: naming `returned` in a block is
// refused by Process and predict alike.
func TestReturn_MarkerNotExcludable(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	for _, ret := range []*types.Return{{Exclude: []string{"returned"}}, {Include: []string{"returned"}}} {
		req := returnCorpus(cohort)["grouped"]()
		req.Return = ret
		_, err := p.Process(context.Background(), req)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_RETURN_INVALID {
			t.Errorf("Process(%+v) err = %v; want PULSE_RETURN_INVALID", ret, err)
		}
		pr, err := p.Predict(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if pr.Valid {
			t.Errorf("predict accepted %+v", ret)
		}
	}
}

// TestReturn_UnmatchedOpenInclude: an include through an open map that
// matches nothing raises PULSE_RETURN_PATH_UNMATCHED; a matching one
// does not.
func TestReturn_UnmatchedOpenInclude(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	base, _ := processJSON(t, p, returnCorpus(cohort)["tests"]())
	var real string
	for k := range base.Tests[0].Details {
		real = k
		break
	}
	if real == "" {
		t.Fatal("TEST_T carries no details key to select")
	}
	unmatched := func(resp *types.Response) []string {
		var out []string
		for _, w := range resp.Warnings {
			if w.Code == string(perr.PULSE_RETURN_PATH_UNMATCHED) {
				out = append(out, w.Details["path"].(string))
			}
		}
		return out
	}

	req := returnCorpus(cohort)["tests"]()
	req.Return = &types.Return{Include: []string{"tests[*].details." + real}}
	resp, _ := processJSON(t, p, req)
	if got := unmatched(resp); len(got) != 0 {
		t.Errorf("a matching open include warned: %v", got)
	}

	req = returnCorpus(cohort)["tests"]()
	req.Return = &types.Return{Include: []string{"tests[*].details." + real, "tests[*].details.zz_nope"}}
	resp, b := processJSON(t, p, req)
	if got := unmatched(resp); len(got) != 1 || got[0] != "tests[*].details.zz_nope" {
		t.Errorf("unmatched warnings = %v; want exactly tests[*].details.zz_nope", got)
	}
	if !strings.Contains(string(b), string(perr.PULSE_RETURN_PATH_UNMATCHED)) {
		t.Errorf("the warning is not on the wire: %s", b)
	}

	req = returnCorpus(cohort)["tests"]()
	req.Return = &types.Return{Include: []string{"tests[*].details.zz_nope"}, Exclude: []string{"warnings"}}
	resp, _ = processJSON(t, p, req)
	if len(resp.Warnings) != 0 {
		t.Errorf("excluded warnings still carry %v", resp.Warnings)
	}
}

// TestReturn_RuntimeColumnRefusalMatchesPredict: a `data[*].<column>`
// the request cannot produce is refused by Process with predict's code,
// before any record is read.
func TestReturn_RuntimeColumnRefusalMatchesPredict(t *testing.T) {
	p, _, cohort := acceptanceCohort(t)
	req := returnCorpus(cohort)["grouped"]()
	req.Return = &types.Return{Include: []string{"data[*].nope"}}
	_, err := p.Process(context.Background(), req)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_RETURN_PATH_UNKNOWN {
		t.Fatalf("Process err = %v; want PULSE_RETURN_PATH_UNKNOWN", err)
	}
	pr, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Valid {
		t.Error("predict accepted a column Process refuses")
	}
}
