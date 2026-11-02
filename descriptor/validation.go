package descriptor

// OperatorValidation is the external evidence behind one operator's
// figures, as Explain's operator mode returns it (ExplainResult.Validation):
// the reference implementations its figures are pinned to, a known gap
// in plain words, the pinned weight refusal of an operator with no
// weighted form, and the NIST StRD certification digits where a
// certified dataset checks it. It is the record the book's "How Pulse
// is validated" page renders, never a separate source, and it is pulled
// on request only: no manifest or default payload carries it.
//
// References is always an array; it is empty for an operator whose
// record declares only a Gap. On an instance that does not offer
// weighting, weighted references and the weight refusal are absent.
type OperatorValidation struct {
	References    []ValidationReference     `json:"references"`
	Gap           string                    `json:"gap,omitempty"`
	WeightRefusal *ValidationWeightRefusal  `json:"weight_refusal,omitempty"`
	Certification []ValidationCertification `json:"certification,omitempty"`
}

// ValidationReference is one external check: the implementation and
// its pinned version (Against), the tolerance the tests apply, the
// repository-relative test files that hold the check, and whether it is
// a weighted-reference check.
type ValidationReference struct {
	Against   string   `json:"against"`
	Tolerance string   `json:"tolerance"`
	Tests     []string `json:"tests"`
	Weighted  bool     `json:"weighted,omitempty"`
}

// ValidationWeightRefusal states that a weight on the operator is
// refused, at predict and at run, rather than validated: Code is the
// refusal's code (PULSE_WEIGHT_UNSUPPORTED), Reason the permanent
// reason the refusal itself carries in details.reason, and Tests the
// test files pinning it.
type ValidationWeightRefusal struct {
	Code   string   `json:"code"`
	Reason string   `json:"reason"`
	Tests  []string `json:"tests"`
}

// ValidationCertification is one NIST StRD dataset run end to end
// through the operator. Family is the StRD suite ("linear regression",
// "one-way ANOVA", "univariate summary statistics"), Difficulty NIST's
// rating ("lower", "average", "higher"; empty where NIST states none).
// Digits holds the log relative error of each figure the operator
// produces against the certified value, LRE = −log10(|x − c| / |c|),
// floored to 0.1 and capped at 15; MinDigits is the smallest of them.
type ValidationCertification struct {
	Family     string             `json:"family"`
	Dataset    string             `json:"dataset"`
	Difficulty string             `json:"difficulty,omitempty"`
	Digits     map[string]float64 `json:"digits"`
	MinDigits  float64            `json:"min_digits"`
}
