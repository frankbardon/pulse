package main

import (
	"strings"
	"testing"
)

// depsFixture is a miniature tree with one site (and one near miss) per
// rule of the param index, the shared primitives and the metric
// consumers.
var depsFixture = map[string]string{
	"internal/descriptor/capabilities_attributes.go": `package descriptor

type Param struct{ Name, Type, Description string }
type Operator struct {
	Name, Description string
	Params []Param
}

func attributeCapabilities() []Operator {
	return []Operator{comparisonCapability(ATTR_VEC_DISTANCE)}
}

func comparisonCapability(t string) Operator {
	params := []Param{
		{Name: "reference", Type: "any", Description: "a literal array, {\"centroid\": {\"by\": f}} or " + "{\"population\": name}"},
		{Name: "min_valid", Type: "int", Description: "fewest complete pairs"},
	}
	return Operator{Name: t, Description: "per-row distance", Params: params}
}
`,
	"internal/vectors/compare.go": `package vectors

// comparisonParams decodes ATTR_VEC_DISTANCE's params.
type comparisonParams struct {
	Reference []byte ` + "`json:\"reference\"`" + `
	MinValid  int    ` + "`json:\"min_valid,omitempty\"`" + `
}

func comparisonReference() {
	var ref struct {
		Centroid   []byte ` + "`json:\"centroid\"`" + `
		Population any    ` + "`json:\"population\"`" + `
	}
	_ = ref
}

type unrelated struct {
	Other string ` + "`json:\"other\"`" + `
}
`,
	"types/types.go": `package types

type Request struct {
	MinValid int ` + "`json:\"min_valid\"`" + `
}
`,
	"internal/processing/attribute_vec_distance.go": `package processing

import (
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

func applyVecDistance() {
	_ = statdist.BinomialTwoSidedP()
	_ = types.Thing()
	local()
}

func local() {}
`,
	"internal/processing/attribute.go": `package processing

import "github.com/frankbardon/pulse/internal/statdist"

func shared() { _ = statdist.Other() }
`,
	"internal/statdist/binomial.go": `package statdist

func BinomialTwoSidedP() float64 { return 0 }

func Other() float64 { return 0 }
`,
	"internal/statdist/binomial_test.go": `package statdist

import "testing"

func TestBinomialTwoSidedP(t *testing.T) { _ = BinomialTwoSidedP() }

func TestOther(t *testing.T) { _ = Other() }
`,
	"linalg/metric/metric.go": `package metric

type Consumer string

const (
	ConsumerExpr        Consumer = "expr"
	ConsumerVecDistance Consumer = "ATTR_VEC_DISTANCE"
)

var consumerForms = map[Consumer]int{ConsumerExpr: 1, ConsumerVecDistance: 1}

func ResolveFor(c Consumer) error { return nil }

func lone() Consumer { return ConsumerExpr }
`,
	"linalg/metric/metric_test.go": `package metric

import "testing"

func TestConsumerForms(t *testing.T) { _ = consumerForms[ConsumerVecDistance] }

func TestUnrelated(t *testing.T) {}
`,
	"internal/vectors/metric_kind.go": `package vectors

import "github.com/frankbardon/pulse/linalg/metric"

func KindDefault(c metric.Consumer) {}

func resolve() error { return metric.ResolveFor(metric.ConsumerVecDistance) }

func none() {}
`,
	"internal/descriptor/manifest.go": `package descriptor

func assembleManifest() { KindDefault(nil) }

func other() {}
`,
}

func vecDistanceOp() operator {
	op := newOperator(categoryDef{Prefix: "ATTR_", Skill: "op-attr-"}, "ATTR_VEC_DISTANCE")
	op.Consts = []string{"ConsumerVecDistance"}
	return op
}

type ruleCase struct {
	rule, path, sym, note string
	want                  bool
}

func checkRules(t *testing.T, sec Section, cases []ruleCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			s := siteOf(sec, tc.path)
			note, found := symbolNote(s, tc.sym)
			if found != tc.want {
				t.Fatalf("%s · %q listed = %v, want %v (site %+v)", tc.path, tc.sym, found, tc.want, s)
			}
			if tc.want && !strings.Contains(note, tc.note) {
				t.Errorf("%s · %q note %q, want it to contain %q", tc.path, tc.sym, note, tc.note)
			}
		})
	}
}

func TestParamIndexSection_Rules(t *testing.T) {
	t.Parallel()

	r := fixtureRepo(t, depsFixture)
	opFiles := []string{"internal/descriptor/capabilities_attributes.go", "internal/vectors/compare.go", "types/types.go"}
	sec, ok := r.paramIndexSection(vecDistanceOp(), opFiles)
	if !ok {
		t.Fatal("no param index for the fixture")
	}
	checkRules(t, sec, []ruleCase{
		{"a non-enum param through a capability helper", "internal/descriptor/capabilities_attributes.go", "params.reference", "any; form keys {centroid, population}", true},
		{"a plain param", "internal/descriptor/capabilities_attributes.go", "params.min_valid", "int", true},
		{"the operator literal is no param", "internal/descriptor/capabilities_attributes.go", "params.ATTR_VEC_DISTANCE", "", false},
		{"param decoder", "internal/vectors/compare.go", "type comparisonParams", "decodes params.min_valid; decodes params.reference", true},
		{"form decoder", "internal/vectors/compare.go", "func comparisonReference", `decodes params.reference form {"centroid"}`, true},
		{"an unrelated tag is no decoder", "internal/vectors/compare.go", "type unrelated", "", false},
		{"wire types decode slots, not params", "types/types.go", "type Request", "", false},
	})
}

func TestPrimitiveSection_Rules(t *testing.T) {
	t.Parallel()

	r := fixtureRepo(t, depsFixture)
	opFiles := []string{"internal/processing/attribute_vec_distance.go", "internal/processing/attribute.go"}
	sec, gates, ok := r.primitiveSection(vecDistanceOp(), opFiles, map[string]bool{})
	if !ok {
		t.Fatal("no primitives for the fixture")
	}
	checkRules(t, sec, []ruleCase{
		{"a primitive its own file calls", "internal/statdist/binomial.go", "func BinomialTwoSidedP", "called from internal/processing/attribute_vec_distance.go · func applyVecDistance", true},
		{"a call from a shared file is not its own", "internal/statdist/binomial.go", "func Other", "", false},
	})
	if siteOf(sec, "types/types.go") != nil {
		t.Error("the wire types are listed as a primitive")
	}
	if len(gates) != 1 || strings.Join(gates[0].Tests, ",") != "TestBinomialTwoSidedP" {
		t.Errorf("primitive gates %+v, want only TestBinomialTwoSidedP", gates)
	}
	// a primitive already listed as a site is not repeated
	sec, _, ok = r.primitiveSection(vecDistanceOp(), opFiles, map[string]bool{"internal/statdist/binomial.go\x00func BinomialTwoSidedP": true})
	if ok || siteOf(sec, "internal/statdist/binomial.go") != nil {
		t.Errorf("a listed site repeated: %+v", sec)
	}
}

func TestMetricConsumerSection_Rules(t *testing.T) {
	t.Parallel()

	r := fixtureRepo(t, depsFixture)
	if !r.isMetricConsumer(vecDistanceOp()) {
		t.Fatal("ATTR_VEC_DISTANCE is spelled by metric.ConsumerVecDistance but not a consumer")
	}
	if r.isMetricConsumer(newOperator(categoryDef{Prefix: "ATTR_", Skill: "op-attr-"}, "ATTR_ZSCORE")) {
		t.Error("ATTR_ZSCORE reported a metric consumer")
	}
	sec, gates, ok := r.metricConsumerSection()
	if !ok {
		t.Fatal("no metric consumer section for the fixture")
	}
	checkRules(t, sec, []ruleCase{
		{"registry consumer table", "linalg/metric/metric.go", "var consumerForms", "consumer registry table: 2 consumers", true},
		{"one constant is no table", "linalg/metric/metric.go", "func lone", "", false},
		{"consumer API use", "internal/vectors/metric_kind.go", "func resolve", "uses metric.ConsumerVecDistance, ResolveFor", true},
		{"consumer in a signature", "internal/vectors/metric_kind.go", "func KindDefault", "uses metric.Consumer", true},
		{"no consumer API", "internal/vectors/metric_kind.go", "func none", "", false},
		{"caller of a consumer-typed func", "internal/descriptor/manifest.go", "func assembleManifest", "calls KindDefault", true},
		{"no such caller", "internal/descriptor/manifest.go", "func other", "", false},
	})
	if len(gates) != 1 || strings.Join(gates[0].Tests, ",") != "TestConsumerForms" {
		t.Errorf("metric gates %+v, want only TestConsumerForms", gates)
	}
}
