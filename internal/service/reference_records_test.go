package service

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// recordFixture is buildLookupFixture plus other.pulse (id + 100,
// score / 10) indexed on id: two cohorts on one fs.NewMemMap.
func recordFixture(t *testing.T) *Service {
	t.Helper()
	svc := buildLookupFixture(t)
	var other [][]uint64
	for _, r := range indexTestRecords() {
		other = append(other, []uint64{r[0] + 100, math.Float64bits(math.Float64frombits(r[1]) / 10), r[2]})
	}
	if err := afero.WriteFile(svc.fs.Fs(), "other.pulse", writePulseFile(t, indexTestSchema(), other), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BuildIndex(context.Background(), "other.pulse", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	return svc
}

func recordAttr(label, ref string) *types.Attribute {
	return &types.Attribute{Type: types.ATTR_VEC_DISTANCE, Label: label, Fields: []string{"id", "score"},
		Params: json.RawMessage(`{"metric":"euclidean","reference":` + ref + `}`)}
}

// TestBindReferenceRecords: every distinct {record} reference resolves
// once onto the request's set — the own cohort when cohort is omitted,
// another when named — two slots naming one record share it, a lookup
// refusal is bound in the record's place, a request without one leaves
// ctx untouched, and a cancelled context returns at once.
func TestBindReferenceRecords(t *testing.T) {
	svc := recordFixture(t)
	own := `{"record":{"keys":[{"field":"id","value":"3"}]}}`
	other := `{"record":{"cohort":{"filename":"other.pulse"},"keys":[{"field":"id","value":"102"}]}}`
	missing := `{"record":{"keys":[{"field":"id","value":"99"}]}}`
	req := &types.Request{Cohort: &types.Cohort{Filename: "cohort.pulse"}, Attributes: []*types.Attribute{
		recordAttr("a", own), recordAttr("b", own), recordAttr("c", other), recordAttr("d", missing), nil,
		{Type: types.ATTR_FORMULA, Label: "f", Expression: "id + 1"},
	}}
	ctx, err := svc.bindReferenceRecords(context.Background(), req, req.Cohort)
	if err != nil {
		t.Fatal(err)
	}
	set := model.SetFrom(ctx)
	key := func(a *types.Attribute) string {
		r, ok := vectors.ComparisonRecord(a.Type, a)
		if !ok {
			t.Fatalf("%s: not a record reference", a.Label)
		}
		return r.RecordKey()
	}
	for _, c := range []struct {
		attr      *types.Attribute
		id, score float64
		cohort    string
		refusal   errors.Code
	}{
		{attr: req.Attributes[0], id: 3, score: 30, cohort: "cohort.pulse"},
		{attr: req.Attributes[1], id: 3, score: 30, cohort: "cohort.pulse"},
		{attr: req.Attributes[2], id: 102, score: 2, cohort: "other.pulse"},
		{attr: req.Attributes[3], refusal: errors.PULSE_LOOKUP_NOT_FOUND},
	} {
		rec, err := set.LookupRecord(key(c.attr))
		if c.refusal != "" {
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.refusal {
				t.Errorf("%s: %v, want %s", c.attr.Label, err, c.refusal)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.attr.Label, err)
		}
		id, _, _ := rec.Member("id")
		score, _, _ := rec.Member("score")
		if id != c.id || score != c.score || rec.Cohort() != c.cohort {
			t.Errorf("%s: id %v score %v from %s, want %v %v from %s", c.attr.Label, id, score, rec.Cohort(), c.id, c.score, c.cohort)
		}
	}
	// No {record}: ctx untouched.
	plain := &types.Request{Cohort: req.Cohort, Attributes: []*types.Attribute{recordAttr("x", "[1, 2]")}}
	base := context.Background()
	if got, err := svc.bindReferenceRecords(base, plain, plain.Cohort); err != nil || got != base {
		t.Errorf("no record reference: ctx replaced (%v)", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.bindReferenceRecords(cancelled, req, req.Cohort); !stderrors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v, want context.Canceled", err)
	}
}

// TestProcess_RecordReference: Process scores every row against the
// stored record (own and another cohort) exactly as against the literal
// of its member values, and a record the cohort's index cannot find
// refuses the run with PULSE_LOOKUP_NOT_FOUND located on the slot.
func TestProcess_RecordReference(t *testing.T) {
	svc := recordFixture(t)
	ctx := context.Background()
	run := func(ref string) (*types.Response, error) {
		return svc.Process(ctx, &types.Request{Cohort: &types.Cohort{Filename: "cohort.pulse"},
			Attributes:   []*types.Attribute{recordAttr("d", ref)},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "sum_d"}, {Type: types.AGG_MAX, Field: "d", Label: "max_d"}}})
	}
	for _, c := range []struct{ record, literal string }{
		{`{"record":{"keys":[{"field":"id","value":"3"}]}}`, `[3, 30]`},
		{`{"record":{"cohort":{"filename":"other.pulse"},"keys":[{"field":"id","value":"104"}]}}`, `[104, 4]`},
	} {
		got, err := run(c.record)
		if err != nil {
			t.Fatal(err)
		}
		want, err := run(c.literal)
		if err != nil {
			t.Fatal(err)
		}
		g, _ := json.Marshal(got.Data)
		w, _ := json.Marshal(want.Data)
		if string(g) != string(w) || string(g) == "null" {
			t.Errorf("%s scores %s, literal %s", c.record, g, w)
		}
	}
	_, err := run(`{"record":{"keys":[{"field":"id","value":"42"}]}}`)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LOOKUP_NOT_FOUND || ce.Details["attribute"] != "d" {
		t.Fatalf("not found: %v", err)
	}
}
