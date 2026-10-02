package template_test

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
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
// as an undeclared key — same message, details and cause text.
func TestRenderWith_WithheldKeyIsUnknownField(t *testing.T) {
	render := func(key string, opts template.RenderOptions) (string, string) {
		tmpl := targetTmpl(template.TargetRequest, nil,
			`{"cohort": {"filename": "c.pulse"}, "`+key+`": {}}`)
		_, err := template.RenderWith(tmpl, nil, opts)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TEMPLATE_RENDER_INVALID {
			t.Fatalf("err = %v, want PULSE_TEMPLATE_RENDER_INVALID", err)
		}
		wire, _ := json.Marshal(ce)
		return string(wire), stderrors.Unwrap(ce).Error()
	}
	// "crosstab_" is undeclared on types.Request; withholding the real
	// "crosstab" must reproduce its fault for that name.
	gotWire, gotCause := render("crosstab", withholdRequestKeys("crosstab"))
	wantWire, wantCause := render("crosstab_", template.RenderOptions{})
	strip := func(s string) string { return strings.ReplaceAll(s, "crosstab_", "crosstab") }
	if gotWire != strip(wantWire) {
		t.Errorf("wire\n got: %s\nwant: %s", gotWire, strip(wantWire))
	}
	if gotCause != strip(wantCause) {
		t.Errorf("cause = %q, want %q", gotCause, strip(wantCause))
	}
}
