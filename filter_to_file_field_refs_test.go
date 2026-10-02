package pulse_test

import (
	"context"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestFilterToFile_FilterersRefusedLikeProcess: FilterToFileWithRequest
// judges its structured Filterers with the shared field rule
// (FieldRefRefusals, filterer half) — over a single-file cohort and a
// shard archive's canonical schema — so an unknown or empty Field is
// refused with exactly the code, message and details Process uses.
// Before, the filterers were translated to an expression first and the
// caller saw PROCESSING_RUNTIME "compiling filter expression: nope in
// [...]" instead. Known fields still filter.
func TestFilterToFile_FilterersRefusedLikeProcess(t *testing.T) {
	fs := afero.NewMemMapFs()
	ctx := context.Background()
	p := zonePulse(t, fs, "")
	var shards []string
	for i, body := range []string{"cat,n\nc,1\na,2\n", "cat,n\na,3\nb,4\n"} {
		name := fmt.Sprintf("s%d.csv", i)
		if err := afero.WriteFile(fs, name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: name})
		if err != nil {
			t.Fatal(err)
		}
		shards = append(shards, res.Path)
	}
	if _, err := p.CreateShardArchive(ctx, "arch.pulse", shards); err != nil {
		t.Fatal(err)
	}

	refused := map[string]*types.Filterer{
		"unknown include": {Type: types.FILTER_INCLUDE, Field: "nope", Values: []string{"a"}},
		"empty include":   {Type: types.FILTER_INCLUDE, Values: []string{"a"}},
		"unknown null":    {Type: types.FILTER_NULL, Field: "nope"},
		"unknown range":   {Type: types.FILTER_RANGE, Field: "nope", Values: []string{"1", "2"}},
	}
	for _, cohort := range []string{shards[0], "arch.pulse"} {
		for name, f := range refused {
			t.Run(cohort+"/"+name, func(t *testing.T) {
				_, perr := p.Process(ctx, &types.Request{Cohort: &types.Cohort{Filename: cohort},
					Filterers: []*types.Filterer{f}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "n"}}})
				want := requireCode(t, perr, errors.SERVICE_VALIDATION)
				_, ferr := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{SourcePath: cohort,
					Filterers: []*types.Filterer{f}, OutputDir: "out"})
				var got *errors.CodedError
				if !stderrors.As(ferr, &got) {
					t.Fatalf("filter_to_file error = %v, want coded", ferr)
				}
				if got.Code != want.Code || got.Message != want.Message || fmt.Sprint(got.Details) != fmt.Sprint(want.Details) {
					t.Fatalf("filter_to_file %s %q %v != Process %s %q %v",
						got.Code, got.Message, got.Details, want.Code, want.Message, want.Details)
				}
			})
		}
		t.Run(cohort+"/known field filters", func(t *testing.T) {
			res, err := p.FilterToFileWithRequest(ctx, &pulse.FilterToFileRequest{SourcePath: cohort,
				Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "cat", Values: []string{"a"}}},
				OutputDir: "out-" + cohort})
			if err != nil {
				t.Fatal(err)
			}
			want := int64(1)
			if cohort == "arch.pulse" {
				want = 2
			}
			if res.RowCount != want {
				t.Fatalf("rows = %d, want %d", res.RowCount, want)
			}
		})
	}
}
