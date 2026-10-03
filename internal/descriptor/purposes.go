package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
)

// builtinPurposes is the per-operator Purpose registry, keyed by the
// manifest entry name: the operator constant (AGG_*, ATTR_*, FILTER_*,
// GROUP_*, WIN_*, FEAT_*), the TEST_* family (one purpose covers both
// tiers), the REG_* type, the OVERLAY_* kind or the synth distribution
// kind. Purpose declarations land beside the capability tables; an
// undeclared name simply carries no intents.
//
// purposeLookup is the seam the manifest builder reads through, so a
// test can inject purposes without touching the registry.
var (
	builtinPurposes = map[string]descriptor.Purpose{
		"AGG_AVERAGE":    purposeAggAverage,
		"TEST_ANOVA_F":   purposeTestAnovaF,
		"TEST_PEARSON_R": purposeTestPearsonR,
	}
	purposeLookup = func(name string) (descriptor.Purpose, bool) {
		p, ok := builtinPurposes[name]
		return p, ok
	}
)

// PurposeOf returns the declared Purpose of a built-in manifest entry.
func PurposeOf(name string) (descriptor.Purpose, bool) {
	return purposeLookup(name)
}

// intentsOf returns the sorted intent IDs name's purpose declares, or
// nil when it declares none (so the entry's intents key is omitted).
func intentsOf(name string) []string {
	p, ok := purposeLookup(name)
	if !ok || len(p.Intents) == 0 {
		return nil
	}
	out := append([]string(nil), p.Intents...)
	sort.Strings(out)
	return out
}

// withOpIntents and its siblings stamp each manifest entry with its purpose's intent IDs.
// Every slice it is handed is already a fresh copy of the capability
// table, so writing in place never reaches the shared tables.
func withOpIntents(ops []descriptor.Operator) []descriptor.Operator {
	for i := range ops {
		ops[i].Intents = intentsOf(ops[i].Name)
	}
	return ops
}

func withTestIntents(ts []descriptor.TestMeta) []descriptor.TestMeta {
	for i := range ts {
		key := ts[i].Family
		if key == "" {
			key = ts[i].Name
		}
		ts[i].Intents = intentsOf(key)
	}
	return ts
}

func withRegressionIntents(rs []descriptor.RegressionMeta) []descriptor.RegressionMeta {
	for i := range rs {
		rs[i].Intents = intentsOf(rs[i].Name)
	}
	return rs
}

func withDistributionIntents(ds []descriptor.DistributionMeta) []descriptor.DistributionMeta {
	for i := range ds {
		ds[i].Intents = intentsOf(ds[i].Name)
	}
	return ds
}

func withOverlayIntents(os []descriptor.OverlayCapability) []descriptor.OverlayCapability {
	for i := range os {
		os[i].Intents = intentsOf(string(os[i].Kind))
	}
	return os
}

// The exemplar purposes. Every other built-in is listed by the
// TestSkillsCoverAllPurposes coverage report until it declares one.
var (
	purposeAggAverage = descriptor.Purpose{
		Plain:   "Average of a numeric field, over all rows or per group.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"What is the average order value?",
			"What is the typical rating in each region?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Mean satisfaction score per segment.",
			descriptor.DomainOps:     "Average order value by sales channel.",
			descriptor.DomainScience: "Mean measurement per treatment arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the field is skewed or has extreme values and you want the typical row", Use: "AGG_MEDIAN"},
			{When: "rows carry weights", Use: "AGG_WEIGHTED_MEAN"},
			{When: "you also need the spread around the average", Use: "AGG_WELFORD"},
		},
		Assumptions: []string{
			"Missing values are skipped: the average is over rows that have a value.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"mean", "median", "outlier", "skew"},
	}

	purposeTestAnovaF = descriptor.Purpose{
		Plain:   "Checks whether the average of a numeric measure differs across three or more groups.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Does average spend differ across regions?",
			"Do the treatment arms produce different average outcomes?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare mean satisfaction across age bands.",
			descriptor.DomainOps:     "Compare average delivery time across warehouses.",
			descriptor.DomainScience: "Compare mean yield across fertiliser treatments.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the groups have clearly unequal spread", Use: "TEST_ANOVA_WELCH"},
			{When: "the measure is heavily skewed or only ordered", Use: "TEST_KRUSKAL_WALLIS"},
			{When: "the same subjects are measured in every group", Use: "TEST_ANOVA_RM"},
			{When: "there are only two groups", Use: "TEST_WELCH"},
			{When: "you need to know which pairs of groups differ", Use: "TEST_TUKEY_HSD"},
		},
		Assumptions: []string{
			"Rows are independent of each other.",
			"The measure is roughly normally distributed within each group.",
			"Groups have similar variances.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"effect-size", "eta-squared", "f-statistic", "normal-distribution",
			"null-hypothesis", "omega-squared", "p-value", "post-hoc-test", "variance",
		},
	}

	purposeTestPearsonR = descriptor.Purpose{
		Plain:   "Measures how strongly two numeric fields rise and fall together along a straight line.",
		Intents: []string{IntentRelationship},
		Questions: []string{
			"Do customers who spend more also visit more often?",
			"Does study time go with higher test scores?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Check whether satisfaction tracks likelihood to recommend.",
			descriptor.DomainOps:     "See whether ad spend moves with weekly revenue.",
			descriptor.DomainScience: "Relate dose to measured response.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the link is consistent but curved, or the values are ranks", Use: "TEST_SPEARMAN_R"},
			{When: "the sample is small or has many tied values", Use: "TEST_KENDALL_TAU"},
			{When: "both fields are categories", Use: "TEST_CHISQ"},
			{When: "you want to predict one field from several others", Use: "REG_OLS"},
		},
		Assumptions: []string{
			"The relationship is roughly a straight line.",
			"Pairs of values are independent of each other.",
			"A few extreme values can dominate the result.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"confidence-interval", "correlation", "outlier", "p-value", "r-squared",
		},
	}
)
