package types

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/returnplan"
)

// floatLeafPaths enumerates every float leaf statically reachable from
// t under encoding/json's field rules: `[*]` for a slice / array step,
// `*` for a string-keyed map step. An open (`any`) slot ends the walk —
// what rides it is classified by Go kind at runtime (an int is never
// rounded) or by a Plan.Exact path the resolver derives.
func floatLeafPaths(t reflect.Type, path string, stack map[reflect.Type]bool, out map[string]bool) {
	switch t.Kind() {
	case reflect.Float32, reflect.Float64:
		out[path] = true
	case reflect.Pointer:
		floatLeafPaths(t.Elem(), path, stack, out)
	case reflect.Slice, reflect.Array:
		floatLeafPaths(t.Elem(), path+"[*]", stack, out)
	case reflect.Map:
		if t.Key().Kind() == reflect.String {
			floatLeafPaths(t.Elem(), path+".*", stack, out)
		}
	case reflect.Struct:
		if stack[t] {
			return
		}
		stack[t] = true
		defer delete(stack, t)
		for i := range t.NumField() {
			sf := t.Field(i)
			tag := sf.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, _, _ := strings.Cut(tag, ",")
			if sf.Anonymous && name == "" {
				ft := sf.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					floatLeafPaths(ft, path, stack, out)
					continue
				}
			}
			if !sf.IsExported() {
				continue
			}
			if name == "" {
				name = sf.Name
			}
			p := name
			if path != "" {
				p = path + "." + name
			}
			floatLeafPaths(sf.Type, p, stack, out)
		}
	}
}

// precisionClass classifies every float leaf of the Response type:
// "round" — rounded under Return.precision; "exempt" — wholly an
// integer-semantics float, which must be covered by a precisionExempt
// entry; "keyed" — a map whose keys differ in kind (a precisionExempt
// entry names the exempt key, every other key rounds). A float leaf
// added to any result type fails this test until it is classified here,
// so a new count carried as a float cannot silently get rounded.
var precisionClass = map[string]string{
	"tests[*].statistic": "round", "tests[*].df": "round", "tests[*].p_value": "round",
	"tests[*].alpha": "round", "tests[*].p_adjusted": "round", "tests[*].multiplicity.alpha": "round",
	"post_tests[*].statistic": "round", "post_tests[*].df": "round", "post_tests[*].p_value": "round",
	"post_tests[*].alpha": "round", "post_tests[*].p_adjusted": "round", "post_tests[*].multiplicity.alpha": "round",
	"regressions[*].alpha": "round", "regressions[*].l1_ratio": "round",
	"regressions[*].coefficients.*": "round", "regressions[*].std_errors.*": "round",
	"regressions[*].p_values.*": "round", "regressions[*].r2": "round", "regressions[*].adj_r2": "round",
	"regressions[*].deviance": "round", "regressions[*].null_deviance": "round",
	"regressions[*].pseudo_r2": "round", "regressions[*].sum_weights": "round",
	"regressions[*].n_eff": "round", "regressions[*].residual_std_err": "round",
	"regressions[*].credible_intervals.*[*]": "round",
	// Matrices: primary rounds; auxiliary is keyed — `n` (the pairwise
	// N, integer counts) is exempt, any future companion rounds.
	"matrices[*].primary.values[*][*]":     "round",
	"matrices[*].auxiliary.*.values[*][*]": "keyed",
	"matrices[*].scalars.*":                "round",
	"overlays[*].payload.scalar":           "round",
	"overlays[*].summary.min":              "round", "overlays[*].summary.max": "round",
	"overlays[*].summary.baseline": "round", "overlays[*].summary.statistic": "round",
	"overlays[*].summary.p_value": "round", "overlays[*].summary.parameters.*": "round",
	"overlays[*].summary.p_adjusted": "round", "overlays[*].multiplicity.alpha": "round",
	"overlays[*].payload.series.entries[*].summary.min":          "round",
	"overlays[*].payload.series.entries[*].summary.max":          "round",
	"overlays[*].payload.series.entries[*].summary.baseline":     "round",
	"overlays[*].payload.series.entries[*].summary.statistic":    "round",
	"overlays[*].payload.series.entries[*].summary.p_value":      "round",
	"overlays[*].payload.series.entries[*].summary.parameters.*": "round",
	"overlays[*].payload.series.entries[*].summary.p_adjusted":   "round",
	"components.aggregations[*].sum_weights":                     "round",
	"components.aggregations[*].n_eff":                           "round",
	"components.aggregations[*].groups[*].sum_weights":           "round",
	"components.aggregations[*].groups[*].n_eff":                 "round",
	"components.matrices[*].sum_weights":                         "round",
	"components.matrices[*].n_eff":                               "round",
}

// wantPrecisionExempt is the exact precisionExempt registry: removing an
// entry (so a count starts rounding) fails here.
var wantPrecisionExempt = []string{"matrices[*].auxiliary.n"}

func TestPrecision_FloatLeavesClassified(t *testing.T) {
	got := map[string]bool{}
	floatLeafPaths(reflect.TypeFor[Response](), "", map[reflect.Type]bool{}, got)
	var unclassified, stale []string
	for p := range got {
		if _, ok := precisionClass[p]; !ok {
			unclassified = append(unclassified, p)
		}
	}
	for p := range precisionClass {
		if !got[p] {
			stale = append(stale, p)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(stale)
	if len(unclassified) > 0 {
		t.Errorf("float leaves not classified (round / exempt / keyed) — a count carried as a float needs a precisionExempt entry: %v", unclassified)
	}
	if len(stale) > 0 {
		t.Errorf("classified paths no longer reachable: %v", stale)
	}

	reg := returnplan.Strings(precisionExempt)
	sort.Strings(reg)
	if !reflect.DeepEqual(reg, wantPrecisionExempt) {
		t.Errorf("precisionExempt = %v, want %v", reg, wantPrecisionExempt)
	}
	// Every exempt / keyed leaf is covered by a registry entry.
	for p, class := range precisionClass {
		if class == "round" {
			continue
		}
		covered := false
		for _, x := range reg {
			head := x
			if class == "keyed" {
				head = x[:strings.LastIndex(x, ".")] + ".*"
			}
			if strings.HasPrefix(p, head) {
				covered = true
			}
		}
		if !covered {
			t.Errorf("%s leaf %q has no precisionExempt entry", class, p)
		}
	}
}

// precisionPlan is a plan that selects the whole response at precision.
func precisionPlan(precision int, exact ...returnplan.Path) *returnplan.Plan {
	var keys []string
	var paths []returnplan.Path
	for _, f := range jsonFields(reflect.ValueOf(Response{})) {
		if f.name == returnedKey {
			continue
		}
		keys = append(keys, f.name)
		paths = append(paths, returnplan.Path{Segments: []returnplan.Segment{returnplan.Key(f.name)}})
	}
	p := returnplan.New("full", paths, nil, nil, precision, keys)
	p.Exact = exact
	return p
}

func precisionResponse() *Response {
	det := 0.000123456789
	return &Response{
		Data: []map[string]any{{
			"tiny": 3e-9, "big": 123456.789, "huge": 1.2345678e21, "count": 42,
			"n": 1234567.0, "dec": "12.3456789", "undef": math.NaN(), "f32": float32(2.718281828),
		}},
		Tests:       []*TestResult{{Type: "TEST_T", Statistic: 2.3456789, PValue: 3e-9, Alpha: 0.05}},
		Regressions: []*RegressionResult{{Coefficients: map[string]float64{"x": 0.123456789}, R2: 0.987654321}},
		Matrices: []MatrixResult{{
			Name: "m", Type: "MAT_CORRELATION",
			Primary: &MatrixValues{Kind: "square_symmetric", Encoding: MatrixEncodingUpper,
				RowKeys: []string{"a", "b"}, ColumnKeys: []string{"a", "b"},
				Values: [][]float64{{1, 0.123456789}, {1}}},
			Auxiliary: map[string]*MatrixValues{"n": {Kind: "square_symmetric", Encoding: MatrixEncodingUpper,
				RowKeys: []string{"a", "b"}, ColumnKeys: []string{"a", "b"},
				Values: [][]float64{{1234567, 1234567}, {1234567}}}},
			Vectors: map[string]any{"top_pairs": []MatrixPair{{Row: "a", Col: "b", R: 0.123456789, N: 1234567}}},
			Scalars: map[string]float64{"determinant": det},
		}},
		Overlays: []OverlayLayer{{Name: "o", Payload: OverlayPayload{Scalar: &det}}},
		Components: &ResponseComponents{Aggregations: []AggregationComponents{{
			Label: "m", N: 1234567, Operator: map[string]any{"mean": 0.123456789, "count": 1234567},
		}}},
	}
}

// TestPrecision_SlotFamilies: Return.precision rounds every slot
// family's floats on the wire ('g' significant digits, exponent form
// accepted), leaves ints, decimal strings, the exempt auxiliary.n and a
// Plan.Exact column exact, keeps NaN null and the upper encoding's
// ragged shape — and never touches the Go values.
func TestPrecision_SlotFamilies(t *testing.T) {
	r := precisionResponse()
	before := precisionResponse()
	r.plan = precisionPlan(4, returnplan.Path{Segments: []returnplan.Segment{
		returnplan.Key("data"), returnplan.Elem(), returnplan.Key("n")}})
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`"tiny":3e-09`, `"big":1.235e+05`, `"huge":1.235e+21`, `"count":42`,
		`"n":1234567`, `"dec":"12.3456789"`, `"undef":null`, `"f32":2.718`,
		`"statistic":2.346`, `"p_value":3e-09`, `"alpha":0.05`,
		`"coefficients":{"x":0.1235}`, `"r2":0.9877`,
		`"values":[[1,0.1235],[1]]`,              // primary, upper encoding keeps its ragged rows
		`"values":[[1234567,1234567],[1234567]]`, // auxiliary.n exempt
		`"r":0.1235`, `"n":1234567`, `"determinant":0.0001235`,
		`"scalar":0.0001235`,
		`"operator":{"count":1234567,"mean":0.1235}`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("wire lacks %s:\n%s", want, s)
		}
	}
	r.plan = nil
	if !reflect.DeepEqual(dropNaN(r), dropNaN(before)) {
		t.Errorf("shaped marshal changed the Go values")
	}
}

func dropNaN(r *Response) *Response {
	delete(r.Data[0], "undef")
	return r
}

// TestPrecision_AbsentIsUnchanged: a plan without precision walks the
// same response to the same bytes as the unshaped form.
func TestPrecision_AbsentIsUnchanged(t *testing.T) {
	r := precisionResponse()
	want, err := MarshalFinite(r)
	if err != nil {
		t.Fatal(err)
	}
	r.plan = precisionPlan(0)
	got, err := marshalPlanned(reflect.ValueOf(r), r.plan)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("precision 0 differs:\n got %s\nwant %s", got, want)
	}
}
