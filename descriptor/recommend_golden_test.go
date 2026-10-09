package descriptor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// recommendGoldenCohort is the bound fixture: one field of each kind an
// intent's roles take (two categoricals, three numerics, a date and a
// boolean), header + schema only.
const recommendGoldenCohort = "recommend.pulse"

func recommendGoldenPulse(t *testing.T) *pulse.Pulse {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region of the account", Dictionary: goldenDictionary(t, "north", "south", "east", "west")},
		{Name: "plan", Type: encoding.FieldTypeCategoricalU8, Description: "Subscription plan of the account", Dictionary: goldenDictionary(t, "basic", "pro")},
		{Name: "revenue", Type: encoding.FieldTypeF64, Description: "Monthly revenue of the account"},
		{Name: "seats", Type: encoding.FieldTypeU16, Description: "Licensed seats on the account"},
		{Name: "score", Type: encoding.FieldTypeF64, Description: "Satisfaction score of the account"},
		{Name: "signup", Type: encoding.FieldTypeDate, Description: "Day the account signed up"},
		{Name: "churned", Type: encoding.FieldTypePackedBool, Description: "Whether the account has churned"},
	}}
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, recommendGoldenCohort, goldenPulseFile(t, schema), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRecommendGolden pins Recommend per analytic intent, unbound and
// bound to the fixture cohort, as the --json envelope carries it.
// Regenerate with go test ./descriptor/ -run 'Test.*Golden' -update.
func TestRecommendGolden(t *testing.T) {
	p := recommendGoldenPulse(t)
	analytic := 0
	for _, in := range pulse.Intents() {
		if !in.Analytic {
			continue
		}
		analytic++
		for _, mode := range []string{"unbound", "bound"} {
			t.Run(in.ID+"/"+mode, func(t *testing.T) {
				req := descriptor.RecommendRequest{Intent: in.ID}
				if mode == "bound" {
					req.Cohort = &types.Cohort{Filename: recommendGoldenCohort}
				}
				res, err := p.Recommend(context.Background(), req)
				if err != nil {
					t.Fatalf("Recommend: %v", err)
				}
				var buf bytes.Buffer
				enc := json.NewEncoder(&buf)
				enc.SetEscapeHTML(false)
				enc.SetIndent("", "  ")
				if err := enc.Encode(descriptor.NewEnvelope(res)); err != nil {
					t.Fatal(err)
				}
				compareGolden(t, "recommend."+in.ID+"."+mode+".json", bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
			})
		}
	}
	if analytic == 0 {
		t.Fatal("vacuous: no analytic intent")
	}
}
