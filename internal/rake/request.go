// Package rake is the one record-level raking (rim weighting) and cell
// weighting solver behind Pulse.Rake: it validates a rake request
// against a cohort schema (Compile), reads the margin categories of
// every record (Plan.Load), runs iterative proportional fitting over
// the RECORDS to the requested margins (Plan.Solve) and reports the
// convergence, the achieved margins and the Kish efficiency of the
// resulting weights.
//
// It is a leaf (stdlib + encoding / errors / types + internal/weighting)
// so the no-execute layer can reuse Compile's static refusals without
// reaching the engine. It is NOT OVERLAY_RAKE (which rakes a crosstab
// table and writes nothing) and reads no Populations file (a population
// is a saved vector fit, not census targets): targets are inline.
// Contract: .claude/reference/weighting/rake.md.
package rake

// Defaults applied by Compile when the request leaves a knob at zero.
const (
	// DefaultMaxIter is the sweep ceiling when Request.MaxIter is 0.
	DefaultMaxIter = 100
	// DefaultTol is the convergence tolerance when Request.Tol is 0: the
	// largest absolute gap between an achieved and a target share, over
	// every margin category.
	DefaultTol = 1e-6
	// DefaultWeightField names the output weight column when
	// Request.WeightField is empty.
	DefaultWeightField = "rake_weight"
)

// Missing-value policies (Request.Missing).
const (
	// MissingRefuse (the default) refuses a record whose margin value is
	// null with PULSE_RAKE_MISSING_VALUE.
	MissingRefuse = "refuse"
	// MissingBase keeps the record's current factor for a margin whose
	// value it lacks: the margin neither adjusts it nor counts it.
	MissingBase = "base"
)

// Target spellings (Margin.TargetsAs).
const (
	// TargetsShares (the default): the targets are shares summing to 1,
	// or percentages summing to 100.
	TargetsShares = "shares"
	// TargetsCounts: the targets are population counts of any positive
	// total; only their proportions are used.
	TargetsCounts = "counts"
)

// Request is a rake run: which cohort to read, which new cohort to
// write, and the margins its weights must reproduce. Root alias:
// pulse.RakeRequest.
type Request struct {
	// Cohort is the source cohort path (a single file, a shard archive
	// or an anchored shard).
	Cohort string `json:"cohort"`
	// Out is the path of the NEW single-file cohort written: every source
	// column verbatim plus the nullable f64 weight column.
	Out string `json:"out"`
	// WeightField names the output weight column; empty selects
	// DefaultWeightField. A name the source already carries is refused
	// unless Overwrite is set, which replaces that column in place.
	WeightField string `json:"weight_field,omitempty"`
	// Margins are the target distributions the weights reproduce. One
	// margin of several fields is cell weighting (post-stratification).
	Margins []Margin `json:"margins"`
	// By rakes every group of this field separately to the SAME targets
	// (per-wave weighting of a tracker); each group is normalised on its
	// own.
	By string `json:"by,omitempty"`
	// BaseWeight names a design-weight column the raking starts from: the
	// output is that weight times the raking adjustment.
	BaseWeight string `json:"base_weight,omitempty"`
	// Missing is the null-margin-value policy: "refuse" (default) or
	// "base".
	Missing string `json:"missing,omitempty"`
	// MinWeight / MaxWeight cap every weight relative to the mean weight
	// of its group (0 = no cap), applied after every sweep.
	MinWeight float64 `json:"min_weight,omitempty"`
	MaxWeight float64 `json:"max_weight,omitempty"`
	// MaxIter is the sweep ceiling (0 = DefaultMaxIter).
	MaxIter int `json:"max_iter,omitempty"`
	// Tol is the convergence tolerance on shares (0 = DefaultTol).
	Tol float64 `json:"tol,omitempty"`
	// Total normalises every group's weights to sum to this value; 0
	// (the default) normalises to the group's record count, so the mean
	// weight is 1.
	Total float64 `json:"total,omitempty"`
	// Overwrite replaces an existing cohort at Out and an existing source
	// column named WeightField.
	Overwrite bool `json:"overwrite,omitempty"`
}

// Margin is one target distribution. Exactly one form is set: Field +
// Targets (a single-field rim margin) or Fields (two or more) + Cells
// (a joint margin — cell weighting). Root alias: pulse.RakeMargin.
type Margin struct {
	// Field is the single margin field.
	Field string `json:"field,omitempty"`
	// Targets maps each category of Field (its dictionary label, its
	// decimal integer, or "true" / "false") to its target.
	Targets map[string]float64 `json:"targets,omitempty"`
	// Fields are the joint margin's fields, in Cell.Values order.
	Fields []string `json:"fields,omitempty"`
	// Cells are the joint margin's targets, one per category tuple.
	Cells []Cell `json:"cells,omitempty"`
	// TargetsAs is "shares" (default) or "counts".
	TargetsAs string `json:"targets_as,omitempty"`
}

// Cell is one joint-margin target. Root alias: pulse.RakeCell.
type Cell struct {
	// Values holds one category per Margin.Fields entry.
	Values []string `json:"values"`
	// Target is the cell's share or count.
	Target float64 `json:"target"`
}
