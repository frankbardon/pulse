//go:build heavy

package sem

import (
	"os"
	"testing"
)

// TestSEM_BootstrapMatchesLavaanSampling holds a seeded 1000-replicate
// bootstrap of the brand-survey mediation (free and := rows) to
// lavaan's se = "bootstrap" within the sampling tolerances.
func TestSEM_BootstrapMatchesLavaanSampling(t *testing.T) {
	t.Parallel()

	ref := loadLavaanBootRef(t, "testdata/brand_boot_def_lavaan.json")
	desc, err := os.ReadFile("testdata/brand_def_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModelWithOptions(string(desc), ModelOptions{MeanStructure: true})
	if err != nil {
		t.Fatal(err)
	}
	mean, s, n := readCSVMoments(t, "testdata/brand_data.csv", m.Observed)
	fit, err := FitMoments(m, s, s, mean, n, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rows := BootstrapRows(fit)
	b, err := RunBootstrap(m, csvRows(t, "testdata/brand_data.csv", "", nil, m.Observed), Options{},
		BootstrapOptions{Iters: bootIters, Seed: bootSeed, Level: 0.95}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if b.Requested != bootIters || b.Succeeded != bootIters {
		t.Fatalf("requested %d succeeded %d", b.Requested, b.Succeeded)
	}
	b.Apply(rows)
	checkBootAgainstLavaan(t, rows, ref, func(int) string { return "" })
}

// TestSEM_BootstrapMultiGroupMatchesLavaan is the scalar-invariant
// two-wave fit's bootstrap (resampled within each wave) against
// lavaan's: every group's rows and the cross-wave := rows.
func TestSEM_BootstrapMultiGroupMatchesLavaan(t *testing.T) {
	t.Parallel()

	ref := loadLavaanBootRef(t, "testdata/brand_boot_mg_lavaan.json")
	desc, err := os.ReadFile("testdata/brand_mgdef_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewGroupModel(string(desc), ModelOptions{GroupEqual: ref.Options.GroupEqual})
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{"1", "2"}
	groups := readCSVGroupMoments(t, "testdata/brand_data.csv", "wave", labels, m.Observed)
	groups[0].Reference = true
	gf, err := FitGroups(m, groups, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rows := BootstrapGroupRows(gf)
	b, err := RunBootstrap(m, csvRows(t, "testdata/brand_data.csv", "wave", labels, m.Observed), Options{},
		BootstrapOptions{Iters: bootIters, Seed: bootSeed, Level: 0.95}, rows)
	if err != nil {
		t.Fatal(err)
	}
	b.Apply(rows)
	n0 := len(gf.Groups[0].Estimates)
	checkBootAgainstLavaan(t, rows, ref, func(i int) string {
		if i < n0 {
			return labels[0]
		}
		return labels[1]
	})
}
