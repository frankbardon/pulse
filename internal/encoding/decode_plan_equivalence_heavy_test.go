//go:build heavy

package encoding

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestDecodePlan_Equivalence_BigSchemaSampled exercises a 26-field schema
// across ~200 deterministically sampled projection subsets covering every
// shape the plan builder distinguishes: empty, full, only nullable, only
// bit-packed, leading skip + trailing decode, mid retention with leading
// AND trailing skips, every-other-field, plus uniformly random subsets.
func TestDecodePlan_Equivalence_BigSchemaSampled(t *testing.T) {
	t.Parallel()

	schema := bigMixedSchema()
	records := generateBigSchemaRecords(t, schema, 0xB16D15CE)
	all := allFieldNames(schema)
	nullable := nullableFieldNames(schema)
	bitPacked := bitPackedFieldNames(schema)

	type sample struct {
		label    string
		retained []string
	}
	samples := []sample{
		{"empty", nil},
		{"full", all},
		{"only_nullable", nullable},
		{"only_bit_packed", bitPacked},
	}
	// Leading skip + trailing decode: retain only the last field.
	samples = append(samples, sample{
		label:    "trailing_only",
		retained: []string{all[len(all)-1]},
	})
	// Leading retain + trailing skip: retain only the first field.
	samples = append(samples, sample{
		label:    "leading_only",
		retained: []string{all[0]},
	})
	// Mid retention only: retain a single mid-schema field so the plan
	// emits a leading skip, a tiny decode segment, and a trailing skip
	// (plus the bitmap segment).
	samples = append(samples, sample{
		label:    "mid_only",
		retained: []string{all[len(all)/2]},
	})
	// Two mid-only retains, far apart, so the plan emits ≥ 3 skip
	// segments (leading, between-retains, trailing).
	samples = append(samples, sample{
		label:    "two_mid_apart",
		retained: []string{all[6], all[18]},
	})
	// Every-other-field retain: alternating skip:decode pattern.
	{
		var pat []string
		for i := 0; i < len(all); i += 2 {
			pat = append(pat, all[i])
		}
		samples = append(samples, sample{label: "every_other", retained: pat})
	}
	// Odd-indexed retain to complement every_other.
	{
		var pat []string
		for i := 1; i < len(all); i += 2 {
			pat = append(pat, all[i])
		}
		samples = append(samples, sample{label: "every_other_odd", retained: pat})
	}
	// All - one (each field, in turn — but we cap to avoid blowing the
	// sample budget; tested for every field via PerType already).
	for i := 0; i < len(all); i++ {
		except := make([]string, 0, len(all)-1)
		for j, n := range all {
			if j == i {
				continue
			}
			except = append(except, n)
		}
		samples = append(samples, sample{
			label:    fmt.Sprintf("all_except_%s", all[i]),
			retained: except,
		})
	}

	// Random uniform samples to push toward ~200 total. We deduplicate
	// by sorted-name signature so the budget covers distinct subsets.
	seen := make(map[string]struct{}, len(samples))
	for _, s := range samples {
		seen[signature(s.retained)] = struct{}{}
	}
	r := rand.New(rand.NewSource(0xBA5E5EED))
	for len(samples) < 200 {
		// Build a random subset of expected size ~|schema|/2.
		var pat []string
		for _, n := range all {
			if r.Intn(2) == 0 {
				pat = append(pat, n)
			}
		}
		sig := signature(pat)
		if _, ok := seen[sig]; ok {
			continue
		}
		seen[sig] = struct{}{}
		samples = append(samples, sample{
			label:    fmt.Sprintf("random_%d", len(samples)),
			retained: pat,
		})
	}

	// For each sampled subset, walk the entire 1000-record corpus.
	// 200 * 1000 = 200K compareDecodeResults invocations; each is cheap
	// (one plan build, two record decodes against an in-memory slice).
	for _, s := range samples {
		for ri, raw := range records {
			label := fmt.Sprintf("big/%s/rec=%d", s.label, ri)
			compareDecodeResults(t, schema, raw, s.retained, label)
			if t.Failed() {
				t.Logf("first failure at %s; retained=%v", label, s.retained)
				return
			}
		}
	}
}
