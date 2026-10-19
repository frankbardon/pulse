package pulse_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// extend_record_u64_test.go pins #459 end to end: an extension operator
// reads a u64 field through extend.Record.Uint64Value and sees the EXACT
// stored word on every execution mode (streaming, buffered, parallel
// decode, shard archive). The map-decode fallbacks (projection without
// a plan, point lookup, filter-to-file) are pinned record-level in
// internal/service/exact_u64_fallback_test.go. The filter keeps
// 2^53+1 and MaxUint64-1 only — each shares its float64 echo with a
// neighbour (2^53, MaxUint64) the filter must drop, so a float route
// keeps the wrong rows.

const u64ExactFilter = types.FiltererType("FILTER_U64X_EXACT_HIT")

type u64ExactHit struct{}

func (u64ExactHit) Build(spec *types.Filterer, _ *encoding.Schema) (extend.FilterFunc, error) {
	field := spec.Field
	return func(rec extend.Record) (bool, error) {
		v, ok := rec.Uint64Value(field)
		return ok && (v == u64LoNext || v == u64HiPrev), nil
	}, nil
}

func u64ExactExtensions() pulse.Extensions {
	return pulse.Extensions{Filterers: []pulse.FiltererRegistration{{
		Name:    u64ExactFilter,
		Factory: func() extend.FiltererBuilder { return u64ExactHit{} },
	}}}
}

// u64ExactHits counts fixture rows [0, rows) whose key the filter keeps.
func u64ExactHits(rows int) int {
	n := 0
	for r := 0; r < rows; r++ {
		if k, _, _ := u64KeyRow(r); k == u64LoNext || k == u64HiPrev {
			n++
		}
	}
	return n
}

func TestExtendRecord_Uint64ValueExactEveryMode(t *testing.T) {
	ctx := context.Background()
	for name, m := range u64KeyModes(t, u64ExactExtensions()) {
		t.Run(name, func(t *testing.T) {
			req := u64KeyRequest(m, name, nil, []*types.Aggregation{{Type: types.AGG_COUNT, Field: "a", Label: "n"}})
			req.Vectors = nil
			req.Filterers = []*types.Filterer{{Type: u64ExactFilter, Field: "k"}}
			resp, err := m.p.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			want := float64(u64ExactHits(m.rows))
			if got, ok := vexprFloat(resp.Data[0]["n"]); !ok || got != want {
				t.Errorf("n = %v, want %v (only 2^53+1 and MaxUint64-1 pass)", resp.Data[0]["n"], want)
			}
		})
	}
}
