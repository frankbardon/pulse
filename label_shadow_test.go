package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestLabelShadow_RefusedOnEverySide: a derived column whose name is an
// already-available column — an attribute label equal to a schema field
// (a NULLABLE one, where the buffered arm kept the field's null mark
// through injectValue and the streaming arm cleared it through Set, so
// the two answered different numbers) or to an earlier attribute label,
// a feature output equal to a schema field, a window label equal to an
// output-row column or an earlier window label — is refused by the
// shared field rule with one code, message and details on Process,
// ProcessStream and predict. No documented request relies on the
// overwrite. A FEAT_POLY Label equal to its Field is a PREFIX (outputs
// <label>_2...), not a shadow, and stays accepted.
func TestLabelShadow_RefusedOnEverySide(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "s.csv", []byte("cat,n,m\na,10,\nb,20,5\na,30,\nb,40,7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "s.csv"})
	if err != nil {
		t.Fatal(err)
	}
	sch, err := schemaLoaderFor(fs)(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	if f := sch.Field("m"); f == nil || !f.Nullable {
		t.Fatalf("m imported non-nullable — the shadowed field must carry null marks for the test to bite")
	}
	c := func() *types.Cohort { return &types.Cohort{Filename: res.Path} }
	formula := func(label string) *types.Attribute {
		return &types.Attribute{Type: types.ATTR_FORMULA, Label: label, Params: json.RawMessage(`{"expression":"n * 10"}`)}
	}
	sumM := []*types.Aggregation{{Type: types.AGG_SUM, Field: "m", Label: "total"}}
	cases := map[string]struct {
		req     func() *types.Request
		message string
	}{
		"attribute shadows nullable schema field": {func() *types.Request {
			return &types.Request{Cohort: c(), Attributes: []*types.Attribute{formula("m")}, Aggregations: sumM}
		}, "attribute label m shadows an existing field"},
		"attribute shadows earlier attribute": {func() *types.Request {
			return &types.Request{Cohort: c(), Attributes: []*types.Attribute{formula("x"), formula("x")}, Aggregations: sumM}
		}, "attribute label x shadows an existing field"},
		"feature output shadows schema field": {func() *types.Request {
			return &types.Request{Cohort: c(), Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "n", Label: "m"}}, Aggregations: sumM}
		}, "feature FEAT_LOG output m shadows an existing field"},
		"window shadows aggregation label": {func() *types.Request {
			return &types.Request{Cohort: c(), Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}, Aggregations: sumM,
				Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "total", OrderBy: []types.OrderKey{{Field: "cat"}}}}}
		}, "window[0] label total shadows an existing column"},
		"window shadows earlier window": {func() *types.Request {
			w := func() *types.Window {
				return &types.Window{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "n"}}}
			}
			return &types.Request{Cohort: c(), Windows: []*types.Window{w(), w()}}
		}, "window[1] label rn shadows an existing column"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, perr := p.Process(ctx, tc.req())
			ce := requireCode(t, perr, errors.SERVICE_VALIDATION)
			if ce.Message != tc.message {
				t.Fatalf("runtime message = %q, want %q", ce.Message, tc.message)
			}
			_, serr := p.ProcessStream(ctx, tc.req())
			if se := requireCode(t, serr, errors.SERVICE_VALIDATION); se.Message != ce.Message {
				t.Fatalf("stream = %q, process = %q", se.Message, ce.Message)
			}
			sameEntry(t, predictEnvelope(t, p, fs, res.Path, tc.req()), perr)
		})
	}

	t.Run("FEAT_POLY label is a prefix, not a shadow", func(t *testing.T) {
		req := func() *types.Request {
			return &types.Request{Cohort: c(), Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n_2"}},
				Features: []*types.Feature{{Type: types.FEAT_POLY, Field: "n", Label: "n", Params: json.RawMessage(`{"degree":2}`)}}}
		}
		if _, err := p.Process(ctx, req()); err != nil {
			t.Fatalf("runtime: %v", err)
		}
		if env := predictEnvelope(t, p, fs, res.Path, req()); len(env.Errors) != 0 {
			t.Fatalf("predict: %+v", env.Errors[0])
		}
	})
}
