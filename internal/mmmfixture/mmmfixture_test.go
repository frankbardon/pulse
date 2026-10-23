package mmmfixture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestHoldout_MMMFixtureShape pins the fixture's shape: one row per
// market-week in schema field order, and every channel window of both
// request variants reading the swept decay.
func TestHoldout_MMMFixtureShape(t *testing.T) {
	rows, fields := Rows(), len(Schema().Fields)
	if len(rows) != len(Markets)*Weeks {
		t.Fatalf("rows = %d, want %d", len(rows), len(Markets)*Weeks)
	}
	for i, r := range rows {
		if len(r) != fields {
			t.Fatalf("row %d has %d values, want %d", i, len(r), fields)
		}
	}
	for name, raw := range map[string][]byte{"week": DecaySweep, "split": DecaySweepSplit} {
		body, err := SlotBody(raw, "0.5")
		if err != nil {
			t.Fatalf("%s: SlotBody: %v", name, err)
		}
		if bytes.Contains(body, []byte("$var")) {
			t.Fatalf("%s: placeholder left in %s", name, body)
		}
		var req struct {
			Windows []struct {
				Params struct {
					Decay  json.Number `json:"decay"`
					MaxLag int         `json:"max_lag"`
					Normal *bool       `json:"normalize"`
					Kernel string      `json:"kernel"`
				} `json:"params"`
			} `json:"windows"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(req.Windows) != 4 {
			t.Fatalf("%s: %d windows, want one adstock per channel", name, len(req.Windows))
		}
		for _, w := range req.Windows {
			p := w.Params
			if p.Decay != "0.5" || p.MaxLag != 13 || p.Normal == nil || *p.Normal || p.Kernel != "geometric" {
				t.Fatalf("%s: window params %+v", name, p)
			}
		}
	}
	if _, err := SlotBody([]byte(`{"sweep":{"request":{"windows":[]}}}`), "0.5"); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("SlotBody without a placeholder: err = %v", err)
	}
}
