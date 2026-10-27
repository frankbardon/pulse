package pulse_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// filter_to_file_direct_test.go pins U34 E3-S4: FilterToFileWithRequest
// runs every registered filterer it cannot translate into an expression
// — an extension filterer, FILTER_TRUE / FILTER_FALSE, FILTER_SET_* —
// through the engine filterer registry, exactly as Process builds it,
// so the kept rows match Process with the same filterers. Before, each
// was refused PROCESSING_CONFIG "unsupported filter type". The
// zone-capable FILTER_DATE_RANGES runs too since E3-S5
// (filter_to_file_zone_test.go).

// u64ExactPosB keeps the exact-hit keys (2^53+1, MaxUint64-1) whose b
// is positive. b is not its Field: it reaches the filter only through
// the FieldInputs hook, so a projection that ignored the hook would
// hand it a null b and keep nothing.
const u64ExactPosB = types.FiltererType("FILTER_U64X_EXACT_POS_B")

type u64ExactPosBHit struct{}

func (u64ExactPosBHit) Build(spec *types.Filterer, _ *encoding.Schema) (extend.FilterFunc, error) {
	field := spec.Field
	return func(rec extend.Record) (bool, error) {
		v, ok := rec.Uint64Value(field)
		b, bok := rec.NumericValue("b")
		return ok && bok && b > 0 && (v == u64LoNext || v == u64HiPrev), nil
	}, nil
}

func filterToFileDirectExtensions() pulse.Extensions {
	ext := u64ExactExtensions()
	ext.Filterers = append(ext.Filterers, pulse.FiltererRegistration{
		Name:        u64ExactPosB,
		Factory:     func() extend.FiltererBuilder { return u64ExactPosBHit{} },
		FieldInputs: func(json.RawMessage) []string { return []string{"b"} },
	})
	return ext
}

// processCount is AGG_COUNT over cohort under filterers.
func processCount(t *testing.T, p *pulse.Pulse, cohort string, filterers []*types.Filterer) int64 {
	t.Helper()
	resp, err := p.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Filterers:    filterers,
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "a", Label: "n"}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	n, ok := vexprFloat(resp.Data[0]["n"])
	if !ok {
		t.Fatalf("Process n = %v", resp.Data[0]["n"])
	}
	return int64(n)
}

// TestFilterToFile_ExtensionFiltererMatchesProcess: an extension
// filterer (alone, behind a FieldInputs hook, and beside a translated
// built-in) runs through FilterToFileWithRequest on a single file, a
// parallel-decode instance and a shard archive; the kept row count
// equals Process with the same filterers, and every kept row is an
// exact u64 hit (2^53+1 or MaxUint64-1 — each shares its float64 echo
// with a neighbour the filter must drop).
func TestFilterToFile_ExtensionFiltererMatchesProcess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cases := map[string][]*types.Filterer{
		"extension":    {{Type: u64ExactFilter, Field: "k"}},
		"field_inputs": {{Type: u64ExactPosB, Field: "k"}},
		"with_translated": {
			{Type: types.FILTER_RANGE, Field: "a", Values: []string{"0", "25"}},
			{Type: u64ExactFilter, Field: "k"},
		},
	}
	for mode, m := range u64KeyModes(t, filterToFileDirectExtensions()) {
		if mode == "buffered" {
			continue // the same instance and cohort as streaming
		}
		for name, filterers := range cases {
			t.Run(mode+"/"+name, func(t *testing.T) {
				want := processCount(t, m.p, m.path, filterers)
				if want == 0 || want == int64(m.rows) {
					t.Fatalf("Process kept %d of %d rows: the filter does not separate the cohort", want, m.rows)
				}
				if name == "extension" && want != int64(u64ExactHits(m.rows)) {
					t.Fatalf("Process kept %d, want %d exact hits", want, u64ExactHits(m.rows))
				}
				out := "ftf-" + mode + "-" + name
				if err := m.p.Fs().MkdirAll(out, 0o755); err != nil {
					t.Fatal(err)
				}
				res, err := m.p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{
					SourcePath: m.path, Filterers: filterers, OutputDir: out,
				})
				if err != nil {
					t.Fatalf("FilterToFileWithRequest: %v", err)
				}
				if res.RowCount != want {
					t.Fatalf("filter_to_file kept %d rows, Process %d", res.RowCount, want)
				}
				// Every kept row is an exact hit: the output, filtered
				// again, keeps all of its rows.
				all := processCount(t, m.p, res.OutputPath, nil)
				hits := processCount(t, m.p, res.OutputPath, []*types.Filterer{{Type: u64ExactFilter, Field: "k"}})
				if all != want || hits != want {
					t.Fatalf("output: %d rows, %d exact hits; want %d of each", all, hits, want)
				}
			})
		}
	}
}

// TestFilterToFile_BuiltinDirectFiltererMatchesProcess: the built-in
// filterers with no expression translation (FILTER_TRUE / FILTER_FALSE,
// FILTER_SET_*, on a narrow and a wide set) run through
// FilterToFileWithRequest and keep exactly the rows Process keeps.
func TestFilterToFile_BuiltinDirectFiltererMatchesProcess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _, cohort := acceptanceCohort(t)
	cases := []*types.Filterer{
		{Type: types.FILTER_TRUE, Field: "t_packed_bool"},
		{Type: types.FILTER_FALSE, Field: "t_packed_bool"},
	}
	for _, set := range []string{"t_set_u8", "t_set_u256"} {
		cases = append(cases,
			&types.Filterer{Type: types.FILTER_SET_CONTAINS_ANY, Field: set, Values: []string{"a"}},
			&types.Filterer{Type: types.FILTER_SET_CONTAINS_ALL, Field: set, Values: []string{"a", "b"}},
			&types.Filterer{Type: types.FILTER_SET_CONTAINS_NONE, Field: set, Values: []string{"c"}},
			&types.Filterer{Type: types.FILTER_SET_EQUALS, Field: set, Values: []string{"b"}},
		)
	}
	total := processCountX(t, p, cohort, nil)
	for _, f := range cases {
		t.Run(string(f.Type)+"/"+f.Field, func(t *testing.T) {
			filterers := []*types.Filterer{f}
			want := processCountX(t, p, cohort, filterers)
			if want == 0 || want == total {
				t.Fatalf("Process kept %d of %d rows: the filter does not separate the cohort", want, total)
			}
			res, err := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{
				SourcePath: cohort, Filterers: filterers, OutputDir: "ftf-" + string(f.Type) + "-" + f.Field,
			})
			if err != nil {
				t.Fatalf("FilterToFileWithRequest: %v", err)
			}
			if res.RowCount != want {
				t.Fatalf("filter_to_file kept %d rows, Process %d", res.RowCount, want)
			}
		})
	}
}

// processCountX is processCount over the acceptance cohort's x column.
func processCountX(t *testing.T, p *pulse.Pulse, cohort string, filterers []*types.Filterer) int64 {
	t.Helper()
	resp, err := p.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Filterers:    filterers,
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	n, ok := vexprFloat(resp.Data[0]["n"])
	if !ok {
		t.Fatalf("Process n = %v", resp.Data[0]["n"])
	}
	return int64(n)
}

// TestFilterToFile_DirectRefusals: what filter-to-file still refuses,
// each coded. A never-registered type keeps the unsupported-type
// refusal; an extension filterer carrying `tz` is refused like any
// non-zone-capable operator (TestFilterToFile_ZoneRefusalsMatchProcess
// pins it equal to Process's refusal).
func TestFilterToFile_DirectRefusals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m := u64KeyModes(t, filterToFileDirectExtensions())["streaming"]
	cases := []struct {
		name    string
		f       *types.Filterer
		reason  string
		message string
	}{
		{"never_registered", &types.Filterer{Type: "FILTER_U64X_NOPE", Field: "k"}, "", "unsupported filter type"},
		{"extension_tz", &types.Filterer{Type: u64ExactFilter, Field: "k", TimeZone: "UTC"}, "", "does not accept `tz`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := m.p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{
				SourcePath: m.path, Filterers: []*types.Filterer{c.f}, OutputDir: "ftf-refused",
			})
			ce := requireCode(t, err, errors.PROCESSING_CONFIG)
			if ce.Details["operator"] != string(c.f.Type) {
				t.Errorf("details = %v, want operator %s", ce.Details, c.f.Type)
			}
			if c.reason != "" && ce.Details["reason"] != c.reason {
				t.Errorf("details = %v, want reason %s", ce.Details, c.reason)
			}
			if !strings.Contains(ce.Message, c.message) {
				t.Errorf("message = %q, want it to contain %q", ce.Message, c.message)
			}
		})
	}
}
