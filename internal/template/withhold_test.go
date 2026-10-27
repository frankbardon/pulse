package template_test

import (
	stderrors "errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/template"
	"github.com/frankbardon/pulse/types"
)

// withholdRequestKeys withholds keys on *types.Request roots only.
func withholdRequestKeys(keys ...string) template.RenderOptions {
	return template.RenderOptions{WithheldSlots: func(root any) []string {
		if _, ok := root.(*types.Request); ok {
			return keys
		}
		return nil
	}}
}

// TestRenderWith_NoWithheldKeySetIsRender: when the body sets no
// withheld key, RenderWith is Render — same JSON bytes, same typed value.
func TestRenderWith_NoWithheldKeySetIsRender(t *testing.T) {
	t.Parallel()

	body := `{"requests": [{"cohort": {"filename": "c.pulse"}, "aggregations": [{"type": "AGG_COUNT", "field": "x"}]}]}`
	tmpl := targetTmpl(template.TargetComposed, nil, body)
	want, err := template.Render(tmpl, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got, err := template.RenderWith(tmpl, nil, withholdRequestKeys("crosstab"))
	if err != nil {
		t.Fatalf("RenderWith: %v", err)
	}
	if string(got.JSON) != string(want.JSON) {
		t.Errorf("JSON = %s, want %s", got.JSON, want.JSON)
	}
	if !reflect.DeepEqual(got.Composed, want.Composed) {
		t.Errorf("typed value differs")
	}
}

// TestRenderWith_WithheldKeyIsUnknownField: a withheld key fails exactly
// as an undeclared key — same message, details and cause text — on an
// engine that does not offer it: valid_keys never lists it and no
// suggestion names it.
func TestRenderWith_WithheldKeyIsUnknownField(t *testing.T) {
	t.Parallel()

	render := func(key string, opts template.RenderOptions) (*perr.CodedError, string) {
		tmpl := targetTmpl(template.TargetRequest, nil,
			`{"cohort": {"filename": "c.pulse"}, "`+key+`": {}}`)
		_, err := template.RenderWith(tmpl, nil, opts)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TEMPLATE_RENDER_INVALID {
			t.Fatalf("err = %v, want PULSE_TEMPLATE_RENDER_INVALID", err)
		}
		return ce, stderrors.Unwrap(ce).Error()
	}
	// "zz_unrelated" is undeclared on types.Request and near no key;
	// withholding the real "crosstab" must reproduce its fault for that
	// name, over the valid keys minus the withheld one.
	got, gotCause := render("crosstab", withholdRequestKeys("crosstab"))
	want, wantCause := render("zz_unrelated", template.RenderOptions{})
	strip := func(s string) string { return strings.ReplaceAll(s, "zz_unrelated", "crosstab") }
	if got.Message != strip(want.Message) {
		t.Errorf("message\n got: %s\nwant: %s", got.Message, strip(want.Message))
	}
	if gotCause != strip(wantCause) {
		t.Errorf("cause = %q, want %q", gotCause, strip(wantCause))
	}
	if got.Details["field"] != "crosstab" || got.Details["path"] != "crosstab" {
		t.Errorf("details = %v", got.Details)
	}
	if _, ok := got.Details["suggestion"]; ok {
		t.Errorf("a withheld key is suggested as itself: %v", got.Details)
	}
	wantValid := slices.DeleteFunc(slices.Clone(want.Details["valid_keys"].([]string)), func(k string) bool { return k == "crosstab" })
	if !slices.Equal(got.Details["valid_keys"].([]string), wantValid) {
		t.Errorf("valid_keys = %v, want %v", got.Details["valid_keys"], wantValid)
	}

	// A typo next to the withheld slot never suggests it.
	typo, _ := render("crosstabb", withholdRequestKeys("crosstab"))
	if _, ok := typo.Details["suggestion"]; ok {
		t.Errorf("typo suggests the withheld slot: %v", typo.Details)
	}
}
