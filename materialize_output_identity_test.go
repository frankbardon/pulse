package pulse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The absent-output pins: a materialize run whose request carries no
// `output` writes the bytes it wrote before the slot existed. The hashes
// below were recorded from the pre-slot Materialize; every derived value
// in the fixture is exact (0/1 one-hot cells, row numbers, a lag copying
// exact weights), so the bytes are the same on every architecture.
const (
	absentOutputCohortSHA256  = "f107a2dd05ad633db2b601145a205d5da3534d4d37ad16840369d0de44e5ec93"
	absentOutputSidecarSHA256 = "d5de3484ef6832391d54f1b40e93c139c72d4442bcc303054a20df4861b02599"
)

// absentOutputRequest is the pinned fixture's request: a one-hot feature
// over the value-labelled Q1, a row number and a lag of the weight, over
// the imported .sav whose SPSS sidecar the run carries.
func absentOutputRequest() *Request {
	return &Request{
		Cohort:   &types.Cohort{Filename: suggestCohort},
		Features: []*types.Feature{{Type: types.FEAT_ONE_HOT, Field: "Q1"}},
		Windows: []*types.Window{
			{Type: types.WIN_ROW_NUMBER, Label: "rn", OrderBy: []types.OrderKey{{Field: "ID"}}},
			{Type: types.WIN_LAG, Field: "WT", Label: "prev_wt", OrderBy: []types.OrderKey{{Field: "ID"}}},
		},
	}
}

// sidecarDigest hashes the carried sidecar beside cohort with its
// fingerprint's source_mod_time zeroed — the only byte that follows the
// wall clock rather than the run.
func sidecarDigest(t *testing.T, afs afero.Fs, cohort string) string {
	t.Helper()
	raw, err := afero.ReadFile(afs, spss.SidecarPath(cohort))
	if err != nil {
		t.Fatalf("no sidecar beside %s: %v", cohort, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	fp, ok := doc["fingerprint"].(map[string]any)
	if !ok {
		t.Fatalf("sidecar has no fingerprint object: %s", raw)
	}
	fp["source_mod_time"] = 0
	norm, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(norm)
	return hex.EncodeToString(sum[:])
}

// TestMaterialize_AbsentOutputIsByteIdentical: with no `output` block
// the cohort bytes and the carried SPSS sidecar (clock field zeroed)
// equal the pre-slot recordings.
func TestMaterialize_AbsentOutputIsByteIdentical(t *testing.T) {
	t.Parallel()

	afs := afero.NewMemMapFs()
	p := importSav(t, afs, labelledSavSpec(), Options{})
	res, err := p.Materialize(context.Background(), absentOutputRequest(), "derived.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings %v", res.Warnings)
	}
	raw, err := afero.ReadFile(afs, "derived.pulse")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != absentOutputCohortSHA256 {
		t.Errorf("cohort sha256 = %s, want %s", got, absentOutputCohortSHA256)
	}
	if got := sidecarDigest(t, afs, "derived.pulse"); got != absentOutputSidecarSHA256 {
		t.Errorf("sidecar sha256 = %s, want %s", got, absentOutputSidecarSHA256)
	}
}
