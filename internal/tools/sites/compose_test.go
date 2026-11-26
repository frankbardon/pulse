package main

import (
	"strings"
	"testing"
)

// composeFixture is a miniature tree with one site (and one near miss)
// per compose-section rule, plus the types the slot resolver walks.
var composeFixture = map[string]string{
	"types/types.go": `package types

type Request struct {
	Aggregations []Aggregation ` + "`json:\"aggregations\"`" + `
	Windows      []*Window     ` + "`json:\"windows\"`" + `
	Overlays     []string      ` + "`json:\"overlays\"`" + `
}

type Aggregation struct {
	Label   string   ` + "`json:\"label\"`" + `
	ForEach *ForEach ` + "`json:\"for_each\"`" + `
}

type Window struct {
	ForEach *ForEach ` + "`json:\"for_each\"`" + `
}

type ForEach struct {
	Mode string ` + "`json:\"mode\"`" + `
}

type ComposedRequest struct {
	Requests []*Request ` + "`json:\"requests\"`" + `
	Overlays []string   ` + "`json:\"overlays\"`" + `
	Sweep    *SweepSpec ` + "`json:\"sweep\"`" + `
}

type SweepSpec struct {
	Request *Request ` + "`json:\"request\"`" + `
}

type Response struct {
	Warnings []string ` + "`json:\"warnings\"`" + `
}

type ComposedResponse struct {
	Responses     []*Response         ` + "`json:\"responses\"`" + `
	Warnings      []string            ` + "`json:\"warnings\"`" + `
	Optimizations []*OptimizeResult   ` + "`json:\"optimizations\"`" + `
}

type OptimizeResult struct {
	Objective float64 ` + "`json:\"objective\"`" + `
}
`,
	"internal/cli/strict_acceptance_test.go": `package cli

import "testing"

type row struct{ family string }

func strictAcceptanceRows() []row {
	return []row{{"composed"}, {"composed.overlays"}, {"composed.fits"}, {"composed.base"}, {"composed.sweep"}, {"composed.sweep.rank"}}
}

func nearRows() []row {
	return []row{{"composed"}, {"composed.overlays"}, {"composed.fits"}, {"composed.base"}}
}

func TestStrictInput(t *testing.T) { _ = strictAcceptanceRows() }

func TestOther(t *testing.T) { _ = nearRows() }
`,
	"internal/service/limits_parity_test.go": `package service

type parityCase struct{ site string }

func parityCases() []parityCase {
	return []parityCase{{"serial"}, {"compose"}, {"compose (slot join backstop)"}}
}

func oneArm() []parityCase { return []parityCase{{"compose"}, {"composed sweep"}} }
`,
	"internal/descriptor/compose_optimize.go": `package descriptor

func ResultCatalog(requests []int) int { return 0 }

func NotShared(requests []int) int { return 0 }
`,
	"internal/service/compose_optimize.go": `package service

func runComposeOptimize() {}
`,
	"examples/profiles/read-only-analyst.json": `{"features": ["capability:compose_sweep", "capability:compose_fits", "capability:compose_banner", "capability:compose_base", "capability:compose_optimize"]}
`,
	"examples/profiles/minimal.json": `{"features": ["capability:compose_sweep", "capability:compose_fits", "capability:compose_banner", "capability:compose_base"]}
`,
	"internal/skills/compose-sweeps.md": "# Compose sweeps\n\n```json\n{\"sweep\": {\"axes\": []}}\n```\n",
	"internal/skills/compose-fits.md":   "# Compose fits\n\nNo sweep key here.\n",
	"internal/examples/compose/01_sweep_grid.json": `{"sweep": {"axes": []}}
`,
}

func TestComposeSection_Rules(t *testing.T) {
	t.Parallel()

	r := fixtureRepo(t, composeFixture)
	calls := []sharedCall{{Callee: "ResultCatalog", Arg: "Requests", Path: "internal/service/compose_optimize.go"}}
	sec, ok := r.composeSection("sweep", false, calls)
	if !ok {
		t.Fatal("no compose section for the fixture")
	}
	cases := []struct {
		rule, path, sym, note string
		want                  bool
	}{
		{"strict-input family table", "internal/cli/strict_acceptance_test.go", "func strictAcceptanceRows", `names "composed.sweep"`, true},
		{"family table below the minimum", "internal/cli/strict_acceptance_test.go", "func nearRows", "", false},
		{"limits-parity site list", "internal/service/limits_parity_test.go", "func parityCases", "limits-parity site list: 2 compose runtime sites", true},
		{"one runtime site is no list", "internal/service/limits_parity_test.go", "func oneArm", "", false},
		{"capability list", "examples/profiles/read-only-analyst.json", "", "capability list: 5 compose features, names capability:compose_sweep", true},
		{"capability list below the minimum", "examples/profiles/minimal.json", "", "", false},
		{"shared derivation", "internal/descriptor/compose_optimize.go", "func ResultCatalog", "ComposedRequest.Requests", true},
		{"uncalled descriptor func", "internal/descriptor/compose_optimize.go", "func NotShared", "", false},
		{"skill carrying the key", "internal/skills/compose-sweeps.md", "# Compose sweeps", `carries the "sweep" key`, true},
		{"skill without the key", "internal/skills/compose-fits.md", "# Compose fits", "", false},
		{"example carrying the key", "internal/examples/compose/01_sweep_grid.json", "", `carries the "sweep" key`, true},
	}
	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			s := siteOf(sec, tc.path)
			note, found := symbolNote(s, tc.sym)
			if found != tc.want {
				t.Fatalf("%s · %q listed = %v, want %v (site %+v)", tc.path, tc.sym, found, tc.want, s)
			}
			if tc.want && !strings.Contains(note, tc.note) {
				t.Errorf("%s · %q note %q, want it to contain %q", tc.path, tc.sym, note, tc.note)
			}
		})
	}
	gates := r.sectionTestGates("x", sec)
	if len(gates) != 1 || strings.Join(gates[0].Tests, ",") != "TestStrictInput" {
		t.Errorf("section test gates %+v, want only TestStrictInput (the test reading strictAcceptanceRows)", gates)
	}

	// a NEW slot: the family table asks for its row; no key carriers yet
	sec, _ = r.composeSection("new_thing", true, nil)
	if note, _ := symbolNote(siteOf(sec, "internal/cli/strict_acceptance_test.go"), "func strictAcceptanceRows"); !strings.Contains(note, `add a "composed.new_thing" row`) {
		t.Errorf("new slot family note %q", note)
	}
	if siteOf(sec, "internal/skills/compose-sweeps.md") != nil {
		t.Error("a new slot lists skills carrying another slot's key")
	}
}

func TestResolveSlot(t *testing.T) {
	t.Parallel()

	r := fixtureRepo(t, composeFixture)
	cases := []struct {
		which, name, root string
		owners            []string // Struct.Field
		err               bool
	}{
		{"request", "overlays", "Request", []string{"Request.Overlays"}, false},
		{"request", "sweep", "ComposedRequest", []string{"ComposedRequest.Sweep"}, false},
		{"request", "Sweep", "ComposedRequest", []string{"ComposedRequest.Sweep"}, false},
		{"compose", "overlays", "ComposedRequest", []string{"ComposedRequest.Overlays"}, false},
		{"composed", "sweep", "ComposedRequest", []string{"ComposedRequest.Sweep"}, false},
		{"request", "for_each", "", []string{"Aggregation.ForEach", "Window.ForEach"}, false},
		{"request", "not_yet", "Request", nil, false},
		{"compose", "not_yet", "ComposedRequest", nil, false},
		// a `response` slot not on types.Response is looked up on
		// types.ComposedResponse (optimizations, fits, ranking, …)
		{"response", "warnings", "Response", []string{"Response.Warnings"}, false},
		{"response", "optimizations", "ComposedResponse", []string{"ComposedResponse.Optimizations"}, false},
		{"resp", "Optimizations", "ComposedResponse", []string{"ComposedResponse.Optimizations"}, false},
		{"response", "objective", "", []string{"OptimizeResult.Objective"}, false},
		{"response", "not_yet", "Response", nil, false},
		{"neither", "x", "", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.which+" "+tc.name, func(t *testing.T) {
			root, owners, err := r.resolveSlot(tc.which, tc.name)
			if (err != nil) != tc.err {
				t.Fatalf("err %v, want error %v", err, tc.err)
			}
			var got []string
			for _, o := range owners {
				got = append(got, o.Struct+"."+o.Field.Name)
			}
			if root != tc.root || strings.Join(got, ",") != strings.Join(tc.owners, ",") {
				t.Errorf("resolveSlot = %q %v, want %q %v", root, got, tc.root, tc.owners)
			}
		})
	}
}

func TestDeclIdent(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{"func rows": "rows", "var gatedSlots": "gatedSlots", "func (readsWalker) compose": "", "(file)": "", "type Request": "Request"} {
		if got := declIdent(in); got != want {
			t.Errorf("declIdent(%q) = %q, want %q", in, got, want)
		}
	}
}
