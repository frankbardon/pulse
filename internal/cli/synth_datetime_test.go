package cli

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSynthCLI_DatetimeProfileRoundTrip: `profile create` captures a
// datetime as its own section, `synth from-profile` rebuilds it as a
// `datetime` drawn by uniform_datetime (the emitted spec says so) and the
// generated rows keep the source's UTC hour marginal.
func TestSynthCLI_DatetimeProfileRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(spec, []byte(`{"row_count": 3000, "fields": [
		{"name": "id", "type": "u32", "distribution": "monotonic_from", "params": {"start": 1}},
		{"name": "ts", "type": "datetime", "distribution": "uniform_datetime", "params": {
			"start": "2026-01-05", "end": "2026-02-01", "time_zone": "America/New_York",
			"hour_weights": [0,0,0,0,0,0,0,0,1,2,3,3,2,2,3,3,2,1,0,0,0,0,0,0]}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "src.pulse")
	var buf bytes.Buffer
	if err := runSynthCLI(t, &buf, "from-schema", "--spec", spec, "--output", src, "--seed", "4"); err != nil {
		t.Fatalf("from-schema: %v (%s)", err, buf.String())
	}
	profile := filepath.Join(dir, "profile.json")
	if err := runProfileCLI(t, &buf, "create", "--input", src, "--output", profile); err != nil {
		t.Fatalf("profile create: %v (%s)", err, buf.String())
	}
	raw, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Fields []struct {
			Name     string           `json:"name"`
			Numeric  *json.RawMessage `json:"numeric"`
			Datetime *struct {
				Start, End string
				Hours      [24]int `json:"hours"`
			} `json:"datetime"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	ts := doc.Fields[1]
	if ts.Datetime == nil || ts.Numeric != nil || ts.Datetime.Start != "2026-01-05" {
		t.Fatalf("profile ts section: %s", raw)
	}

	out := filepath.Join(dir, "out.pulse")
	emitted := filepath.Join(dir, "emitted.json")
	if err := runSynthCLI(t, &buf, "from-profile", "--profile", profile, "--source", src, "--output", out,
		"--rows", "3000", "--seed", "5", "--emit-spec", emitted); err != nil {
		t.Fatalf("from-profile: %v (%s)", err, buf.String())
	}
	rawSpec, err := os.ReadFile(emitted)
	if err != nil {
		t.Fatal(err)
	}
	var es struct {
		Fields []struct {
			Name, Type, Distribution string
		} `json:"fields"`
	}
	if err := json.Unmarshal(rawSpec, &es); err != nil {
		t.Fatal(err)
	}
	if f := es.Fields[1]; f.Type != "datetime" || f.Distribution != "uniform_datetime" {
		t.Fatalf("emitted ts = %+v, want a uniform_datetime datetime", f)
	}

	p, err := newPulse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c, err := p.Open(t.Context(), out)
	if err != nil {
		t.Fatal(err)
	}
	rd, err := c.Reader()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	var src24, gen24 [24]float64
	var nSrc, nGen float64
	for r := int64(0); r < rd.Len(); r++ {
		row, err := rd.RecordAt(r)
		if err != nil {
			t.Fatal(err)
		}
		sec, ok := row[1].(int64)
		if !ok {
			t.Fatalf("row %d ts %T, want int64 epoch seconds", r, row[1])
		}
		at := time.Unix(sec, 0)
		if row[2] == true {
			gen24[at.UTC().Hour()]++
			nGen++
		} else {
			src24[at.UTC().Hour()]++
			nSrc++
		}
	}
	if nSrc != 3000 || nGen != 3000 {
		t.Fatalf("source %v / generated %v rows", nSrc, nGen)
	}
	for h := range src24 {
		if math.Abs(src24[h]/nSrc-gen24[h]/nGen) > 0.025 || (src24[h] == 0) != (gen24[h] == 0) {
			t.Errorf("UTC hour %02d: generated share %.4f, source %.4f", h, gen24[h]/nGen, src24[h]/nSrc)
		}
	}
}
