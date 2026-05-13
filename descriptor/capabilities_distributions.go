package descriptor

import "github.com/frankbardon/pulse/synth"

// distributionCapabilities returns metadata for every entry in
// synth.AllDistributions(). TestManifestDistributionsComplete enforces
// coverage.
func distributionCapabilities() []DistributionMeta {
	return []DistributionMeta{
		{
			Name:        synth.DistUniform,
			Description: "Uniform real-valued samples in [min, max).",
			AppliesTo:   []string{"numeric"},
			Params: []Param{
				{Name: "min", Type: "float", Required: false, Default: 0.0, Description: "Lower bound (inclusive)."},
				{Name: "max", Type: "float", Required: false, Default: 1.0, Description: "Upper bound (exclusive)."},
			},
		},
		{
			Name:        synth.DistNormal,
			Description: "Gaussian samples with optional [min, max] clamp.",
			AppliesTo:   []string{"numeric"},
			Params: []Param{
				{Name: "mean", Type: "float", Required: false, Default: 0.0, Description: "Distribution mean."},
				{Name: "std", Type: "float", Required: false, Default: 1.0, Description: "Standard deviation (> 0)."},
				{Name: "min", Type: "float", Required: false, Description: "Optional lower clamp."},
				{Name: "max", Type: "float", Required: false, Description: "Optional upper clamp."},
			},
		},
		{
			Name:        synth.DistLogNormal,
			Description: "Log-normal samples with log-space mu and sigma.",
			AppliesTo:   []string{"numeric"},
			Params: []Param{
				{Name: "mu", Type: "float", Required: false, Default: 0.0, Description: "Log-space mean."},
				{Name: "sigma", Type: "float", Required: false, Default: 1.0, Description: "Log-space standard deviation (> 0)."},
			},
		},
		{
			Name:        synth.DistExponential,
			Description: "Exponential samples with rate parameter lambda.",
			AppliesTo:   []string{"numeric"},
			Params: []Param{
				{Name: "lambda", Type: "float", Required: false, Default: 1.0, Description: "Rate parameter (> 0); mean is 1/lambda."},
			},
		},
		{
			Name:        synth.DistPoisson,
			Description: "Poisson-distributed non-negative integers with mean lambda.",
			AppliesTo:   []string{"numeric"},
			Params: []Param{
				{Name: "lambda", Type: "float", Required: false, Default: 1.0, Description: "Rate parameter (> 0); equal to the mean."},
			},
		},
		{
			Name:        synth.DistPareto,
			Description: "Heavy-tailed Pareto samples; xm is the scale, alpha the shape.",
			AppliesTo:   []string{"numeric"},
			Params: []Param{
				{Name: "xm", Type: "float", Required: false, Default: 1.0, Description: "Scale (minimum value, > 0)."},
				{Name: "alpha", Type: "float", Required: false, Default: 1.5, Description: "Shape (> 0); larger alpha = lighter tail."},
			},
		},
		{
			Name:        synth.DistBernoulli,
			Description: "Bernoulli samples emitting 0 or 1 with probability p.",
			AppliesTo:   []string{"bool", "numeric"},
			Params: []Param{
				{Name: "p", Type: "float", Required: false, Default: 0.5, Description: "Probability of 1 in [0, 1]."},
			},
		},
		{
			Name:        synth.DistMonotonicFrom,
			Description: "Strictly increasing or decreasing integer counter; useful for synthetic primary keys.",
			AppliesTo:   []string{"numeric"},
			Params: []Param{
				{Name: "start", Type: "int", Required: false, Default: 0, Description: "First emitted value."},
				{Name: "step", Type: "int", Required: false, Default: 1, Description: "Increment per row (non-zero)."},
			},
		},
		{
			Name:        synth.DistWeightedCategorical,
			Description: "Discrete categorical samples drawn from values with optional weights.",
			AppliesTo:   []string{"categorical"},
			Params: []Param{
				{Name: "values", Type: "list", Required: true, Description: "List of dictionary values to draw from."},
				{Name: "weights", Type: "list", Required: false, Description: "Optional weight list matching values length; uniform when absent."},
			},
		},
		{
			Name:        synth.DistUniformDate,
			Description: "Uniform date samples in [start, end] (days-since-epoch internally).",
			AppliesTo:   []string{"date"},
			Params: []Param{
				{Name: "start", Type: "string", Required: true, Description: "ISO-8601 calendar date (YYYY-MM-DD)."},
				{Name: "end", Type: "string", Required: true, Description: "ISO-8601 calendar date (YYYY-MM-DD); must exceed start."},
			},
		},
		{
			Name:        synth.DistRegex,
			Description: "String samples generated from a Perl regex pattern; bounded repetition counts must be finite.",
			AppliesTo:   []string{"categorical"},
			Params: []Param{
				{Name: "pattern", Type: "string", Required: true, Description: "Regex source string (Perl/RE2 syntax)."},
			},
		},
		{
			Name:        synth.DistConstant,
			Description: "Emit a constant value on every row; useful for unit testing and placeholder fields.",
			AppliesTo:   []string{"any"},
			Params: []Param{
				{Name: "value", Type: "any", Required: true, Description: "Value to emit on every row (interpreted by field type)."},
			},
		},
	}
}
