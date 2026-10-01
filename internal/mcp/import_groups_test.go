package mcp

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/spf13/afero"
)

// newGroupImportPulse seeds a synthetic denormalized orders⋈customers CSV:
// each customer's two f64 coordinates repeat on every one of its orders.
func newGroupImportPulse(t *testing.T, rows, customers int) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	afs := afero.NewMemMapFs()
	var b strings.Builder
	b.WriteString("order_id,cust_id,cust_lat,cust_lon,amount\n")
	for i := 0; i < rows; i++ {
		c := i % customers
		fmt.Fprintf(&b, "%d,%d,%.6f,%.6f,%.2f\n", 1000+i, c+1, 10.123457+float64(c)*1.5, -70.654321-float64(c)*0.75, float64(i%37)+0.25)
	}
	if err := afero.WriteFile(afs, "orders.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, afs
}

// invokeImport calls pulse_import through the catalog's Invoke — the
// decode the transport uses — and returns the output marshalled as the
// wire JSON an agent reads.
func invokeImport(t *testing.T, p *pulse.Pulse, args string) (map[string]any, error) {
	t.Helper()
	for _, td := range Tools(Config{}) {
		if td.Name != toolmeta.ToolImport {
			continue
		}
		out, err := td.Invoke(context.Background(), p, json.RawMessage(args))
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m, nil
	}
	t.Fatalf("pulse_import not in the catalog")
	return nil, nil
}

// TestPulseImport_GroupsReachTheCohort: a structured groups entry is
// applied and the group report rides the result.
func TestPulseImport_GroupsReachTheCohort(t *testing.T) {
	p, _ := newGroupImportPulse(t, 200, 10)
	out, err := invokeImport(t, p, `{"source":"orders.csv","groups":[{"key":["cust_id"],"members":["cust_lat","cust_lon"]}]}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	groups, _ := out["groups"].([]any)
	if len(groups) != 1 {
		t.Fatalf("groups = %v, want one group report", out["groups"])
	}
	if v := groups[0].(map[string]any)["verdict"]; v != "admitted" {
		t.Errorf("verdict = %v, want admitted", v)
	}
	if _, ok := out["group_warnings"]; ok {
		t.Errorf("group_warnings present for a 20x group: %v", out["group_warnings"])
	}
}

// TestPulseImport_GroupWarningIsCoded: a gate finding surfaces as a coded
// {code, message, details} entry an agent can look up.
func TestPulseImport_GroupWarningIsCoded(t *testing.T) {
	p, _ := newGroupImportPulse(t, 20, 12)
	out, err := invokeImport(t, p, `{"source":"orders.csv","groups":[{"key":["cust_id"],"members":["cust_lat","cust_lon"]}]}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	warns, _ := out["group_warnings"].([]any)
	if len(warns) != 1 {
		t.Fatalf("group_warnings = %v, want one entry", out["group_warnings"])
	}
	w := warns[0].(map[string]any)
	if w["code"] != string(perr.PULSE_DEDUP_LOW_RATIO) || w["message"] == "" || w["details"] == nil {
		t.Errorf("warning = %v, want coded PULSE_DEDUP_LOW_RATIO with message and details", w)
	}
}

// TestPulseImport_GroupsStrictDecode: a misspelled key inside a group
// entry is refused, not decoded as a keyless tuple group.
func TestPulseImport_GroupsStrictDecode(t *testing.T) {
	p, afs := newGroupImportPulse(t, 200, 10)
	_, err := invokeImport(t, p, `{"source":"orders.csv","groups":[{"keys":["cust_id"],"members":["cust_lat","cust_lon"]}]}`)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_GROUP_DECLARATION_INVALID {
		t.Fatalf("err = %v, want PULSE_GROUP_DECLARATION_INVALID", err)
	}
	if ok, _ := afero.Exists(afs, "imports/orders.pulse"); ok {
		t.Errorf("a refused declaration still wrote a cohort")
	}
}

// TestPulseImport_SuggestGroups: suggest_groups returns candidates whose
// key/members are a ready-to-use groups entry.
func TestPulseImport_SuggestGroups(t *testing.T) {
	p, _ := newGroupImportPulse(t, 200, 10)
	out, err := invokeImport(t, p, `{"source":"orders.csv","suggest_groups":true}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	det, _ := out["group_candidates"].(map[string]any)
	if det == nil {
		t.Fatalf("group_candidates missing: %v", out)
	}
	cands, _ := det["candidates"].([]any)
	var entry map[string]any
	for _, c := range cands {
		cm := c.(map[string]any)
		if cm["suggested"] == true {
			entry = map[string]any{"key": cm["key"], "members": cm["members"]}
			break
		}
	}
	if entry == nil {
		t.Fatalf("no suggested candidate in %v", cands)
	}
	// Apply it back verbatim: the suggest→apply loop closes over MCP.
	body, _ := json.Marshal(map[string]any{"source": "orders.csv", "overwrite": true, "groups": []any{entry}})
	out, err = invokeImport(t, p, string(body))
	if err != nil {
		t.Fatalf("applying the suggestion: %v", err)
	}
	if groups, _ := out["groups"].([]any); len(groups) != 1 {
		t.Errorf("applied suggestion produced groups = %v", out["groups"])
	}
}
