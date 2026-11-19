package integration_test

import (
	"context"
	stderrors "errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestScoringAttributes_ZeroSpreadWarnsEveryArm: a zero spread used to
// null every ATTR_ZSCORE / ATTR_TSCORE / ATTR_NORMALIZED score
// silently (U50 E7-S2); it now raises one PULSE_ATTR_ZERO_SPREAD per
// slot (U75 #482) — details {label, type, field, n, spread, reason} —
// and under Options.Strict the run fails with that code and the same
// details. Every case runs on the streaming two-pass arm (ungrouped),
// the buffered arm (grouped; serial decode too) and a shard archive
// reduced in parallel and serially, and every arm must raise the same
// warning. A positive spread and an all-null base raise none, and the
// scores stay null either way.
func TestScoringAttributes_ZeroSpreadWarnsEveryArm(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	constFS, spreadFS := afero.NewMemMapFs(), afero.NewMemMapFs()
	constRows := scoreNullRows()
	for i := range constRows {
		constRows[i].x = 7.25
	}
	constCohort, constArchive := writeScoreNulls(t, constFS, constRows)
	spreadCohort, spreadArchive := writeScoreNulls(t, spreadFS, scoreNullRows())
	nonNull := int64(0)
	for _, r := range constRows {
		if !r.null {
			nonNull++
		}
	}

	type zsCase struct {
		name           string
		fs             afero.Fs
		single, arch   string
		typ            types.AttributeType
		weight         bool
		lo, hi         string // FILTER_RANGE on n ("" = none)
		reason, spread string // "" = no warning
		n              int64
	}
	var cases []zsCase
	for _, typ := range []types.AttributeType{types.ATTR_ZSCORE, types.ATTR_TSCORE, types.ATTR_NORMALIZED} {
		spread := "stddev"
		if typ == types.ATTR_NORMALIZED {
			spread = "range"
		}
		cases = append(cases,
			zsCase{name: "constant", fs: constFS, single: constCohort, arch: constArchive, typ: typ, reason: "constant", spread: spread, n: nonNull},
			// Rows 1 (null x) and 2: one value contributes.
			zsCase{name: "single-value", fs: spreadFS, single: spreadCohort, arch: spreadArchive, typ: typ, lo: "1", hi: "2", reason: "single_value", spread: spread, n: 1},
			zsCase{name: "positive-spread", fs: spreadFS, single: spreadCohort, arch: spreadArchive, typ: typ},
			// Row 1 only: x is null, so nothing is nulled by the spread.
			zsCase{name: "all-null", fs: spreadFS, single: spreadCohort, arch: spreadArchive, typ: typ, lo: "1", hi: "1"},
		)
		if typ != types.ATTR_NORMALIZED { // not weightable
			cases = append(cases,
				zsCase{name: "weighted-constant", fs: constFS, single: constCohort, arch: constArchive, typ: typ, weight: true,
					// Row 0 carries weight n = 0: no mass.
					reason: "constant", spread: spread, n: nonNull - 1},
				// Row 0 only: a non-null x whose weight n is 0.
				zsCase{name: "no-mass", fs: spreadFS, single: spreadCohort, arch: spreadArchive, typ: typ, weight: true, lo: "0", hi: "0",
					reason: "no_mass", spread: spread, n: 0},
				zsCase{name: "weighted-single-value", fs: spreadFS, single: spreadCohort, arch: spreadArchive, typ: typ, weight: true, lo: "0", hi: "2",
					reason: "single_value", spread: spread, n: 1},
			)
		}
	}

	for _, c := range cases {
		build := func(path string, grouped bool) *types.Request {
			a := &types.Attribute{Type: c.typ, Field: "x", Label: "s"}
			if c.weight {
				a.Weight = types.SlotWeightField("n")
			}
			req := &types.Request{Cohort: &types.Cohort{Filename: path}, Attributes: []*types.Attribute{a},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "s", Label: "ns"}}}
			if grouped {
				req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "site"}}
			}
			if c.lo != "" {
				req.Filterers = []*types.Filterer{{Type: types.FILTER_RANGE, Field: "n", Values: []string{c.lo, c.hi}}}
			}
			return req
		}
		var want *types.ResponseWarning
		if c.reason != "" {
			want = &types.ResponseWarning{Code: string(errors.PULSE_ATTR_ZERO_SPREAD), Details: map[string]any{
				"label": "s", "type": string(c.typ), "field": "x", "n": c.n, "spread": c.spread, "reason": c.reason,
			}}
		}
		var ref string // the first arm's message: every arm must match it
		for _, arm := range []struct {
			name    string
			o       pulse.Options
			archive bool
			grouped bool
		}{
			{"stream", pulse.Options{}, false, false},
			{"buffered", pulse.Options{}, false, true},
			{"buffered-serial", pulse.Options{DecodeWorkers: 1}, false, true},
			{"archive-stream", pulse.Options{}, true, false},
			{"archive-buffered", pulse.Options{}, true, true},
			{"archive-serial", pulse.Options{ShardWorkers: 1}, true, true},
		} {
			path := c.single
			if arm.archive {
				path = c.arch
			}
			name := fmt.Sprintf("%s/%s/%s", c.name, c.typ, arm.name)
			for _, strict := range []bool{false, true} {
				o := arm.o
				o.FS, o.Strict = c.fs, strict
				resp, err := nullDatePulse(t, o).Process(ctx, build(path, arm.grouped))
				if strict && want != nil {
					var ce *errors.CodedError
					if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_ATTR_ZERO_SPREAD {
						t.Fatalf("%s strict: err %v, want %s", name, err, errors.PULSE_ATTR_ZERO_SPREAD)
					}
					if !reflect.DeepEqual(ce.Details, want.Details) {
						t.Fatalf("%s strict details %+v, want %+v", name, ce.Details, want.Details)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s strict=%v: %v", name, strict, err)
				}
				var got []*types.ResponseWarning
				for _, w := range resp.Warnings {
					if w.Code == string(errors.PULSE_ATTR_ZERO_SPREAD) {
						got = append(got, w)
					}
				}
				for _, a := range resp.Components.Aggregations {
					if a.Label == "ns" && a.N != 0 && want != nil {
						t.Fatalf("%s: %d scores are non-null under a zero spread", name, a.N)
					}
				}
				if want == nil {
					if len(got) != 0 {
						t.Fatalf("%s: warned %+v, want none", name, got)
					}
					continue
				}
				if len(got) != 1 {
					t.Fatalf("%s: %d %s warnings %+v, want 1", name, len(got), errors.PULSE_ATTR_ZERO_SPREAD, resp.Warnings)
				}
				if !reflect.DeepEqual(got[0].Details, want.Details) {
					t.Fatalf("%s details %+v, want %+v", name, got[0].Details, want.Details)
				}
				if ref == "" {
					ref = got[0].Message
				} else if got[0].Message != ref {
					t.Fatalf("%s message %q, other arms %q", name, got[0].Message, ref)
				}
			}
		}
	}
}
