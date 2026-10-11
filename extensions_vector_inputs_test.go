package pulse_test

import (
	"context"
	stderrors "errors"
	"reflect"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Extension operators that take a vector input (vector / fields, and a
// test's vector2 / fields2) have their member sets judged by the
// built-in member rules in predict AND at runtime, and their factories
// receive the resolved literal members — the TODO #409 contract.

// vecSeen records the spec each extension factory was handed.
type vecSeen struct {
	mu                    sync.Mutex
	test, attr, group     []string
	testVec, attrVec      string
	groupVec, groupField  string
	testFields2           []string
	testCalls, groupCalls int
	attrCalls             int
}

type vecRowTest struct{ n int }

func (vecRowTest) UpdateRow(extend.Record) error { return nil }
func (t vecRowTest) Finalize() (*types.TestResult, error) {
	return &types.TestResult{Statistic: float64(t.n), PValue: 1, Alpha: 0.05}, nil
}

type vecAttr struct{}

func (vecAttr) Compute(rows extend.Rows, _ string) ([]float64, error) {
	return make([]float64, rows.Len()), nil
}

type vecGrouper struct{}

func (vecGrouper) Group(rows extend.Rows, _ string) (map[string][]int, error) {
	idx := make([]int, rows.Len())
	for i := range idx {
		idx[i] = i
	}
	return map[string][]int{"all": idx}, nil
}

func openVectorExtensions(t *testing.T) (*pulse.Pulse, []byte, *vecSeen) {
	t.Helper()
	seen := &vecSeen{}
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "vec.pulse", paritySchema(t), 0, paritySmallRows)
	data, err := afero.ReadFile(fsys, "vec.pulse")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: pulse.Extensions{
		Tests: []pulse.TestRegistration{{
			Name: "TEST_ACME_VEC_P", Tier: pulse.TestTierRow,
			RowFactory: func(spec *types.Test, _ *encoding.Schema) (extend.RowTest, error) {
				seen.mu.Lock()
				defer seen.mu.Unlock()
				seen.testCalls++
				seen.test, seen.testVec, seen.testFields2 = spec.Fields, spec.Vector, spec.Fields2
				return vecRowTest{n: len(spec.Fields)}, nil
			},
		}},
		Attributes: []pulse.AttributeRegistration{{
			Name: "ATTR_ACME_VEC_ZERO", Mode: pulse.AttributeModeBuffered,
			Factory: func(spec *types.Attribute, _ *encoding.Schema) (extend.AttributeComputer, error) {
				seen.mu.Lock()
				defer seen.mu.Unlock()
				seen.attrCalls++
				seen.attr, seen.attrVec = spec.Fields, spec.Vector
				return vecAttr{}, nil
			},
		}},
		Groupers: []pulse.GrouperRegistration{{
			Name: "GROUP_ACME_VEC_ALL",
			Factory: func(spec *types.Group, _ *encoding.Schema) (extend.Grouper, error) {
				seen.mu.Lock()
				defer seen.mu.Unlock()
				seen.groupCalls++
				seen.group, seen.groupVec, seen.groupField = spec.Fields, spec.Vector, spec.Field
				return vecGrouper{}, nil
			},
		}},
	}})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	// pulse.New's probe constructs every factory once; count from here.
	seen.testCalls, seen.attrCalls, seen.groupCalls = 0, 0, 0
	return p, data, seen
}

// TestExtensions_VectorInputsResolvedForFactories: a named vector on an
// extension test, attribute and grouper reaches each factory as the
// resolved literal members (Vector cleared); the grouper keeps its
// authored Field.
func TestExtensions_VectorInputsResolvedForFactories(t *testing.T) {
	p, data, seen := openVectorExtensions(t)
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: "vec.pulse"},
		Vectors:      []types.VectorSpec{{Name: "v", Fields: []string{"score", "qty"}}},
		Tests:        []*types.Test{{Type: "TEST_ACME_VEC_P", Vector: "v", Fields2: []string{"q*"}, Label: "t"}},
		Attributes:   []*types.Attribute{{Type: "ATTR_ACME_VEC_ZERO", Fields: []string{"s*", "qty"}, Label: "z"}},
		Groups:       []*types.Group{{Type: "GROUP_ACME_VEC_ALL", Field: "region", Vector: "v"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
	}
	env, err := p.PredictBytes(context.Background(), data, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("predict refused a valid request: %+v", env.Errors[0])
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	want := []string{"score", "qty"}
	seen.mu.Lock()
	defer seen.mu.Unlock()
	if seen.testCalls == 0 || seen.attrCalls == 0 || seen.groupCalls == 0 {
		t.Fatalf("a factory never ran: %+v", seen)
	}
	if !reflect.DeepEqual(seen.test, want) || seen.testVec != "" || !reflect.DeepEqual(seen.testFields2, []string{"qty"}) {
		t.Errorf("test factory got fields %v vector %q fields2 %v, want %v / \"\" / [qty]", seen.test, seen.testVec, seen.testFields2, want)
	}
	if !reflect.DeepEqual(seen.attr, want) || seen.attrVec != "" {
		t.Errorf("attribute factory got fields %v vector %q, want %v (the glob resolved)", seen.attr, seen.attrVec, want)
	}
	if !reflect.DeepEqual(seen.group, want) || seen.groupVec != "" || seen.groupField != "region" {
		t.Errorf("grouper factory got fields %v vector %q field %q, want %v / \"\" / region", seen.group, seen.groupVec, seen.groupField, want)
	}
	if len(resp.Tests) != 1 || resp.Tests[0].Statistic != 2 {
		t.Errorf("tests %+v, want the extension's member count 2", resp.Tests)
	}
}

// TestExtensions_VectorInputsJudgedAsBuiltins: a bad member list on an
// extension test, attribute or grouper is refused by predict and by
// Process with the built-in member rule's code — never reaching the
// factory.
func TestExtensions_VectorInputsJudgedAsBuiltins(t *testing.T) {
	vecs := []types.VectorSpec{{Name: "v", Fields: []string{"score", "qty"}}}
	cases := []struct {
		name   string
		mut    func(*types.Request)
		code   errors.Code
		reason string
	}{
		{"test non-numeric member", func(r *types.Request) {
			r.Tests = []*types.Test{{Type: "TEST_ACME_VEC_P", Fields: []string{"score", "region"}}}
		}, errors.PULSE_VECTOR_MEMBER_TYPE, ""},
		{"test unknown field", func(r *types.Request) {
			r.Tests = []*types.Test{{Type: "TEST_ACME_VEC_P", Fields: []string{"score", "nope"}}}
		}, errors.SERVICE_VALIDATION, ""},
		{"test duplicate member", func(r *types.Request) {
			r.Tests = []*types.Test{{Type: "TEST_ACME_VEC_P", Fields: []string{"score", "s*"}}}
		}, errors.PULSE_VECTOR_DUPLICATE, ""},
		{"test unknown vector", func(r *types.Request) {
			r.Tests = []*types.Test{{Type: "TEST_ACME_VEC_P", Vector: "nope"}}
		}, errors.PULSE_VECTOR_UNKNOWN, ""},
		{"test vector and fields", func(r *types.Request) {
			r.Tests = []*types.Test{{Type: "TEST_ACME_VEC_P", Vector: "v", Fields: []string{"qty"}}}
		}, errors.SERVICE_VALIDATION, "vector_and_fields"},
		{"attribute non-numeric member", func(r *types.Request) {
			r.Attributes = []*types.Attribute{{Type: "ATTR_ACME_VEC_ZERO", Fields: []string{"region"}, Label: "z"}}
		}, errors.PULSE_VECTOR_MEMBER_TYPE, ""},
		{"attribute unknown vector", func(r *types.Request) {
			r.Attributes = []*types.Attribute{{Type: "ATTR_ACME_VEC_ZERO", Vector: "nope", Label: "z"}}
		}, errors.PULSE_VECTOR_UNKNOWN, ""},
		{"grouper unknown field", func(r *types.Request) {
			r.Groups = []*types.Group{{Type: "GROUP_ACME_VEC_ALL", Field: "region", Fields: []string{"nope"}}}
		}, errors.SERVICE_VALIDATION, ""},
		{"grouper vector and fields", func(r *types.Request) {
			r.Groups = []*types.Group{{Type: "GROUP_ACME_VEC_ALL", Field: "region", Vector: "v", Fields: []string{"qty"}}}
		}, errors.SERVICE_VALIDATION, "vector_and_fields"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, data, seen := openVectorExtensions(t)
			req := &types.Request{
				Cohort:       &types.Cohort{Filename: "vec.pulse"},
				Vectors:      vecs,
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
			}
			c.mut(req)
			env, err := p.PredictBytes(context.Background(), data, req)
			if err != nil {
				t.Fatal(err)
			}
			if !hasEnvelopeError(env.Errors, c.code, c.reason) {
				t.Errorf("predict errors %+v, want %s %s", env.Errors, c.code, c.reason)
			}
			_, perr := p.Process(context.Background(), req)
			var coded *errors.CodedError
			if !stderrors.As(perr, &coded) || coded.Code != c.code || (c.reason != "" && coded.Details["reason"] != c.reason) {
				t.Errorf("Process error %v, want %s %s", perr, c.code, c.reason)
			}
			seen.mu.Lock()
			defer seen.mu.Unlock()
			if seen.testCalls+seen.attrCalls+seen.groupCalls != 0 {
				t.Errorf("a factory ran on a refused request: %+v", seen)
			}
		})
	}
}

func hasEnvelopeError(entries []*descriptor.EnvelopeEntry, code errors.Code, reason string) bool {
	for _, e := range entries {
		if e.Code == string(code) && (reason == "" || e.Details["reason"] == reason) {
			return true
		}
	}
	return false
}
