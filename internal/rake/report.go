package rake

import "github.com/frankbardon/pulse/types"

// Report is a rake run's convergence, achieved margins and efficiency.
// Without `by` it describes the one fit; with `by`, Iterations is the
// most any group took, Converged holds only when every group converged,
// Residual is the worst group's, and Margins / Efficiency pool every
// record (each group's own figures are in Groups).
type Report struct {
	Iterations int            `json:"iterations"`
	Converged  bool           `json:"converged"`
	Residual   float64        `json:"residual"`
	Margins    []MarginReport `json:"margins"`
	Groups     []GroupReport  `json:"groups,omitempty"`
	Efficiency Efficiency     `json:"efficiency"`
}

// GroupReport is one `by` group's fit. Root alias: pulse.RakeGroupReport.
type GroupReport struct {
	// Value is the group's by-field category.
	Value      string         `json:"value"`
	Records    int64          `json:"records"`
	Iterations int            `json:"iterations"`
	Converged  bool           `json:"converged"`
	Residual   float64        `json:"residual"`
	Margins    []MarginReport `json:"margins"`
	Efficiency Efficiency     `json:"efficiency"`
}

// MarshalJSON writes an undefined figure as null (types.MarshalFinite).
func (g GroupReport) MarshalJSON() ([]byte, error) {
	type plain GroupReport
	return types.MarshalFinite(plain(g))
}

// MarginReport is one margin's before / target / after audit. Root
// alias: pulse.RakeMarginReport.
type MarginReport struct {
	// Fields names the margin's field(s).
	Fields []string `json:"fields"`
	// NMissing counts records with no value for the margin (missing
	// "base"); the shares are over the other records.
	NMissing   int64            `json:"n_missing"`
	Categories []CategoryReport `json:"categories"`
}

// CategoryReport is one margin category. Root alias:
// pulse.RakeCategoryReport.
type CategoryReport struct {
	// Values holds the category, one entry per margin field.
	Values []string `json:"values"`
	// ObservedN is the number of records in the category.
	ObservedN int64 `json:"observed_n"`
	// Observed is its share before raking (under the base weight, when
	// one is given).
	Observed float64 `json:"observed"`
	// Target is its requested share (targets normalised by their sum).
	Target float64 `json:"target"`
	// Achieved is its share under the output weights.
	Achieved float64 `json:"achieved"`
}

// MarshalJSON writes an undefined share as null (types.MarshalFinite).
func (c CategoryReport) MarshalJSON() ([]byte, error) {
	type plain CategoryReport
	return types.MarshalFinite(plain(c))
}

// Efficiency is the Kish weighting-efficiency report of the output
// weights. DeffKish is the UNEQUAL-WEIGHTING design effect only
// (n·Σw²/(Σw)² = 1 + WeightCV²): it carries no clustering,
// stratification or finite-population component. An undefined figure
// is NaN in Go and null on the wire. Root alias: pulse.RakeEfficiency.
type Efficiency struct {
	// N is the number of weighted records.
	N int64 `json:"n"`
	// SumWeights is Σw.
	SumWeights float64 `json:"sum_weights"`
	// NEff is Kish's effective sample size (Σw)²/Σw².
	NEff float64 `json:"n_eff"`
	// DeffKish is n / n_eff.
	DeffKish float64 `json:"deff_kish"`
	// EfficiencyPct is 100 · n_eff / n.
	EfficiencyPct float64 `json:"efficiency_pct"`
	// WeightMin / WeightMax are the smallest and largest weight.
	WeightMin float64 `json:"weight_min"`
	WeightMax float64 `json:"weight_max"`
	// WeightRangeRatio is max / min (null when min is 0).
	WeightRangeRatio float64 `json:"weight_range_ratio"`
	// WeightCV is the weights' coefficient of variation, population
	// standard deviation over the mean.
	WeightCV float64 `json:"weight_cv"`
	// NCapped counts records the final sweep held at a cap.
	NCapped int64 `json:"n_capped"`
}

// MarshalJSON writes an undefined figure as null (types.MarshalFinite).
func (e Efficiency) MarshalJSON() ([]byte, error) {
	type plain Efficiency
	return types.MarshalFinite(plain(e))
}
