package guide

import (
	"github.com/frankbardon/pulse/descriptor"
)

// Operator categories a draft request can carry. Overlays and synth
// distributions are absent on purpose: an overlay needs a host request
// (crosstab or grouped series) a single-operator draft does not have,
// and a distribution serves the non-analytic simulate intent, which
// routes to tooling.
const (
	catAggregator = "aggregator"
	catAttribute  = "attribute"
	catFilterer   = "filterer"
	catGrouper    = "grouper"
	catWindow     = "window"
	catFeature    = "feature"
	catTest       = "test"
	catPostTest   = "post_test"
	catRegression = "regression"
	catMatrix     = "matrix"
)

// slotKey is the wire key of the request slot each category's entry
// rides in. The JSON names differ from the catalog names
// (aggregations, groups); TestSlotKeysMatchWire holds them to
// types.Request's tags.
var slotKey = map[string]string{
	catAggregator: "aggregations",
	catAttribute:  "attributes",
	catFilterer:   "filterers",
	catGrouper:    "groups",
	catWindow:     "windows",
	catFeature:    "features",
	catTest:       "tests",
	catPostTest:   "post_tests",
	catRegression: "regressions",
	catMatrix:     "matrices",
}

// requireKey maps a TestMeta.Requires entry — a types.Test Go field
// name — to its wire key. TestRequireKeysMatchWire holds every entry to
// the types.Test JSON tag and every manifest Requires value to a row,
// so a test that grows a new required slot fails until it is mapped.
var requireKey = map[string]string{
	"Field":        "field",
	"Field2":       "field2",
	"SplitBy":      "split_by",
	"Rows":         "rows",
	"Cols":         "cols",
	"SubjectField": "subject_field",
	"OrderBy":      "order_by",
}

// topLevelParams lists, per category, the declared params that are
// top-level keys of the slot entry rather than members of its `params`
// object. Every regression param is a RegressionSpec field; a grouper's
// interval is Group.Interval. Everything else rides `params`.
var topLevelParams = map[string]map[string]bool{
	catGrouper: {"interval": true},
}

// opInfo is what a draft needs to know about one operator: its
// category and the declared slots and params it cannot run without.
type opInfo struct {
	name         string
	category     string
	ignoresField bool
	accepts      []string // field type names the operator's field takes; empty = undeclared
	requires     []string // tests: types.Test Go field names
	params       []param  // required params without a default
}

// param is one required, default-less param. typ and fieldFilter are
// the manifest's: a "field" param names a schema field, so bound mode
// fills it from the schema instead of asking the caller.
type param struct {
	name        string
	list        bool
	typ         string
	fieldFilter string
}

// catalog indexes the instance manifest's operator entries by name. A
// hidden operator is absent from the instance manifest, so it is absent
// here too.
func catalog(m *descriptor.Manifest) map[string]opInfo {
	out := map[string]opInfo{}
	addOps := func(ops []descriptor.Operator) {
		for _, o := range ops {
			out[o.Name] = opInfo{name: o.Name, category: o.Category, ignoresField: o.IgnoresField, accepts: o.AcceptsTypes, params: requiredParams(o.Params)}
		}
	}
	c := m.Components
	for _, ops := range [][]descriptor.Operator{c.Aggregators, c.Attributes, c.Filterers, c.Groupers, c.Windows, c.Features} {
		addOps(ops)
	}
	for _, t := range m.Tests {
		if t.Name == t.Family {
			out[t.Name] = opInfo{name: t.Name, category: catTest, requires: t.Requires, params: requiredParams(t.Params)}
		}
	}
	// TREND and TUKEY_HSD are natively tier 2: their family entry is a
	// post-test. A tier-1 family wins over its post-test twins.
	for _, t := range m.PostTests {
		if _, ok := out[t.Name]; !ok && t.Name == t.Family {
			out[t.Name] = opInfo{name: t.Name, category: catPostTest, requires: t.Requires, params: requiredParams(t.Params)}
		}
	}
	for _, r := range m.Regressions {
		out[r.Name] = opInfo{name: r.Name, category: catRegression, accepts: r.AcceptsTypes, params: requiredParams(r.Params)}
	}
	for _, x := range m.Matrices {
		out[x.Name] = opInfo{name: x.Name, category: catMatrix, params: requiredParams(x.Params)}
	}
	e := m.Extensions
	for cat, metas := range map[string][]descriptor.OperatorMeta{
		catAggregator: e.Aggregators, catAttribute: e.Attributes, catFilterer: e.Filterers,
		catGrouper: e.Groupers, catWindow: e.Windows, catFeature: e.Features, catTest: e.Tests,
	} {
		for _, o := range metas {
			info := opInfo{name: o.Name, category: cat, accepts: o.Accepts}
			if cat == catTest {
				info.requires = []string{"Field"}
				if o.Tier == "tier2" {
					info.category = catPostTest
				}
			}
			for _, p := range o.Params {
				if p.Required && p.Default == nil {
					info.params = append(info.params, param{name: p.Name, list: p.JSONType == "array", typ: p.JSONType})
				}
			}
			out[o.Name] = info
		}
	}
	return out
}

// requiredParams keeps the params a draft must name: required and with
// no default.
func requiredParams(ps []descriptor.Param) []param {
	var out []param
	for _, p := range ps {
		if p.Required && p.Default == nil {
			out = append(out, param{name: p.Name, list: p.Type == "list" || p.Type == "array", typ: p.Type, fieldFilter: p.FieldFilter})
		}
	}
	return out
}
