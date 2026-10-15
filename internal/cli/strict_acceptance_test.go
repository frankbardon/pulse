package cli

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/internal/synth"
	"github.com/frankbardon/pulse/internal/template"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// strict_acceptance_test.go is the U44 #311 in-pulse acceptance: a key
// misspelled in EVERY slot family of EVERY JSON request root is a coded
// refusal — PULSE_REQUEST_UNKNOWN_FIELD with details.path locating it
// (PULSE_TEMPLATE_RENDER_INVALID on a rendered template, by provenance) —
// on the predict leaf AND the process leaf of the root wherever both
// exist, never a silently dropped key. The family list is not
// hand-maintained: strictAcceptanceRequiredFamilies derives it from the
// root types' json tags (and the template target list), and the test
// fails when a family has no row — a new slot cannot be skipped.

// strictRow is one misspelling.
type strictRow struct {
	family  string // "<root>.<key>[.<key>...]" — matched against the derived family list
	body    string // the whole JSON document
	path    string // details.path; "" = the document root (absent)
	unknown string // details.unknown_keys[0]
	locator string // "request" / "fit" / "stage" / "slot" (sweep point) detail expected (index 0), "" = none
}

// strictRoot names the leaves one root runs through.
type strictRoot struct {
	leaves [][]string // predict + process leaves (one when only one exists)
}

func strictAcceptanceRoots() map[string]strictRoot {
	return map[string]strictRoot{
		"request":  {leaves: [][]string{{"predict"}, {"process"}}},
		"composed": {leaves: [][]string{{"predict-compose"}, {"compose"}}},
		"chain":    {leaves: [][]string{{"predict-chain"}, {"process-chain"}}},
		"facet":    {leaves: [][]string{{"predict-facet"}, {"facet", "--top-k", "3"}}},
		"lookup":   {leaves: [][]string{{"lookup"}}}, // no predict leaf
	}
}

// strictAcceptanceRows is the table. c is the cohort object JSON.
func strictAcceptanceRows(c string) []strictRow {
	req := func(frag string) string { return `{"cohort":` + c + `,` + frag + `}` }
	agg := `"aggregations":[{"type":"AGG_SUM","field":"n","label":"t"}]`
	grp := `{"type":"GROUP_CATEGORY","field":"cat"}`
	cell := `"cell":{"type":"AGG_COUNT","field":"n"}`
	xt := func(frag string) string {
		return req(`"crosstab":{` + frag + `}`)
	}
	nested := func(frag string) string { return `{"cohort":` + c + `,` + frag + `}` }
	composed := func(frag string) string { return `{"requests":[` + nested(agg) + `],` + frag + `}` }
	chain := func(frag string) string {
		return `{"cohort":` + c + `,"stages":[{"name":"s","request":` + nested(agg) + `}],` + frag + `}`
	}
	facet := func(frag string) string { return `{"cohort":` + c + `,"fields":["cat"],` + frag + `}` }
	sweepAxes := `"axes":[{"name":"k","values":[1,2]}]`

	return []strictRow{
		// --- Request root and every slot family ---
		{family: "request", body: req(`"aggregatoins":[]`), unknown: "aggregatoins"},
		{family: "request.cohort", body: `{"cohort":{"filename":` + strconv.Quote(cohortFile(c)) + `,"data_dri":"x"},` + agg + `}`, path: "cohort.data_dri", unknown: "data_dri"},
		{family: "request.filterers", body: req(`"filterers":[{"type":"FILTER_INCLUDE","field":"cat","valuse":["a"]}]`), path: "filterers[0].valuse", unknown: "valuse"},
		{family: "request.filterers.params", body: req(`"filterers":[{"type":"FILTER_INCLUDE","field":"cat","values":["a"],"params":{"negete":true}}]`), path: "filterers[0].params.negete", unknown: "negete"},
		{family: "request.aggregations", body: req(`"aggregations":[{"type":"AGG_SUM","field":"n","lable":"t"}]`), path: "aggregations[0].lable", unknown: "lable"},
		{family: "request.aggregations.params", body: req(`"aggregations":[{"type":"AGG_PERCENTILE","field":"n","params":{"percentil":50}}]`), path: "aggregations[0].params.percentil", unknown: "percentil"},
		{family: "request.aggregations.weight", body: req(`"aggregations":[{"type":"AGG_SUM","field":"n","weight":{"feild":"n"}}]`), path: "aggregations[0].weight.feild", unknown: "feild"},
		{family: "request.attributes", body: req(agg + `,"attributes":[{"type":"ATTR_ZSCORE","field":"n","lable":"z"}]`), path: "attributes[0].lable", unknown: "lable"},
		{family: "request.attributes.params", body: req(agg + `,"attributes":[{"type":"ATTR_ZSCORE","field":"n","label":"z","params":{"ddoff":1}}]`), path: "attributes[0].params.ddoff", unknown: "ddoff"},
		{family: "request.attributes.for_each", body: req(agg + `,"attributes":[{"for_each":{"axis":[{"name":"f","values":["n"]}]},"type":"ATTR_ZSCORE","field":"{{f}}","label":"z_{{f}}"}]`), path: "attributes[0].for_each.axis", unknown: "axis"},
		{family: "request.attributes.for_each.item", body: req(agg + `,"attributes":[{"for_each":{"axes":[{"name":"f","values":["n"]}]},"type":"ATTR_ZSCORE","field":"{{f}}","lable":"z_{{f}}"}]`), path: "attributes[0].lable", unknown: "lable"},
		{family: "request.groups", body: req(agg + `,"groups":[{"type":"GROUP_CATEGORY","feild":"cat"}]`), path: "groups[0].feild", unknown: "feild"},
		{family: "request.groups.params", body: req(agg + `,"groups":[{"type":"GROUP_CATEGORY","field":"cat","params":{"bogus_order":"asc"}}]`), path: "groups[0].params.bogus_order", unknown: "bogus_order"},
		{family: "request.outputs", body: req(agg + `,"outputs":[{"format":"json","fromat":"csv"}]`), path: "outputs[0].fromat", unknown: "fromat"},
		{family: "request.windows", body: req(`"windows":[{"type":"WIN_LAG","field":"n","lable":"l"}]`), path: "windows[0].lable", unknown: "lable"},
		{family: "request.windows.params", body: req(`"windows":[{"type":"WIN_LAG","field":"n","label":"l","params":{"ofset":1}}]`), path: "windows[0].params.ofset", unknown: "ofset"},
		{family: "request.windows.for_each", body: req(`"windows":[{"for_each":{"axes":[{"name":"k","values":[1]}],"mdoe":"zip"},"type":"WIN_LAG","field":"n","label":"l{{k}}"}]`), path: "windows[0].for_each.mdoe", unknown: "mdoe"},
		{family: "request.features", body: req(`"features":[{"type":"FEAT_LOG","field":"n","lable":"x"}]`), path: "features[0].lable", unknown: "lable"},
		{family: "request.features.params", body: req(`"features":[{"type":"FEAT_LOG","field":"n","label":"x","params":{"bsae":10}}]`), path: "features[0].params.bsae", unknown: "bsae"},
		{family: "request.features.for_each", body: req(`"features":[{"for_each":{"axis":[{"name":"f","values":["n"]}]},"type":"FEAT_LOG","field":"{{f}}","label":"log_{{f}}"}]`), path: "features[0].for_each.axis", unknown: "axis"},
		{family: "request.sort", body: req(agg + `,"sort":[{"field":"t","direction":"desc"}]`), path: "sort[0].direction", unknown: "direction"},
		{family: "request.tests", body: req(`"tests":[{"type":"TEST_WELCH","field":"n","split_by":"cat","alpah":0.05}]`), path: "tests[0].alpah", unknown: "alpah"},
		{family: "request.tests.params", body: req(`"tests":[{"type":"TEST_WELCH","field":"n","split_by":"cat","params":{"tials":2}}]`), path: "tests[0].params.tials", unknown: "tials"},
		{family: "request.post_tests", body: req(`"post_tests":[{"type":"TEST_TUKEY_HSD","field":"n","split_by":"cat","alpah":0.05}]`), path: "post_tests[0].alpah", unknown: "alpah"},
		{family: "request.post_tests.params", body: req(`"post_tests":[{"type":"TEST_TUKEY_HSD","field":"n","split_by":"cat","params":{"n_colum":"x"}}]`), path: "post_tests[0].params.n_colum", unknown: "n_colum"},
		{family: "request.regressions", body: req(`"regressions":[{"type":"REG_OLS","target":"n","predictor":["n"]}]`), path: "regressions[0].predictor", unknown: "predictor"},
		{family: "request.vectors", body: req(agg + `,"vectors":[{"name":"v","feilds":["n"]}]`), path: "vectors[0].feilds", unknown: "feilds"},
		{family: "request.matrices", body: req(`"matrices":[{"name":"m","type":"MAT_CORRELATION","feilds":["n"]}]`), path: "matrices[0].feilds", unknown: "feilds"},
		{family: "request.matrices.params", body: req(`"matrices":[{"name":"m","type":"MAT_COVARIANCE","fields":["n","n2"],"params":{"ddoff":1}}]`), path: "matrices[0].params.ddoff", unknown: "ddoff"},
		{family: "request.models", body: req(agg + `,"models":[{"name":"m","paht":"x.json"}]`), path: "models[0].paht", unknown: "paht"},
		{family: "request.populations", body: req(agg + `,"populations":[{"name":"p","paht":"x.json"}]`), path: "populations[0].paht", unknown: "paht"},
		{family: "request.joins", body: req(agg + `,"joins":[{"right":"x.pulse","knid":"inner"}]`), path: "joins[0].knid", unknown: "knid"},
		{family: "request.labels", body: req(agg + `,"labels":[{"field":"cat","tabel":"t"}]`), path: "labels[0].tabel", unknown: "tabel"},
		{family: "request.crosstab", body: xt(`"rows":[` + grp + `],"colums":[` + grp + `],` + cell), path: "crosstab.colums", unknown: "colums"},
		{family: "request.crosstab.rows", body: xt(`"rows":[{"type":"GROUP_CATEGORY","feild":"cat"}],"columns":[` + grp + `],` + cell), path: "crosstab.rows[0].feild", unknown: "feild"},
		{family: "request.crosstab.rows.params", body: xt(`"rows":[{"type":"GROUP_CATEGORY","field":"cat","params":{"bogus_order":1}}],"columns":[` + grp + `],` + cell), path: "crosstab.rows[0].params.bogus_order", unknown: "bogus_order"},
		{family: "request.crosstab.columns", body: xt(`"rows":[` + grp + `],"columns":[{"type":"GROUP_CATEGORY","feild":"cat"}],` + cell), path: "crosstab.columns[0].feild", unknown: "feild"},
		{family: "request.crosstab.columns.params", body: xt(`"rows":[` + grp + `],"columns":[{"type":"GROUP_CATEGORY","field":"cat","params":{"bogus_order":1}}],` + cell), path: "crosstab.columns[0].params.bogus_order", unknown: "bogus_order"},
		{family: "request.crosstab.cell", body: xt(`"rows":[` + grp + `],"columns":[` + grp + `],"cell":{"type":"AGG_COUNT","feild":"n"}`), path: "crosstab.cell.feild", unknown: "feild"},
		{family: "request.crosstab.cell.weight", body: xt(`"rows":[` + grp + `],"columns":[` + grp + `],"cell":{"type":"AGG_SUM","field":"n","weight":{"knid":"frequency"}}`), path: "crosstab.cell.weight.knid", unknown: "knid"},
		{family: "request.crosstab.cell.params", body: xt(`"rows":[` + grp + `],"columns":[` + grp + `],"cell":{"type":"AGG_PERCENTILE","field":"n","params":{"percentil":50}}`), path: "crosstab.cell.params.percentil", unknown: "percentil"},
		{family: "request.crosstab.margin_aggregations", body: xt(`"rows":[` + grp + `],"columns":[` + grp + `],` + cell + `,"margin_aggregations":[{"type":"AGG_SUM","feild":"n"}]`), path: "crosstab.margin_aggregations[0].feild", unknown: "feild"},
		{family: "request.crosstab.margin_aggregations.params", body: xt(`"rows":[` + grp + `],"columns":[` + grp + `],` + cell + `,"margin_aggregations":[{"type":"AGG_PERCENTILE","field":"n","label":"p","params":{"percentil":50}}]`), path: "crosstab.margin_aggregations[0].params.percentil", unknown: "percentil"},
		{family: "request.crosstab.margins", body: xt(`"rows":[` + grp + `],"columns":[` + grp + `],` + cell + `,"margins":{"rows":true,"grnad":true}`), path: "crosstab.margins.grnad", unknown: "grnad"},
		{family: "request.overlays", body: req(`"crosstab":{"rows":[` + grp + `],"columns":[` + grp + `],` + cell + `},"overlays":[{"name":"o","kind":"OVERLAY_SHARE_OF_ROW","scoep":"row"}]`), path: "overlays[0].scoep", unknown: "scoep"},
		{family: "request.overlays.params", body: req(`"crosstab":{"rows":[` + grp + `],"columns":[` + grp + `],` + cell + `},"overlays":[{"name":"o","kind":"OVERLAY_PAIRWISE_PROP_Z","scope":"row","params":{"n_sourse":"row_margin_n"}}]`), path: "overlays[0].params.n_sourse", unknown: "n_sourse"},
		{family: "request.weight", body: req(agg + `,"weight":{"feild":"n"}`), path: "weight.feild", unknown: "feild"},
		{family: "request.multiplicity", body: req(agg + `,"multiplicity":{"metod":"bh"}`), path: "multiplicity.metod", unknown: "metod"},
		{family: "request.return", body: req(agg + `,"return":{"preest":"standard"}`), path: "return.preest", unknown: "preest"},

		// --- Composed root: its own slots, a nested request, a fit, the sweep ---
		{family: "composed", body: `{"requestz":[` + nested(agg) + `]}`, unknown: "requestz"},
		{family: "composed.requests", body: `{"requests":[` + nested(agg+`,"groups":[{"type":"GROUP_CATEGORY","feild":"cat"}]`) + `]}`, path: "groups[0].feild", unknown: "feild", locator: "request"},
		{family: "composed.requests.params", body: `{"requests":[` + nested(agg+`,"groups":[{"type":"GROUP_CATEGORY","field":"cat","params":{"bogus_order":1}}]`) + `]}`, path: "groups[0].params.bogus_order", unknown: "bogus_order", locator: "request"},
		{family: "composed.overlays", body: composed(`"overlays":[{"name":"o","kind":"OVERLAY_INDEX_VS_REF","reference":"a","targts":["b"]}]`), path: "overlays[0].targts", unknown: "targts"},
		{family: "composed.overlays.params", body: `{"requests":[` + nested(agg) + `,` + nested(agg) + `],"overlays":[{"name":"o","kind":"OVERLAY_INDEX_VS_REF","reference":"0","targets":["1"],"params":{"bogus_scale":1}}]}`, path: "overlays[0].params.bogus_scale", unknown: "bogus_scale"},
		{family: "composed.multiplicity", body: composed(`"multiplicity":{"metod":"bh"}`), path: "multiplicity.metod", unknown: "metod"},
		{family: "composed.return", body: composed(`"return":{"preest":"standard"}`), path: "return.preest", unknown: "preest"},
		{family: "composed.sweep", body: `{"sweep":{` + sweepAxes + `,"mdoe":"grid","request":` + nested(agg) + `}}`, path: "sweep.mdoe", unknown: "mdoe"},
		{family: "composed.sweep.axes", body: `{"sweep":{"axes":[{"name":"k","valuse":[1,2]}],"request":` + nested(agg) + `}}`, path: "sweep.axes[0].valuse", unknown: "valuse"},
		{family: "composed.sweep.rank", body: `{"sweep":{` + sweepAxes + `,"request":` + nested(agg) + `,"rank":{"by":"t","ordr":"desc"}}}`, path: "sweep.rank.ordr", unknown: "ordr"},
		{family: "composed.sweep.request", body: `{"sweep":{` + sweepAxes + `,"request":` + nested(`"aggregations":[{"type":"AGG_SUM","field":"n","label":"t{{k}}","lable":"x"}]`) + `}}`, path: "aggregations[0].lable", unknown: "lable", locator: "slot"},
		{family: "composed.fits", body: composed(`"fits":[{"name":"f","request":` + nested(`"regressions":[{"type":"REG_OLS","target":"n","predictors":["n2"]}]`) + `,"svae":"x"}]`), path: "fits[0].svae", unknown: "svae"},
		{family: "composed.fits.request", body: composed(`"fits":[{"name":"f","request":` + nested(`"regresions":[]`) + `}]`), unknown: "regresions", locator: "fit"},

		// --- Chain root ---
		{family: "chain", body: `{"cohort":` + c + `,"stagse":[]}`, unknown: "stagse"},
		{family: "chain.cohort", body: `{"cohort":{"filename":` + strconv.Quote(cohortFile(c)) + `,"data_dri":"x"},"stages":[{"name":"s","request":` + nested(agg) + `}]}`, path: "cohort.data_dri", unknown: "data_dri"},
		{family: "chain.stages", body: `{"cohort":` + c + `,"stages":[{"name":"s","requset":` + nested(agg) + `}]}`, path: "stages[0].requset", unknown: "requset"},
		{family: "chain.stages.request", body: `{"cohort":` + c + `,"stages":[{"name":"s","request":` + nested(agg+`,"sort":[{"feild":"t"}]`) + `}]}`, path: "sort[0].feild", unknown: "feild", locator: "stage"},
		{family: "chain.overlays", body: chain(`"overlays":[{"name":"o","kind":"OVERLAY_DELTA_VS_STAGE","scoep":"row"}]`), path: "overlays[0].scoep", unknown: "scoep"},
		{family: "chain.overlays.params", body: `{"cohort":` + c + `,"stages":[{"name":"a","request":` + nested(agg) + `},{"name":"b","request":` + nested(agg) + `}],"overlays":[{"name":"o","kind":"OVERLAY_DELTA_VS_STAGE","ref":{"index":0},"target":{"index":1},"params":{"bogus_scale":1}}]}`, path: "overlays[0].params.bogus_scale", unknown: "bogus_scale"},

		// --- Facet root ---
		{family: "facet", body: `{"cohort":` + c + `,"feilds":["cat"]}`, unknown: "feilds"},
		{family: "facet.cohort", body: `{"cohort":{"filename":` + strconv.Quote(cohortFile(c)) + `,"data_dri":"x"},"fields":["cat"]}`, path: "cohort.data_dri", unknown: "data_dri"},
		{family: "facet.filterers", body: facet(`"filterers":[{"type":"FILTER_INCLUDE","field":"cat","valuse":["a"]}]`), path: "filterers[0].valuse", unknown: "valuse"},
		{family: "facet.filterers.params", body: facet(`"filterers":[{"type":"FILTER_INCLUDE","field":"cat","values":["a"],"params":{"negete":true}}]`), path: "filterers[0].params.negete", unknown: "negete"},
		{family: "facet.labels", body: facet(`"labels":[{"field":"cat","tabel":"t"}]`), path: "labels[0].tabel", unknown: "tabel"},
		{family: "facet.overlays", body: facet(`"overlays":[{"name":"o","kind":"OVERLAY_SHARE_OF_TOTAL","scoep":"row"}]`), path: "overlays[0].scoep", unknown: "scoep"},
		{family: "facet.overlays.params", body: facet(`"overlays":[{"name":"o","kind":"OVERLAY_SHARE_OF_TOTAL","params":{"bogus_scale":1}}]`), path: "overlays[0].params.bogus_scale", unknown: "bogus_scale"},
		{family: "facet.models", body: facet(`"models":[{"name":"m","paht":"x.json"}]`), path: "models[0].paht", unknown: "paht"},

		// --- Lookup root ---
		{family: "lookup", body: `{"cohort":` + c + `,"keyz":[{"field":"cat","value":"a"}]}`, unknown: "keyz"},
		{family: "lookup.cohort", body: `{"cohort":{"filename":` + strconv.Quote(cohortFile(c)) + `,"data_dri":"x"},"keys":[{"field":"cat","value":"a"}]}`, path: "cohort.data_dri", unknown: "data_dri"},
		{family: "lookup.keys", body: `{"cohort":` + c + `,"keys":[{"field":"cat","valu":"a"}]}`, path: "keys[0].valu", unknown: "valu"},
	}
}

// cohortFile recovers the filename from the cohort object JSON.
func cohortFile(c string) string {
	var co types.Cohort
	_ = json.Unmarshal([]byte(c), &co)
	return co.Filename
}

// strictAcceptanceRootTypes are the JSON request roots whose slot
// families the table must cover, keyed by the row family prefix.
func strictAcceptanceRootTypes() map[string]reflect.Type {
	return map[string]reflect.Type{
		"request":  reflect.TypeFor[types.Request](),
		"composed": reflect.TypeFor[types.ComposedRequest](),
		"chain":    reflect.TypeFor[types.ChainRequest](),
		"facet":    reflect.TypeFor[types.FacetRequest](),
		"lookup":   reflect.TypeFor[types.LookupRequest](),
		"synth":    reflect.TypeFor[synth.Spec](),
	}
}

// strictAcceptanceRequiredFamilies derives the family list from the root
// types: the root itself; every object-valued key (a struct, or a
// slice / array / pointer of one); below a slice element, its `params`,
// `for_each`, `weight` and nested `request`; below a single object,
// recursively the same. Scalars, string lists and raw / map values carry
// no keys of their own to misspell and are not families.
func strictAcceptanceRequiredFamilies() []string {
	var out []string
	var walk func(prefix string, t reflect.Type)
	walk = func(prefix string, t reflect.Type) {
		for _, f := range jsonFields(t) {
			name := prefix + "." + f.name
			if f.name == "params" && isRawOrMap(f.Type) {
				out = append(out, name)
				continue
			}
			et := structElem(f.Type)
			if et == nil {
				continue
			}
			out = append(out, name)
			if isSingle(f.Type) {
				walk(name, et)
				continue
			}
			// A slice element: the keys that open a second decode
			// layer (params allowlist, for_each expansion, a nested
			// request) are families of their own.
			for _, sub := range jsonFields(et) {
				switch {
				case sub.name == "params" && isRawOrMap(sub.Type),
					sub.name == "for_each",
					sub.name == "request" && structElem(sub.Type) != nil:
					out = append(out, name+"."+sub.name)
				}
			}
		}
	}
	for root, t := range strictAcceptanceRootTypes() {
		out = append(out, root)
		walk(root, t)
	}
	// The sweep body is a raw template (decoded strictly per point).
	out = append(out, "composed.sweep.request")
	// Every request-template target renders through the strict decoder.
	for _, tg := range template.AllTargets() {
		out = append(out, "template."+string(tg))
	}
	sort.Strings(out)
	return slices.Compact(out)
}

func isRawOrMap(t reflect.Type) bool {
	return t.Kind() == reflect.Map || t == reflect.TypeFor[json.RawMessage]()
}

type jsonField struct {
	reflect.StructField
	name string
}

func jsonFields(t reflect.Type) []jsonField {
	var out []jsonField
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Anonymous && f.Tag.Get("json") == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				out = append(out, jsonFields(ft)...)
				continue
			}
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, jsonField{f, name})
	}
	return out
}

// structElem returns the struct type a field's value holds an object of
// (through pointers, slices and arrays), or nil.
func structElem(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		if t == reflect.TypeFor[json.RawMessage]() {
			return nil
		}
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return t
}

func isSingle(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct
}

// strictSynthRows misspell a key in the synth spec and each of its
// object-valued slots (synth from-schema; synth has no predict leaf).
func strictSynthRows() []strictRow {
	f := `"fields":[{"name":"a","type":"f64","distribution":"normal"},{"name":"b","type":"f64","distribution":"normal"}]`
	spec := func(frag string) string { return `{"row_count":5,` + f + `,` + frag + `}` }
	return []strictRow{
		{family: "synth", body: spec(`"row_cuont":5`), unknown: "row_cuont"},
		{family: "synth.fields", body: `{"row_count":5,"fields":[{"name":"a","type":"f64","distribtion":"normal"}]}`, path: "fields[0].distribtion", unknown: "distribtion"},
		{family: "synth.fields.params", body: `{"row_count":5,"fields":[{"name":"e","type":"f64","distribution":"exponential","params":{"rate":5}}]}`, path: "fields[0].params.rate", unknown: "rate"},
		{family: "synth.constraints", body: spec(`"constraints":[{"bogus":1}]`), path: "constraints[0].bogus", unknown: "bogus"},
		{family: "synth.correlations", body: spec(`"correlations":[{"a":"a","b":"b","correlation":0.5}]`), path: "correlations[0].correlation", unknown: "correlation"},
		{family: "synth.categorical_pairs", body: spec(`"categorical_pairs":[{"bogus":1}]`), path: "categorical_pairs[0].bogus", unknown: "bogus"},
		{family: "synth.categorical_numeric_pairs", body: spec(`"categorical_numeric_pairs":[{"bogus":1}]`), path: "categorical_numeric_pairs[0].bogus", unknown: "bogus"},
		{family: "synth.set_categorical_pairs", body: spec(`"set_categorical_pairs":[{"bogus":1}]`), path: "set_categorical_pairs[0].bogus", unknown: "bogus"},
		{family: "synth.set_numeric_pairs", body: spec(`"set_numeric_pairs":[{"bogus":1}]`), path: "set_numeric_pairs[0].bogus", unknown: "bogus"},
		{family: "synth.set_set_pairs", body: spec(`"set_set_pairs":[{"bogus":1}]`), path: "set_set_pairs[0].bogus", unknown: "bogus"},
		{family: "synth.models", body: spec(`"models":[{"bogus":1}]`), path: "models[0].bogus", unknown: "bogus"},
		{family: "synth.residual_correlations", body: spec(`"residual_correlations":[{"a":"a","b":"b","correlation":0.5}]`), path: "residual_correlations[0].correlation", unknown: "correlation"},
		{family: "synth.rules", body: spec(`"rules":[{"set_nul":["a"]}]`), path: "rules[0].set_nul", unknown: "set_nul"},
	}
}

// strictTemplateRows misspell a key in a template body of every target.
func strictTemplateRows(c string) []strictRow {
	return []strictRow{
		{family: "template.request", body: `{"cohort":` + c + `,"aggregations":[{"type":"AGG_SUM","field":"n","lable":"t"}]}`, path: "aggregations[0].lable", unknown: "lable"},
		{family: "template.composed", body: `{"requests":[{"cohort":` + c + `,"sort":[{"feild":"n"}]}]}`, path: "requests[0].sort[0].feild", unknown: "feild"},
		{family: "template.chain", body: `{"cohort":` + c + `,"stagse":[]}`, unknown: "stagse"},
		{family: "template.facet", body: `{"cohort":` + c + `,"feilds":["cat"]}`, unknown: "feilds"},
		{family: "template.sample", body: `{"cohort":` + c + `,"n":3,"lables":[]}`, unknown: "lables"},
	}
}

// TestStrictInput_EveryFamilyAndRootRefusesAMisspelledKey is the #311
// acceptance table.
func TestStrictInput_EveryFamilyAndRootRefusesAMisspelledKey(t *testing.T) {
	dir := withTempDataDir(t)
	cohort := seedCohortViaImport(t, dir, "strict.csv", "n,n2,cat\n1,2,a\n2,3,b\n3,5,a\n4,4,b\n")
	c := `{"filename":` + strconv.Quote(cohort) + `}`

	rows := strictAcceptanceRows(c)
	synthRows := strictSynthRows()
	tmplRows := strictTemplateRows(c)

	// Coverage: every derived family has a row (and no row names a
	// family the roots do not have — a stale row would test nothing).
	have := map[string]bool{}
	for _, set := range [][]strictRow{rows, synthRows, tmplRows} {
		for _, r := range set {
			have[r.family] = true
		}
	}
	required := strictAcceptanceRequiredFamilies()
	// Guard the derivation itself: a reflection walk that found nothing
	// would require nothing and pass.
	for _, anchor := range []string{"request.aggregations.params", "request.crosstab.cell", "composed.fits.request", "chain.stages.request", "facet.overlays.params", "synth.fields.params", "template.sample"} {
		if !slices.Contains(required, anchor) {
			t.Fatalf("derived family list lacks %q: %v", anchor, required)
		}
	}
	for _, fam := range required {
		if !have[fam] {
			t.Errorf("slot family %q has no misspelled-key row", fam)
		}
	}
	extra := map[string]bool{
		// Rows beyond the derived list: a slot weight object, a
		// for_each-expanded item's own key.
		"request.aggregations.weight":      true,
		"request.attributes.for_each.item": true,
		"composed.requests.params":         true, // the params allowlist through a nested request
	}
	for fam := range have {
		if !slices.Contains(required, fam) && !extra[fam] {
			t.Errorf("row family %q is not a slot family of any root (stale row?)", fam)
		}
	}

	roots := strictAcceptanceRoots()
	for _, r := range rows {
		root, _, _ := strings.Cut(r.family, ".")
		spec, ok := roots[root]
		if !ok {
			t.Fatalf("row %s: no leaves for root %q", r.family, root)
		}
		for _, leaf := range spec.leaves {
			t.Run(r.family+"/"+leaf[0], func(t *testing.T) {
				reqPath := filepath.Join(dir, "strict_req.json")
				if err := os.WriteFile(reqPath, []byte(r.body), 0o644); err != nil {
					t.Fatal(err)
				}
				args := append(append([]string{}, leaf...), "-r", reqPath, "--json")
				out, _ := runAPI(t, args...)
				assertStrictRefusal(t, out, perr.PULSE_REQUEST_UNKNOWN_FIELD, r)
			})
		}
	}

	// The MCP arm: the same rows through the agent surface's tools —
	// pulse_predict (the root under its own key for a non-Request root)
	// and the root's runtime tool.
	p, err := pulse.New(pulse.Options{FS: afero.NewOsFs()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	tools := map[string]mcp.ToolDescriptor{}
	for _, td := range mcp.Tools(mcp.Config{}) {
		tools[td.Name] = td
	}
	type mcpLeaf struct{ tool, wrap string }
	mcpLeaves := map[string][]mcpLeaf{
		"request":  {{toolmeta.ToolPredict, ""}, {toolmeta.ToolProcess, ""}},
		"composed": {{toolmeta.ToolPredict, "composed"}, {toolmeta.ToolCompose, ""}},
		"chain":    {{toolmeta.ToolPredict, "chain"}, {toolmeta.ToolProcessChain, ""}},
		"facet":    {{toolmeta.ToolPredict, "facet"}, {toolmeta.ToolFacetSchema, ""}},
		"lookup":   {{toolmeta.ToolLookup, ""}},
	}
	for _, r := range rows {
		root, _, _ := strings.Cut(r.family, ".")
		for _, leaf := range mcpLeaves[root] {
			t.Run(r.family+"/"+leaf.tool, func(t *testing.T) {
				td, ok := tools[leaf.tool]
				if !ok {
					t.Fatalf("%s not in the catalog", leaf.tool)
				}
				args := r.body
				if leaf.wrap != "" {
					args = `{"` + leaf.wrap + `":` + r.body + `}`
				}
				// A strict-decode refusal is the tool's error; a refusal
				// past decode (the params allowlist, a sweep point) is
				// pulse_predict's own errors list — predict reports, it
				// does not fail.
				out, err := td.Invoke(context.Background(), p, json.RawMessage(args))
				var raw []byte
				if err != nil {
					var ce *perr.CodedError
					if !stderrors.As(err, &ce) {
						t.Fatalf("err = %v, want a coded %s", err, perr.PULSE_REQUEST_UNKNOWN_FIELD)
					}
					raw, _ = json.Marshal(map[string]any{"errors": []any{map[string]any{"code": string(ce.Code), "details": ce.Details}}})
				} else {
					if leaf.tool != toolmeta.ToolPredict {
						t.Fatalf("%s ran a misspelled request: %+v", leaf.tool, out)
					}
					if raw, err = types.MarshalFinite(out); err != nil {
						t.Fatal(err)
					}
				}
				assertStrictRefusal(t, string(raw), perr.PULSE_REQUEST_UNKNOWN_FIELD, r)
			})
		}
	}

	for _, r := range synthRows {
		t.Run(r.family+"/synth-from-schema", func(t *testing.T) {
			spec := filepath.Join(dir, "strict_spec.json")
			if err := os.WriteFile(spec, []byte(r.body), 0o644); err != nil {
				t.Fatal(err)
			}
			outPath := filepath.Join(dir, "strict_synth.pulse")
			var buf bytes.Buffer
			_ = runSynthCLI(t, &buf, "from-schema", "--spec", spec, "--output", outPath, "--json")
			assertStrictRefusal(t, buf.String(), perr.PULSE_REQUEST_UNKNOWN_FIELD, r)
			if _, err := os.Stat(outPath); err == nil {
				t.Error("a refused spec still produced a cohort")
			}
		})
	}

	for _, r := range tmplRows {
		t.Run(r.family+"/render", func(t *testing.T) {
			_, target, _ := strings.Cut(r.family, ".")
			tm := &template.Template{Name: "strict/" + target, Target: template.Target(target), Body: json.RawMessage(r.body)}
			_, err := template.Render(tm, nil)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TEMPLATE_RENDER_INVALID {
				t.Fatalf("err = %v, want %s", err, perr.PULSE_TEMPLATE_RENDER_INVALID)
			}
			if got := ce.Details["field"]; got != r.unknown {
				t.Errorf("details.field = %v, want %q", got, r.unknown)
			}
			if got, ok := ce.Details["path"]; r.path == "" && ok && got != r.unknown || r.path != "" && got != r.path {
				t.Errorf("details.path = %v, want %q", got, r.path)
			}
		})
	}
}

// assertStrictRefusal checks an envelope's first error is code with the
// row's path, unknown key and locator.
func assertStrictRefusal(t *testing.T, out string, code perr.Code, r strictRow) {
	t.Helper()
	var env struct {
		Errors []struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("not an envelope: %v\n%s", err, out)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(code) {
		t.Fatalf("errors = %+v, want %s", env.Errors, code)
	}
	d := env.Errors[0].Details
	if got, ok := d["path"]; r.path == "" && ok || r.path != "" && got != r.path {
		t.Errorf("details.path = %v, want %q", got, r.path)
	}
	if keys, _ := d["unknown_keys"].([]any); len(keys) != 1 || keys[0] != r.unknown {
		t.Errorf("unknown_keys = %v, want [%s]", d["unknown_keys"], r.unknown)
	}
	if r.locator != "" {
		if idx, ok := d[r.locator].(float64); !ok || idx != 0 {
			t.Errorf("details.%s = %v, want 0 (details %v)", r.locator, d[r.locator], d)
		}
	}
}
