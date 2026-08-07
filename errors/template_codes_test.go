package errors_test

import (
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

// templateFamily is the PULSE_TEMPLATE_* error family. Every code that
// a template declaration, variable resolution, or render pass can raise
// must appear here AND in errors.AllCodes(); the table is the contract
// the rest of the request-templating surface codes against.
var templateFamily = []struct {
	code perr.Code
	want string
}{
	{perr.PULSE_TEMPLATE_NOT_FOUND, "PULSE_TEMPLATE_NOT_FOUND"},
	{perr.PULSE_TEMPLATE_INVALID, "PULSE_TEMPLATE_INVALID"},
	{perr.PULSE_TEMPLATE_TARGET_UNKNOWN, "PULSE_TEMPLATE_TARGET_UNKNOWN"},
	{perr.PULSE_TEMPLATE_VAR_MISSING, "PULSE_TEMPLATE_VAR_MISSING"},
	{perr.PULSE_TEMPLATE_VAR_UNKNOWN, "PULSE_TEMPLATE_VAR_UNKNOWN"},
	{perr.PULSE_TEMPLATE_VAR_TYPE, "PULSE_TEMPLATE_VAR_TYPE"},
	{perr.PULSE_TEMPLATE_VAR_ENUM, "PULSE_TEMPLATE_VAR_ENUM"},
	{perr.PULSE_TEMPLATE_UNRESOLVED, "PULSE_TEMPLATE_UNRESOLVED"},
	{perr.PULSE_TEMPLATE_RENDER_INVALID, "PULSE_TEMPLATE_RENDER_INVALID"},
}

// TestTemplateCodes_Registered asserts every member of the family is in
// allCodes, parses back from its string form, and sits in the PULSE
// domain.
func TestTemplateCodes_Registered(t *testing.T) {
	registered := make(map[perr.Code]struct{}, len(perr.AllCodes()))
	for _, c := range perr.AllCodes() {
		registered[c] = struct{}{}
	}

	for _, tt := range templateFamily {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.code) != tt.want {
				t.Errorf("code string = %q, want %q", string(tt.code), tt.want)
			}
			if _, ok := registered[tt.code]; !ok {
				t.Errorf("code %s missing from AllCodes(); add it to the allCodes slice", tt.code)
			}
			got, ok := perr.ParseCode(tt.want)
			if !ok {
				t.Fatalf("ParseCode(%q) returned not-found", tt.want)
			}
			if got != tt.code {
				t.Errorf("ParseCode(%q) = %s, want %s", tt.want, got, tt.code)
			}
			if d := perr.Domain(tt.code); d != "PULSE" {
				t.Errorf("Domain(%s) = %q, want \"PULSE\"", tt.code, d)
			}
		})
	}
}

// TestTemplateCodes_MetadataComplete asserts every member carries a
// Message and at least one actionable Fixup. No member may take the
// FixupNotApplicable shortcut — every template failure is caller-fixable
// by editing the template document or the supplied variables.
func TestTemplateCodes_MetadataComplete(t *testing.T) {
	for _, tt := range templateFamily {
		t.Run(tt.want, func(t *testing.T) {
			m, ok := perr.MetadataFor(tt.code)
			if !ok {
				t.Fatalf("code %s has no codeMetadata entry", tt.code)
			}
			if strings.TrimSpace(m.Message) == "" {
				t.Errorf("code %s has empty Message", tt.code)
			}
			if m.FixupNotApplicable {
				t.Errorf("code %s is tagged FixupNotApplicable; every template failure is caller-fixable", tt.code)
			}
			if len(m.Fixups) == 0 {
				t.Fatalf("code %s has no Fixups", tt.code)
			}
			for i, f := range m.Fixups {
				if strings.TrimSpace(f.Hint) == "" {
					t.Errorf("code %s fixup[%d] has empty Hint", tt.code, i)
				}
				if !validAction(f.Action) {
					t.Errorf("code %s fixup[%d] has unknown Action=%q", tt.code, i, f.Action)
				}
			}
		})
	}
}

// TestTemplateCodes_Lookup asserts the family is reachable through the
// depth-on-demand lookup surface that backs `pulse errors lookup CODE`
// and the pulse_errors_lookup MCP tool.
func TestTemplateCodes_Lookup(t *testing.T) {
	for _, tt := range templateFamily {
		t.Run(tt.want, func(t *testing.T) {
			res, ok := perr.Lookup(tt.want)
			if !ok {
				t.Fatalf("Lookup(%q) returned not-found", tt.want)
			}
			if res.Code != tt.want {
				t.Errorf("Lookup(%q).Code = %q, want %q", tt.want, res.Code, tt.want)
			}
			if res.Domain != "PULSE" {
				t.Errorf("Lookup(%q).Domain = %q, want \"PULSE\"", tt.want, res.Domain)
			}
			if strings.TrimSpace(res.Message) == "" {
				t.Errorf("Lookup(%q).Message is empty", tt.want)
			}
			if len(res.Fixups) == 0 {
				t.Errorf("Lookup(%q).Fixups is empty", tt.want)
			}
			if res.FixupNotApplicable {
				t.Errorf("Lookup(%q).FixupNotApplicable = true, want false", tt.want)
			}
		})
	}
}

// TestTemplateCodes_ByDomainIncludesFamily asserts the family surfaces
// in the PULSE domain listing (the `pulse errors list --domain PULSE`
// path), not only in single-code lookups.
func TestTemplateCodes_ByDomainIncludesFamily(t *testing.T) {
	seen := make(map[string]struct{})
	for _, r := range perr.ByDomain("PULSE") {
		seen[r.Code] = struct{}{}
	}
	for _, tt := range templateFamily {
		if _, ok := seen[tt.want]; !ok {
			t.Errorf("ByDomain(\"PULSE\") missing %s", tt.want)
		}
	}
}

// TestTemplateCodes_VarMissingProse spot-checks the canonical remedy
// prose for PULSE_TEMPLATE_VAR_MISSING: the hint must name both escape
// hatches — supply a value, or declare a default.
func TestTemplateCodes_VarMissingProse(t *testing.T) {
	res, ok := perr.Lookup("PULSE_TEMPLATE_VAR_MISSING")
	if !ok {
		t.Fatal("PULSE_TEMPLATE_VAR_MISSING not found")
	}
	if !strings.Contains(strings.ToLower(res.Message), "required") {
		t.Errorf("Message %q does not mention the required variable", res.Message)
	}
	joined := strings.ToLower(strings.Join(hints(res.Fixups), " "))
	for _, want := range []string{"default", "variables"} {
		if !strings.Contains(joined, want) {
			t.Errorf("fixup hints %q do not mention %q", joined, want)
		}
	}
}

// hints projects the Hint field out of a fixup slice.
func hints(fs []perr.Fixup) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Hint)
	}
	return out
}

// TestTemplateDetailKeys pins the Details map key names the whole
// PULSE_TEMPLATE_* family uses. Downstream stories attach context with
// these constants; renaming one is a wire-shape change.
func TestTemplateDetailKeys(t *testing.T) {
	if perr.DetailTemplate != "template" {
		t.Errorf("DetailTemplate = %q, want \"template\"", perr.DetailTemplate)
	}
	if perr.DetailVariable != "variable" {
		t.Errorf("DetailVariable = %q, want \"variable\"", perr.DetailVariable)
	}

	ce := perr.NewCodedErrorWithDetails(
		perr.PULSE_TEMPLATE_VAR_MISSING,
		"required variable has no value",
		map[string]any{
			perr.DetailTemplate: "quarterly-revenue",
			perr.DetailVariable: "quarter",
		},
	)
	if got := ce.Details[perr.DetailTemplate]; got != "quarterly-revenue" {
		t.Errorf("Details[%q] = %v, want \"quarterly-revenue\"", perr.DetailTemplate, got)
	}
	if got := ce.Details[perr.DetailVariable]; got != "quarter" {
		t.Errorf("Details[%q] = %v, want \"quarter\"", perr.DetailVariable, got)
	}
	if !perr.HasCode(ce, perr.PULSE_TEMPLATE_VAR_MISSING) {
		t.Error("HasCode did not match PULSE_TEMPLATE_VAR_MISSING")
	}
}
