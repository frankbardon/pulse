package pulse

import (
	"encoding/json"
	stderrors "errors"
	"os"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// profiledTemplatePulse builds an engine over the named feature-profile
// fixture ("" = no profile) that loads templates from dir.
func profiledTemplatePulse(t *testing.T, fixture, dir string) *Pulse {
	t.Helper()
	opts := Options{TemplateDirs: []string{dir}}
	if fixture != "" {
		raw, err := os.ReadFile(featureSetFixtureDir + fixture + ".json")
		if err != nil {
			t.Fatalf("read fixture %s: %v", fixture, err)
		}
		fp, err := ParseFeatureProfile(raw)
		if err != nil {
			t.Fatalf("parse fixture %s: %v", fixture, err)
		}
		opts.FeatureProfile = fp
	}
	return newPulse(t, opts)
}

// neverSlotKey is a key no request root declares. Its spelling sorts
// after every gated slot so a body using it in place of a hidden slot
// keeps the same document order.
const neverSlotKey = "zz_never_slot"

// renderFault renders name and returns the coded fault's wire bytes and
// its cause text, with neverSlotKey substituted back to key.
func renderFault(t *testing.T, p *Pulse, name, key string) (string, string) {
	t.Helper()
	_, err := p.RenderTemplate(name, nil)
	ce := codedErr(t, err, perr.PULSE_TEMPLATE_RENDER_INVALID)
	wire, mErr := json.Marshal(ce)
	if mErr != nil {
		t.Fatalf("marshal: %v", mErr)
	}
	cause := ""
	if c := stderrors.Unwrap(ce); c != nil {
		cause = c.Error()
	}
	sub := func(s string) string { return strings.ReplaceAll(s, neverSlotKey, key) }
	return sub(string(wire)), sub(cause)
}

// TestRenderTemplate_HiddenSlotRefusedAsUnknownField: on a profiled
// engine a rendered body setting a hidden slot is refused with the
// PULSE_TEMPLATE_RENDER_INVALID the same body gets for a key no target
// declares — byte-identical after name substitution, cause included —
// for every target with a gated slot, nested positions included. An
// empty value still counts: a set KEY is what strict decode refuses.
// The unprofiled engine renders the hidden-slot body (non-vacuity).
func TestRenderTemplate_HiddenSlotRefusedAsUnknownField(t *testing.T) {
	const agg = `"aggregations": [{"type": "AGG_COUNT", "field": "age"}]`
	const req = `{"cohort": {"filename": "c.pulse"}, ` + agg + `, "SLOT": VALUE}`
	cases := []struct {
		name, target, key, value, body string
	}{
		{"request/crosstab", "request", "crosstab", `{}`, req},
		{"request/joins-empty", "request", "joins", `[]`, req},
		{"request/overlays-empty", "request", "overlays", `[]`, req},
		{"request/crosstab-casefold", "request", "Crosstab", `{}`, req},
		{"composed/overlays", "composed", "overlays", `[]`,
			`{"requests": [{"cohort": {"filename": "c.pulse"}, ` + agg + `}], "SLOT": VALUE}`},
		{"composed/requests[1].crosstab", "composed", "crosstab", `{}`,
			`{"requests": [{"cohort": {"filename": "c.pulse"}, ` + agg + `}, ` + req + `]}`},
		{"chain/overlays", "chain", "overlays", `[]`,
			`{"stages": [{"request": {"cohort": {"filename": "c.pulse"}, ` + agg + `}}], "SLOT": VALUE}`},
		{"chain/stages[0].request.joins", "chain", "joins", `[]`,
			`{"stages": [{"request": ` + req + `}]}`},
		{"facet/overlays", "facet", "overlays", `[]`,
			`{"cohort": {"filename": "c.pulse"}, "fields": ["region"], "SLOT": VALUE}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Same template name in two roots, so details.template agrees.
			hiddenDir, neverDir := t.TempDir(), t.TempDir()
			body := strings.ReplaceAll(tc.body, "VALUE", tc.value)
			doc := func(key string) string {
				return `{"target": "` + tc.target + `", "body": ` + strings.ReplaceAll(body, "SLOT", key) + `}`
			}
			writeTmpl(t, hiddenDir, "t.json", doc(tc.key))
			writeTmpl(t, neverDir, "t.json", doc(neverSlotKey))

			if _, err := profiledTemplatePulse(t, "", hiddenDir).RenderTemplate("t", nil); err != nil {
				t.Fatalf("unprofiled render of hidden-slot body: %v", err)
			}

			gotWire, gotCause := renderFault(t, profiledTemplatePulse(t, "minimal", hiddenDir), "t", tc.key)
			wantWire, wantCause := renderFault(t, profiledTemplatePulse(t, "minimal", neverDir), "t", tc.key)
			if gotWire != wantWire {
				t.Errorf("hidden-slot fault differs from unknown-field fault\n got: %s\nwant: %s", gotWire, wantWire)
			}
			if gotCause != wantCause {
				t.Errorf("cause = %q, want %q", gotCause, wantCause)
			}
			if !strings.Contains(gotWire, `"field":"`+tc.key+`"`) {
				t.Errorf("fault does not name field %q: %s", tc.key, gotWire)
			}
		})
	}
}

// TestRenderTemplate_HiddenSlotDocumentOrder: a hidden slot is refused
// at the point in the document an unknown key would be — a genuine
// unknown key earlier in the body wins, one later loses.
func TestRenderTemplate_HiddenSlotDocumentOrder(t *testing.T) {
	dir := t.TempDir()
	writeTmpl(t, dir, "typo-first.json", `{"target": "request", "body": {"aaa_typo": 1, "crosstab": {}}}`)
	writeTmpl(t, dir, "typo-last.json", `{"target": "request", "body": {"crosstab": {}, "zzz_typo": 1}}`)
	p := profiledTemplatePulse(t, "minimal", dir)
	for name, want := range map[string]string{"typo-first": "aaa_typo", "typo-last": "crosstab"} {
		_, err := p.RenderTemplate(name, nil)
		ce := codedErr(t, err, perr.PULSE_TEMPLATE_RENDER_INVALID)
		if got := ce.Details["field"]; got != want {
			t.Errorf("%s: details[field] = %v, want %q", name, got, want)
		}
	}
}

// TestRenderTemplate_HiddenOperatorRendersLikeNeverRegistered: an
// operator name is not a slot. A template naming an operator the
// profile hides renders exactly as one naming a never-registered
// operator — both succeed, identical after name substitution — and
// RenderTemplateRequest agrees.
func TestRenderTemplate_HiddenOperatorRendersLikeNeverRegistered(t *testing.T) {
	const hiddenOp, neverOp = "AGG_AVERAGE", "AGG_ZZ_NEVER_REGISTERED"
	dir := t.TempDir()
	doc := func(op string) string {
		return `{"target": "request", "body": {"cohort": {"filename": "c.pulse"}, "aggregations": [{"type": "` + op + `", "field": "age"}]}}`
	}
	writeTmpl(t, dir, "hidden.json", doc(hiddenOp))
	writeTmpl(t, dir, "never.json", doc(neverOp))
	p := profiledTemplatePulse(t, "minimal", dir)
	if p.svc.InstanceSnapshot().Enabled(hiddenOp) {
		t.Fatalf("fixture must hide %s", hiddenOp)
	}

	hidden, err := p.RenderTemplate("hidden", nil)
	if err != nil {
		t.Fatalf("render hidden-operator template: %v", err)
	}
	never, err := p.RenderTemplate("never", nil)
	if err != nil {
		t.Fatalf("render never-registered-operator template: %v", err)
	}
	if got, want := string(hidden.JSON), strings.ReplaceAll(string(never.JSON), neverOp, hiddenOp); got != want {
		t.Errorf("rendered JSON differs\n got: %s\nwant: %s", got, want)
	}
	hReq, err := p.RenderTemplateRequest("hidden", nil)
	if err != nil {
		t.Fatalf("RenderTemplateRequest hidden: %v", err)
	}
	nReq, err := p.RenderTemplateRequest("never", nil)
	if err != nil {
		t.Fatalf("RenderTemplateRequest never: %v", err)
	}
	hb, _ := json.Marshal(hReq)
	nb, _ := json.Marshal(nReq)
	if got, want := string(hb), strings.ReplaceAll(string(nb), neverOp, hiddenOp); got != want {
		t.Errorf("typed request differs\n got: %s\nwant: %s", got, want)
	}
}

// TestRenderTemplateRequest_HiddenSlotRefused: the request shorthand
// surfaces the same refusal.
func TestRenderTemplateRequest_HiddenSlotRefused(t *testing.T) {
	dir := t.TempDir()
	writeTmpl(t, dir, "x.json", `{"target": "request", "body": {"cohort": {"filename": "c.pulse"}, "joins": []}}`)
	_, err := profiledTemplatePulse(t, "minimal", dir).RenderTemplateRequest("x", nil)
	ce := codedErr(t, err, perr.PULSE_TEMPLATE_RENDER_INVALID)
	if got := ce.Details["field"]; got != "joins" {
		t.Errorf("details[field] = %v, want %q", got, "joins")
	}
}
