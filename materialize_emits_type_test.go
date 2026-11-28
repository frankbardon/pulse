package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// emitsProbe is one materialize request over matSchema's source for an
// operator that declares a manifest emits_type, and the derived columns
// it appends.
type emitsProbe struct {
	req     func(*Request)
	columns []string
}

// emitsOut is the one-column probe: the slot labelled "out".
var emitsOut = []string{"out"}

func attrProbe(a *types.Attribute) emitsProbe {
	a.Label = "out"
	return emitsProbe{func(r *Request) { r.Attributes = append(r.Attributes, a) }, emitsOut}
}

func winProbe(w *types.Window) emitsProbe {
	w.Label = "out"
	if w.OrderBy == nil && w.Type != types.WIN_FORMULA {
		w.OrderBy = []types.OrderKey{{Field: "id"}}
	}
	return emitsProbe{func(r *Request) { r.Windows = append(r.Windows, w) }, emitsOut}
}

func featProbe(f *types.Feature, cols ...string) emitsProbe {
	f.Label = "out"
	if len(cols) == 0 {
		cols = emitsOut
	}
	return emitsProbe{func(r *Request) { r.Features = append(r.Features, f) }, cols}
}

// emitsModel is the saved REG_OLS model ATTR_REG_PREDICT scores.
const emitsModel = "emits.pulse-model.json"

func rawParams(s string) json.RawMessage { return json.RawMessage(s) }

// emitsProbes holds one probe per attribute, window and feature that
// declares an emits_type, keyed "<category>/<name>".
func emitsProbes() map[string]emitsProbe {
	three := []string{"week", "spend", "level"}
	zero, two := 0, 2
	reg := func(t types.AttributeType, params string) emitsProbe {
		a := &types.Attribute{Type: t, Target: "spend", Predictors: []string{"week", "level"}}
		if params != "" {
			a.Params = rawParams(params)
		}
		return attrProbe(a)
	}
	return map[string]emitsProbe{
		"attributes/ATTR_ZSCORE":        attrProbe(&types.Attribute{Type: types.ATTR_ZSCORE, Field: "spend"}),
		"attributes/ATTR_TSCORE":        attrProbe(&types.Attribute{Type: types.ATTR_TSCORE, Field: "spend"}),
		"attributes/ATTR_NORMALIZED":    attrProbe(&types.Attribute{Type: types.ATTR_NORMALIZED, Field: "spend"}),
		"attributes/ATTR_PERCENTILE":    attrProbe(&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "spend"}),
		"attributes/ATTR_FORMULA":       attrProbe(&types.Attribute{Type: types.ATTR_FORMULA, Expression: "week * 2"}),
		"attributes/ATTR_DATE_PART":     attrProbe(&types.Attribute{Type: types.ATTR_DATE_PART, Field: "day", Params: rawParams(`{"part":"year_month_day"}`)}),
		"attributes/ATTR_SET_POPCOUNT":  attrProbe(&types.Attribute{Type: types.ATTR_SET_POPCOUNT, Field: "tags"}),
		"attributes/ATTR_SET_HAS":       attrProbe(&types.Attribute{Type: types.ATTR_SET_HAS, Field: "tags", Params: rawParams(`{"label":"a"}`)}),
		"attributes/ATTR_CODE_IN":       attrProbe(&types.Attribute{Type: types.ATTR_CODE_IN, Field: "level", Params: rawParams(`{"codes":[2,3]}`)}),
		"attributes/ATTR_REG_FITTED":    reg(types.ATTR_REG_FITTED, ""),
		"attributes/ATTR_REG_RESIDUAL":  reg(types.ATTR_REG_RESIDUAL, ""),
		"attributes/ATTR_REG_LEVERAGE":  reg(types.ATTR_REG_LEVERAGE, ""),
		"attributes/ATTR_REG_INFLUENCE": reg(types.ATTR_REG_INFLUENCE, ""),
		"attributes/ATTR_REG_PREDICT": {func(r *Request) {
			r.Models = append(r.Models, types.ModelRef{Name: "m", Path: emitsModel})
			r.Attributes = append(r.Attributes, &types.Attribute{Type: types.ATTR_REG_PREDICT, Label: "out", Params: rawParams(`{"fit":"m"}`)})
		}, emitsOut},
		"attributes/ATTR_MAHALANOBIS":        attrProbe(&types.Attribute{Type: types.ATTR_MAHALANOBIS, Fields: three}),
		"attributes/ATTR_PC_SCORE":           attrProbe(&types.Attribute{Type: types.ATTR_PC_SCORE, Fields: three, Params: rawParams(`{"component":1}`)}),
		"attributes/ATTR_FACTOR_SCORE":       attrProbe(&types.Attribute{Type: types.ATTR_FACTOR_SCORE, Fields: three, Params: rawParams(`{"factor":1,"factors":1}`)}),
		"attributes/ATTR_SEM_SCORE":          attrProbe(&types.Attribute{Type: types.ATTR_SEM_SCORE, Fields: three, Params: rawParams(`{"model":"f =~ week + spend + level"}`)}),
		"attributes/ATTR_SCALE_SCORE":        attrProbe(&types.Attribute{Type: types.ATTR_SCALE_SCORE, Fields: three}),
		"attributes/ATTR_PROFILE_SIMILARITY": attrProbe(&types.Attribute{Type: types.ATTR_PROFILE_SIMILARITY, Fields: three, Params: rawParams(`{"metric":"cosine","reference":[1,2,3]}`)}),
		"attributes/ATTR_VEC_DISTANCE":       attrProbe(&types.Attribute{Type: types.ATTR_VEC_DISTANCE, Fields: three, Params: rawParams(`{"metric":"euclidean","reference":[1,2,3]}`)}),

		"windows/WIN_ROW_NUMBER":      winProbe(&types.Window{Type: types.WIN_ROW_NUMBER}),
		"windows/WIN_RANK":            winProbe(&types.Window{Type: types.WIN_RANK, OrderBy: []types.OrderKey{{Field: "week"}}}),
		"windows/WIN_DENSE_RANK":      winProbe(&types.Window{Type: types.WIN_DENSE_RANK, OrderBy: []types.OrderKey{{Field: "week"}}}),
		"windows/WIN_RUNNING_SUM":     winProbe(&types.Window{Type: types.WIN_RUNNING_SUM, Field: "spend", Frame: &types.FrameSpec{Mode: "rows", Following: &zero}}),
		"windows/WIN_RUNNING_AVG":     winProbe(&types.Window{Type: types.WIN_RUNNING_AVG, Field: "spend", Frame: &types.FrameSpec{Mode: "rows", Following: &zero}}),
		"windows/WIN_MOVING_AVG":      winProbe(&types.Window{Type: types.WIN_MOVING_AVG, Field: "spend", Frame: &types.FrameSpec{Mode: "rows", Preceding: &two, Following: &zero}}),
		"windows/WIN_EWMA":            winProbe(&types.Window{Type: types.WIN_EWMA, Field: "spend", Frame: &types.FrameSpec{Mode: "rows"}, Params: rawParams(`{"alpha":0.5}`)}),
		"windows/WIN_DISTRIBUTED_LAG": winProbe(&types.Window{Type: types.WIN_DISTRIBUTED_LAG, Field: "spend", Params: rawParams(`{"kernel":"geometric","decay":0.5,"max_lag":2}`)}),
		"windows/WIN_PCT_CHANGE":      winProbe(&types.Window{Type: types.WIN_PCT_CHANGE, Field: "spend"}),
		"windows/WIN_DELTA":           winProbe(&types.Window{Type: types.WIN_DELTA, Field: "spend"}),
		"windows/WIN_SHARE":           winProbe(&types.Window{Type: types.WIN_SHARE, Field: "spend"}),
		"windows/WIN_LIFT":            winProbe(&types.Window{Type: types.WIN_LIFT, Field: "spend", Params: rawParams(`{"base":"week"}`)}),
		"windows/WIN_FORMULA":         winProbe(&types.Window{Type: types.WIN_FORMULA, Params: rawParams(`{"expression":"spend / week"}`)}),

		"features/FEAT_LOG":              featProbe(&types.Feature{Type: types.FEAT_LOG, Field: "spend"}),
		"features/FEAT_SQRT":             featProbe(&types.Feature{Type: types.FEAT_SQRT, Field: "spend"}),
		"features/FEAT_BUCKETIZE":        featProbe(&types.Feature{Type: types.FEAT_BUCKETIZE, Field: "spend", Params: rawParams(`{"boundaries":[5,15,25]}`)}),
		"features/FEAT_POLY":             featProbe(&types.Feature{Type: types.FEAT_POLY, Field: "spend", Params: rawParams(`{"degree":3}`)}, "out_2", "out_3"),
		"features/FEAT_FREQUENCY_ENCODE": featProbe(&types.Feature{Type: types.FEAT_FREQUENCY_ENCODE, Field: "region"}),
		"features/FEAT_TARGET_ENCODE":    featProbe(&types.Feature{Type: types.FEAT_TARGET_ENCODE, Field: "region", Params: rawParams(`{"target":"spend"}`)}),
		"features/FEAT_TRAIN_TEST_SPLIT": featProbe(&types.Feature{Type: types.FEAT_TRAIN_TEST_SPLIT, Params: rawParams(`{"ratios":[0.6,0.2,0.2],"seed":7}`)}),
	}
}

// TestManifestEmitsTypeMatchesMaterialize (#784, E4-S9): an attribute,
// window or feature operator's manifest emits_type is the field type
// Materialize writes its column as — the column a client sizes from
// the claim — never the output's value domain (a whole-number rank or
// a 0/1 flag is still an f64 column; the note states the domain). Every
// declaring operator is probed (a new declaration without a probe
// fails), and no probe outlives its declaration.
func TestManifestEmitsTypeMatchesMaterialize(t *testing.T) {
	t.Parallel()

	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	if _, err := p.Process(context.Background(), &Request{Cohort: &types.Cohort{Filename: "src.pulse"},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "spend", Predictors: []string{"week"}, Save: emitsModel}}}); err != nil {
		t.Fatalf("saving the ATTR_REG_PREDICT model: %v", err)
	}
	c := p.Manifest(context.Background()).Components
	declared := map[string]string{}
	for cat, ops := range map[string][]descriptor.Operator{"attributes": c.Attributes, "windows": c.Windows, "features": c.Features} {
		for _, op := range ops {
			if op.EmitsType != "" {
				declared[cat+"/"+op.Name] = op.EmitsType
			}
		}
	}
	for _, cat := range []string{"attributes/", "windows/", "features/"} {
		n := 0
		for k := range declared {
			if strings.HasPrefix(k, cat) {
				n++
			}
		}
		if n == 0 {
			t.Fatalf("no %s operator declares an emits_type; the manifest walk is broken", cat)
		}
	}
	probes := emitsProbes()
	for key := range probes {
		if _, ok := declared[key]; !ok {
			t.Errorf("probe for %s, which declares no emits_type: drop the probe or restore the declaration", key)
		}
	}
	keys := make([]string, 0, len(declared))
	for k := range declared {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, key := range keys {
		emits := declared[key]
		probe, ok := probes[key]
		if !ok {
			t.Errorf("%s declares emits_type %q with no probe in emitsProbes", key, emits)
			continue
		}
		out := fmt.Sprintf("emits_%d.pulse", i)
		req := &Request{Cohort: &types.Cohort{Filename: "src.pulse"}}
		probe.req(req)
		if _, err := p.Materialize(context.Background(), req, out, MaterializeOptions{}); err != nil {
			t.Errorf("%s: Materialize: %v", key, err)
			continue
		}
		schema, _ := readAll(t, p, out)
		for _, col := range probe.columns {
			f := schema.Field(col)
			if f == nil {
				t.Errorf("%s: no column %q in %v", key, col, schema.Fields)
				continue
			}
			if got := f.Type.String(); got != emits {
				t.Errorf("%s: manifest emits_type %q, materialized column %q is %s", key, emits, col, got)
			}
		}
	}
}
