package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

// writeRakeCSV writes a 10-row survey: 7 F / 3 M and 6 N / 4 S, so
// raking to 50/50 gender and 50/50 region must move every weight.
func writeRakeCSV(t *testing.T, dir string) string {
	t.Helper()
	body := "id,gender,region\n" +
		"1,F,N\n2,F,N\n3,F,N\n4,F,N\n5,F,S\n6,F,S\n7,F,S\n8,M,N\n9,M,N\n10,M,S\n"
	path := filepath.Join(dir, "survey.csv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// rakeEnvelope is the slice of a `pulse rake --json` envelope the tests
// read.
type rakeEnvelope struct {
	FormatVersion string `json:"format_version"`
	Data          struct {
		Target      string  `json:"target"`
		Records     int64   `json:"records"`
		WeightField string  `json:"weight_field"`
		Converged   bool    `json:"converged"`
		Iterations  int     `json:"iterations"`
		Residual    float64 `json:"residual"`
		Margins     []struct {
			Fields     []string `json:"fields"`
			Categories []struct {
				Values   []string `json:"values"`
				Target   float64  `json:"target"`
				Achieved float64  `json:"achieved"`
			} `json:"categories"`
		} `json:"margins"`
		Efficiency struct {
			NEff     float64 `json:"n_eff"`
			DeffKish float64 `json:"deff_kish"`
		} `json:"efficiency"`
	} `json:"data"`
	Errors []struct {
		Code string `json:"code"`
	} `json:"errors"`
	Warnings []struct {
		Code string `json:"code"`
	} `json:"warnings"`
}

func decodeRakeEnvelope(t *testing.T, out string) rakeEnvelope {
	t.Helper()
	var env rakeEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("not an envelope: %v\n%s", err, out)
	}
	return env
}

// weightedShares reads the written cohort back through the library and
// returns the weighted share of each value of field.
func weightedShares(t *testing.T, path, field, weight string) map[string]float64 {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: afero.NewOsFs()})
	if err != nil {
		t.Fatal(err)
	}
	c, err := p.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fi, wi := -1, -1
	for i, f := range r.Schema().Fields {
		switch f.Name {
		case field:
			fi = i
		case weight:
			wi = i
		}
	}
	if fi < 0 || wi < 0 {
		t.Fatalf("fields %q / %q not in %s", field, weight, path)
	}
	sums, total := map[string]float64{}, 0.0
	for i := int64(0); i < r.Len(); i++ {
		row, err := r.RecordAt(i)
		if err != nil {
			t.Fatal(err)
		}
		w := row[wi].(float64)
		sums[row[fi].(string)] += w
		total += w
	}
	for k := range sums {
		sums[k] /= total
	}
	return sums
}

// TestCliRake_WritesWeightsThatHitTheTargets is the route the leaf
// exists for: `pulse rake` with inline --margins writes a cohort whose
// weight column reproduces every target, the --json envelope carries
// the RakeResult, and the same request via --request (a literal, with a
// flag overriding its out) or a margins FILE gives the same report.
func TestCliRake_WritesWeightsThatHitTheTargets(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "survey.pulse")
	if text, err := runApp(t, "import", "csv", "--input", writeRakeCSV(t, dir), "--output", src); err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	margins := `[{"field":"gender","targets":{"F":0.5,"M":0.5}},{"field":"region","targets":{"N":50,"S":50}}]`
	out := filepath.Join(dir, "weighted.pulse")
	text, err := runApp(t, "rake", src, "-o", out, "--margins", margins, "--json")
	if err != nil {
		t.Fatalf("rake: %v\n%s", err, text)
	}
	env := decodeRakeEnvelope(t, text)
	if env.FormatVersion != "1.1" || len(env.Errors) != 0 {
		t.Fatalf("envelope = %+v", env)
	}
	if env.Data.Target != out || env.Data.Records != 10 || env.Data.WeightField != "rake_weight" || !env.Data.Converged {
		t.Fatalf("data = %+v", env.Data)
	}
	if len(env.Data.Margins) != 2 {
		t.Fatalf("margins = %+v", env.Data.Margins)
	}
	for _, m := range env.Data.Margins {
		for _, c := range m.Categories {
			if math.Abs(c.Achieved-0.5) > 1e-6 || math.Abs(c.Target-0.5) > 1e-12 {
				t.Errorf("margin %v %v: target %v achieved %v", m.Fields, c.Values, c.Target, c.Achieved)
			}
		}
	}
	if env.Data.Efficiency.DeffKish <= 1 || env.Data.Efficiency.NEff >= 10 {
		t.Errorf("efficiency = %+v, want deff > 1 and n_eff < 10 for unequal weights", env.Data.Efficiency)
	}
	for field, want := range map[string]map[string]float64{"gender": {"F": 0.5, "M": 0.5}, "region": {"N": 0.5, "S": 0.5}} {
		got := weightedShares(t, out, field, "rake_weight")
		for k, w := range want {
			if math.Abs(got[k]-w) > 1e-6 {
				t.Errorf("weighted %s share of %s = %v, want %v", field, k, got[k], w)
			}
		}
	}

	// --request literal: its out is overridden by -o; margins from a file.
	mfile := filepath.Join(dir, "margins.json")
	if err := os.WriteFile(mfile, []byte(margins), 0o644); err != nil {
		t.Fatal(err)
	}
	out2 := filepath.Join(dir, "weighted2.pulse")
	reqLit := `{"cohort":"` + src + `","out":"` + filepath.Join(dir, "ignored.pulse") + `","weight_field":"w"}`
	text, err = runApp(t, "rake", "--request", reqLit, "--margins", mfile, "-o", out2, "--json")
	if err != nil {
		t.Fatalf("rake --request: %v\n%s", err, text)
	}
	env2 := decodeRakeEnvelope(t, text)
	if env2.Data.Target != out2 || env2.Data.WeightField != "w" || env2.Data.Residual != env.Data.Residual {
		t.Fatalf("--request data = %+v (first run residual %v)", env2.Data, env.Data.Residual)
	}
	if _, err := os.Stat(filepath.Join(dir, "ignored.pulse")); !os.IsNotExist(err) {
		t.Errorf("-o did not override the request's out: %v", err)
	}

	// Text mode: a human report.
	out3 := filepath.Join(dir, "weighted3.pulse")
	text, err = runApp(t, "rake", src, "-o", out3, "--margins", margins)
	if err != nil {
		t.Fatalf("rake (text): %v\n%s", err, text)
	}
	for _, want := range []string{"Raked 10 record(s) to " + out3, "converged", "deff_kish", "Margin gender:"} {
		if !strings.Contains(text, want) {
			t.Errorf("text output lacks %q:\n%s", want, text)
		}
	}
}

// TestCliRake_RefusalsKeepTheirCode: every refusal is the envelope's
// error with its OWN code, exits 1 and writes nothing.
func TestCliRake_RefusalsKeepTheirCode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "survey.pulse")
	if text, err := runApp(t, "import", "csv", "--input", writeRakeCSV(t, dir), "--output", src); err != nil {
		t.Fatalf("import: %v\n%s", err, text)
	}
	out := filepath.Join(dir, "never.pulse")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"untargeted category", []string{src, "-o", out, "--margins", `[{"field":"gender","targets":{"F":1}}]`}, "PULSE_RAKE_UNTARGETED_CATEGORY"},
		{"unknown category", []string{src, "-o", out, "--margins", `[{"field":"gender","targets":{"F":0.5,"X":0.5}}]`}, "PULSE_RAKE_UNKNOWN_CATEGORY"},
		{"missing out", []string{src, "--margins", `[{"field":"gender","targets":{"F":0.5,"M":0.5}}]`}, "PULSE_RAKE_INVALID"},
		{"no margins", []string{src, "-o", out}, "PULSE_RAKE_INVALID"},
		{"unknown margin key", []string{src, "-o", out, "--margins", `[{"feild":"gender","targets":{"F":0.5,"M":0.5}}]`}, "PULSE_REQUEST_UNKNOWN_FIELD"},
		{"unknown request key", []string{"--request", `{"cohort":"` + src + `","bogus":1}`, "-o", out}, "PULSE_REQUEST_UNKNOWN_FIELD"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, stdout, _ := runExit(t, append([]string{"rake", "--json"}, c.args...)...)
			env := decodeRakeEnvelope(t, stdout)
			if status != 1 || len(env.Errors) == 0 || env.Errors[0].Code != c.want {
				t.Fatalf("exit %d, envelope %s; want exit 1 with %s", status, stdout, c.want)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("a refusal wrote %s", out)
			}
		})
	}
}
