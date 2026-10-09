package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// invokeRecommend calls pulse_recommend through the catalog's Invoke and
// returns the wire JSON an agent reads.
func invokeRecommend(t *testing.T, p *pulse.Pulse, args string) (map[string]any, error) {
	t.Helper()
	for _, td := range Tools(Config{}) {
		if td.Name != toolmeta.ToolRecommend {
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
	t.Fatal("pulse_recommend not in the catalog")
	return nil, nil
}

// TestPulseRecommend: the tool is pulse.Recommend over the wire —
// unbound skeletons without cohort, drafts bound to the cohort path's
// fields with it (fields / level / limit forwarded), and every refusal
// a coded error with its own code.
func TestPulseRecommend(t *testing.T) {
	p, _, path := flatManagedCohort(t)

	out, err := invokeRecommend(t, p, `{"intent":"compare_groups"}`)
	if err != nil {
		t.Fatalf("unbound: %v", err)
	}
	recs, _ := out["recommendations"].([]any)
	if out["bound"] != false || len(recs) == 0 {
		t.Fatalf("unbound result = %v", out)
	}

	out, err = invokeRecommend(t, p, `{"intent":"describe","cohort":"`+path+`","fields":["amount"],"level":"basic","limit":2}`)
	if err != nil {
		t.Fatalf("bound: %v", err)
	}
	recs, _ = out["recommendations"].([]any)
	if out["bound"] != true || len(recs) == 0 || len(recs) > 2 || out["truncated"] != true {
		t.Fatalf("bound result = %v", out)
	}
	top := recs[0].(map[string]any)
	if req, _ := json.Marshal(top["request"]); !strings.Contains(string(req), `"amount"`) || top["level"] != "basic" {
		t.Errorf("top draft = %v; want the hinted field at level basic", top)
	}

	for args, want := range map[string]perr.Code{
		`{}`:                                     perr.PULSE_RECOMMEND_INTENT_UNKNOWN,
		`{"intent":"nope"}`:                      perr.PULSE_RECOMMEND_INTENT_UNKNOWN,
		`{"intent":"describe","level":"expert"}`: perr.SERVICE_VALIDATION,
		`{"intent":"describe","cohort":"no.pulse"}`:                       perr.DATA_FILE,
		`{"intent":"describe","cohort":"` + path + `","fields":["nope"]}`: perr.SERVICE_VALIDATION,
	} {
		if _, err := invokeRecommend(t, p, args); !perr.HasCode(err, want) {
			t.Errorf("%s: err = %v, want %s", args, err, want)
		}
	}
}

// TestPulseRecommend_InputSchema: the input contract names exactly the
// recommend request's slots, with cohort a path string.
func TestPulseRecommend_InputSchema(t *testing.T) {
	ts, ok := SchemaFor(toolmeta.ToolRecommend)
	if !ok {
		t.Fatal("no schema for pulse_recommend")
	}
	var s struct {
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(ts.InputSchema, &s); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"intent", "cohort", "fields", "level", "limit"} {
		if _, ok := s.Properties[k]; !ok {
			t.Errorf("input schema lacks %q", k)
		}
	}
	if len(s.Properties) != 5 {
		t.Errorf("input schema properties = %v, want the five recommend slots", s.Properties)
	}
	if s.Properties["cohort"].Type != "string" {
		t.Errorf("cohort type = %v, want string (a path)", s.Properties["cohort"].Type)
	}
}
