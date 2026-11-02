package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// feature_profile_lookup_tables_test.go pins capability:lookup_tables:
// with it hidden, an instance behaves as if no lookup table was ever
// registered — programmatic or loaded from Options.LookupTablesDir —
// at predict and at run, on the manifest and on the error list.

// lookupHiddenProfile offers the expression operators that call
// lookup() and range_label(), with range tables but no lookup tables.
func lookupHiddenProfile() []string {
	return []string{"capability:process", "capability:range_tables", "AGG_COUNT", "ATTR_FORMULA", "FILTER_EXPRESSION"}
}

func lookupEnabledProfile() []string {
	return append(lookupHiddenProfile(), "capability:lookup_tables")
}

// lookupTableHost builds an instance over the parity cohort. tables
// "prog" registers the table programmatically, "dir" loads it from a
// LookupTablesDir file, "" registers none.
func lookupTableHost(t *testing.T, fsys afero.Fs, profile []string, tables string) *Pulse {
	t.Helper()
	opts := Options{FS: fsys, Extensions: Extensions{
		RangeTables: map[string]RangeTable{"age_bands": {NumericRanges: []NumericRangeSpec{
			{Label: "young", End: rlF64(30)}, {Label: "old", Start: rlF64(30)},
		}}},
	}}
	switch tables {
	case "prog":
		opts.Extensions.LookupTables = map[string]LookupTable{"rates": {Rows: map[string]float64{
			"north": 1, "south": 2, "east": 3, "west": 4,
		}}}
	case "dir":
		dir := t.TempDir()
		writeLookupFile(t, dir, "rates.json", `{"north": 1, "south": 2, "east": 3, "west": 4}`)
		opts.LookupTablesDir = dir
	}
	if profile != nil {
		opts.FeatureProfile = &FeatureProfile{Features: profile}
	}
	p, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func lookupRequests() map[string]*types.Request {
	cohort := &types.Cohort{Filename: parityCohort}
	count := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "age", Label: "n"}}
	return map[string]*types.Request{
		"attribute": {
			Cohort:       cohort,
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "r", Expression: `lookup("rates", region) * age`}},
			Aggregations: count,
		},
		"filter": {
			Cohort:       cohort,
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `lookup("rates", region) > 1`}},
			Aggregations: count,
		},
		"literal": {
			Cohort:       cohort,
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `lookup("nope", "north") > 1`}},
			Aggregations: count,
		},
	}
}

// TestHiddenLookupTables_LikeNeverRegistered: with
// capability:lookup_tables hidden, a request calling lookup() over a
// registered table — programmatic or directory-loaded — predicts and
// runs byte-identically to the same profiled instance with no table
// registered (lookup is an unknown function), and the run's refusal
// carries predict's code. With the capability enabled the table
// resolves.
func TestHiddenLookupTables_LikeNeverRegistered(t *testing.T) {
	t.Parallel()

	fsys := parityFS(t)
	ctx := context.Background()
	data := parityCohortBytes(t)
	none := lookupTableHost(t, fsys, lookupHiddenProfile(), "")
	enabled := lookupTableHost(t, fsys, lookupEnabledProfile(), "prog")
	for _, src := range []string{"prog", "dir"} {
		hidden := lookupTableHost(t, fsys, lookupHiddenProfile(), src)
		for name, req := range lookupRequests() {
			t.Run(src+"/"+name, func(t *testing.T) {
				run := func(p *Pulse) (string, string) {
					resp, err := p.Process(ctx, req)
					pred, perr := p.PredictBytes(ctx, data, req)
					return string(parityOutcome(resp, err)), string(parityOutcome(pred, perr))
				}
				hRun, hPred := run(hidden)
				nRun, nPred := run(none)
				if hRun != nRun {
					t.Errorf("run: hidden table differs from none\nhidden: %s\nnone:   %s", hRun, nRun)
				}
				if hPred != nPred {
					t.Errorf("predict: hidden table differs from none\nhidden: %s\nnone:   %s", hPred, nPred)
				}
				_, runErr := hidden.Process(ctx, req)
				pred, err := hidden.Predict(ctx, req)
				if err != nil {
					t.Fatalf("Predict: %v", err)
				}
				if runErr == nil || pred.Valid {
					t.Fatalf("hidden lookup(): run err %v, predict valid %v — want both refused", runErr, pred.Valid)
				}
				ce := wantCoded(t, runErr)
				if len(pred.Errors) == 0 || pred.Errors[0].Code != string(ce.Code) {
					t.Errorf("predict errors %+v, want run code %s first", pred.Errors, ce.Code)
				}
				if name != "literal" {
					if _, err := enabled.Process(ctx, req); err != nil {
						t.Errorf("enabled lookup(): %v", err)
					}
				}
			})
		}
	}
}

// TestHiddenLookupTables_RangeLabelUnaffected: hiding lookup tables
// leaves range_label() (capability:range_tables) resolving.
func TestHiddenLookupTables_RangeLabelUnaffected(t *testing.T) {
	t.Parallel()

	p := lookupTableHost(t, parityFS(t), lookupHiddenProfile(), "prog")
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: parityCohort},
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `range_label("age_bands", age) == "old"`}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "age", Label: "n"}},
	}
	pred, err := p.Predict(context.Background(), req)
	if err != nil || !pred.Valid {
		t.Fatalf("predict range_label: %v %+v", err, pred)
	}
	if _, err := p.Process(context.Background(), req); err != nil {
		t.Fatalf("run range_label: %v", err)
	}
}

// TestHiddenLookupTables_ManifestAndErrors: the manifest
// extensions.lookup_tables is [] with the capability hidden (the table
// listed when enabled), and every PULSE_LOOKUP_* table code is absent
// from the error list even though the expression operators stay on.
func TestHiddenLookupTables_ManifestAndErrors(t *testing.T) {
	t.Parallel()

	fsys := parityFS(t)
	codes := []errors.Code{
		errors.PULSE_LOOKUP_TABLE_UNKNOWN, errors.PULSE_LOOKUP_MISS, errors.PULSE_LOOKUP_FILE_INVALID,
		errors.PULSE_LOOKUP_CELL_NOT_NUMERIC, errors.PULSE_LOOKUP_TABLE_EMPTY, errors.PULSE_LOOKUP_AXIS_INVALID,
		errors.PULSE_LOOKUP_KEY_ARITY, errors.PULSE_LOOKUP_KEY_OFF_AXIS, errors.PULSE_LOOKUP_KEY_COLLISION,
		errors.PULSE_LOOKUP_TABLE_INCOMPLETE,
	}
	for _, tc := range []struct {
		name    string
		profile []string
		want    bool
	}{
		{"hidden", lookupHiddenProfile(), false},
		{"enabled", lookupEnabledProfile(), true},
		{"no profile", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := lookupTableHost(t, fsys, tc.profile, "dir")
			m := p.Manifest(context.Background())
			if got := len(m.Extensions.LookupTables) == 1 && m.Extensions.LookupTables[0].Name == "rates"; got != tc.want {
				t.Errorf("lookup_tables = %+v, want listed=%v", m.Extensions.LookupTables, tc.want)
			}
			raw, err := json.Marshal(m.Extensions)
			if err != nil {
				t.Fatal(err)
			}
			var ext map[string]json.RawMessage
			if err := json.Unmarshal(raw, &ext); err != nil {
				t.Fatal(err)
			}
			if !tc.want && string(ext["lookup_tables"]) != "[]" {
				t.Errorf("hidden lookup_tables wire = %s, want []", ext["lookup_tables"])
			}
			listed := map[string]bool{}
			for _, c := range m.ErrorCodes {
				listed[c] = true
			}
			for _, c := range codes {
				if _, ok := p.ErrorLookup(string(c)); ok != tc.want {
					t.Errorf("ErrorLookup(%s) ok = %v, want %v", c, ok, tc.want)
				}
				if listed[string(c)] != tc.want {
					t.Errorf("manifest error_codes lists %s = %v, want %v", c, listed[string(c)], tc.want)
				}
			}
		})
	}
}

// TestHiddenLookupTables_FilesStillValidated: a hidden capability never
// skips the directory parse — a malformed lookup-table file fails
// pulse.New on a hiding instance exactly as on any other (range tables
// behave the same way).
func TestHiddenLookupTables_FilesStillValidated(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeLookupFile(t, dir, "bad.json", `{"north": "one"}`)
	_, err := New(Options{FS: afero.NewMemMapFs(), LookupTablesDir: dir,
		FeatureProfile: &FeatureProfile{Features: []string{"capability:process"}}})
	if err == nil {
		t.Fatal("New accepted a malformed lookup-table file on a hiding instance")
	}
	wantCoded(t, err)
}

func wantCoded(t *testing.T, err error) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("want a coded error, got %v", err)
	}
	return ce
}
