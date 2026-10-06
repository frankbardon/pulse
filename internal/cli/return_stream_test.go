package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
)

// legacyStreamLines is what `--stream` wrote before response shaping:
// each buffered row through encodeFinite (compose: {"index","row"}).
func legacyStreamLines(t *testing.T, path string, compose bool) []string {
	t.Helper()
	p, err := newPulseOpts(pulse.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if compose {
		req, err := loadComposedRequest(path)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := p.Compose(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		for i, sub := range resp.Responses {
			for _, row := range sub.Data {
				if err := encodeFinite(enc, map[string]any{"index": i, "row": row}); err != nil {
					t.Fatal(err)
				}
			}
		}
	} else {
		req, err := loadRequest(path)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := p.Process(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range resp.Data {
			if err := encodeFinite(enc, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	return strings.Split(strings.TrimSpace(buf.String()), "\n")
}

// TestAPI_StreamHonoursReturn: `pulse api process --stream` and
// `pulse api compose --stream` write NDJSON rows shaped by the request's
// `return` — only the selected columns, measures at the block's
// precision — while a request without `return` streams exactly the rows
// it streamed before (MarshalFinite per row).
func TestAPI_StreamHonoursReturn(t *testing.T) {
	dir := withTempDataDir(t)
	csv := "cat,n\n"
	for i := range 21 {
		csv += fmt.Sprintf("%s,%d.137\n", []string{"a", "b", "c"}[i%3], i)
	}
	cohort := seedCohortViaImport(t, dir, "s.csv", csv)
	write := func(name string, v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	req := func(ret any) map[string]any {
		r := map[string]any{
			"cohort":       map[string]any{"filename": cohort},
			"aggregations": []any{map[string]any{"type": "AGG_AVERAGE", "field": "n", "label": "avg"}},
			"groups":       []any{map[string]any{"type": "GROUP_CATEGORY", "field": "cat"}},
		}
		if ret != nil {
			r["return"] = ret
		}
		return r
	}
	shapedRet := map[string]any{"include": []any{"data[*].avg"}, "precision": 2}
	run := func(args ...string) []string {
		t.Helper()
		var buf bytes.Buffer
		root := APICommand()
		root.Writer = &buf
		if err := root.Run(context.Background(), args); err != nil {
			t.Fatalf("run %v: %v", args, err)
		}
		return strings.Split(strings.TrimSpace(buf.String()), "\n")
	}
	// rowOf extracts the row object of one NDJSON line (compose lines
	// wrap it as {"index","row"}).
	rowOf := func(line string, compose bool) map[string]json.RawMessage {
		t.Helper()
		var row map[string]json.RawMessage
		if compose {
			var ev struct {
				Index int                        `json:"index"`
				Row   map[string]json.RawMessage `json:"row"`
			}
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				t.Fatalf("line %s: %v", line, err)
			}
			row = ev.Row
		} else if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("line %s: %v", line, err)
		}
		return row
	}
	for _, compose := range []bool{false, true} {
		name := "process"
		mk := func(ret any) string { return write(name+".json", req(ret)) }
		args := func(path string) []string { return []string{"api", "process", "--stream", "-r", path} }
		if compose {
			name = "compose"
			mk = func(ret any) string {
				return write(name+".json", map[string]any{"requests": []any{req(ret)}})
			}
			args = func(path string) []string { return []string{"api", "compose", "--stream", "-r", path} }
		}
		t.Run(name, func(t *testing.T) {
			plainPath := mk(nil)
			plain := run(args(plainPath)...)
			if len(plain) != 3 {
				t.Fatalf("plain stream = %d lines: %v", len(plain), plain)
			}
			// The pre-shaping writer, verbatim: encodeFinite per row.
			want := legacyStreamLines(t, plainPath, compose)
			if strings.Join(plain, "\n") != strings.Join(want, "\n") {
				t.Errorf("unshaped stream drifted:\n got %v\nwant %v", plain, want)
			}
			for _, line := range plain {
				row := rowOf(line, compose)
				if _, ok := row["cat"]; !ok || len(row["avg"]) <= 4 {
					t.Errorf("unshaped row lost a column or digits: %s", line)
				}
			}
			shaped := run(args(mk(shapedRet))...)
			if len(shaped) != len(plain) {
				t.Fatalf("shaped stream = %d lines, want %d", len(shaped), len(plain))
			}
			for _, line := range shaped {
				row := rowOf(line, compose)
				if len(row) != 1 {
					t.Errorf("shaped row kept unselected columns: %s", line)
				}
				avg := strings.TrimLeft(strings.ReplaceAll(strings.Split(string(row["avg"]), "e")[0], ".", ""), "0-")
				if len(avg) == 0 || len(avg) > 2 {
					t.Errorf("shaped avg %s not at precision 2: %s", row["avg"], line)
				}
			}
		})
	}
}
