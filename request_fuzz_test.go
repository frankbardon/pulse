package pulse_test

import (
	"context"
	stderrors "errors"
	iofs "io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	pfs "github.com/frankbardon/pulse/internal/fs"
	iio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/io/csv"
	"github.com/frankbardon/pulse/internal/strictjson"
	"github.com/frankbardon/pulse/internal/testutil/fuzzcontract"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// requestFuzzCohorts are the example fixtures the fuzzed requests read:
// the cohorts the example library's single-request roots name.
var requestFuzzCohorts = []string{"experiment", "customers", "orders", "all_types", "training_data", "subscribers", "card_issuers", "transactions", "brand_survey"}

// requestFuzzPulse builds the hermetic instance FuzzRequestDecode
// predicts against: the fixture cohorts imported into an in-memory fs at
// the root and under `.data/` (the example library's data_dir), the
// engine capped by fuzzcontract.Limits.
func requestFuzzPulse(tb testing.TB) *pulse.Pulse {
	tb.Helper()
	mem := pfs.NewMemMap().Fs()
	p, err := pulse.New(pulse.Options{FS: mem, Limits: fuzzcontract.Limits()})
	if err != nil {
		tb.Fatal(err)
	}
	for _, name := range requestFuzzCohorts {
		csvBody, err := os.ReadFile(filepath.Join("internal", "examples", "fixtures", name+".csv"))
		if err != nil {
			tb.Fatal(err)
		}
		schemaBody, err := os.ReadFile(filepath.Join("internal", "examples", "fixtures", "schemas", name+".json"))
		if err != nil {
			tb.Fatal(err)
		}
		schema, err := iio.ParseSchemaJSON(schemaBody)
		if err != nil {
			tb.Fatal(err)
		}
		src := "src/" + name + ".csv"
		if err := afero.WriteFile(mem, src, csvBody, 0o644); err != nil {
			tb.Fatal(err)
		}
		target := name + ".pulse"
		job := &pio.ImportJob{Source: csv.NewReader(mem, src), Target: target, Schema: schema, SampleRows: 50, FS: mem}
		if _, err := p.Import(context.Background(), job); err != nil {
			tb.Fatalf("import %s: %v", name, err)
		}
		body, err := afero.ReadFile(mem, target)
		if err != nil {
			tb.Fatal(err)
		}
		if err := afero.WriteFile(mem, path.Join(".data", target), body, 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return p
}

// FuzzRequestDecode holds the request entry — the strict request decode
// every CLI request file and MCP tool argument passes through
// (strictjson.DecodeRequestInto) and the validation Predict runs on the
// decoded request (pulse.Predict: slot refusals, item expansion, every
// operator's schema check, expression compiles, limits) — to the fuzz
// contract: no panic, a bounded run, every returned error a registered
// *errors.CodedError, and every coded entry Predict reports a registered
// code. Seeds: every example request in internal/examples plus the
// committed corpus under testdata/fuzz/FuzzRequestDecode.
func FuzzRequestDecode(f *testing.F) {
	examples, err := filepath.Glob(filepath.Join("internal", "examples", "*", "*.json"))
	if err != nil {
		f.Fatal(err)
	}
	sort.Strings(examples)
	for _, ex := range examples {
		body, err := os.ReadFile(ex)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(body)
	}
	for _, s := range []string{
		``,
		`{}`,
		`null`,
		`[]`,
		`{"cohort":{"filename":"experiment.pulse"}}`,
		`{"cohort":{"filename":"missing.pulse"}}`,
		`{"cohort":{"filename":"experiment.pulse"},"aggregations":[{"type":"AGG_COUNT"}]}`,
		`{"cohort":{"filename":"experiment.pulse"},"weight":7}`,
		`{"cohort":{"filename":"experiment.pulse"},"filterers":[{"type":"FILTER_EXPRESSION","expression":"x +"}]}`,
		`{"cohort":{"filename":"experiment.pulse"}} trailing`,
	} {
		f.Add([]byte(s))
	}

	p := requestFuzzPulse(f)
	f.Fuzz(func(t *testing.T, body []byte) {
		fuzzcontract.Run(t, body, func(in []byte) error {
			var req types.Request
			if err := strictjson.DecodeRequestInto(in, &req, strictjson.Options{AllowMeta: true}, "request"); err != nil {
				return err
			}
			res, err := p.Predict(context.Background(), &req)
			if err != nil {
				return err
			}
			for _, e := range res.Errors {
				fuzzcontract.RequireCode(t, in, e.Code)
			}
			for _, w := range res.Warnings {
				fuzzcontract.RequireCode(t, in, w.Code)
			}
			return nil
		})
	})
}

// TestPredict_EntryFaultsCoded pins the codes behind the uncoded
// Predict faults FuzzRequestDecode's seeds exposed: an unopenable cohort
// is SERVICE_RESOURCE (the runtime open's code, the fs error kept as
// Cause), a request without a cohort — or no request — SERVICE_VALIDATION.
func TestPredict_EntryFaultsCoded(t *testing.T) {
	t.Parallel()
	p, err := pulse.New(pulse.Options{FS: pfs.NewMemMap().Fs()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, c := range map[string]struct {
		req  *types.Request
		code perr.Code
	}{
		"missing file":   {&types.Request{Cohort: &types.Cohort{Filename: "missing.pulse"}}, perr.SERVICE_RESOURCE},
		"missing anchor": {&types.Request{Cohort: &types.Cohort{Filename: "missing.pulse#a.pulse"}}, perr.SERVICE_RESOURCE},
		"no cohort":      {&types.Request{}, perr.SERVICE_VALIDATION},
		"no request":     {nil, perr.SERVICE_VALIDATION},
	} {
		_, err := p.Predict(ctx, c.req)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != c.code {
			t.Fatalf("%s: want %s, got %v", name, c.code, err)
		}
		if c.code == perr.SERVICE_RESOURCE && !stderrors.Is(err, iofs.ErrNotExist) {
			t.Fatalf("%s: the fs error is not kept as Cause: %v", name, err)
		}
	}
	if _, err := p.PredictEnvelope(ctx, &types.Request{}); !perr.HasCode(err, perr.SERVICE_VALIDATION) {
		t.Fatalf("PredictEnvelope without a cohort: %v", err)
	}
	if _, err := p.PredictBytes(ctx, nil, nil); !perr.HasCode(err, perr.SERVICE_VALIDATION) {
		t.Fatalf("PredictBytes without a request: %v", err)
	}
}
