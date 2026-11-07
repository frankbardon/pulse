package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// extensions_attribute_dictionary_test.go (U67 E3-S2, #486): an
// extension attribute declaring InheritsDictionary emits its source
// field's dictionary codes, and its output column reads as that field —
// GROUP_CATEGORY keys it by the dictionary's labels, Include and
// expression comparisons resolve through them, a Type-less group takes
// the categorical default — at predict and at runtime alike, in every
// arm (buffered, streaming, fused crosstab, shard-parallel). A source
// that is no coded cohort field is refused identically on both sides;
// a value that is no code is refused on the run and at pulse.New's
// probe.

const (
	attrDictCopy types.AttributeType = "ATTR_DICT_COPY" // row_local, source: Field
	attrDictFrom types.AttributeType = "ATTR_DICT_FROM" // buffered, source: params.src
	attrDictBad  types.AttributeType = "ATTR_DICT_BAD"  // row_local, code 3 -> 9
	attrDictPlus types.AttributeType = "ATTR_DICT_PLUS" // row_local, code + 1
	attrDictBadB types.AttributeType = "ATTR_DICT_BADB" // buffered, code 3 -> 9
)

// dictLabels seeds the plan dictionary (code i is dictLabels[i]).
var dictLabels = []string{"free", "paid", "pro", "team"}

// dictCode copies the code of column src (Field when src is empty),
// remapped by remap; a null source is NaN (a null: EmitsNull).
type dictCode struct {
	src   string
	remap func(float64) float64
}

func (a *dictCode) Row(rec extend.Record, field string) (float64, error) {
	name := a.src
	if name == "" {
		name = field
	}
	v, ok := rec.NumericValue(name)
	if !ok {
		return math.NaN(), nil
	}
	if a.remap != nil {
		v = a.remap(v)
	}
	return v, nil
}

func (a *dictCode) Compute(rows extend.Rows, field string) ([]float64, error) {
	out := make([]float64, rows.Len())
	for i := range out {
		v, err := a.Row(rows.At(i), field)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (a *dictCode) EmitsNull() bool { return true }

// dictRegistration declares a code-copying attribute: a Field source in
// mode, or (param) the string param "src".
func dictRegistration(name types.AttributeType, mode pulse.AttributeMode, param bool, remap func(float64) float64) pulse.AttributeRegistration {
	reg := pulse.AttributeRegistration{
		Name:               name,
		Description:        "Test-only attribute copying its source's dictionary code.",
		Mode:               mode,
		InheritsDictionary: &pulse.AttributeDictionarySource{},
		Factory: func(spec *types.Attribute, _ *encoding.Schema) (extend.AttributeComputer, error) {
			a := &dictCode{remap: remap}
			if param && len(spec.Params) > 0 {
				var p struct {
					Src string `json:"src"`
				}
				if err := json.Unmarshal(spec.Params, &p); err != nil {
					return nil, err
				}
				a.src = p.Src
			}
			return a, nil
		},
	}
	// Declared reads (the slot's Field is read anyway): a crosstab with
	// a row_local attribute fuses only when its reads are declared.
	reg.FieldInputs = func(json.RawMessage) []string { return nil }
	if param {
		reg.InheritsDictionary.Param = "src"
		reg.Params = []pulse.ParamMeta{{Name: "src", JSONType: "string", Description: "Coded source column."}}
		reg.FieldInputs = func(raw json.RawMessage) []string {
			var p struct {
				Src string `json:"src"`
			}
			if json.Unmarshal(raw, &p) != nil || p.Src == "" {
				return nil
			}
			return []string{p.Src}
		}
	}
	return reg
}

func dictRegistrations() []pulse.AttributeRegistration {
	return []pulse.AttributeRegistration{
		dictRegistration(attrDictCopy, pulse.AttributeModeRowLocal, false, nil),
		dictRegistration(attrDictFrom, pulse.AttributeModeBuffered, true, nil),
		dictRegistration(attrDictBad, pulse.AttributeModeRowLocal, false, dictBadRemap),
		dictRegistration(attrDictBadB, pulse.AttributeModeBuffered, false, dictBadRemap),
	}
}

// dictBadRemap mints code 9 for code 3 (past the 4-entry dictionary);
// the probe's three-entry source never carries code 3.
func dictBadRemap(v float64) float64 {
	if v == 3 {
		return 9
	}
	return v
}

// dictSchema: plan (categorical on dictLabels), active (packed_bool),
// x (f64), g (u8).
func dictSchema(t *testing.T) encoding.Schema {
	t.Helper()
	seed := encoding.NewDictionary()
	for _, l := range dictLabels {
		if _, err := seed.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "plan", Type: encoding.FieldTypeCategoricalU8, Dictionary: seed, Description: "Subscription plan of the account"},
		{Name: "active", Type: encoding.FieldTypePackedBool, Description: "Whether the account is active"},
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Spend of the account in the period"},
		{Name: "g", Type: encoding.FieldTypeU8, Description: "Region the account belongs to"},
	}}
}

// writeDictCohort writes rows [from, to) of the fixture to path.
func writeDictCohort(t *testing.T, p *pulse.Pulse, path string, from, to int) {
	t.Helper()
	b, err := p.NewCohortBuilder(context.Background(), path, dictSchema(t), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := from; i < to; i++ {
		if err := b.Append(pulse.CohortRow{dictLabels[i%len(dictLabels)], i%3 == 0, float64(i%5 + 1), uint64(i % 2)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

const dictRows = 24

func dictPulse(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: pulse.Extensions{Attributes: dictRegistrations(),
		LabelTables: map[string]pulse.LabelTable{"plans": {Description: "Plan display names", Rows: map[string]string{
			"free": "Free plan", "paid": "Paid plan", "pro": "Pro plan", "team": "Team plan"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	writeDictCohort(t, p, "dict.pulse", 0, dictRows)
	return p, fsys
}

// dictAttr is the slot under test: label tier over source src.
func dictAttr(typ types.AttributeType, src string) *types.Attribute {
	if typ == attrDictFrom {
		return &types.Attribute{Type: typ, Field: "x", Label: "tier", Params: json.RawMessage(`{"src": "` + src + `"}`)}
	}
	return &types.Attribute{Type: typ, Field: src, Label: "tier"}
}

// dictRowsOf is resp's grouped rows as sorted "key=s" pairs, the key
// read from column key.
func dictRowsOf(t *testing.T, resp *pulse.Response, key string) []string {
	t.Helper()
	out := make([]string, 0, len(resp.Data))
	for _, r := range resp.Data {
		out = append(out, fmt.Sprintf("%v=%v", r[key], r["s"]))
	}
	sort.Strings(out)
	return out
}

// TestExtensions_InheritedDictionaryReadsAsSource: GROUP_CATEGORY over
// the extension attribute's label groups by the source dictionary's
// labels exactly as GROUP_CATEGORY over the source field does — for a
// row_local Field source and a buffered param source, over a
// categorical and a packed_bool source — with Include, a Type-less
// group (the categorical smart default, reported by predict) and an
// expression attribute comparing the label to a dictionary label. Predict
// accepts every request.
func TestExtensions_InheritedDictionaryReadsAsSource(t *testing.T) {
	t.Parallel()

	p, _ := dictPulse(t)
	ctx := context.Background()
	at := &types.Cohort{Filename: "dict.pulse"}
	sum := []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}
	shapes := []struct {
		name string
		req  func(key string) *pulse.Request
	}{
		{"group", func(key string) *pulse.Request {
			return &pulse.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: key}}, Aggregations: sum}
		}},
		{"include", func(key string) *pulse.Request {
			return &pulse.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: key, Include: []string{"paid", "team"}}}, Aggregations: sum}
		}},
		{"typeless group", func(key string) *pulse.Request {
			return &pulse.Request{Groups: []*types.Group{{Field: key}}, Aggregations: sum}
		}},
		{"expression", func(key string) *pulse.Request {
			// An expression attribute after the slot compares its label
			// to a dictionary label, as it compares the source's.
			return &pulse.Request{Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "pro_x", Expression: key + ` == "pro" ? x : 0`}},
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "pro_x", Label: "s"}}}
		}},
	}
	for _, typ := range []types.AttributeType{attrDictCopy, attrDictFrom} {
		for _, src := range []string{"plan", "active"} {
			for _, shape := range shapes {
				if src == "active" && (shape.name == "include" || shape.name == "expression") {
					continue // a packed_bool carries no labels to name
				}
				t.Run(string(typ)+"/"+src+"/"+shape.name, func(t *testing.T) {
					req := shape.req("tier")
					req.Cohort, req.Attributes = at, append([]*types.Attribute{dictAttr(typ, src)}, req.Attributes...)
					res, err := p.Predict(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
					if !res.Valid {
						t.Fatalf("predict refused: %v", predictErrors(res))
					}
					if shape.name == "typeless group" && !dictDefaulted(res.DefaultsApplied) {
						t.Fatalf("defaults %+v, want GROUP_CATEGORY on groups[0]", res.DefaultsApplied)
					}
					got, err := p.Process(ctx, req)
					if err != nil {
						t.Fatalf("Process: %v", err)
					}
					twin := shape.req(src)
					twin.Cohort = at
					want, err := p.Process(ctx, twin)
					if err != nil {
						t.Fatalf("source twin: %v", err)
					}
					key, twinKey := "tier", src
					if shape.name == "expression" {
						key, twinKey = "g", "g"
					}
					g, w := dictRowsOf(t, got, key), dictRowsOf(t, want, twinKey)
					if fmt.Sprint(g) != fmt.Sprint(w) || len(g) == 0 {
						t.Fatalf("rows %v, want the source twin's %v", g, w)
					}
				})
			}
		}
	}
}

// dictDefaulted reports whether defaults name GROUP_CATEGORY on the
// first group.
func dictDefaulted(defaults []descriptor.DefaultApplied) bool {
	for _, d := range defaults {
		if fmt.Sprint(d.Path) == "[Groups 0 Type]" && d.Type == string(types.GROUP_CATEGORY) {
			return true
		}
	}
	return false
}

// predictErrors renders res's errors for a failure message.
func predictErrors(res *descriptor.PredictResult) string {
	b, _ := json.Marshal(res.Errors)
	return string(b)
}

// TestExtensions_InheritedDictionaryCrosstabAxis: as a crosstab row axis
// (fused for the row_local attribute, buffered for the buffered one)
// the output keys by the source's labels exactly as the source does.
func TestExtensions_InheritedDictionaryCrosstabAxis(t *testing.T) {
	t.Parallel()

	p, _ := dictPulse(t)
	ctx := context.Background()
	at := &types.Cohort{Filename: "dict.pulse"}
	ct := func(row string, attrs []*types.Attribute) *pulse.Request {
		return &pulse.Request{Cohort: at, Attributes: attrs, Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: row}}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Cell: &types.Aggregation{Type: types.AGG_SUM, Field: "x", Label: "s"}}}
	}
	for _, typ := range []types.AttributeType{attrDictCopy, attrDictFrom} {
		t.Run(string(typ), func(t *testing.T) {
			req := ct("tier", []*types.Attribute{dictAttr(typ, "plan")})
			res, err := p.Predict(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if !res.Valid {
				t.Fatalf("predict refused: %v", predictErrors(res))
			}
			if fused := res.CrosstabFusable != nil && *res.CrosstabFusable; fused != (typ == attrDictCopy) {
				t.Fatalf("crosstab_fusable %v for %s (%v)", fused, typ, res.CrosstabFusionReasons)
			}
			got, err := p.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			want, err := p.Process(ctx, ct("plan", nil))
			if err != nil {
				t.Fatal(err)
			}
			g, _ := json.Marshal(struct{ R, C any }{got.Crosstab.Matrix.RowKeys, got.Crosstab.Matrix.Cells})
			w, _ := json.Marshal(struct{ R, C any }{want.Crosstab.Matrix.RowKeys, want.Crosstab.Matrix.Cells})
			if string(g) != string(w) {
				t.Fatalf("crosstab %s\nwant the source twin's %s", g, w)
			}
		})
	}
}

// TestExtensions_InheritedDictionaryShardParallel: over a 3-shard
// archive the shard-parallel reducers key the output by the source's
// labels exactly as the serial single file does.
func TestExtensions_InheritedDictionaryShardParallel(t *testing.T) {
	t.Parallel()

	mem := afero.NewMemMapFs()
	w, err := pulse.New(pulse.Options{FS: mem})
	if err != nil {
		t.Fatal(err)
	}
	writeDictCohort(t, w, "twin.pulse", 0, dictRows)
	shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
	for i, path := range shards {
		writeDictCohort(t, w, path, i*dictRows/3, (i+1)*dictRows/3)
	}
	if _, err := w.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	run := func(path string, workers int) ([]string, observe.Arm) {
		p, arm := invEngine(t, pulse.Options{Extensions: pulse.Extensions{Attributes: dictRegistrations()}},
			invCohort{mem, path}, invariance.Knobs{ShardWorkers: workers, DecodeWorkers: 1})
		resp, err := p.Process(context.Background(), &pulse.Request{Cohort: &types.Cohort{Filename: path},
			Attributes:   []*types.Attribute{dictAttr(attrDictCopy, "plan")},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tier"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}})
		if err != nil {
			t.Fatalf("%s/%d: %v", path, workers, err)
		}
		return dictRowsOf(t, resp, "tier"), arm()
	}
	want, _ := run("twin.pulse", 1)
	got, arm := run("archive.pulse", 3)
	if arm != observe.ArmShardParallel {
		t.Fatalf("archive arm %s, want %s", arm, observe.ArmShardParallel)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) || len(want) != len(dictLabels) || !strings.HasPrefix(want[0], "free") {
		t.Fatalf("shard-parallel rows %v, want the serial twin's %v (keyed by label)", got, want)
	}
}

// dictEntry is a coded error's comparable wire form.
func dictEntry(code, message string, details map[string]any) string {
	b, _ := json.Marshal(struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}{code, message, details})
	return string(b)
}

// TestExtensions_InheritedDictionarySourceRefusalsPredictMatchesRuntime:
// a source that is unset, names no cohort field, or names an uncoded
// field is refused by predict and by Process with the same code,
// message and details.
func TestExtensions_InheritedDictionarySourceRefusalsPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, _ := dictPulse(t)
	ctx := context.Background()
	earlier := &types.Attribute{Type: types.ATTR_FORMULA, Label: "zz", Expression: "x * 2"}
	cases := []struct {
		name   string
		attrs  []*types.Attribute
		reason string
	}{
		{"field uncoded", []*types.Attribute{dictAttr(attrDictCopy, "x")}, "source_not_coded"},
		{"param uncoded", []*types.Attribute{dictAttr(attrDictFrom, "x")}, "source_not_coded"},
		{"param unset", []*types.Attribute{{Type: attrDictFrom, Field: "x", Label: "tier"}}, "source_unset"},
		{"field another slot's output", []*types.Attribute{earlier, dictAttr(attrDictCopy, "zz")}, "source_not_cohort_field"},
		{"param another slot's output", []*types.Attribute{earlier, dictAttr(attrDictFrom, "zz")}, "source_not_cohort_field"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &pulse.Request{Cohort: &types.Cohort{Filename: "dict.pulse"}, Attributes: c.attrs,
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}}
			res, err := p.Predict(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if res.Valid || len(res.Errors) == 0 {
				t.Fatal("predict admitted the source")
			}
			_, err = p.Process(ctx, req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Details["reason"] != c.reason {
				t.Fatalf("Process err %v, want reason %s", err, c.reason)
			}
			got, want := dictEntry(res.Errors[0].Code, res.Errors[0].Message, res.Errors[0].Details), dictEntry(string(ce.Code), ce.Message, ce.Details)
			if got != want {
				t.Fatalf("predict %s\nruntime %s", got, want)
			}
		})
	}
}

// TestExtensions_InheritedDictionaryCodeOutOfRange: a value that is no
// code of the source's dictionary fails the run with
// PULSE_EXTENSION_ATTRIBUTE_OUTPUT_MISMATCH (grouped or not), never a
// silently minted group.
func TestExtensions_InheritedDictionaryCodeOutOfRange(t *testing.T) {
	t.Parallel()

	p, _ := dictPulse(t)
	ctx := context.Background()
	at := &types.Cohort{Filename: "dict.pulse"}
	bad := []*types.Attribute{dictAttr(attrDictBad, "plan")}
	badB := []*types.Attribute{dictAttr(attrDictBadB, "plan")}
	for name, req := range map[string]*pulse.Request{
		"grouped": {Cohort: at, Attributes: bad, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tier"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}},
		"ungrouped": {Cohort: at, Attributes: bad, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "tier", Label: "n"}}},
		"buffered":  {Cohort: at, Attributes: badB, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "tier", Label: "n"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := p.Process(ctx, req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_EXTENSION_ATTRIBUTE_OUTPUT_MISMATCH {
				t.Fatalf("Process err %v, want PULSE_EXTENSION_ATTRIBUTE_OUTPUT_MISMATCH", err)
			}
			if ce.Details["reason"] != "dictionary_code_out_of_range" || ce.Details["value"] != "9" || ce.Details["dictionary_size"] != 4 || ce.Details["field"] != "plan" || ce.Details["label"] != "tier" {
				t.Fatalf("details %v", ce.Details)
			}
		})
	}
}

// TestExtensions_InheritedDictionaryRegistration: pulse.New's probe
// refuses an attribute whose value is no code of the synthetic source,
// and the registration rules refuse an undeclared source param and a
// non-number Emits; the manifest names each declared source.
func TestExtensions_InheritedDictionaryRegistration(t *testing.T) {
	t.Parallel()

	plus := dictRegistration(attrDictPlus, pulse.AttributeModeRowLocal, false, func(v float64) float64 { return v + 1 })
	undeclared := dictRegistration(attrDictFrom, pulse.AttributeModeBuffered, true, nil)
	undeclared.Params = nil
	emits := dictRegistration(attrDictCopy, pulse.AttributeModeRowLocal, false, nil)
	emits.Emits = pulse.AttributeEmitBool
	for _, c := range []struct {
		name   string
		reg    pulse.AttributeRegistration
		code   errors.Code
		reason string
	}{
		{"probe", plus, errors.PULSE_EXTENSION_ATTRIBUTE_OUTPUT_MISMATCH, "dictionary_code_out_of_range"},
		{"param undeclared", undeclared, errors.PULSE_EXTENSION_PARAM_INVALID, "dictionary_param_undeclared"},
		{"emits", emits, errors.PULSE_EXTENSION_ATTRIBUTE_OUTPUT_MISMATCH, "dictionary_emits_conflict"},
	} {
		_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{Attributes: []pulse.AttributeRegistration{c.reg}}})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != c.code || ce.Details["reason"] != c.reason {
			t.Errorf("%s: pulse.New err %v, want %s reason %s", c.name, err, c.code, c.reason)
			continue
		}
		if c.name == "probe" && (ce.Details["stage"] != "probe" || ce.Details["value"] != "3") {
			t.Errorf("probe details %v, want stage probe, value 3", ce.Details)
		}
	}

	p, _ := dictPulse(t)
	m := p.Manifest(context.Background())
	got := map[string]string{}
	for _, a := range m.Extensions.Attributes {
		got[a.Name] = a.InheritsDictionary
	}
	if got[string(attrDictCopy)] != "field" || got[string(attrDictFrom)] != "params.src" {
		t.Fatalf("manifest inherits_dictionary %v", got)
	}
}

// TestExtensions_InheritedDictionaryLabelsAndMaterialize: a label
// binding on the output column validates and displays through the
// source's dictionary, as one on the source does; and Materialize
// writes the column as the source's type on its dictionary, so the
// stored column groups by the same labels.
func TestExtensions_InheritedDictionaryLabelsAndMaterialize(t *testing.T) {
	t.Parallel()

	p, _ := dictPulse(t)
	ctx := context.Background()
	at := &types.Cohort{Filename: "dict.pulse"}
	sum := []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}
	labelled := func(key string, attrs []*types.Attribute) *pulse.Request {
		return &pulse.Request{Cohort: at, Attributes: attrs, Labels: []*types.LabelBinding{{Field: key, Table: "plans"}},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: key}}, Aggregations: sum}
	}
	req := labelled("tier", []*types.Attribute{dictAttr(attrDictCopy, "plan")})
	res, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid {
		t.Fatalf("predict refused the binding: %v", predictErrors(res))
	}
	got, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	want, err := p.Process(ctx, labelled("plan", nil))
	if err != nil {
		t.Fatal(err)
	}
	if g, w := dictRowsOf(t, got, "tier"), dictRowsOf(t, want, "plan"); fmt.Sprint(g) != fmt.Sprint(w) || !strings.HasPrefix(g[0], "Free plan") {
		t.Fatalf("labelled rows %v, want the source twin's %v", g, w)
	}

	if _, err := p.Materialize(ctx, &pulse.Request{Cohort: at, Attributes: []*types.Attribute{dictAttr(attrDictFrom, "plan")}}, "derived.pulse", pulse.MaterializeOptions{}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	stored, err := p.Process(ctx, &pulse.Request{Cohort: &types.Cohort{Filename: "derived.pulse"},
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tier"}}, Aggregations: sum})
	if err != nil {
		t.Fatalf("Process derived: %v", err)
	}
	twin, err := p.Process(ctx, &pulse.Request{Cohort: at, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "plan"}}, Aggregations: sum})
	if err != nil {
		t.Fatal(err)
	}
	if g, w := dictRowsOf(t, stored, "tier"), dictRowsOf(t, twin, "plan"); fmt.Sprint(g) != fmt.Sprint(w) {
		t.Fatalf("stored column rows %v, want %v", g, w)
	}
}

// TestExtensions_InheritedDictionaryPredictsAxisBound: predict bounds a
// crosstab axis over the output by the source's dictionary — a per-row
// chi-square predicts one p-value per dictionary entry, on the
// dictionary basis, as over the source itself — and the runtime's row
// axis carries that many labels.
func TestExtensions_InheritedDictionaryPredictsAxisBound(t *testing.T) {
	t.Parallel()

	p, _ := dictPulse(t)
	ctx := context.Background()
	ct := func(row string, attrs []*types.Attribute) *pulse.Request {
		return &pulse.Request{Cohort: &types.Cohort{Filename: "dict.pulse"}, Attributes: attrs, Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: row}}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "active"}},
			Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
			Overlays: []types.OverlaySpec{{Kind: types.OverlayKindChiSqRow, Scope: types.OverlayScopeRow}}}
	}
	pvalues := func(r *pulse.Request) *descriptor.PValueCount {
		res, err := p.Predict(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Valid || res.PValues == nil {
			t.Fatalf("predict valid %v, p_values %v (%v)", res.Valid, res.PValues, predictErrors(res))
		}
		return res.PValues
	}
	req := ct("tier", []*types.Attribute{dictAttr(attrDictCopy, "plan")})
	got, want := pvalues(req), pvalues(ct("plan", nil))
	if *got != *want || got.Basis != descriptor.PValueBasisDictionary || got.Total != len(dictLabels) {
		t.Fatalf("predicted p-values %+v, source twin %+v; want both %d on the dictionary basis", got, want, len(dictLabels))
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Crosstab.Matrix.RowKeys) != got.Total {
		t.Fatalf("runtime rows %v, want %d", resp.Crosstab.Matrix.RowKeys, got.Total)
	}
}

// TestExtensions_InheritedDictionaryAutoLabels: an instance default
// binding on the source field also displays the output column, as it
// does an ATTR_PREVIOUS_STATE output.
func TestExtensions_InheritedDictionaryAutoLabels(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys, AutoLabels: []pulse.LabelBinding{{Field: "plan", Table: "plans"}},
		Extensions: pulse.Extensions{Attributes: dictRegistrations(),
			LabelTables: map[string]pulse.LabelTable{"plans": {Description: "Plan display names", Rows: map[string]string{
				"free": "Free plan", "paid": "Paid plan", "pro": "Pro plan", "team": "Team plan"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	writeDictCohort(t, p, "dict.pulse", 0, dictRows)
	resp, err := p.Process(context.Background(), &pulse.Request{Cohort: &types.Cohort{Filename: "dict.pulse"},
		Attributes:   []*types.Attribute{dictAttr(attrDictCopy, "plan")},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "tier"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}})
	if err != nil {
		t.Fatal(err)
	}
	if rows := dictRowsOf(t, resp, "tier"); len(rows) != len(dictLabels) || !strings.HasPrefix(rows[0], "Free plan") {
		t.Fatalf("rows %v, want the default binding's display labels", rows)
	}
	// The copied default is a default: a request not outputting the
	// column raises no PULSE_LABEL_NOT_APPLIED for it.
	resp, err = p.Process(context.Background(), &pulse.Request{Cohort: &types.Cohort{Filename: "dict.pulse"},
		Attributes:   []*types.Attribute{dictAttr(attrDictCopy, "plan")},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range resp.Warnings {
		if w != nil && w.Code == string(errors.PULSE_LABEL_NOT_APPLIED) {
			t.Fatalf("warning %+v for the copied default", w)
		}
	}
}

// TestExtensions_InheritedDictionaryVectorMemberParity: the output is a
// computed column of its source's type (vectors.Classifier.Dict on both
// sides), so a vector naming it is judged alike by predict and the
// runtime — as one naming the source field is.
func TestExtensions_InheritedDictionaryVectorMemberParity(t *testing.T) {
	t.Parallel()

	p, _ := dictPulse(t)
	ctx := context.Background()
	mk := func(member string, attrs []*types.Attribute) *pulse.Request {
		return &pulse.Request{Cohort: &types.Cohort{Filename: "dict.pulse"}, Attributes: attrs,
			Vectors:  []types.VectorSpec{{Name: "v", Fields: []string{member, "x"}}},
			Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "m", Vector: "v"}}}
	}
	verdict := func(r *pulse.Request) string {
		res, err := p.Predict(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		_, perr := p.Process(ctx, r)
		var ce *errors.CodedError
		run := "ok"
		if stderrors.As(perr, &ce) {
			run = string(ce.Code)
		} else if perr != nil {
			run = perr.Error()
		}
		pred := "ok"
		if len(res.Errors) > 0 {
			pred = res.Errors[0].Code
		}
		if pred != run {
			t.Fatalf("predict %s, runtime %s (%v)", pred, run, predictErrors(res))
		}
		return run
	}
	got := verdict(mk("tier", []*types.Attribute{dictAttr(attrDictCopy, "plan")}))
	if want := verdict(mk("plan", nil)); got != want {
		t.Fatalf("verdict over the output %s, over the source %s", got, want)
	}
}

// TestExtensions_InheritedDictionaryOverlayBoundsMatchRuntime: the
// overlay rules predict bounds by a categorical axis's dictionary read
// an extension attribute's inherited dictionary as they read the
// source's (4 plans) — a correspondence dimensions past the cap, a
// FORMULA baseline_position and an INDEX_VS_BASELINE / DELTA_VS_BASELINE
// baseline index past the end are refused by predict exactly as the
// runtime refuses them (code, message, details), over the extension
// column and over the source column alike.
func TestExtensions_InheritedDictionaryOverlayBoundsMatchRuntime(t *testing.T) {
	t.Parallel()

	p, _ := dictPulse(t)
	ctx := context.Background()
	baselineRef := types.OverlayRef{BaselineIndex: &types.OverlayBaselineIndexRef{Position: len(dictLabels)}}
	series := func(host string, spec types.OverlaySpec) *pulse.Request {
		return &pulse.Request{Cohort: &types.Cohort{Filename: "dict.pulse"}, Attributes: []*types.Attribute{dictAttr(attrDictCopy, "plan")},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: host}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
			Overlays:     []types.OverlaySpec{spec}}
	}
	correspondence := func(host string) *pulse.Request {
		return &pulse.Request{Cohort: &types.Cohort{Filename: "dict.pulse"}, Attributes: []*types.Attribute{dictAttr(attrDictCopy, "plan")},
			Crosstab: &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: host}}, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "plan"}},
				Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
			Overlays: []types.OverlaySpec{{Kind: types.OverlayKindCorrespondence, Scope: types.OverlayScopeMatrix,
				Params: json.RawMessage(fmt.Sprintf(`{"dimensions":%d}`, len(dictLabels)))}}}
	}
	cases := []struct {
		name string
		req  func(host string) *pulse.Request
		code errors.Code
	}{
		{"correspondence", correspondence, errors.PULSE_OVERLAY_PARAM_MISSING},
		{"formula", func(h string) *pulse.Request {
			return series(h, types.OverlaySpec{Kind: types.OverlayKindFormula, Scope: types.OverlayScopeGroup,
				Params: json.RawMessage(fmt.Sprintf(`{"formula": "value / baseline", "baseline_position": %d}`, len(dictLabels)))})
		}, errors.PULSE_OVERLAY_REF_UNKNOWN},
		{"index_vs_baseline", func(h string) *pulse.Request {
			return series(h, types.OverlaySpec{Kind: types.OverlayKindIndexVsBaseline, Scope: types.OverlayScopeGroup, Ref: baselineRef})
		}, errors.PULSE_OVERLAY_REF_UNKNOWN},
		{"delta_vs_baseline", func(h string) *pulse.Request {
			return series(h, types.OverlaySpec{Kind: types.OverlayKindDeltaVsBaseline, Scope: types.OverlayScopeGroup, Ref: baselineRef})
		}, errors.PULSE_OVERLAY_REF_UNKNOWN},
	}
	for _, c := range cases {
		for _, host := range []string{"tier", "plan"} {
			t.Run(c.name+"/"+host, func(t *testing.T) {
				req := c.req(host)
				res, err := p.Predict(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				if res.Valid || len(res.Errors) == 0 {
					t.Fatal("predict admitted the overlay")
				}
				_, err = p.Process(ctx, req)
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != c.code {
					t.Fatalf("Process err %v, want %s", err, c.code)
				}
				got, want := dictEntry(res.Errors[0].Code, res.Errors[0].Message, res.Errors[0].Details), dictEntry(string(ce.Code), ce.Message, ce.Details)
				if got != want {
					t.Fatalf("predict %s\nruntime %s", got, want)
				}
			})
		}
	}
}
