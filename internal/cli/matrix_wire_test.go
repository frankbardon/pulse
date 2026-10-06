package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestAPIProcess_MatrixUpperAndTopPairs: through `pulse api process
// --json`, both matrix operators honour encoding "upper" (row r of
// primary.values carries p − r cells, the same cells as the full
// layout's upper triangle, and auxiliary.n follows it), and
// MAT_CORRELATION's params.summary.top_pairs reaches the wire as
// vectors.top_pairs [{row, col, r, n}] ranked by |r|.
func TestAPIProcess_MatrixUpperAndTopPairs(t *testing.T) {
	dir := withTempDataDir(t)
	csv := "a,b,c,d\n"
	for i := 0; i < 40; i++ {
		a := float64(i%13) + 0.5*float64(i%4)
		csv += strconv.FormatFloat(a, 'f', -1, 64) + "," +
			strconv.FormatFloat(3-a+float64(i%5), 'f', -1, 64) + "," +
			strconv.Itoa((i*7)%11) + "," +
			strconv.FormatFloat(2*a+float64(i%3), 'f', -1, 64) + "\n"
	}
	cohort := seedCohortViaImport(t, dir, "m.csv", csv)

	type wireValues struct {
		Encoding string       `json:"encoding"`
		Values   [][]*float64 `json:"values"`
	}
	type wirePair struct {
		Row string  `json:"row"`
		Col string  `json:"col"`
		R   float64 `json:"r"`
		N   int     `json:"n"`
	}
	type wireMatrix struct {
		Name      string                `json:"name"`
		Primary   wireValues            `json:"primary"`
		Auxiliary map[string]wireValues `json:"auxiliary"`
		Vectors   map[string][]wirePair `json:"vectors"`
	}
	run := func(t *testing.T, matrices []any) []wireMatrix {
		t.Helper()
		b, err := json.Marshal(map[string]any{
			"cohort":   map[string]any{"filename": cohort},
			"vectors":  []any{map[string]any{"name": "v", "fields": []string{"a", "b", "c", "d"}}},
			"matrices": matrices,
		})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "req.json")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		root := APICommand()
		root.Writer = &buf
		if err := root.Run(context.Background(), []string{"api", "process", "--json", "-r", path}); err != nil {
			t.Fatalf("run: %v\n%s", err, buf.String())
		}
		var env struct {
			Data struct {
				Matrices []wireMatrix `json:"matrices"`
			} `json:"data"`
			Errors []any `json:"errors"`
		}
		if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
			t.Fatalf("decode envelope: %v\n%s", err, buf.String())
		}
		if len(env.Errors) != 0 || len(env.Data.Matrices) != len(matrices) {
			t.Fatalf("errors %v, %d matrices, want %d:\n%s", env.Errors, len(env.Data.Matrices), len(matrices), buf.String())
		}
		return env.Data.Matrices
	}

	for _, typ := range []string{"MAT_COVARIANCE", "MAT_CORRELATION"} {
		t.Run(typ, func(t *testing.T) {
			pairwise := map[string]any{"missing": "pairwise"}
			got := run(t, []any{
				map[string]any{"name": "full", "type": typ, "vector": "v", "params": pairwise},
				map[string]any{"name": "upper", "type": typ, "vector": "v", "params": pairwise, "encoding": "upper"},
			})
			full, upper := got[0], got[1]
			if full.Primary.Encoding != "full" || upper.Primary.Encoding != "upper" || upper.Auxiliary["n"].Encoding != "upper" {
				t.Fatalf("encodings full=%s upper=%s upper aux=%s", full.Primary.Encoding, upper.Primary.Encoding, upper.Auxiliary["n"].Encoding)
			}
			for _, part := range []struct {
				name     string
				full, up wireValues
			}{
				{"primary", full.Primary, upper.Primary},
				{"auxiliary.n", full.Auxiliary["n"], upper.Auxiliary["n"]},
			} {
				p := len(part.full.Values)
				if p != 4 || len(part.up.Values) != p {
					t.Fatalf("%s: %d full rows, %d upper rows, want 4", part.name, p, len(part.up.Values))
				}
				for r := 0; r < p; r++ {
					if len(part.up.Values[r]) != p-r {
						t.Fatalf("%s: upper row %d has %d cells, want %d", part.name, r, len(part.up.Values[r]), p-r)
					}
					for k, v := range part.up.Values[r] {
						if f := part.full.Values[r][r+k]; (v == nil) != (f == nil) || (v != nil && *v != *f) {
							t.Errorf("%s: upper[%d][%d] != full[%d][%d]", part.name, r, k, r, r+k)
						}
					}
				}
			}
		})
	}

	t.Run("top_pairs", func(t *testing.T) {
		got := run(t, []any{map[string]any{"type": "MAT_CORRELATION", "vector": "v", "encoding": "upper",
			"params": map[string]any{"summary": map[string]any{"top_pairs": 4}}}})[0]
		pairs := got.Vectors["top_pairs"]
		if len(pairs) != 4 {
			t.Fatalf("top_pairs = %+v, want 4 pairs", pairs)
		}
		idx := map[string]int{"a": 0, "b": 1, "c": 2, "d": 3}
		for i, pr := range pairs {
			r, c := idx[pr.Row], idx[pr.Col]
			if r >= c || pr.N != 40 {
				t.Errorf("pair %d = %+v, want row before col and listwise n 40", i, pr)
			}
			if cell := got.Primary.Values[r][c-r]; cell == nil || *cell != pr.R {
				t.Errorf("pair %d r = %v, upper cell %v", i, pr.R, cell)
			}
			if i > 0 && matrixAbs(pr.R) > matrixAbs(pairs[i-1].R) {
				t.Errorf("pair %d |r| %v ranks above pair %d |r| %v", i, matrixAbs(pr.R), i-1, matrixAbs(pairs[i-1].R))
			}
		}
	})
}

func matrixAbs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
