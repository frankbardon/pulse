package embeddersmoke

// The extension-author flow: an operator written against the public
// extend package, registered through pulse.Options.Extensions and run
// end to end through Process. This file is held to the strict
// extension-author import set (see extensionAuthorImports in
// imports_test.go) — pulse, extend, types, encoding, errors and stdlib.

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// smokeSum is AGG_SUM authored against extend: it reads rows only
// through extend.Record and implements both the buffered and the
// online contract.
type smokeSum struct {
	sum     float64
	updates *int
}

var (
	_ extend.Aggregator        = (*smokeSum)(nil)
	_ extend.OnlineAggregator  = (*smokeSum)(nil)
	_ extend.AggregatorFactory = newSmokeSum(nil)
)

func newSmokeSum(updates *int) extend.AggregatorFactory {
	return func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
		return &smokeSum{updates: updates}, nil
	}
}

func (a *smokeSum) Aggregate(rows extend.Rows, field string) (float64, error) {
	for i := 0; i < rows.Len(); i++ {
		if v, ok := rows.At(i).NumericValue(field); ok {
			a.sum += v
		}
	}
	return a.sum, nil
}

func (a *smokeSum) UpdateRow(rec extend.Record, field string) error {
	if a.updates != nil {
		*a.updates++
	}
	if v, ok := rec.NumericValue(field); ok {
		a.sum += v
	}
	return nil
}

func (a *smokeSum) Finalize() (float64, error) { return a.sum, nil }

func TestExtendAggregatorThroughProcess(t *testing.T) {
	var updates int
	p, fs := newEngine(t, pulse.Options{Extensions: pulse.Extensions{
		Aggregators: []pulse.AggregatorRegistration{{
			Name:        "AGG_SMOKE_SUM",
			Description: "AGG_SUM authored against the public extend package.",
			Factory:     newSmokeSum(&updates),
			Streamable:  true,
		}},
	}})
	ingest(t, p, fs, "sales.pulse")
	ctx := context.Background()

	builtin, err := p.Process(ctx, sumByRegion("sales.pulse"))
	if err != nil {
		t.Fatalf("Process(AGG_SUM): %v", err)
	}
	req := sumByRegion("sales.pulse")
	req.Aggregations[0].Type = "AGG_SMOKE_SUM"
	ext, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process(AGG_SMOKE_SUM): %v", err)
	}

	want, got := totals(t, builtin), totals(t, ext)
	if len(want) != 2 || want["north"] != 15 || want["south"] != 20 {
		t.Fatalf("built-in totals = %v, want north=15 south=20", want)
	}
	if len(got) != len(want) {
		t.Fatalf("extension totals = %v, built-in %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("extension total[%s] = %v, built-in %v", k, got[k], v)
		}
	}
	if updates != 3 {
		t.Errorf("UpdateRow calls = %d, want 3 (the streamable registration must take the online path)", updates)
	}
}
