package mmmpulse

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/types"
)

// helpers_test.go is the Bayes tests' shared scaffolding. REG_MMM_HILL's
// tests (and its generated cohort) moved with the model into Pulse's
// built-in internal/regmmm.

// passROITol is the MMM pass bar's ROI tolerance (relative), shared
// with REG_MMM_HILL's pass bar (internal/regmmm). Never loosen it: tune
// the fixture instead.
const passROITol = 0.20

// instance opens an instance over fsys with the pack registered; mut
// adjusts the options.
func instance(t testing.TB, fsys afero.Fs, mut func(*pulse.Options)) *pulse.Pulse {
	t.Helper()
	opts := pulse.Options{FS: fsys, Extensions: Extensions(), DisablePanicRecovery: true}
	if mut != nil {
		mut(&opts)
	}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p
}

// jointInstance is an instance with the pack registered and the slot
// budget raised to the canonical sweep's samples, over a fresh
// filesystem holding the joint-truth cohort.
func jointInstance(t testing.TB) (*pulse.Pulse, mmmfixture.JointData) {
	t.Helper()
	return jointInstanceFor(t, mmmfixture.Joint())
}

// jointInstanceFor is jointInstance over the generated cohort d (a
// mmmfixture.JointWithSeed replicate, or Joint()).
func jointInstanceFor(t testing.TB, d mmmfixture.JointData) (*pulse.Pulse, mmmfixture.JointData) {
	t.Helper()
	p := instance(t, afero.NewMemMapFs(), func(o *pulse.Options) { o.Limits.MaxComposeSlots = mmmfixture.JointLHSSamples })
	b, err := p.NewCohortBuilder(context.Background(), mmmfixture.JointCohortFile, mmmfixture.JointSchema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range d.Rows {
		if err := b.Append(pulse.CohortRow(r)); err != nil {
			t.Fatalf("Append row %d: %v", i, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, d
}

// process runs one request and returns its response.
func process(t testing.TB, p *pulse.Pulse, req *types.Request) *types.Response {
	t.Helper()
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp
}

func mustJSON(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
