package pulse

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Two-arm parity for the EXTENSION half of the distinct-key slab
// partition gate, over the same memmap cohort E1-S2 uses.
//
// E2-S2 recorded GrouperRegistration.FansOut and probe-verified it,
// but nothing read it: an embedder's multi-key grouper on a
// summed-across dim still passed E1-S3's gate and inflated n silently.
// E2-S3 projects the fact over descriptor.ExtensionsSnapshot for
// predict and processing.ExtensionRegistry.FansOut for runtime.
//
// The two arms therefore resolve the SAME fact by DIFFERENT routes,
// which is exactly where a bug would hide — so the agreement is
// asserted here, through the real pulse.New wiring, rather than inside
// either package.

const (
	pwExtFanGrouper  = types.GroupType("GROUP_ACME_PANELFAN")
	pwExtFlatGrouper = types.GroupType("GROUP_ACME_TIERFLAT")
)

// pwExtFan is a schema-independent fan-out grouper: every record lands
// in BOTH buckets, which is what MultiKeyStreamingGrouper means and
// what the probe at pulse.New asserts. Schema-independent on purpose —
// the probe constructs the factory against a minimal synthetic schema
// that has no set field.
type pwExtFan struct{}

func (pwExtFan) Group(records []*processing.Record, field string) (map[string][]*processing.Record, error) {
	_ = field
	out := make(map[string][]*processing.Record, 2)
	for _, r := range records {
		out["alpha"] = append(out["alpha"], r)
		out["beta"] = append(out["beta"], r)
	}
	return out, nil
}

func (pwExtFan) KeyForRow(*processing.Record, string) (string, bool, error) {
	return "alpha", true, nil
}

func (pwExtFan) KeysForRow(*processing.Record, string) ([]string, bool, error) {
	return []string{"alpha", "beta"}, true, nil
}

// pwExtFlat maps each record to exactly one bucket — the coherent
// FansOut=false registration.
type pwExtFlat struct{}

func (pwExtFlat) Group(records []*processing.Record, field string) (map[string][]*processing.Record, error) {
	_ = field
	return map[string][]*processing.Record{"all": records}, nil
}

func (pwExtFlat) KeyForRow(*processing.Record, string) (string, bool, error) {
	return "all", true, nil
}

// pwExtExtensions registers both groupers with declarations that match
// their factories, so pulse.New's probe accepts them.
func pwExtExtensions() Extensions {
	return Extensions{
		Groupers: []GrouperRegistration{
			{
				Name:        pwExtFanGrouper,
				Description: "test fan-out grouper",
				Factory: func(*types.Group, *encoding.Schema) (processing.Grouper, error) {
					return pwExtFan{}, nil
				},
				Streamable: true,
				FansOut:    true,
			},
			{
				Name:        pwExtFlatGrouper,
				Description: "test single-key grouper",
				Factory: func(*types.Group, *encoding.Schema) (processing.Grouper, error) {
					return pwExtFlat{}, nil
				},
				Streamable: true,
			},
		},
	}
}

// pwExtRequest is pwPartitionRequest with the INNER (summed-across)
// row level replaced by an extension grouper.
func pwExtRequest(inner types.GroupType, nSource string, depth int) *Request {
	req := pwDistinctRequest("")
	req.Crosstab.Rows = []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "segment"},
		{Type: inner, Field: "brand"},
	}
	params, _ := json.Marshal(map[string]any{"n_source": nSource, "n_within_depth": depth})
	req.Overlays = []types.OverlaySpec{{
		Name:   "pw",
		Kind:   types.OverlayKindPairwisePropZ,
		Scope:  types.OverlayScopeRow,
		Params: params,
	}}
	return req
}

// pwExtPredict drives predict the way pulse.Predict does — with the
// live extensions snapshot attached. Calling PredictFromBytes with nil
// options would test the wrong thing: the snapshot IS the bridge.
func pwExtPredict(t *testing.T, p *Pulse, memFs afero.Fs, req *Request) *descriptor.Envelope {
	t.Helper()
	data, err := afero.ReadFile(memFs, pwDistinctCohort)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return descriptor.PredictFromBytes(data, req, &descriptor.PredictOptions{
		Extensions: p.svc.ExtensionsSnapshot(),
	})
}

func pwExtNewPulse(t *testing.T) (*Pulse, afero.Fs) {
	t.Helper()
	memFs := afero.NewMemMapFs()
	writePairwiseDistinctCohort(t, memFs)
	p, err := New(Options{FS: memFs, Extensions: pwExtExtensions()})
	if err != nil {
		t.Fatalf("New with extension groupers: %v", err)
	}
	return p, memFs
}

// TestPairwiseExtensionFanOut_PredictAndProcessAgree is the story's
// acceptance criterion in one test: a registered multi-key grouper as
// the inner pair-axis level with n_within_distinct refuses at predict
// AND at process, with the same code, the same prose and the same
// details.
func TestPairwiseExtensionFanOut_PredictAndProcessAgree(t *testing.T) {
	p, memFs := pwExtNewPulse(t)
	req := pwExtRequest(pwExtFanGrouper, types.PairwiseNSourceNWithinDistinct, 0)
	wantCode := string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)

	env := pwExtPredict(t, p, memFs, req)
	entry := pwPartitionEnvelopeCode(env, wantCode)
	if entry == nil {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("predict accepted an extension fan-out grouper on a summed-across dim; codes %v", codes)
	}
	if entry.Details["dim_index"] != 1 || entry.Details["group_type"] != string(pwExtFanGrouper) {
		t.Errorf("predict details = %v, want dim_index 1 and group_type %s", entry.Details, pwExtFanGrouper)
	}

	_, perr := p.Process(context.Background(), req)
	if perr == nil {
		t.Fatal("Process accepted a request predict refuses — a predict-only gate does not stop the wrong number")
	}
	coded, ok := perr.(*errors.CodedError)
	if !ok {
		t.Fatalf("Process error %v is not a *errors.CodedError", perr)
	}
	if string(coded.Code) != entry.Code {
		t.Fatalf("arms disagree on code: predict %q vs process %q", entry.Code, coded.Code)
	}
	if coded.Message != entry.Message {
		t.Errorf("arms disagree on prose:\n predict: %s\n process: %s", entry.Message, coded.Message)
	}
	if coded.Details["group_type"] != entry.Details["group_type"] {
		t.Errorf("arms disagree on group_type: predict %v vs process %v",
			entry.Details["group_type"], coded.Details["group_type"])
	}
}

// TestPairwiseExtensionFanOut_ArmsAgreeOnEveryCase walks the whole
// resolution table and requires the two arms to reach the SAME verdict
// on each row. The arms read different sources — the snapshot and the
// live registry — so "predict refuses, process runs" (or the reverse)
// is the failure mode this exists to catch.
func TestPairwiseExtensionFanOut_ArmsAgreeOnEveryCase(t *testing.T) {
	p, memFs := pwExtNewPulse(t)
	wantCode := string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)

	cases := []struct {
		name       string
		req        *Request
		wantRefuse bool
	}{
		{"registered fan-out, summed across",
			pwExtRequest(pwExtFanGrouper, types.PairwiseNSourceNWithinDistinct, 0), true},
		{"registered fan-out, inside the fixed prefix",
			pwExtRequest(pwExtFanGrouper, types.PairwiseNSourceNWithinDistinct, 1), false},
		{"registered single-key, summed across",
			pwExtRequest(pwExtFlatGrouper, types.PairwiseNSourceNWithinDistinct, 0), false},
		{"registered fan-out, plain n_within",
			pwExtRequest(pwExtFanGrouper, types.PairwiseNSourceNWithin, 0), false},
		{"built-in fan-out still gated",
			pwExtRequest(types.GROUP_SET_PER_ELEMENT, types.PairwiseNSourceNWithinDistinct, 0), true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			env := pwExtPredict(t, p, memFs, tc.req)
			predictRefused := pwPartitionEnvelopeCode(env, wantCode) != nil

			_, perr := p.Process(context.Background(), tc.req)
			processRefused := perr != nil && errors.HasCode(perr, errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)

			if predictRefused != processRefused {
				t.Fatalf("arms disagree: predict refused=%v, process refused=%v (process err %v)",
					predictRefused, processRefused, perr)
			}
			if predictRefused != tc.wantRefuse {
				t.Fatalf("refused=%v, want %v (process err %v)", predictRefused, tc.wantRefuse, perr)
			}
			if !tc.wantRefuse && perr != nil {
				t.Fatalf("expected the request to run, got %v", perr)
			}
		})
	}
}

// TestPairwiseExtensionFanOut_SnapshotCarriesTheFlag pins the bridge
// end of the wiring: what pulse.New hands descriptor must actually
// carry the declaration, per registration. Without this the gate above
// could pass for the wrong reason (e.g. a name-shaped heuristic).
func TestPairwiseExtensionFanOut_SnapshotCarriesTheFlag(t *testing.T) {
	p, _ := pwExtNewPulse(t)
	snap := p.svc.ExtensionsSnapshot()
	if snap == nil {
		t.Fatal("ExtensionsSnapshot() = nil for a host with registered groupers")
	}
	for name, want := range map[types.GroupType]bool{
		pwExtFanGrouper:  true,
		pwExtFlatGrouper: false,
	} {
		fan, ok := snap.GrouperFanOut(string(name))
		if !ok {
			t.Errorf("snapshot does not carry grouper %s", name)
			continue
		}
		if fan != want {
			t.Errorf("snapshot FansOut for %s = %v, want %v", name, fan, want)
		}
	}

	// The manifest extensions block is the operator-visible half of the
	// same projection.
	m := descriptor.BuildManifestWithExtensions(snap)
	seen := map[string]bool{}
	for _, g := range m.Extensions.Groupers {
		seen[g.Name] = g.FansOut
	}
	if !seen[string(pwExtFanGrouper)] {
		t.Errorf("manifest extensions block does not report fans_out for %s: %+v",
			pwExtFanGrouper, m.Extensions.Groupers)
	}
	if seen[string(pwExtFlatGrouper)] {
		t.Errorf("manifest extensions block reports fans_out for the single-key grouper %s", pwExtFlatGrouper)
	}
}
