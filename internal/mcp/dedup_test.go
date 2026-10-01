package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/spf13/afero"
)

// invokeDedup calls pulse_dedup through the catalog's Invoke and returns
// the wire JSON an agent reads.
func invokeDedup(t *testing.T, p *pulse.Pulse, args string) (map[string]any, error) {
	t.Helper()
	for _, td := range Tools(Config{}) {
		if td.Name != toolmeta.ToolDedup {
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
	t.Fatal("pulse_dedup not in the catalog")
	return nil, nil
}

// flatManagedCohort imports the synthetic orders⋈customers CSV flat and
// returns its managed .pulse path.
func flatManagedCohort(t *testing.T) (*pulse.Pulse, afero.Fs, string) {
	t.Helper()
	p, afs := newGroupImportPulse(t, 400, 20)
	out, err := invokeImport(t, p, `{"source":"orders.csv"}`)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	path, _ := out["path"].(string)
	if path == "" {
		t.Fatalf("import result has no path: %v", out)
	}
	return p, afs, path
}

// TestPulseDedup_SuggestThenDeclare: the agent loop — suggest_groups
// alone is read-only and returns a candidate whose key/members pass
// straight back as groups; declaring it with out writes a grouped
// cohort beside the untouched original.
func TestPulseDedup_SuggestThenDeclare(t *testing.T) {
	p, afs, path := flatManagedCohort(t)
	before, _ := afero.ReadFile(afs, path)

	out, err := invokeDedup(t, p, `{"path":"`+path+`","suggest_groups":true}`)
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if out["rewritten"] != false {
		t.Fatalf("suggest-only rewrote: %v", out)
	}
	cands, _ := out["group_candidates"].(map[string]any)["candidates"].([]any)
	var decl map[string]any
	for _, c := range cands {
		if cm := c.(map[string]any); cm["suggested"] == true {
			decl = map[string]any{"key": cm["key"], "members": cm["members"]}
			break
		}
	}
	if decl == nil {
		t.Fatalf("no suggested candidate in %v", out["group_candidates"])
	}
	if now, _ := afero.ReadFile(afs, path); !bytes.Equal(now, before) {
		t.Fatal("suggest-only changed the cohort")
	}

	args, _ := json.Marshal(map[string]any{"path": path, "groups": []any{decl}, "out": "grouped.pulse"})
	out, err = invokeDedup(t, p, string(args))
	if err != nil {
		t.Fatalf("declare: %v", err)
	}
	if out["rewritten"] != true || out["format_version_after"] != float64(2) || out["in_place"] != false {
		t.Fatalf("declare result = %v", out)
	}
	if now, _ := afero.ReadFile(afs, path); !bytes.Equal(now, before) {
		t.Fatal("out dedup changed the original")
	}
	if ok, _ := afero.Exists(afs, "grouped.pulse"); !ok {
		t.Fatal("out cohort not written")
	}
}

// TestPulseDedup_StrictGroupsShape: a misspelled key inside a groups
// entry is PULSE_GROUP_DECLARATION_INVALID, never a keyless tuple group;
// a missing path is refused.
func TestPulseDedup_StrictGroupsShape(t *testing.T) {
	p, _, path := flatManagedCohort(t)
	_, err := invokeDedup(t, p, `{"path":"`+path+`","groups":[{"keys":["cust_id"],"members":["cust_lat","cust_lon"]}]}`)
	if !perr.HasCode(err, perr.PULSE_GROUP_DECLARATION_INVALID) || !strings.Contains(err.Error(), "pulse_dedup") {
		t.Fatalf("err = %v, want PULSE_GROUP_DECLARATION_INVALID naming pulse_dedup", err)
	}
	if _, err := invokeDedup(t, p, `{}`); err == nil {
		t.Fatal("missing path accepted")
	}
}

// TestPulseDedup_OutputSchemaInlinesReport: the reflected output schema
// carries the report's fields at the top level (the embedded
// pio.DedupReport is inlined, as its JSON is).
func TestPulseDedup_OutputSchemaInlinesReport(t *testing.T) {
	ts, ok := SchemaFor(toolmeta.ToolDedup)
	if !ok {
		t.Fatal("no schema for pulse_dedup")
	}
	for _, k := range []string{`"records"`, `"rewritten"`, `"invalidated_sidecars"`, `"group_candidates"`} {
		if !strings.Contains(string(ts.OutputSchema), k) {
			t.Errorf("output schema lacks %s", k)
		}
	}
	if strings.Contains(string(ts.InputSchema), "elide") {
		t.Error("pulse_dedup input exposes constant elision; it is CLI + library only")
	}
}
