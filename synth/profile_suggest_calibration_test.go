package synth_test

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/synth"
)

// knownAnswerSpec is a SHAPE-EQUIVALENT miniature of the motivating
// 381,324-row survey cohort, carrying one instance of every relationship
// the real one was known to hold before detection existed. The real
// profile is 6 MB and not committable; this is what stands in for it.
//
//	aware gates a perception block            -> a gating candidate
//	that block is exactly co-missing          -> a null_together candidate
//	a second cluster is co-missing, ungated   -> a null_together candidate
//	lgbt is entirely empty                    -> an always-null finding
//	nps/promoter/passive/detractor co-miss    -> a null_together candidate
//	the three flags partition nps by band     -> a set_expr candidate
//
// It is generated THROUGH the rule layer, the pattern every detector
// fixture in this package uses: the claim under test is a round trip,
// and a source built by the mechanism the round trip ends in is the one
// shape where "recovered" and "reproducible" mean the same thing.
func knownAnswerSpec(rows int) *synth.Spec {
	u4 := func(name string, nullable bool, nullRate, mean, min, max float64) synth.FieldSpec {
		return synth.FieldSpec{
			Name: name, Type: "u4", Nullable: nullable, NullRate: nullRate,
			Distribution: synth.DistNormal,
			Params:       map[string]any{"mean": mean, "std": 1.4, "min": min, "max": max},
		}
	}
	spec := &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{Name: "aware", Type: "packed_bool", Distribution: synth.DistBernoulli,
				Params: map[string]any{"p": 0.75}},
		},
	}
	// The gated perception block. Their own null_rate is 0 — the gate
	// thresholds are 0.98/0.02, so a target carrying an MCAR rate of its
	// own lands between them at the gate's open levels and is classified
	// as ungated. Their nulls come from the gate alone, which is also
	// what makes them EXACTLY co-missing.
	var perception []string
	for i := 1; i <= 6; i++ {
		name := fmt.Sprintf("perception%d", i)
		perception = append(perception, name)
		spec.Fields = append(spec.Fields, u4(name, true, 0, 4, 1, 7))
	}
	// The second cluster: co-missing at a DIFFERENT rate and gated by
	// nothing — the shape of the motivating cohort's 26-field 0.3564
	// cluster, which no single gate explained.
	var cluster []string
	for i := 1; i <= 4; i++ {
		name := fmt.Sprintf("cluster%d", i)
		cluster = append(cluster, name)
		spec.Fields = append(spec.Fields, u4(name, true, 0.40, 4, 1, 7))
	}
	spec.Fields = append(spec.Fields,
		synth.FieldSpec{Name: "lgbt", Type: "categorical_u8", Nullable: true,
			Distribution: synth.DistWeightedCategorical,
			Params: map[string]any{
				"values":  []any{"yes", "no"},
				"weights": []any{0.1, 0.9},
			}},
		u4("nps", true, 0.35, 7, 0, 10),
		synth.FieldSpec{Name: "promoter", Type: "packed_bool", Nullable: true, Distribution: synth.DistBernoulli,
			Params: map[string]any{"p": 0.4}},
		synth.FieldSpec{Name: "passive", Type: "packed_bool", Nullable: true, Distribution: synth.DistBernoulli,
			Params: map[string]any{"p": 0.3}},
		synth.FieldSpec{Name: "detractor", Type: "packed_bool", Nullable: true, Distribution: synth.DistBernoulli,
			Params: map[string]any{"p": 0.3}},
	)
	spec.Rules = []synth.RuleSpec{
		{NullTogether: cluster},
		{SetNull: []string{"lgbt"}},
		{SetExpr: map[string]string{
			"promoter":  "round(nps) >= 9",
			"passive":   "round(nps) >= 7 && round(nps) <= 8",
			"detractor": "round(nps) <= 6",
		}, NullTogether: []string{"nps", "promoter", "passive", "detractor"}},
		{When: "round(aware) == 0", SetNull: perception},
	}
	return spec
}

func knownAnswerProfile(t *testing.T) (*synth.Profile, []byte) {
	t.Helper()
	src, _, err := synth.SynthBytes(knownAnswerSpec(8000), synth.Options{Seed: 907})
	if err != nil {
		t.Fatalf("SynthBytes(source cohort): %v", err)
	}
	prof, err := synth.ProfileBytes(src, synth.ProfileOptions{
		TopK: 8, IncludeStats: true, SuggestRules: true, Seed: 1,
	})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	return prof, src
}

// knownAnswer is one row of the table the effort's interview established
// BY HAND on the motivating cohort, restated against the miniature above.
type knownAnswer struct {
	name string
	// found reports whether the candidate file (or the warnings) carries
	// the finding, and returns what it found for the failure message.
	found func(*synth.Profile) (string, bool)
}

// TestSuggestRules_FindsEveryKnownRelationship is the epic's acceptance
// test in committable form. Each row is a relationship established
// independently of the detectors — by hand, from null rates and means —
// and a detector that misses one is a detector nobody should trust.
//
// SILENCE ON A ROW IS THE FAILURE. A row whose finding is reported in
// the warnings rather than proposed as a rule still passes, because
// "reported as an unexplained co-missing block" was always an acceptable
// answer; saying nothing was never one.
func TestSuggestRules_FindsEveryKnownRelationship(t *testing.T) {
	prof, _ := knownAnswerProfile(t)

	perception := []string{"perception1", "perception2", "perception3", "perception4",
		"perception5", "perception6"}
	npsBlock := []string{"nps", "promoter", "passive", "detractor"}

	rows := []knownAnswer{
		{
			name: "a boolean gates a perception block (the `aware` row)",
			found: func(p *synth.Profile) (string, bool) {
				for _, c := range p.RuleCandidates {
					if c.Evidence.Detector != "gating" || c.Evidence.GateField != "aware" {
						continue
					}
					return fmt.Sprintf("%s -> %v", c.When, c.SetNull),
						sameFields(c.SetNull, perception)
				}
				return "no gating candidate on \"aware\"", false
			},
		},
		{
			name: "the gated block is EXACTLY co-missing, and says so separately",
			found: func(p *synth.Profile) (string, bool) {
				return blockOver(p, perception)
			},
		},
		{
			name: "an UNGATED cluster is resolved as a co-missing block",
			found: func(p *synth.Profile) (string, bool) {
				return blockOver(p, []string{"cluster1", "cluster2", "cluster3", "cluster4"})
			},
		},
		{
			name: "the four-field NPS block",
			found: func(p *synth.Profile) (string, bool) {
				return blockOver(p, npsBlock)
			},
		},
		{
			name: "the promoter/passive/detractor partition, with edges read off the data",
			found: func(p *synth.Profile) (string, bool) {
				for _, c := range p.RuleCandidates {
					if c.Evidence.Detector != "dependency" || c.Evidence.SourceField != "nps" {
						continue
					}
					got := fmt.Sprintf("%v + null_together%v", c.SetExpr, c.NullTogether)
					// The EDGES are the claim: 9-10 promoter, 7-8
					// passive, 0-6 detractor, discovered rather than
					// assumed. They are checked against the measured
					// MAPPING, not the rendered expression, because the
					// rendering is a separate judgement (`form`) and a
					// threshold that agreed with the data by accident
					// would pass a string comparison.
					ok := len(c.SetExpr) == 3 && sameFields(c.NullTogether, npsBlock)
					for _, d := range c.Evidence.Dependency {
						for _, m := range d.Mapping {
							var lv int
							fmt.Sscanf(m.Level, "%d", &lv)
							want := false
							switch d.Field {
							case "promoter":
								want = lv >= 9
							case "passive":
								want = lv >= 7 && lv <= 8
							case "detractor":
								want = lv <= 6
							default:
								ok = false
							}
							if truthy(m.Value) != want {
								ok = false
							}
						}
					}
					return got, ok
				}
				return "no dependency candidate sourced from \"nps\"", false
			},
		},
		{
			name: "the always-empty column is flagged (the `lgbt` row)",
			found: func(p *synth.Profile) (string, bool) {
				for _, w := range p.Warnings {
					if contains(w, "always-null column \"lgbt\"") {
						return w, true
					}
				}
				return "no always-null finding for \"lgbt\"", false
			},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			got, ok := row.found(prof)
			if !ok {
				t.Fatalf("known relationship not found: %s\n  detectors returned: %s\n  warnings: %v",
					row.name, got, prof.Warnings)
			}
			t.Logf("%s", got)
		})
	}

	// The count is a criterion of its own: a candidate file nobody reads
	// is not an improvement over no file.
	if n := len(prof.RuleCandidates); n == 0 || n > 20 {
		t.Errorf("emitted %d candidate(s); the file is meant to be readable on one screen "+
			"(bounded at 20 with a counted remainder)", n)
	}
	t.Logf("%d candidate(s) emitted", len(prof.RuleCandidates))
}

// TestSuggestRules_TheLoopCloses feeds the emitted file back through the
// generation half UNMODIFIED and measures the coherence properties on
// the resulting cohort — the criterion proving the two halves share one
// format.
//
// It asserts the coherence AND the firing together, in one test, because
// each is trivially satisfiable by abandoning the other: a rule that
// nulls every row is perfectly coherent and says nothing, and a rule
// that fires on every row can still leave the block in pieces. E2-S3
// gave firing no external surface beyond the zero case, so the assertion
// is the ABSENCE of a `never fired` line — which is the strongest form
// available and the one E3-S1's suite uses.
func TestSuggestRules_TheLoopCloses(t *testing.T) {
	const rows = 8000
	prof, _ := knownAnswerProfile(t)
	if len(prof.RuleCandidates) == 0 {
		t.Fatalf("no candidates to close the loop with; warnings = %v", prof.Warnings)
	}

	cfg := fs.NewMemMap()
	if err := synth.WriteRuleCandidates(cfg.Fs(), prof.RuleCandidates, "/candidates.json"); err != nil {
		t.Fatalf("WriteRuleCandidates: %v", err)
	}
	spec, _ := synth.SpecFromProfile(prof, rows)
	if err := synth.ApplyRulesFile(cfg.Fs(), spec, "/candidates.json"); err != nil {
		t.Fatalf("the emitted file was not consumed unmodified: %v", err)
	}
	if len(spec.Rules) != len(prof.RuleCandidates) {
		t.Fatalf("loaded %d rule(s) from a %d-candidate file", len(spec.Rules), len(prof.RuleCandidates))
	}

	out, res, err := synth.SynthBytes(spec, synth.Options{Seed: 4242})
	if err != nil {
		t.Fatalf("SynthBytes(generated cohort): %v", err)
	}

	// Nothing detection proposed is inert.
	for _, w := range res.Warnings {
		if contains(w, "never fired") {
			t.Errorf("a proposed candidate is inert: %s", w)
		}
	}

	coh := readCoherence(t, out, []string{"perception1", "perception2", "perception3",
		"perception4", "perception5", "perception6"})
	t.Logf("rows=%d  perception block partial=%d  nps block partial=%d  "+
		"gated rows with a perception answer=%d  scored rows=%d  band agrees=%d",
		coh.rows, coh.perceptionPartial, coh.npsPartial, coh.gatedButAnswered,
		coh.scored, coh.bandAgrees)

	if coh.rows != rows {
		t.Fatalf("generated %d rows, want %d", coh.rows, rows)
	}
	// The fixture guard. Every assertion below is satisfied by a cohort
	// in which the whole NPS block is null on every row, and that cohort
	// says nothing at all.
	if coh.scored == 0 {
		t.Fatal("no row carries a scored NPS block, so the band assertion below is vacuous")
	}
	if coh.gatedRows == 0 {
		t.Fatal("no row is gated, so the gate assertions below are vacuous")
	}
	if coh.perceptionPartial != 0 {
		t.Errorf("perception block is in pieces on %d of %d rows", coh.perceptionPartial, coh.rows)
	}
	if coh.npsPartial != 0 {
		t.Errorf("NPS block is in pieces on %d of %d rows", coh.npsPartial, coh.rows)
	}
	if coh.gatedButAnswered != 0 {
		t.Errorf("%d gated row(s) still carry a perception answer", coh.gatedButAnswered)
	}
	if coh.bandAgrees != coh.scored {
		t.Errorf("the flag agrees with the nps band on %d of %d scored rows", coh.bandAgrees, coh.scored)
	}
}

// coherence is the miniature of the E2-S4 coherence table: the
// properties a reader would check by hand on the generated cohort.
type coherence struct {
	rows              int
	perceptionPartial int
	npsPartial        int
	gatedRows         int
	gatedButAnswered  int
	scored            int
	bandAgrees        int
}

func readCoherence(t *testing.T, data []byte, perception []string) coherence {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	rr := encoding.NewRecordReader(r, schema)
	values := map[string]float64{}
	nulls := map[string]bool{}
	flags := []string{"promoter", "passive", "detractor"}
	var out coherence
	for {
		if err := rr.ReadRecord(values, nulls); err != nil {
			break
		}
		out.rows++

		present := 0
		for _, f := range perception {
			if !nulls[f] {
				present++
			}
		}
		if present != 0 && present != len(perception) {
			out.perceptionPartial++
		}
		if values["aware"] == 0 {
			out.gatedRows++
			if present != 0 {
				out.gatedButAnswered++
			}
		}

		npsNull := nulls["nps"]
		flagNull := 0
		set, which := 0, ""
		for _, f := range flags {
			if nulls[f] {
				flagNull++
				continue
			}
			if values[f] != 0 {
				set++
				which = f
			}
		}
		blockPresent := 0
		if !npsNull {
			blockPresent++
		}
		blockPresent += len(flags) - flagNull
		if blockPresent != 0 && blockPresent != len(flags)+1 {
			out.npsPartial++
		}
		if !npsNull && flagNull == 0 {
			out.scored++
			if set == 1 && which == npsBand(values["nps"]) {
				out.bandAgrees++
			}
		}
	}
	return out
}

// npsBand is the standard reading — and it is the fixture's OWN
// definition, restated, so a detector that discovered different edges
// fails here rather than agreeing with itself.
func npsBand(nps float64) string {
	switch {
	case nps >= 9:
		return "promoter"
	case nps >= 7:
		return "passive"
	default:
		return "detractor"
	}
}

// blockOver reports whether some co-missing candidate names exactly the
// given members.
func blockOver(p *synth.Profile, members []string) (string, bool) {
	var seen []string
	for _, c := range p.RuleCandidates {
		if c.Evidence.Detector != "co_missing" {
			continue
		}
		seen = append(seen, fmt.Sprintf("%v", c.NullTogether))
		if sameFields(c.NullTogether, members) {
			return fmt.Sprintf("%v", c.NullTogether), true
		}
	}
	return fmt.Sprintf("no block over %v; blocks found: %v", members, seen), false
}

func sameFields(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	a := append([]string(nil), got...)
	b := append([]string(nil), want...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	default:
		return false
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
