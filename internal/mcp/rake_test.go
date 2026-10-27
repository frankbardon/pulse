package mcp

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// invokeRake calls pulse_rake through the catalog's Invoke — the strict
// decode the transport runs — and returns the typed result.
func invokeRake(t *testing.T, p *pulse.Pulse, args string) (RakeOut, error) {
	t.Helper()
	for _, td := range Tools(Config{}) {
		if td.Name != toolmeta.ToolRake {
			continue
		}
		out, err := td.Invoke(context.Background(), p, json.RawMessage(args))
		if err != nil {
			return RakeOut{}, err
		}
		res, ok := out.(RakeOut)
		if !ok {
			t.Fatalf("Invoke returned %T, want RakeOut", out)
		}
		return res, nil
	}
	t.Fatal("pulse_rake not in the catalog")
	return RakeOut{}, nil
}

// TestPulseRake_WritesWeights: the tool rakes the managed cohort to the
// inline targets, writes the weight column into a new cohort under the
// data dir and reports the fit; the written weights reproduce the
// targets.
func TestPulseRake_WritesWeights(t *testing.T) {
	t.Parallel()

	p, src := materializeSource(t)
	res, err := invokeRake(t, p, `{"cohort":"`+src+`","out":"weighted.pulse","weight_field":"w",
		"margins":[{"field":"name","targets_as":"counts","targets":{"Alice":2,"Bob":1,"Carol":1}}]}`)
	if err != nil {
		t.Fatalf("rake: %v", err)
	}
	if res.Records != 3 || res.WeightField != "w" || !res.Converged || len(res.Margins) != 1 {
		t.Fatalf("result = %+v", res)
	}
	schema, rows := readCohort(t, p, "weighted.pulse")
	last := len(schema.Fields) - 1
	if schema.Fields[last].Name != "w" {
		t.Fatalf("last field = %q, want w", schema.Fields[last].Name)
	}
	// Mean weight 1 over 3 records: Alice 1.5, Bob 0.75, Carol 0.75.
	for i, want := range []float64{1.5, 0.75, 0.75} {
		got, err := strconv.ParseFloat(rows[i][last], 64)
		if err != nil || math.Abs(got-want) > 1e-9 {
			t.Fatalf("row %d weight = %s, want %v", i, rows[i][last], want)
		}
	}
}

// TestPulseRake_RefusalsKeepTheirCode: an unknown key at the root or
// inside a margin is PULSE_REQUEST_UNKNOWN_FIELD; a missing out or
// margins is the facade's PULSE_RAKE_INVALID; a record category with no
// target is PULSE_RAKE_UNTARGETED_CATEGORY — none writes anything.
func TestPulseRake_RefusalsKeepTheirCode(t *testing.T) {
	t.Parallel()

	p, src := materializeSource(t)
	c := `"cohort":"` + src + `"`
	for _, tc := range []struct{ name, args, want string }{
		{"unknown root key", `{` + c + `,"out":"x.pulse","margins":[],"marginz":[]}`, "PULSE_REQUEST_UNKNOWN_FIELD"},
		{"unknown margin key", `{` + c + `,"out":"x.pulse","margins":[{"field":"name","target":{"Alice":1}}]}`, "PULSE_REQUEST_UNKNOWN_FIELD"},
		{"missing out", `{` + c + `,"margins":[{"field":"name","targets_as":"counts","targets":{"Alice":1,"Bob":1,"Carol":1}}]}`, "PULSE_RAKE_INVALID"},
		{"missing margins", `{` + c + `,"out":"x.pulse"}`, "PULSE_RAKE_INVALID"},
		{"untargeted", `{` + c + `,"out":"x.pulse","margins":[{"field":"name","targets":{"Alice":1}}]}`, "PULSE_RAKE_UNTARGETED_CATEGORY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := invokeRake(t, p, tc.args)
			if got := codeOf(err); string(got) != tc.want {
				t.Fatalf("code = %q (err %v), want %s", got, err, tc.want)
			}
			if _, err := p.Open(context.Background(), "x.pulse"); err == nil {
				t.Fatal("a refusal wrote x.pulse")
			}
		})
	}
}

// TestRakeIn_SchemaCoversEveryRequestKey: the reflected input schema
// offers every pulse.RakeRequest JSON key, each with a description, so
// the mirror struct cannot drop a key the facade reads. (A field that
// drifts in Go type or order already fails to compile: HandleRake
// converts RakeIn to pulse.RakeRequest.)
func TestRakeIn_SchemaCoversEveryRequestKey(t *testing.T) {
	t.Parallel()

	ts, ok := SchemaFor(toolmeta.ToolRake)
	if !ok {
		t.Fatal("pulse_rake has no schema")
	}
	var schema struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(ts.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	var want []string
	rt := reflect.TypeFor[pulse.RakeRequest]()
	for i := range rt.NumField() {
		want = append(want, strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
	}
	var got []string
	for k, v := range schema.Properties {
		got = append(got, k)
		if strings.TrimSpace(v.Description) == "" {
			t.Errorf("property %q has no description", k)
		}
	}
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("schema properties %v, want the RakeRequest keys %v", got, want)
	}
}
