package mcp

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// TestHandlers_RefusalsAreCoded: the thin tools answer every refusal with
// a registered code (the adapter renders it as {code, message, details}),
// never plain text: pulse_inspect keeps the envelope's own code for a
// cohort it cannot read; an unknown skill or example, an absent required
// argument and an axis-less pulse_errors_lookup are SERVICE_VALIDATION
// naming the argument in details.
func TestHandlers_RefusalsAreCoded(t *testing.T) {
	t.Parallel()
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "bad.pulse", []byte("NOTAPULSEFILE"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cases := []struct {
		name     string
		call     func() error
		code     perr.Code
		argument string
	}{
		{"inspect bad header", func() error { _, err := HandleInspect(ctx, p, InspectIn{Path: "bad.pulse"}); return err }, perr.ENCODING_INVALID, ""},
		{"inspect missing", func() error { _, err := HandleInspect(ctx, p, InspectIn{Path: "nope.pulse"}); return err }, perr.SERVICE_RESOURCE, ""},
		{"inspect no path", func() error { _, err := HandleInspect(ctx, p, InspectIn{}); return err }, perr.SERVICE_VALIDATION, "path"},
		{"skill unknown", func() error { _, err := HandleSkillsGet(ctx, p, SkillsGetIn{Name: "no-such-skill"}); return err }, perr.SERVICE_VALIDATION, "name"},
		{"example unknown", func() error { _, err := HandleExamplesGet(ctx, p, ExamplesGetIn{Name: "no-such-example"}); return err }, perr.SERVICE_VALIDATION, "name"},
		{"errors lookup no axis", func() error { _, err := HandleErrorsLookup(ctx, p, ErrorsLookupIn{}); return err }, perr.SERVICE_VALIDATION, ""},
	}
	for _, c := range cases {
		err := c.call()
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) {
			t.Errorf("%s: err = %v, want a coded error", c.name, err)
			continue
		}
		if ce.Code != c.code {
			t.Errorf("%s: code = %s, want %s (%v)", c.name, ce.Code, c.code, err)
		}
		if c.argument != "" && ce.Details["argument"] != c.argument {
			t.Errorf("%s: details = %v, want argument %q", c.name, ce.Details, c.argument)
		}
	}
}
