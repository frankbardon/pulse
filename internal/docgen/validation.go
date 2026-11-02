package docgen

import (
	_ "embed"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// validation.go renders the reference-validation record: which external
// implementation each inferential operator is pinned to, at what
// tolerance, and by which test file. It is the human-facing answer to
// "why should I trust this number?" and renders two ways: a
// "Validated against" list in every operator's catalog detail block, and
// the validation.md summary page.
//
// The registry itself, and its gates, live in internal/descriptor
// (validation_registry.go), shared with Explain's operator mode so the
// book and the engine surface render one record.

//go:embed validation_intro.md
var validationIntro string

const (
	validationPage    = "validation.md"
	validationIntroID = "validation-intro"
)

// validationFor returns name's record as this instance shows it (the
// same view Explain's operator mode returns): weighted checks and the
// weight refusal are dropped when weighting is hidden. ok is false when
// the operator has no record (an extension, or a descriptive operator
// outside the registry's scope).
func (g *gen) validationFor(name string) (descriptor.OperatorValidation, bool) {
	return g.inst.Validation(name)
}

// renderValidationDetail writes the "Validated against" list of a
// catalog detail block; nothing for an operator with no record.
func (g *gen) renderValidationDetail(b *strings.Builder, name string) {
	v, ok := g.validationFor(name)
	if !ok {
		return
	}
	var items []string
	for _, ref := range v.References {
		items = append(items, refLine(ref))
	}
	if v.WeightRefusal != nil {
		items = append(items, weightRefusalLine(v.WeightRefusal))
	}
	if len(items) == 0 {
		items = append(items, "No external reference yet.")
	}
	writeList(b, "Validated against", items)
	if v.Gap != "" {
		b.WriteString("\n**Validation gap:** ")
		b.WriteString(v.Gap)
		b.WriteString("\n")
	}
	b.WriteString("\nSee [How Pulse is validated](../" + validationPage + ").\n")
}

// weightRefusalLine states a pinned permanent weight refusal: the
// operator has no weighted form, so a weight is refused rather than
// validated, for the reason the refusal itself carries.
func weightRefusalLine(w *descriptor.ValidationWeightRefusal) string {
	return "Weighted analysis: refused with `" + w.Code + "` at predict and at run, never run unweighted, because " +
		w.Reason + "; " + strings.Join(quoteTests(w.Tests), ", ")
}

func refLine(ref descriptor.ValidationReference) string {
	return ref.Against + " — tolerance " + ref.Tolerance + "; " + strings.Join(quoteTests(ref.Tests), ", ")
}

func quoteTests(tests []string) []string {
	out := make([]string, len(tests))
	for i, t := range tests {
		out[i] = "`" + t + "`"
	}
	return out
}

// renderValidation renders validation.md: the method (preserved prose)
// and one summary row per visible operator with a record, grouped by
// catalog category.
func (g *gen) renderValidation(cats []category) string {
	var b strings.Builder
	b.WriteString("# How Pulse is validated\n\n")
	writePreserved(&b, validationIntroID, validationIntro)
	for _, c := range cats {
		var rows []entry
		for _, e := range c.ops {
			if _, ok := g.validationFor(e.name); ok {
				rows = append(rows, e)
			}
		}
		if len(rows) == 0 {
			continue
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
		b.WriteString("\n## ")
		b.WriteString(c.title)
		b.WriteString("\n\n| Operator | Validated against | Tightest tolerance | Gap |\n|---|---|---|---|\n")
		for _, e := range rows {
			v, _ := g.validationFor(e.name)
			var against []string
			tightest := emptyCell
			for i, ref := range v.References {
				if ref.Weighted {
					against = append(against, "weighted reference (see above)")
				} else {
					against = append(against, ref.Against)
				}
				if i == 0 || tolRank(ref.Tolerance) < tolRank(tightest) {
					tightest = ref.Tolerance
				}
			}
			if v.WeightRefusal != nil {
				against = append(against, "weights refused, not validated (see the catalog entry)")
			}
			if len(against) == 0 {
				against = []string{"No external reference yet."}
			}
			b.WriteString("| [`")
			b.WriteString(e.name)
			b.WriteString("`](")
			b.WriteString(catalogPath(c.key))
			b.WriteString("#")
			b.WriteString(operatorAnchor(e.name))
			b.WriteString(") | ")
			b.WriteString(cell(strings.Join(against, "<br>")))
			b.WriteString(" | ")
			b.WriteString(cell(tightest))
			b.WriteString(" | ")
			b.WriteString(cell(v.Gap))
			b.WriteString(" |\n")
		}
	}
	g.renderStRD(&b)
	return b.String()
}

// renderStRD writes the NIST StRD table from the embedded record: one
// row per dataset whose operator the instance offers.
func (g *gen) renderStRD(b *strings.Builder) {
	var out strings.Builder
	family := ""
	for _, r := range descx.StRDResults() {
		fam, set, _ := strings.Cut(r.Dataset, "/")
		title, famOps, _ := descx.StRDFamily(fam)
		var ops []string
		for _, op := range famOps {
			if g.operatorVisible(op) {
				ops = append(ops, "`"+op+"`")
			}
		}
		if len(ops) == 0 {
			continue
		}
		if fam != family {
			family = fam
			out.WriteString("\n### " + strings.ToUpper(title[:1]) + title[1:] + " (" + strings.Join(ops, ", ") + ")\n\n| Dataset | NIST difficulty | Fewest digits | Figures (digits) | Note |\n|---|---|---|---|---|\n")
		}
		diff := r.Difficulty
		if diff == "" {
			diff = "not stated"
		}
		least := emptyCell
		if r.Min != nil {
			least = strconv.FormatFloat(*r.Min, 'f', 1, 64)
		}
		keys := make([]string, 0, len(r.Digits))
		for k := range r.Digits {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		figs := make([]string, len(keys))
		for i, k := range keys {
			figs[i] = "`" + k + "` " + strconv.FormatFloat(r.Digits[k], 'f', 1, 64)
		}
		out.WriteString("| " + set + " | " + diff + " | " + least + " | " + cell(strings.Join(figs, ", ")) + " | " + cell(r.NotOffered) + " |\n")
	}
	if out.Len() == 0 {
		return
	}
	b.WriteString("\n## NIST StRD certification\n\n")
	b.WriteString("The [NIST Statistical Reference Datasets](https://www.itl.nist.gov/div898/strd/) carry certified values to 15 significant digits and are the accuracy benchmark statistical packages are measured against. ")
	b.WriteString("Each dataset runs end to end through Pulse; a figure's *digits* are its log relative error against the certified value, LRE = −log10(|x − c| / |c|), floored to 0.1 and capped at 15. ")
	b.WriteString("Roughly, 15 is exact in double precision, 7 or more is accurate for any practical use, and under 4 means the figure is unreliable on that data. ")
	b.WriteString("Lower- and average-difficulty sets must reach 7 digits on every figure; higher-difficulty sets are published as achieved. ")
	b.WriteString("These numbers are the record the tests assert (`strd_reference_test.go`), so any change in accuracy fails the build until it is reviewed.\n")
	b.WriteString(out.String())
}

// tolRank orders tolerance strings by their leading 1e-N exponent
// (tighter first); a string without one ranks last.
func tolRank(s string) int {
	i := strings.Index(s, "1e-")
	if i < 0 {
		return 1 << 20
	}
	n := 0
	for _, ch := range s[i+3:] {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int(ch-'0')
	}
	return -n
}
