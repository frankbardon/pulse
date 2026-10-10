package sweep

import (
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// rankResp is a slot response whose regression "fit" carries the given
// residual_std_err (NaN marshals as null) beside a row list.
func rankResp(se float64) *types.Response {
	return &types.Response{
		Data:        []map[string]any{{"name": "north", "v": se * 2}, {"name": "south", "v": "x"}},
		Regressions: []*types.RegressionResult{{Name: "fit", ResidualStdErr: se}},
	}
}

func intp(n int) *int { return &n }

func rankCode(t *testing.T, err error) *perr.CodedError {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_SWEEP_RANK_PATH {
		t.Fatalf("want PULSE_SWEEP_RANK_PATH, got %v", err)
	}
	return ce
}

func TestRank_OrdersTiesTopAndRank(t *testing.T) {
	labels := []string{"a", "b", "c", "d"}
	mk := func() []*types.Response {
		return []*types.Response{rankResp(3), rankResp(1), rankResp(3), rankResp(2)}
	}
	cases := []struct {
		name string
		r    types.SweepRank
		want []types.RankEntry
	}{
		{"asc default, ties in expansion order", types.SweepRank{By: "regressions.fit.residual_std_err"},
			[]types.RankEntry{{Label: "b", Value: 1, Rank: 1}, {Label: "d", Value: 2, Rank: 2}, {Label: "a", Value: 3, Rank: 3}, {Label: "c", Value: 3, Rank: 4}}},
		{"desc", types.SweepRank{By: "regressions.fit.residual_std_err", Order: types.SweepRankDesc},
			[]types.RankEntry{{Label: "a", Value: 3, Rank: 1}, {Label: "c", Value: 3, Rank: 2}, {Label: "d", Value: 2, Rank: 3}, {Label: "b", Value: 1, Rank: 4}}},
		{"top trims the ranking", types.SweepRank{By: "regressions.fit.residual_std_err", Top: intp(2)},
			[]types.RankEntry{{Label: "b", Value: 1, Rank: 1}, {Label: "d", Value: 2, Rank: 2}}},
		{"top past the slot count", types.SweepRank{By: "regressions.fit.residual_std_err", Top: intp(9)},
			[]types.RankEntry{{Label: "b", Value: 1, Rank: 1}, {Label: "d", Value: 2, Rank: 2}, {Label: "a", Value: 3, Rank: 3}, {Label: "c", Value: 3, Rank: 4}}},
		{"list segment selects by name in data rows", types.SweepRank{By: "data.north.v"},
			[]types.RankEntry{{Label: "b", Value: 2, Rank: 1}, {Label: "d", Value: 4, Rank: 2}, {Label: "a", Value: 6, Rank: 3}, {Label: "c", Value: 6, Rank: 4}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Rank(&tc.r, labels, mk())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// TestRank_NullSlotExcludedWithWarning: an undefined figure (NaN, null
// on the wire) is left out — never ranked last — and its slot carries
// the warning naming it.
func TestRank_NullSlotExcludedWithWarning(t *testing.T) {
	resps := []*types.Response{rankResp(2), rankResp(math.NaN()), rankResp(1)}
	got, err := Rank(&types.SweepRank{By: "regressions.fit.residual_std_err"}, []string{"a", "b", "c"}, resps)
	if err != nil {
		t.Fatal(err)
	}
	want := []types.RankEntry{{Label: "c", Value: 1, Rank: 1}, {Label: "a", Value: 2, Rank: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if len(resps[0].Warnings)+len(resps[2].Warnings) != 0 {
		t.Fatal("a ranked slot got a warning")
	}
	if len(resps[1].Warnings) != 1 {
		t.Fatalf("excluded slot warnings: %+v", resps[1].Warnings)
	}
	w := resps[1].Warnings[0]
	if w.Code != string(perr.PULSE_SWEEP_RANK_PATH) || w.Details["label"] != "b" || w.Details["reason"] != RankReasonNull {
		t.Fatalf("warning %+v", w)
	}
}

// TestRank_MissingInSomeSlotsWarns: a path missing in one slot but
// resolved in another excludes that slot with a warning.
func TestRank_MissingInSomeSlotsWarns(t *testing.T) {
	resps := []*types.Response{rankResp(2), {Regressions: []*types.RegressionResult{{Name: "other"}}}}
	got, err := Rank(&types.SweepRank{By: "regressions.fit.residual_std_err"}, []string{"a", "b"}, resps)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Label != "a" {
		t.Fatalf("got %+v", got)
	}
	if len(resps[1].Warnings) != 1 || resps[1].Warnings[0].Details["reason"] != RankReasonMissing ||
		resps[1].Warnings[0].Details["segment"] != "fit" {
		t.Fatalf("warnings %+v", resps[1].Warnings)
	}
}

func TestRank_Refusals(t *testing.T) {
	ambiguousRows := &types.Response{Data: []map[string]any{{"name": "north", "v": 1.0}, {"label": "north", "v": 2.0}}}
	nameLabel := &types.Response{Data: []map[string]any{{"name": "north", "label": "n", "v": 1.0}}}
	cases := []struct {
		name    string
		by      string
		resps   []*types.Response
		reason  string
		label   string
		segment string
	}{
		{"missing in every slot", "regressions.nope.residual_std_err",
			[]*types.Response{rankResp(1), rankResp(2)}, RankReasonMissing, "a", "nope"},
		{"ends on a string", "data.south.v",
			[]*types.Response{rankResp(1), rankResp(2)}, RankReasonNotNumber, "a", "v"},
		{"ends on an object", "regressions.fit",
			[]*types.Response{rankResp(1)}, RankReasonNotNumber, "a", "fit"},
		{"descends into a scalar", "regressions.fit.residual_std_err.deeper",
			[]*types.Response{rankResp(1)}, RankReasonNotNumber, "a", "deeper"},
		{"two elements match", "data.north.v",
			[]*types.Response{rankResp(1), ambiguousRows}, RankReasonAmbiguous, "b", "north"},
		{"element whose name and label differ", "data.north.v",
			[]*types.Response{nameLabel}, RankReasonAmbiguous, "a", "north"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Rank(&types.SweepRank{By: tc.by}, []string{"a", "b"}[:len(tc.resps)], tc.resps)
			ce := rankCode(t, err)
			d := ce.Details
			if d["reason"] != tc.reason || d["label"] != tc.label || d["segment"] != tc.segment || d["by"] != tc.by {
				t.Fatalf("details %+v", d)
			}
		})
	}
}

func TestParseRankPath(t *testing.T) {
	for _, by := range []string{".a", "a.", "a..b", "."} {
		if _, err := ParseRankPath(by); err == nil || err.Details["reason"] != RankReasonSyntax {
			t.Fatalf("%q: %v", by, err)
		}
	}
	segs, err := ParseRankPath("regressions.mmm.residual_std_err")
	if err != nil || !reflect.DeepEqual(segs, []string{"regressions", "mmm", "residual_std_err"}) {
		t.Fatalf("%v %v", segs, err)
	}
}

// TestValidate_RankPathSyntax: a malformed non-empty `by` is refused by
// the structural check every surface (predict included) runs.
func TestValidate_RankPathSyntax(t *testing.T) {
	spec := &types.SweepSpec{
		Axes:    []types.SweepAxis{{Name: "x", Values: []any{1}}},
		Request: []byte(`{"label":"{{x}}"}`),
		Rank:    &types.SweepRank{By: "a..b"},
	}
	ce := Validate(spec)
	if ce == nil || ce.Code != perr.PULSE_SWEEP_RANK_PATH || ce.Details["reason"] != RankReasonSyntax {
		t.Fatalf("got %v", ce)
	}
}
