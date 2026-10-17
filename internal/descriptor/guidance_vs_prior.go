package descriptor

import "github.com/frankbardon/pulse/descriptor"

// Purpose and Interpretation of the inferential prior-period kinds
// (U47 #328): OVERLAY_T_VS_PRIOR, OVERLAY_Z_VS_PRIOR and
// OVERLAY_PROP_Z_VS_PRIOR. overlayPurposes / overlayInterpretations
// reference them.

// overlayPriorIndependence is the assumption every prior-period kind
// states: the two periods are independent samples.
const overlayPriorIndependence = "The two periods must be separate, independent samples of people. When the same respondents answer in both periods " +
	"(a panel), the test is wrong: their answers are linked, and the p-value is then usually too large. Pulse cannot detect this."

// overlayPriorFirst is the shared first-period / gap sentence.
const overlayPriorFirst = "The periods must be ordered. The first period has no prior: it gets no statistic and no p-value. A period with no value is " +
	"skipped and the next one is tested against the last period that had one, so a gap can span more than one period."

var (
	purposeOverlayTVsPrior = descriptor.Purpose{
		Plain:   "Welch t-test of each period against the period before it: did the average really change from the last wave, or is it noise?",
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"Did the average satisfaction score change significantly from last wave?",
			"Which months' mean order value moved more than chance from the month before?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Test the wave-on-wave change of a tracker's mean rating.",
			descriptor.DomainOps:     "Test whether average handling time moved from one week to the next.",
			descriptor.DomainScience: "Test step-to-step change of a mean across independent batches.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the measure is a share of respondents (a 0/1 indicator)", Use: "OVERLAY_PROP_Z_VS_PRIOR"},
			{When: "you want the size of the change rather than a p-value", Use: "OVERLAY_DELTA_VS_PRIOR"},
			{When: "the same respondents answer in both periods", Use: "TEST_PAIRED_T"},
		},
		Assumptions: []string{
			"The measure must be AGG_WELFORD (or AGG_VARIANCE / AGG_STDDEV), which supplies each period's mean, variance and n; another measure is refused.",
			overlayPriorIndependence,
			overlayPriorFirst,
			"Equal variances are not assumed. Each period is a separate test; to adjust for multiple comparisons, " + overlayCorrectWith,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"independence", "mean", "multiple-comparisons", "null-hypothesis", "p-value", "t-statistic", "two-tailed", "variance",
		},
	}

	purposeOverlayZVsPrior = descriptor.Purpose{
		Plain:   "Large-sample z-test of each period's mean against the period before it: did the average change from the last wave?",
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"With large waves, did the average spend per visit change from last quarter?",
			"Which weeks' mean basket differs from the week before beyond chance?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Test wave-on-wave mean change on large tracker waves.",
			descriptor.DomainOps:     "Test day-on-day change of a high-volume average.",
			descriptor.DomainScience: "Test change of a mean between large consecutive runs.",
		},
		NotFor: []descriptor.Alternative{
			{When: "some periods are small", Use: "OVERLAY_T_VS_PRIOR"},
			{When: "the measure is a share of respondents (a 0/1 indicator)", Use: "OVERLAY_PROP_Z_VS_PRIOR"},
			{When: "you want the size of the change rather than a p-value", Use: "OVERLAY_DELTA_VS_PRIOR"},
		},
		Assumptions: []string{
			"The measure must be AGG_WELFORD (or AGG_VARIANCE / AGG_STDDEV), which supplies each period's mean, variance and n; another measure is refused.",
			overlayPriorIndependence,
			overlayPriorFirst,
			"The normal curve stands in for the t distribution, which is fair only when every period is large. Each period is a separate test; to adjust for multiple comparisons, " + overlayCorrectWith,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"independence", "mean", "multiple-comparisons", "normal-distribution", "null-hypothesis", "p-value", "two-tailed", "variance", "z-score",
		},
	}

	purposeOverlayPropZVsPrior = descriptor.Purpose{
		Plain:   "Two-proportion z-test of each period's share against the period before it: did awareness (or any yes/no share) change since last wave?",
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"Did brand awareness change significantly from last wave?",
			"Which months' conversion rate moved from the month before beyond chance?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Test the wave-on-wave change of a tracker's awareness or consideration share.",
			descriptor.DomainOps:     "Test whether this week's on-time rate moved from last week's.",
			descriptor.DomainScience: "Test change of a success rate between consecutive independent batches.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the measure is an average of a scale, not a 0/1 share", Use: "OVERLAY_T_VS_PRIOR"},
			{When: "you want the size of the change in points rather than a p-value", Use: "OVERLAY_DELTA_VS_PRIOR"},
			{When: "you want every pair of periods, not only consecutive ones", Use: "OVERLAY_PAIRWISE_PROP_Z"},
		},
		Assumptions: []string{
			"The measure must be AGG_AVERAGE of a 0/1 indicator: its value is the share and its n the respondents with a value; on a crosstab, normalize must be none.",
			overlayPriorIndependence,
			overlayPriorFirst,
			"Weighted, n is the effective sample size (Kish n_eff under probability weights), not the raw count. Each period is a separate test; to adjust for multiple comparisons, " + overlayCorrectWith,
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"effective-sample-size", "independence", "multiple-comparisons", "null-hypothesis", "p-value", "two-tailed", "z-score",
		},
	}
)

// overlayPriorMultiComp is the p-value caveat every prior-period kind
// carries.
const overlayPriorMultiComp = "Every period after the first is a separate test and Pulse reports the p-values raw: with many periods some small ones " +
	"turn up by luck, so adjust for multiple comparisons: set multiplicity on the overlay and read summary.p_adjusted."

// interpOverlayPrior is the shared Interpretation of a prior-period kind
// whose statistic is named stat.
func interpOverlayPrior(stat string) []descriptor.Interpretation {
	return []descriptor.Interpretation{
		{
			Field: "summary.statistic",
			Means: "In each series entry (one per period): the " + stat + " of this period minus the period before it, in standard errors. " +
				"Null on the first period and on a test that could not be computed; read the entry's p_value for the test.",
			Sign: map[string]string{
				"+": "this period is higher than the one before",
				"-": "this period is lower than the one before",
			},
			Caveats: []string{overlayPriorIndependence},
		},
		{
			Field:  "summary.p_value",
			Shared: SharedPValue,
			Caveats: []string{
				overlayPriorMultiComp,
				"The first period has no p-value. The layer does not report an effect size: read OVERLAY_DELTA_VS_PRIOR for how big each change is.",
			},
		},
	}
}

var (
	interpOverlayTVsPrior     = interpOverlayPrior("Welch t statistic")
	interpOverlayZVsPrior     = interpOverlayPrior("z statistic")
	interpOverlayPropZVsPrior = interpOverlayPrior("two-proportion z statistic")
)
