package synth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// paramsGateFields is one VALID field per distribution, carrying every
// key distributionParamKeys declares for it — the probe the
// declared-vs-read gate runs. A distribution missing here fails the
// gate, so a new one cannot land without its allowlist being exercised.
var paramsGateFields = map[string]FieldSpec{
	DistUniform: {Type: "f64", Params: map[string]any{"min": 0.0, "max": 2.0}},
	DistNormal:  {Type: "f64", Params: map[string]any{"mean": 1.0, "std": 2.0, "min": -5.0, "max": 5.0}},
	DistLogNormal: {Type: "f64",
		Params: map[string]any{"mu": 0.0, "sigma": 0.5}},
	DistExponential: {Type: "f64", Params: map[string]any{"lambda": 2.0}},
	DistPoisson:     {Type: "u32", Params: map[string]any{"lambda": 3.0}},
	DistPareto:      {Type: "f64", Params: map[string]any{"xm": 1.0, "alpha": 2.0}},
	DistBernoulli:   {Type: "packed_bool", Params: map[string]any{"p": 0.3}},
	DistMonotonicFrom: {Type: "u32",
		Params: map[string]any{"start": 5.0, "step": 2.0}},
	DistWeightedCategorical: {Type: "categorical_u8",
		Params: map[string]any{"values": []any{"a", "b"}, "weights": []any{1.0, 3.0}}},
	DistDiscrete: {Type: "u8",
		Params: map[string]any{"values": []any{1.0, 2.0, 3.0}, "weights": []any{1.0, 1.0, 2.0}}},
	DistMixture: {Type: "f64",
		Params: map[string]any{"means": []any{0.0, 5.0}, "stds": []any{1.0, 1.0}, "weights": []any{1.0, 1.0}}},
	DistUniformDate: {Type: "date",
		Params: map[string]any{"start": "2024-01-01", "end": "2024-12-31"}},
	DistUniformDateTime: {Type: "datetime",
		Params: map[string]any{"start": "2024-01-01", "end": "2024-12-31",
			"time_zone": "Europe/Berlin", "dst_policy": "later",
			"hour_weights": oneHot(10), "weekday_weights": []any{1.0, 1.0, 1.0, 1.0, 1.0, 0.5, 0.0}}},
	DistRegex: {Type: "categorical_u16",
		Params: map[string]any{"pattern": "[A-C]{2}", "max_repeat": 3.0}},
	DistConstant: {Type: "set_u8",
		Params: map[string]any{"value": []any{"a"}, "options": []any{"a", "b"}}},
	DistSetBernoulli: {Type: "set_u8",
		Params: map[string]any{"options": []any{"a", "b"}, "frequencies": []any{0.5, 0.2}}},
}

// paramsGateAltProbes carries the declared keys a distribution's main
// probe cannot hold because they are EXCLUSIVE with one it does hold
// (uniform_datetime's time_zone vs time_zone_field). Each entry is a
// whole valid field list whose FIRST field is the alternate probe; its
// reads and keys are unioned with the main probe's, so the gate still
// requires every declared key to be both carried and read.
var paramsGateAltProbes = map[string][]FieldSpec{
	DistUniformDateTime: {
		{Type: "datetime", Params: map[string]any{"start": "2024-01-01", "end": "2024-01-31",
			"time_zone_field": "zone_for_probe"}},
		{Name: "zone_for_probe", Type: "categorical_u8", Distribution: DistWeightedCategorical,
			Params: map[string]any{"values": []any{"Asia/Tokyo"}}},
	},
}

// TestDistributionParamsDeclaredMatchRead is the synth twin of
// TestParamsDeclaredMatchRead: distributionParamKeys is the allowlist
// validateSpec refuses params against, so a key a sampler READS that it
// does not declare would be a false refusal of a working spec, and a
// declared key no sampler reads would be accepted and silently ignored.
// Each distribution's probe field generates (and, where the copula
// supports it, correlates) under a recording of every params lookup.
func TestDistributionParamsDeclaredMatchRead(t *testing.T) {
	// serial: swaps the package-level synth.paramReads test hook

	var mu sync.Mutex
	read := map[string]map[string]bool{}
	rec := func(field, key string) {
		mu.Lock()
		defer mu.Unlock()
		if read[field] == nil {
			read[field] = map[string]bool{}
		}
		read[field][key] = true
	}
	paramReads.Store(&rec)
	defer paramReads.Store(nil)

	for _, dist := range AllDistributions() {
		probe, ok := paramsGateFields[dist]
		if !ok {
			t.Errorf("%s: no probe field in paramsGateFields", dist)
			continue
		}
		probe.Name = "p_" + dist
		probe.Distribution = dist
		spec := &Spec{RowCount: 50, Fields: []FieldSpec{probe}}
		if isNumericFieldType(probe.Type) && dist != DistConstant {
			partner := FieldSpec{Name: "partner", Type: "f64", Distribution: DistUniform}
			spec.Fields = append(spec.Fields, partner)
			spec.Correlations = []CorrelationSpec{{A: probe.Name, B: "partner", Rho: 0.3}}
		}
		if _, _, err := SynthBytes(spec, Options{Seed: 1}); err != nil {
			// A distribution the copula refuses still has its sampler
			// keys recorded; retry uncorrelated.
			spec.Correlations, spec.Fields = nil, spec.Fields[:1]
			if _, _, err := SynthBytes(spec, Options{Seed: 1}); err != nil {
				t.Errorf("%s: probe spec must generate: %v", dist, err)
				continue
			}
		}
		probeKeys := make([]string, 0, len(probe.Params))
		for k := range probe.Params {
			probeKeys = append(probeKeys, k)
		}
		if alt, ok := paramsGateAltProbes[dist]; ok {
			fields := slices.Clone(alt)
			fields[0].Name, fields[0].Distribution = probe.Name, dist
			if _, _, err := SynthBytes(&Spec{RowCount: 20, Fields: fields}, Options{Seed: 1}); err != nil {
				t.Errorf("%s: alternate probe spec must generate: %v", dist, err)
			}
			for k := range alt[0].Params {
				if !slices.Contains(probeKeys, k) {
					probeKeys = append(probeKeys, k)
				}
			}
		}
		declared := distributionParamKeys[dist]
		var got []string
		for k := range read[probe.Name] {
			got = append(got, k)
		}
		sort.Strings(got)
		for _, k := range got {
			if !slices.Contains(declared, k) {
				t.Errorf("%s: reads params.%s but distributionParamKeys does not declare it (a false refusal)", dist, k)
			}
		}
		for _, k := range declared {
			if !slices.Contains(got, k) {
				t.Errorf("%s: declares params.%s but no reader reads it (accepted and ignored)", dist, k)
			}
		}
		sort.Strings(probeKeys)
		want := slices.Sorted(slices.Values(declared))
		if !slices.Equal(probeKeys, want) {
			t.Errorf("%s: probe carries %v, want every declared key %v", dist, probeKeys, want)
		}
	}
	for dist := range distributionParamKeys {
		if !slices.Contains(AllDistributions(), dist) {
			t.Errorf("distributionParamKeys lists %s, which is not registered", dist)
		}
	}
}

// TestDistributionParamsReadThroughLookup: the gate above sees only the
// reads that go through paramLookup, so no production file may index a
// params map directly.
func TestDistributionParamsReadThroughLookup(t *testing.T) {
	t.Parallel()

	direct := regexp.MustCompile(`\.Params\[`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if direct.MatchString(line) {
				t.Errorf("%s:%d reads a params map directly; use paramLookup / param*: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestSynthDocuments_NoCustomUnmarshalers: the strict decoder cannot see
// inside a custom UnmarshalJSON, so no type reachable from a synth
// input document (Spec, a rules file, Profile) may carry one — it would
// silently reopen the lenient decode below it.
func TestSynthDocuments_NoCustomUnmarshalers(t *testing.T) {
	t.Parallel()

	unmarshaler := reflect.TypeFor[json.Unmarshaler]()
	seen := map[reflect.Type]bool{}
	var found []string
	var visit func(reflect.Type)
	visit = func(typ reflect.Type) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if seen[typ] {
			return
		}
		seen[typ] = true
		if typ.Implements(unmarshaler) || reflect.PointerTo(typ).Implements(unmarshaler) {
			found = append(found, typ.PkgPath()+"."+typ.Name())
		}
		switch typ.Kind() {
		case reflect.Struct:
			for i := 0; i < typ.NumField(); i++ {
				visit(typ.Field(i).Type)
			}
		case reflect.Slice, reflect.Array, reflect.Map:
			visit(typ.Elem())
		}
	}
	for _, root := range []reflect.Type{reflect.TypeFor[Spec](), reflect.TypeFor[[]RuleSpec](), reflect.TypeFor[Profile]()} {
		visit(root)
	}
	for _, u := range found {
		t.Errorf("%s implements json.Unmarshaler; the strict synth decode cannot check its keys", u)
	}
}
