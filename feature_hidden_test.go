package pulse

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// TestHiddenDefaultTargetGetsNoDefault: on an instance that hides the
// smart-default target, a slot naming only a field is left untyped —
// it fails exactly as it does with defaults disabled — while the same
// request defaults and runs on an unprofiled instance.
func TestHiddenDefaultTargetGetsNoDefault(t *testing.T) {
	fsys := parityFS(t)
	req := func() *Request {
		return &Request{
			Cohort:       &types.Cohort{Filename: parityCohort},
			Aggregations: []*types.Aggregation{{Field: "age"}},
		}
	}
	hiding := newParityHost(t, fsys, "empty").p
	if !hiding.svc.InstanceSnapshot().Hidden(string(types.AGG_SUM)) {
		t.Fatal("the empty fixture does not hide AGG_SUM")
	}
	got := parityOutcome(hiding.Process(context.Background(), req()))

	noDefaults, err := New(Options{FS: fsys, DisableDefaults: true})
	if err != nil {
		t.Fatal(err)
	}
	want := parityOutcome(noDefaults.Process(context.Background(), req()))
	if !parityIsError(want) {
		t.Fatalf("an untyped slot ran with defaults disabled: %s", want)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("hidden default target:\n got  %s\n want %s", got, want)
	}

	open := newParityHost(t, fsys, "").p
	if out := parityOutcome(open.Process(context.Background(), req())); parityIsError(out) {
		t.Errorf("unprofiled smart default failed: %s", out)
	}
}

const keepAllFilter types.FiltererType = "FILTER_HIDDENTEST_KEEPALL"

type keepAllBuilder struct{}

func (keepAllBuilder) Build(*types.Filterer, *encoding.Schema) (extend.FilterFunc, error) {
	return func(extend.Record) (bool, error) { return true, nil }, nil
}

// TestFacetSchemaAdditiveScopeUsesInstanceRegistry: the additive_fields
// scope filters resolve through the instance registry, so an embedder
// filterer works there too (it used to be built against no registry and
// fail as an unknown filter type).
func TestFacetSchemaAdditiveScopeUsesInstanceRegistry(t *testing.T) {
	fsys := parityFS(t)
	p, err := New(Options{FS: fsys, Extensions: Extensions{Filterers: []FiltererRegistration{{
		Name:        keepAllFilter,
		Description: "Test-only filterer that keeps every record.",
		Factory:     func() extend.FiltererBuilder { return keepAllBuilder{} },
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.FacetSchema(context.Background(), &types.FacetRequest{
		Cohort:         &types.Cohort{Filename: parityCohort},
		Fields:         []string{"age"},
		AdditiveFields: []string{"age"},
		Filterers:      []*types.Filterer{{Type: keepAllFilter, Field: "region"}},
	})
	if err != nil {
		t.Fatalf("FacetSchema with an extension scope filter: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
}
