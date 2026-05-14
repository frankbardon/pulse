package regression

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

var updateGolden = flag.Bool("update", false, "update regression golden files")

// TestRegOLS_GoldenRequest fits a small fixed dataset through the
// unpenalized OLS engine and round-trips the RegressionResult to JSON,
// comparing it against a golden artifact under
// processing/regression/testdata/. The golden file carries a
// // golden-hash: <sha256> footer so it can't be hand-edited without
// breaking the matching golden-hygiene check below.
//
// Regenerate with: go test ./processing/regression/ -run TestRegOLS_GoldenRequest -update
func TestRegOLS_GoldenRequest(t *testing.T) {
	predictors := []string{"x1", "x2"}
	// Deterministic non-degenerate fixture: a linear signal with a
	// small periodic disturbance so coefficients have informative std
	// errors instead of round zeros.
	records := make([]Record, 40)
	for i := range records {
		x1 := float64(i)
		x2 := float64(i%5) - 2.0
		y := 3.0 + 0.5*x1 - 1.5*x2 + 0.1*float64(i%3)
		records[i] = mockRecord{values: map[string]float64{
			"x1": x1, "x2": x2, "y": y,
		}}
	}
	spec := &types.RegressionSpec{
		Type:       types.REG_OLS,
		Name:       "golden_ols",
		Target:     "y",
		Predictors: predictors,
	}
	eng := newOLS(t, spec)
	res, err := eng.FitBuffered(records)
	if err != nil {
		t.Fatalf("FitBuffered: %v", err)
	}

	// json.MarshalIndent for human-readable diffs; key order is
	// deterministic via struct-field iteration. Map fields
	// (Coefficients / StdErrors / PValues) marshal with sorted keys
	// per encoding/json's documented contract.
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	goldenPath := filepath.Join("testdata", "ols_golden_request.json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, appendHash(data), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", goldenPath, err)
	}
	expectedData, _ := splitHash(expected)

	// Tolerance compare: floating-point output drifts by 1-2 ULP across
	// platforms (different LAPACK / SIMD orderings). Byte-exact match
	// would be brittle on CI. We still hash the golden file via
	// TestGoldensNotHandEdited to gate hand-edits.
	var got, want map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal(expectedData, &want); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	const floatRelTol = 1e-9
	if diff := diffWithFloatTolerance(got, want, floatRelTol); diff != "" {
		t.Errorf("golden mismatch for %s (tol=%g):\n%s\n\nGot:\n%s\n\nWant:\n%s",
			goldenPath, floatRelTol, diff, string(data), string(expectedData))
	}
}

// diffWithFloatTolerance walks two unmarshaled JSON values and returns
// the first difference path that exceeds the relative tolerance, or "" if
// they agree. Float leaves use relative tolerance against the larger of
// the two magnitudes (with a 1.0 floor); all other types must be deeply
// equal.
func diffWithFloatTolerance(got, want any, relTol float64) string {
	return diffPath(got, want, relTol, "")
}

func diffPath(got, want any, relTol float64, path string) string {
	if path == "" {
		path = "$"
	}
	switch wantVal := want.(type) {
	case map[string]any:
		gotMap, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: type mismatch (got %T, want map)", path, got)
		}
		keys := make([]string, 0, len(wantMapKeysUnion(gotMap, wantVal)))
		for k := range wantMapKeysUnion(gotMap, wantVal) {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			gv, gok := gotMap[k]
			wv, wok := wantVal[k]
			if !gok {
				return fmt.Sprintf("%s.%s: missing in got (want %v)", path, k, wv)
			}
			if !wok {
				return fmt.Sprintf("%s.%s: missing in want (got %v)", path, k, gv)
			}
			if d := diffPath(gv, wv, relTol, path+"."+k); d != "" {
				return d
			}
		}
	case []any:
		gotSlice, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("%s: type mismatch (got %T, want []any)", path, got)
		}
		if len(gotSlice) != len(wantVal) {
			return fmt.Sprintf("%s: length mismatch (got %d, want %d)", path, len(gotSlice), len(wantVal))
		}
		for i := range wantVal {
			if d := diffPath(gotSlice[i], wantVal[i], relTol, fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
	case float64:
		gotFloat, ok := got.(float64)
		if !ok {
			return fmt.Sprintf("%s: type mismatch (got %T, want float64)", path, got)
		}
		denom := math.Max(math.Max(math.Abs(gotFloat), math.Abs(wantVal)), 1.0)
		if math.Abs(gotFloat-wantVal)/denom > relTol {
			return fmt.Sprintf("%s: %g vs %g (rel diff %g > %g)",
				path, gotFloat, wantVal, math.Abs(gotFloat-wantVal)/denom, relTol)
		}
	default:
		if !reflect.DeepEqual(got, want) {
			return fmt.Sprintf("%s: %v vs %v", path, got, want)
		}
	}
	return ""
}

func wantMapKeysUnion(a, b map[string]any) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		out[k] = struct{}{}
	}
	for k := range b {
		out[k] = struct{}{}
	}
	return out
}

// TestGoldensNotHandEdited verifies every regression golden in
// processing/regression/testdata/ ends with a valid // golden-hash:
// footer and that the footer matches the file body. Mirrors the same
// check that runs over descriptor/testdata/.
func TestGoldensNotHandEdited(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("testdata directory does not exist")
		}
		t.Fatalf("reading testdata: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join("testdata", entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("reading %s: %v", path, err)
			continue
		}
		data, storedHash := splitHash(content)
		if storedHash == "" {
			t.Errorf("%s: no golden-hash found (file may have been hand-edited)", entry.Name())
			continue
		}
		computed := sha256.Sum256(data)
		if hex.EncodeToString(computed[:]) != storedHash {
			t.Errorf("%s: hash mismatch (file may have been hand-edited)", entry.Name())
		}
	}
}

func appendHash(data []byte) []byte {
	h := sha256.Sum256(data)
	hashLine := "\n// golden-hash: " + hex.EncodeToString(h[:]) + "\n"
	return append(data, []byte(hashLine)...)
}

func splitHash(content []byte) (data []byte, hash string) {
	s := string(content)
	idx := strings.LastIndex(s, "\n// golden-hash: ")
	if idx < 0 {
		return content, ""
	}
	hashPart := s[idx+len("\n// golden-hash: "):]
	hashPart = strings.TrimSpace(hashPart)
	return []byte(s[:idx]), hashPart
}
