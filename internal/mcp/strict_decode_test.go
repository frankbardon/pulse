package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// TestHandleProcess_UnknownKeyRejected proves the adapter wiring: a structured
// request whose top-level key is misnamed ("groupers") is rejected by the core
// strict-decode before execution, and the go-sdk handler renders the coded
// error verbatim as a {code, message} envelope (not a flattened string).
//
// Under the canonical structured contract the request fields ride at the top
// level of the tool arguments — there is no {request: <json-string>} wrapper.
func TestHandleProcess_UnknownKeyRejected(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	srv := NewWithOptions(p, Options{BindOnOpen: false})
	d, ok := coreDescriptor(ToolProcess)
	if !ok {
		t.Fatal("process descriptor missing from core catalog")
	}
	handler := coreHandler(srv, p, false, d)

	args := map[string]any{
		"cohort":   map[string]any{"filename": "demo.pulse"},
		"groupers": []map[string]any{{"type": "GROUP_CATEGORY", "field": "region"}},
	}
	out, err := handler(context.Background(), toolCall(t, ToolProcess, args))
	if err != nil {
		t.Fatalf("handler returned transport error: %v", err)
	}
	if !out.IsError {
		t.Fatal("expected IsError result for misnamed key, got success")
	}
	text, ok := out.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("expected *TextContent, got %T", out.Content[0])
	}
	var decoded struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil {
		t.Fatalf("error result is not a CodedError JSON: %v\n%s", err, text.Text)
	}
	if decoded.Code != string(perr.PULSE_REQUEST_UNKNOWN_FIELD) {
		t.Errorf("code = %q, want PULSE_REQUEST_UNKNOWN_FIELD", decoded.Code)
	}
	if !strings.Contains(decoded.Message, `"groups"`) {
		t.Errorf("message should name the correct slot 'groups': %s", decoded.Message)
	}
}
