package descriptor

// JoinCapability describes the pushdown hash-join endpoint surfaced
// by Request.Joins. The manifest carries one JoinCapability entry
// under Manifest.Join so LLM clients can detect the v1 envelope
// (inner-only, single-join) without inspecting the source.
type JoinCapability struct {
	// Name is the canonical identifier ("hash_join").
	Name string `json:"name"`

	// MaxJoinsPerRequest is the upper bound on Request.Joins length.
	// 1 today; lifts when multi-join chains land.
	MaxJoinsPerRequest int `json:"max_joins_per_request"`

	// Kinds lists supported JoinSpec.Kind values. "inner" today;
	// "left", "outer", "anti" land once the null-bitmap correctness
	// path is fully wired.
	Kinds []string `json:"kinds"`

	// SpillBytes reports the in-memory threshold beyond which the
	// build side would spill. Zero today (no spill — the build side
	// is always materialised in RAM); set when the spill path lands.
	SpillBytes int64 `json:"spill_bytes"`

	// SpillEnv names the environment variable that overrides the
	// spill threshold. Set when the spill path lands.
	SpillEnv string `json:"spill_env,omitempty"`

	// Limitations lists the v1 envelope notes for LLM clients.
	Limitations []string `json:"limitations"`
}

// joinCapability returns the canonical JoinCapability entry.
func joinCapability() JoinCapability {
	return JoinCapability{
		Name:               "hash_join",
		MaxJoinsPerRequest: 1,
		Kinds:              []string{"inner"},
		SpillBytes:         0,
		Limitations: []string{
			"v1: exactly one JoinSpec per Request — PULSE_JOIN_TOO_MANY otherwise.",
			"v1: inner join only — left/outer/anti return PULSE_JOIN_KIND_NOT_IMPLEMENTED.",
			"v1: no spill — the right (build) side is fully materialised in memory.",
			"v1: build side is always the spec.Right cohort; smaller-side detection arrives in a follow-up.",
			"Per-OnPair type compatibility: identical types match, categorical_* match each other, the unsigned-int + float + date family is interchangeable. decimal128 keys reject cross-type comparisons.",
			"Joined records flow through the standard processor pipeline (filter / attribute / group / aggregator).",
		},
	}
}
