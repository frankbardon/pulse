package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
)

// The intent taxonomy: a small, closed set of question kinds, coarse
// enough that a non-statistician recognises one in their own question.
// Twelve are analytic (answered by operators); three route to tooling
// rather than operators (Analytic false). The taxonomy is static — it
// is not a feature, so a feature profile never prunes it.
const (
	IntentDescribe          = "describe"
	IntentCompareGroups     = "compare_groups"
	IntentRelationship      = "relationship"
	IntentDrivers           = "drivers"
	IntentChangeOverTime    = "change_over_time"
	IntentComposition       = "composition"
	IntentBenchmark         = "benchmark"
	IntentDistributionShape = "distribution_shape"
	IntentSegment           = "segment"
	IntentMeasureConstruct  = "measure_construct"
	IntentFlows             = "flows"
	IntentDataQuality       = "data_quality"
	IntentPrepare           = "prepare"
	IntentSimulate          = "simulate"
	IntentLookup            = "lookup"
)

// Kind groupings the shapes below are written in.
var (
	kNum        = []descriptor.FieldKind{descriptor.FieldKindNumeric}
	kCat        = []descriptor.FieldKind{descriptor.FieldKindCategorical, descriptor.FieldKindBool}
	kDate       = []descriptor.FieldKind{descriptor.FieldKindDate}
	kOutcome    = []descriptor.FieldKind{descriptor.FieldKindNumeric, descriptor.FieldKindCategorical, descriptor.FieldKindBool}
	kMix        = []descriptor.FieldKind{descriptor.FieldKindCategorical, descriptor.FieldKindBool, descriptor.FieldKindSet}
	kKey        = []descriptor.FieldKind{descriptor.FieldKindNumeric, descriptor.FieldKindCategorical, descriptor.FieldKindDate}
	kAny        = []descriptor.FieldKind{descriptor.FieldKindNumeric, descriptor.FieldKindCategorical, descriptor.FieldKindDate, descriptor.FieldKindBool, descriptor.FieldKindSet}
	kBenchGroup = []descriptor.FieldKind{descriptor.FieldKindCategorical, descriptor.FieldKindBool, descriptor.FieldKindDate}
)

func role(name string, minN, maxN int, kinds []descriptor.FieldKind) descriptor.Role {
	return descriptor.Role{Name: name, Kinds: kinds, Min: minN, Max: maxN}
}

func shape(roles ...descriptor.Role) descriptor.Shape {
	return descriptor.Shape{Roles: roles}
}

const unbounded = descriptor.RoleUnbounded

// intentRegistry is the taxonomy in declaration order. Read it through
// Intents / IntentIDs, which hand out copies.
var intentRegistry = []descriptor.Intent{
	{
		ID: IntentDescribe, Label: "Describe a measure", Analytic: true,
		Sounds: []string{"What's the typical value of X?", "What's the total X?", "How spread out is X?"},
		Shapes: []descriptor.Shape{
			shape(role("measure", 1, unbounded, kNum)),
			shape(role("measure", 1, unbounded, kMix)),
		},
	},
	{
		ID: IntentCompareGroups, Label: "Compare groups", Analytic: true,
		Sounds: []string{"Do A and B differ?", "Is this segment different from the rest?"},
		Shapes: []descriptor.Shape{
			shape(role("outcome", 1, 1, kOutcome), role("group", 1, 1, kCat)),
		},
	},
	{
		ID: IntentRelationship, Label: "Relationship between measures", Analytic: true,
		Sounds: []string{"Do X and Y move together?", "Is X associated with Y?"},
		Shapes: []descriptor.Shape{
			shape(role("measures", 2, unbounded, kNum)),
			shape(role("categories", 2, 2, kCat)),
		},
	},
	{
		ID: IntentDrivers, Label: "Find drivers", Analytic: true,
		Sounds: []string{"What explains Y?", "Which factors matter most for Y?"},
		Shapes: []descriptor.Shape{
			shape(role("outcome", 1, 1, kOutcome), role("predictors", 1, unbounded, kOutcome)),
		},
	},
	{
		ID: IntentChangeOverTime, Label: "Change over time", Analytic: true,
		Sounds: []string{"Is it going up?", "How does this month compare to last?"},
		Shapes: []descriptor.Shape{
			shape(role("time", 1, 1, kDate), role("measure", 1, unbounded, kOutcome)),
		},
	},
	{
		ID: IntentComposition, Label: "Mix and share", Analytic: true,
		Sounds: []string{"What's the mix?", "What share does each option have?", "Which attributes go with which brand?"},
		Shapes: []descriptor.Shape{
			shape(role("category", 1, 1, kMix), role("by", 0, 1, kCat)),
		},
	},
	{
		ID: IntentBenchmark, Label: "Benchmark against a reference", Analytic: true,
		Sounds: []string{"How does this compare to the total?", "Is this group above or below the population?", "How does this wave compare to the last?"},
		Shapes: []descriptor.Shape{
			shape(role("measure", 1, unbounded, kOutcome), role("group", 1, 1, kBenchGroup)),
		},
	},
	{
		ID: IntentDistributionShape, Label: "Distribution shape", Analytic: true,
		Sounds: []string{"Is it normally distributed?", "Are there outliers?", "Is it skewed?"},
		Shapes: []descriptor.Shape{
			shape(role("measure", 1, unbounded, kNum)),
		},
	},
	{
		ID: IntentSegment, Label: "Segment into groups", Analytic: true,
		Sounds: []string{"Are there natural groups of customers?", "Split this into tiers."},
		Shapes: []descriptor.Shape{
			shape(role("measures", 1, unbounded, kNum)),
		},
	},
	{
		ID: IntentMeasureConstruct, Label: "Measure a construct", Analytic: true,
		Sounds: []string{"Do these questions measure one thing?", "Combine these items into a score."},
		Shapes: []descriptor.Shape{
			shape(role("items", 2, unbounded, kNum)),
		},
	},
	{
		ID: IntentFlows, Label: "Flows between states", Analytic: true,
		Sounds: []string{"Where do customers move between states?", "Who switched from A to B?"},
		Shapes: []descriptor.Shape{
			shape(role("from", 1, 1, kCat), role("to", 1, 1, kCat)),
		},
	},
	{
		ID: IntentDataQuality, Label: "Data quality", Analytic: true,
		Sounds: []string{"Is this data trustworthy?", "Who answered carelessly?", "How much is missing?"},
		Shapes: []descriptor.Shape{
			shape(role("fields", 1, unbounded, kAny)),
		},
	},
	{
		ID: IntentPrepare, Label: "Prepare data", Analytic: false,
		Sounds: []string{"Keep only last year's rows.", "Derive a new column from these fields."},
		Shapes: []descriptor.Shape{
			shape(role("fields", 1, unbounded, kAny)),
		},
	},
	{
		ID: IntentSimulate, Label: "Simulate data", Analytic: false,
		Sounds: []string{"Generate a synthetic copy of this cohort.", "Make test data with this schema."},
		Shapes: []descriptor.Shape{
			shape(role("fields", 1, unbounded, kAny)),
		},
	},
	{
		ID: IntentLookup, Label: "Look up records", Analytic: false,
		Sounds: []string{"Show me the record for this ID.", "Fetch the rows for this key."},
		Shapes: []descriptor.Shape{
			shape(role("key", 1, unbounded, kKey)),
		},
	},
}

// Intents returns the intent taxonomy in declaration order. The result
// is a deep copy: callers may mutate it freely.
func Intents() []descriptor.Intent {
	out := make([]descriptor.Intent, len(intentRegistry))
	for i, in := range intentRegistry {
		in.Sounds = append([]string(nil), in.Sounds...)
		shapes := make([]descriptor.Shape, len(in.Shapes))
		for j, s := range in.Shapes {
			roles := make([]descriptor.Role, len(s.Roles))
			for k, r := range s.Roles {
				r.Kinds = append([]descriptor.FieldKind(nil), r.Kinds...)
				roles[k] = r
			}
			shapes[j] = descriptor.Shape{Roles: roles}
		}
		in.Shapes = shapes
		out[i] = in
	}
	return out
}

// IntentIDs returns the sorted intent IDs — the manifest's top-level
// intents list.
func IntentIDs() []string {
	out := make([]string, len(intentRegistry))
	for i, in := range intentRegistry {
		out[i] = in.ID
	}
	sort.Strings(out)
	return out
}

// IsIntent reports whether id names an intent in the taxonomy.
func IsIntent(id string) bool {
	for _, in := range intentRegistry {
		if in.ID == id {
			return true
		}
	}
	return false
}

// FieldKindOf maps a cohort field type to the coarse kind intent shapes
// are written in. ok is false only for an unknown type byte; every
// registered field type maps (TestFieldKindOf_Total walks them all).
func FieldKindOf(ft encoding.FieldType) (kind descriptor.FieldKind, ok bool) {
	switch ft {
	case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16,
		encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeF32, encoding.FieldTypeF64, encoding.FieldTypeDecimal128:
		return descriptor.FieldKindNumeric, true
	case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
		return descriptor.FieldKindCategorical, true
	case encoding.FieldTypeDate, encoding.FieldTypeDateTime:
		return descriptor.FieldKindDate, true
	case encoding.FieldTypePackedBool:
		return descriptor.FieldKindBool, true
	case encoding.FieldTypeSetU8, encoding.FieldTypeSetU16, encoding.FieldTypeSetU32,
		encoding.FieldTypeSetU64, encoding.FieldTypeSetU128, encoding.FieldTypeSetU256:
		return descriptor.FieldKindSet, true
	}
	return "", false
}

// isFieldKind reports whether k is one of the five coarse field kinds.
func isFieldKind(k descriptor.FieldKind) bool {
	switch k {
	case descriptor.FieldKindNumeric, descriptor.FieldKindCategorical,
		descriptor.FieldKindDate, descriptor.FieldKindBool, descriptor.FieldKindSet:
		return true
	}
	return false
}
