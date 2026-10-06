package gosdk_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// TestProcessTool_MatrixUpperAndTopPairs: over MCP pulse_process, both
// matrix operators honour encoding "upper" (row r of primary.values has
// p − r cells) and MAT_CORRELATION's params.summary.top_pairs arrives
// as vectors.top_pairs [{row, col, r, n}] whose r is the upper cell.
func TestProcessTool_MatrixUpperAndTopPairs(t *testing.T) {
	fs := afero.NewMemMapFs()
	body := "a,b,c\n"
	for i := 0; i < 30; i++ {
		a := float64(i%11) + 0.25*float64(i%4)
		body += strconv.FormatFloat(a, 'f', -1, 64) + "," +
			strconv.FormatFloat(5-a+float64(i%3), 'f', -1, 64) + "," +
			strconv.Itoa((i*7)%9) + "\n"
	}
	if err := afero.WriteFile(fs, "m.csv", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p := newPulse(t, fs)
	imp, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "m.csv"})
	if err != nil {
		t.Fatalf("ImportFile: %v", err)
	}
	srv := newServer()
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "9.9.9", DisableCohortScan: true}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, cancel := connect(t, srv)
	defer cancel()

	res, err := c.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "pulse_process", Arguments: map[string]any{
		"cohort":  map[string]any{"filename": imp.Path},
		"vectors": []any{map[string]any{"name": "v", "fields": []string{"a", "b", "c"}}},
		"matrices": []any{
			map[string]any{"name": "cov", "type": "MAT_COVARIANCE", "vector": "v", "encoding": "upper"},
			map[string]any{"name": "cor", "type": "MAT_CORRELATION", "vector": "v", "encoding": "upper",
				"params": map[string]any{"summary": map[string]any{"top_pairs": 2}}},
		},
	}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	text := res.Content[0].(*mcpsdk.TextContent).Text
	if res.IsError {
		t.Fatalf("tool error: %s", text)
	}
	var out struct {
		Matrices []struct {
			Name    string `json:"name"`
			Primary struct {
				Encoding string       `json:"encoding"`
				Values   [][]*float64 `json:"values"`
			} `json:"primary"`
			Vectors map[string][]struct {
				Row string  `json:"row"`
				Col string  `json:"col"`
				R   float64 `json:"r"`
				N   int     `json:"n"`
			} `json:"vectors"`
		} `json:"matrices"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if len(out.Matrices) != 2 {
		t.Fatalf("%d matrices, want 2:\n%s", len(out.Matrices), text)
	}
	for _, m := range out.Matrices {
		if m.Primary.Encoding != "upper" || len(m.Primary.Values) != 3 {
			t.Fatalf("%s: encoding %s, %d rows", m.Name, m.Primary.Encoding, len(m.Primary.Values))
		}
		for r, row := range m.Primary.Values {
			if len(row) != 3-r {
				t.Errorf("%s: upper row %d has %d cells, want %d", m.Name, r, len(row), 3-r)
			}
		}
	}
	if out.Matrices[0].Vectors != nil {
		t.Errorf("covariance carries vectors %v", out.Matrices[0].Vectors)
	}
	pairs := out.Matrices[1].Vectors["top_pairs"]
	if len(pairs) != 2 {
		t.Fatalf("top_pairs = %+v, want 2", pairs)
	}
	idx := map[string]int{"a": 0, "b": 1, "c": 2}
	for _, pr := range pairs {
		r, col := idx[pr.Row], idx[pr.Col]
		if cell := out.Matrices[1].Primary.Values[r][col-r]; r >= col || cell == nil || *cell != pr.R || pr.N != 30 {
			t.Errorf("pair %+v does not match its upper cell %v (n 30)", pr, cell)
		}
	}
}
