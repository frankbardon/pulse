package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// cfSlot is one unfiltered Compose slot over the multi-grouper cohort,
// grouped by groups and counting ids (label "n").
func cfSlot(f *mgFixture, label string, groups []*types.Group) *types.Request {
	req := mgRequest(f, groups)
	req.Label = label
	return req
}

// cfFormula is a Compose OVERLAY_FORMULA over reference "a" and target
// "b"; partitionBy nil leaves params.partition_by absent.
func cfFormula(name, formula string, partitionBy []string) types.ComposeOverlaySpec {
	params := map[string]any{"formula": formula}
	if partitionBy != nil {
		params["partition_by"] = partitionBy
	}
	return types.ComposeOverlaySpec{Name: name, Kind: types.OverlayKindFormula, Scope: types.OverlayScopeGroup,
		Reference: "a", Targets: []string{"b"}, Params: params}
}

// cfCompose is the a / b Compose request, both slots grouped by groups.
func cfCompose(f *mgFixture, groups func() []*types.Group, specs ...types.ComposeOverlaySpec) *types.ComposedRequest {
	return &types.ComposedRequest{
		Requests: []*types.Request{cfSlot(f, "a", groups()), cfSlot(f, "b", groups())},
		Overlays: specs,
	}
}

// cfStats renders each layer's statistics in entry order.
func cfStats(t *testing.T, resp *types.ComposedResponse) []string {
	t.Helper()
	out := make([]string, 0, len(resp.Overlays))
	for _, l := range resp.Overlays {
		if l.Payload.Series == nil {
			t.Fatalf("layer %s: no series payload: %+v", l.Name, l.Payload)
		}
		parts := make([]string, 0, len(l.Payload.Series.Entries))
		for _, e := range l.Payload.Series.Entries {
			st := "absent"
			if e.Summary.Statistic != nil {
				st = fmt.Sprint(*e.Summary.Statistic)
			}
			parts = append(parts, st)
		}
		out = append(out, l.Name+":"+strings.Join(parts, ","))
	}
	return out
}

func cfRegionSex() []*types.Group { return []*types.Group{mgRegion(), mgSex()} }

// TestComposeFormula_PartitionedOverTwoGroupers (#438): over region x
// sex, a Compose FORMULA's `total` is each region's sum and `prior` the
// previous present row within the region — the Process rule — never the
// grand total or a row of the previous region. Explicit
// partition_by ["region"] equals the default; [] restores one walk over
// every row (the pre-U66 figures). Counts per row, in Data order: north
// F 7, M 5; south F 4, M 6; west F 3, M 5.
func TestComposeFormula_PartitionedOverTwoGroupers(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	cases := []struct {
		name        string
		partitionBy []string
		want        []string
	}{
		{"implicit", nil, []string{"total:12,12,10,10,8,8", "prior:NaN,7,NaN,4,NaN,3"}},
		{"explicit", []string{"region"}, []string{"total:12,12,10,10,8,8", "prior:NaN,7,NaN,4,NaN,3"}},
		{"none", []string{}, []string{"total:30,30,30,30,30,30", "prior:NaN,7,5,4,6,3"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := cfCompose(f, cfRegionSex, cfFormula("total", "total", c.partitionBy), cfFormula("prior", "prior", c.partitionBy))
			if env := csPredict(t, f.p, req); len(env.Errors) > 0 {
				t.Fatalf("predict-compose: %+v", env.Errors)
			}
			resp, err := f.p.Compose(context.Background(), req)
			if err != nil {
				t.Fatalf("compose: %v", err)
			}
			if got := cfStats(t, resp); !reflect.DeepEqual(got, c.want) {
				t.Errorf("stats = %v, want %v", got, c.want)
			}
		})
	}
}

// TestComposeFormula_PartitionInvalidParity: a partition_by that is not
// a leading prefix of the host slot's group key columns is ONE
// PULSE_OVERLAY_PARTITION_INVALID from predict-compose and from the
// runtime, Details equal (the host slot located by Details.slot).
func TestComposeFormula_PartitionInvalidParity(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, c := range []struct {
		name   string
		pb     []string
		reason string
	}{
		{"not prefix", []string{"sex"}, "not_prefix"},
		{"unknown", []string{"tier"}, "unknown_field"},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := cfCompose(f, cfRegionSex, cfFormula("ok", "value", nil), cfFormula("bad", "total", c.pb))
			_, err := f.p.Compose(context.Background(), req)
			ce := requireCodedError(t, err, perr.PULSE_OVERLAY_PARTITION_INVALID)
			want := map[string]any{"index": 1, "kind": string(types.OverlayKindFormula), "partition_by": c.pb,
				"groups": []string{"region", "sex"}, "reason": c.reason, "field": c.pb[0], "slot": "b"}
			if !reflect.DeepEqual(ce.Details, want) {
				t.Errorf("runtime details %#v, want %#v", ce.Details, want)
			}
			env := csPredict(t, f.p, req)
			if len(env.Errors) != 1 || env.Errors[0].Code != string(perr.PULSE_OVERLAY_PARTITION_INVALID) {
				t.Fatalf("predict-compose: want exactly one PULSE_OVERLAY_PARTITION_INVALID, got %+v", env.Errors)
			}
			if !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
				t.Errorf("predict details %#v != runtime %#v", env.Errors[0].Details, ce.Details)
			}
			if env.Errors[0].Message != ce.Message {
				t.Errorf("message drift: predict %q, runtime %q", env.Errors[0].Message, ce.Message)
			}
		})
	}
}

// cfOneGrouperGolden is the overlay layers a 1-grouper Compose FORMULA
// without params.field answered before U66, pinned byte for byte:
// counts north 12, south 10, west 8.
const cfOneGrouperGolden = `[{"name":"mix","kind":"OVERLAY_FORMULA","scope":"group","ref":{},"payload":{"shape":"series","series":{"entries":[{"key":["region=north"],"summary":{"statistic":null}},{"key":["region=south"],"summary":{"statistic":10030012.01}},{"key":["region=west"],"summary":{"statistic":8030010.008}}]}},"summary":{"min":8030010.008,"max":10030012.01,"count":2}},{"name":"share","kind":"OVERLAY_FORMULA","scope":"group","ref":{},"payload":{"shape":"series","series":{"entries":[{"key":["region=north"],"summary":{"statistic":0.4}},{"key":["region=south"],"summary":{"statistic":0.3333333333333333}},{"key":["region=west"],"summary":{"statistic":0.26666666666666666}}]}},"summary":{"min":0.26666666666666666,"max":0.4,"count":3}}]`

// TestComposeFormula_OneGrouperUnchanged: a 1-grouper Compose FORMULA
// without params.field is byte-identical to the pre-U66 layers, and an
// explicit partition_by [] changes nothing on one grouper.
func TestComposeFormula_OneGrouperUnchanged(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	run := func(pb []string) string {
		req := cfCompose(f, csRegion,
			cfFormula("mix", "value * 1000000 + total * 1000 + prior + ref_value / 1000", pb),
			cfFormula("share", "value / total", pb))
		resp, err := f.p.Compose(context.Background(), req)
		if err != nil {
			t.Fatalf("compose: %v", err)
		}
		b, err := json.Marshal(resp.Overlays)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	got := run(nil)
	if got != cfOneGrouperGolden {
		t.Errorf("layers =\n%s\nwant\n%s", got, cfOneGrouperGolden)
	}
	if none := run([]string{}); none != got {
		t.Errorf("partition_by [] =\n%s\nwant the default\n%s", none, got)
	}
}

// TestComposeFormula_ScoresNamedField (#441): a Compose FORMULA's
// value, ref_value and slot.<label>.value read the aggregation
// params.field names on each slot — over [zz_n, aa_sum] it equals the
// same overlay over [aa_sum] alone, and absent field scores
// aggregations[0] (zz_n). A field a slot lacks is ONE
// PULSE_OVERLAY_FIELD_UNKNOWN from predict-compose and the runtime,
// Details equal. Named explicitly, not through seriesfield.ComposeKinds,
// so dropping FORMULA from that set fails here.
func TestComposeFormula_ScoresNamedField(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	kind := types.OverlayKindFormula
	run := func(field string, aggs ...func() *types.Aggregation) string {
		req := csCompose(f, csOverlay(kind, field), csRegion, aggs...)
		if env := csPredict(t, f.p, req); len(env.Errors) > 0 {
			t.Fatalf("predict-compose: %+v", env.Errors)
		}
		resp, err := f.p.Compose(context.Background(), req)
		if err != nil {
			t.Fatalf("compose: %v", err)
		}
		return csRender(t, resp)
	}
	named := run("aa_sum", csZ, csA)
	if want := run("", csA); named != want {
		t.Errorf("field aa_sum over [zz_n, aa_sum]:\n %s\nwant [aa_sum] alone:\n %s", named, want)
	}
	def := run("", csZ, csA)
	if want := run("", csZ); def != want {
		t.Errorf("absent field over [zz_n, aa_sum]:\n %s\nwant [zz_n] alone:\n %s", def, want)
	}
	if named == def {
		t.Errorf("aa_sum and zz_n scored identically: %s", def)
	}

	req := csCompose(f, csOverlay(kind, "aa_sum"), csRegion, csZ, csA)
	req.Requests[1].Aggregations = []*types.Aggregation{csZ()} // target M lacks aa_sum
	_, err := f.p.Compose(context.Background(), req)
	ce := requireCodedError(t, err, perr.PULSE_OVERLAY_FIELD_UNKNOWN)
	want := map[string]any{"index": 0, "kind": string(kind), "field": "aa_sum", "available": []string{"zz_n"}, "slot": "M"}
	if !reflect.DeepEqual(ce.Details, want) {
		t.Errorf("runtime details %#v, want %#v", ce.Details, want)
	}
	env := csPredict(t, f.p, req)
	if len(env.Errors) != 1 || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
		t.Errorf("predict-compose = %+v, want one error with details %#v", env.Errors, ce.Details)
	}
}
