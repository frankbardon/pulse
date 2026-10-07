package gosdk_test

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/mcp/gosdk"
	"github.com/frankbardon/pulse/types"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// TestRegister_DefaultReturn: Config.DefaultReturn reaches the mounted
// tools — the zero value shapes pulse_process by `standard`, `full`
// serves the unshaped output — and an unknown preset fails Register
// with PULSE_RETURN_INVALID.
func TestRegister_DefaultReturn(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "d.csv", []byte("name,amount\nA,1\nB,2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := newPulse(t, fs)
	imp, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "d.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	for _, tc := range []struct {
		preset       types.ReturnPreset
		wantStandard bool
	}{
		{"", true},
		{types.ReturnPresetFull, false},
	} {
		srv := newServer()
		if err := gosdk.Register(srv, p, gosdk.Config{DisableCohortScan: true, DefaultReturn: tc.preset}); err != nil {
			t.Fatalf("Register(%q): %v", tc.preset, err)
		}
		c, cancel := connect(t, srv)
		res, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "pulse_process", Arguments: map[string]any{
			"cohort":       map[string]any{"filename": imp.Path},
			"aggregations": []any{map[string]any{"type": "AGG_SUM", "field": "amount"}},
		}})
		cancel()
		if err != nil || res.IsError {
			t.Fatalf("CallTool(%q): %v %+v", tc.preset, err, res)
		}
		text := res.Content[0].(*mcpsdk.TextContent).Text
		standard := strings.Contains(text, `"preset":"standard"`) && !strings.Contains(text, `"components"`)
		if standard != tc.wantStandard {
			t.Errorf("DefaultReturn %q: standard-shaped = %v, want %v: %s", tc.preset, standard, tc.wantStandard, text)
		}
	}

	err = gosdk.Register(newServer(), p, gosdk.Config{DefaultReturn: "lean"})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_RETURN_INVALID {
		t.Errorf("Register(lean) = %v, want PULSE_RETURN_INVALID", err)
	}
}
