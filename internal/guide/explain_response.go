package guide

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// readAlpha is the level a regression's and an uncorrected overlay's
// p-values are read against: neither carries an alpha of its own, and
// 0.05 is the shared p-value reading's default. A caveat names it
// whenever it is used, so the reading is never silently assumed.
const readAlpha = 0.05

// Verdict classes: what a significant result is evidence of.
const (
	classDifference  = "difference"
	classAssociation = "association"
)

// testClass names, per built-in TEST_* family, what its p-value is
// evidence of. TestTestClassComplete holds it to types.AllTestTypes(),
// so a new family fails until it is classed. An extension test is
// classed from its Purpose intents (classFromIntents).
var testClass = map[string]string{
	"TEST_ANOVA_F":        classDifference,
	"TEST_ANOVA_RM":       classDifference,
	"TEST_ANOVA_WELCH":    classDifference,
	"TEST_BROWN_FORSYTHE": classDifference,
	"TEST_CHISQ":          classAssociation,
	"TEST_FISHER_EXACT":   classAssociation,
	"TEST_KENDALL_TAU":    classAssociation,
	"TEST_KRUSKAL_WALLIS": classDifference,
	"TEST_KS":             classDifference,
	"TEST_MANN_WHITNEY_U": classDifference,
	"TEST_PAIRED_T":       classDifference,
	"TEST_PEARSON_R":      classAssociation,
	"TEST_PROP_Z":         classDifference,
	"TEST_SHAPIRO_WILK":   classDifference,
	"TEST_SPEARMAN_R":     classAssociation,
	"TEST_T":              classDifference,
	"TEST_TREND":          classAssociation,
	"TEST_TUKEY_HSD":      classDifference,
	"TEST_WELCH":          classDifference,
	"TEST_WILCOXON_SR":    classDifference,
	"TEST_Z_TWO_SAMPLE":   classDifference,
}

// overlayClass is testClass for the Inferential overlay kinds
// (TestOverlayClassComplete holds it to the manifest's Inferential
// flags).
var overlayClass = map[string]string{
	"OVERLAY_CHISQ_COL":                     classAssociation,
	"OVERLAY_CHISQ_MATRIX":                  classAssociation,
	"OVERLAY_CHISQ_ROW":                     classAssociation,
	"OVERLAY_CHISQ_VS_POP":                  classDifference,
	"OVERLAY_CHISQ_VS_REF":                  classDifference,
	"OVERLAY_FISHER_EXACT_CELL":             classAssociation,
	"OVERLAY_KS_VS_POP":                     classDifference,
	"OVERLAY_PAIRWISE_PROBIT_T":             classDifference,
	"OVERLAY_PAIRWISE_PROP_Z":               classDifference,
	"OVERLAY_PAIRWISE_TWO_MEANS_Z":          classDifference,
	"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z": classDifference,
	"OVERLAY_PAIRWISE_WELCH_T":              classDifference,
	"OVERLAY_PROP_Z_CELL":                   classDifference,
	"OVERLAY_PROP_Z_PANEL":                  classDifference,
	"OVERLAY_T_CELL":                        classDifference,
	"OVERLAY_T_VS_REF":                      classDifference,
	"OVERLAY_Z_CELL":                        classDifference,
	"OVERLAY_Z_VS_REF":                      classDifference,
}

// selfControlled tests control their own family-wise error (Tukey's
// HSD adjusts every pair), so they never count toward the many-tests
// caveat.
var selfControlled = map[string]bool{"TEST_TUKEY_HSD": true}

// interceptKey is the coefficient key every REG_* type uses for the
// intercept, which is not a predictor and gets no finding.
const interceptKey = "(intercept)"

// responseReader carries the response-mode state beside the explainer.
type responseReader struct {
	uncorrected  int  // uncorrected p-values from tests and overlays
	inferential  int  // findings with an evidence verdict, either way
	evidence     int  // of those, findings with evidence
	regressionsP bool // a regression p-value was read against readAlpha
	overlayAlpha []string
	partial      bool     // aggregations were read without their request
	notes        []string // full-detail sentences, one per finding or fact
	later        []string // per-result caveats, emitted after the root-wide ones
}

// explainResponse reads resp, with req the request that produced it
// (nil when absent), into findings.
func (e *explainer) explainResponse(resp *types.Response, req *types.Request) {
	rr := &responseReader{}
	parts := e.readResponse(rr, resp, req)
	e.slotCaveats(rr, resp, req)
	e.finalCaveats(rr, "request")
	e.res.Summary = "This response reports " + partsPhrase(parts) + recordsPhrase(resp) + rr.evidencePhrase() + "."
	e.notes = rr.notes
}

// readResponse reads one Process response into findings — under the
// explainer's scope and path prefix when it is a slot or stage of a
// larger result — and returns the summary phrases counting its parts.
func (e *explainer) readResponse(rr *responseReader, resp *types.Response, req *types.Request) []string {
	var parts []string
	n := 0
	for i, t := range resp.Tests {
		if t == nil {
			continue
		}
		n++
		var spec *types.Test
		if req != nil && i < len(req.Tests) {
			spec = req.Tests[i]
		}
		e.testFinding(rr, roleTest, i+1, t, spec)
	}
	if n > 0 {
		parts = append(parts, count(n, "test", "tests"))
	}
	n = 0
	for i, t := range resp.PostTests {
		if t == nil {
			continue
		}
		n++
		var spec *types.Test
		if req != nil && i < len(req.PostTests) {
			spec = req.PostTests[i]
		}
		e.testFinding(rr, rolePostTest, i+1, t, spec)
	}
	if n > 0 {
		parts = append(parts, count(n, "post-test", "post-tests"))
	}
	n = 0
	for i, g := range resp.Regressions {
		if g == nil {
			continue
		}
		n++
		var spec *types.RegressionSpec
		if req != nil && i < len(req.Regressions) {
			spec = req.Regressions[i]
		}
		e.regressionFindings(rr, i+1, g, spec)
	}
	if n > 0 {
		parts = append(parts, count(n, "regression", "regressions"))
	}
	for i := range resp.Overlays {
		e.overlayFinding(rr, i+1, &resp.Overlays[i])
	}
	if len(resp.Overlays) > 0 {
		parts = append(parts, count(len(resp.Overlays), "overlay", "overlays"))
	}
	for i := range resp.Matrices {
		e.matrixFinding(rr, i, &resp.Matrices[i], resp.Components)
	}
	if len(resp.Matrices) > 0 {
		parts = append(parts, count(len(resp.Matrices), "matrix result", "matrix results"))
	}
	if p := e.aggregationFindings(rr, resp, req); p != "" {
		parts = append(parts, p)
	}
	e.runNotes(rr, resp)
	return parts
}

// partsPhrase joins a result's part phrases ("no analysis result" when
// it has none).
func partsPhrase(parts []string) string {
	if len(parts) == 0 {
		return "no analysis result"
	}
	return andList(parts)
}

// recordsPhrase is " from F of T records" when the response carries its
// run counts, "" otherwise.
func recordsPhrase(resp *types.Response) string {
	if total, filtered, ok := runCounts(resp); ok {
		return " from " + strconv.FormatInt(filtered, 10) + " of " + strconv.FormatInt(total, 10) + " records"
	}
	return ""
}

// evidencePhrase counts the inferential findings that show evidence
// ("; 1 of 2 inferential findings show evidence at their alpha"), ""
// when there are none.
func (rr *responseReader) evidencePhrase() string {
	if rr.inferential == 0 {
		return ""
	}
	return "; " + strconv.Itoa(rr.evidence) + " of " + count(rr.inferential, "inferential finding shows evidence at its alpha",
		"inferential findings show evidence at their alpha")
}

// testFinding reads one test or post-test result.
func (e *explainer) testFinding(rr *responseReader, r *role, n int, t *types.TestResult, spec *types.Test) {
	op := string(t.Type)
	shown := e.shownOp(op)
	f := descriptor.ExplainFinding{Slot: e.at + slotPath(r, n), Operator: shown, Numbers: map[string]*float64{}}
	subjectWords := ""
	switch {
	case spec != nil:
		in := testStep("", r, n, spec)
		f.Subject = strings.TrimPrefix(in.detail, " on ")
		subjectWords = in.detail
	case t.Label != "":
		f.Subject = t.Label
		subjectWords = " labelled " + tick(t.Label)
	default:
		f.Subject = f.Slot
	}
	if f.Subject == "" {
		f.Subject = f.Slot
	}
	setNum(f.Numbers, "statistic", t.Statistic)
	setNum(f.Numbers, "p_value", t.PValue)
	if t.DF != 0 {
		setNum(f.Numbers, "df", t.DF)
	}
	if t.Alpha > 0 {
		setNum(f.Numbers, "alpha", t.Alpha)
	}
	if t.PAdjusted != nil {
		setNum(f.Numbers, "p_adjusted", *t.PAdjusted)
	}
	effects := effectSizes(t.Details)
	for _, k := range sortedKeys(effects) {
		setNum(f.Numbers, "details.effect_size."+k, effects[k])
	}

	class := e.classOf(op, testClass)
	lead := e.lead(stepIn{role: r, n: n}) + opParen(shown) + subjectWords
	// A p-value that came back undefined is NaN in Go; through JSON it
	// is null, which decodes to 0 with reject_null false — a pair no
	// defined p-value produces (0 is below every alpha).
	undefined := math.IsNaN(t.PValue) || (t.PValue == 0 && !t.RejectNull && t.Alpha > 0)
	var text string
	switch {
	case undefined:
		f.Verdict = descriptor.VerdictNotComputable
		f.Numbers["p_value"] = nil
		text = lead + " has an undefined p-value, so no verdict is read from it."
	case t.Multiplicity != nil:
		m := t.Multiplicity
		f.Multiplicity = &descriptor.ExplainMultiplicity{Method: string(m.Method), Family: string(m.Family), M: m.M}
		if t.SignificantAdjusted == nil {
			f.Verdict = descriptor.VerdictNotComputable
			text = lead + " has an undefined adjusted p-value, so no verdict is read from it."
			break
		}
		f.Verdict = verdictOf(class, *t.SignificantAdjusted)
		text = lead + " finds " + verdictWords(f.Verdict) + " at alpha " + fmtNum(t.Alpha) + " after the " + string(m.Method) +
			" adjustment over its " + string(m.Family) + " family of " + strconv.Itoa(m.M) + " (adjusted p = " + fmtPtr(t.PAdjusted) +
			", raw p = " + fmtNum(t.PValue) + ")."
	default:
		evidence := t.RejectNull
		if t.Alpha > 0 {
			evidence = t.PValue < t.Alpha
		}
		f.Verdict = verdictOf(class, evidence)
		text = lead + " finds " + verdictWords(f.Verdict)
		if t.Alpha > 0 {
			text += " at alpha " + fmtNum(t.Alpha)
		}
		text += " (p = " + fmtNum(t.PValue) + ")."
	}
	if f.Verdict != descriptor.VerdictNotComputable {
		rr.inferential++
		if isEvidence(f.Verdict) {
			rr.evidence++
		}
		if t.Multiplicity == nil && !selfControlled[op] {
			rr.uncorrected++
		}
	}
	values := map[string]float64{"statistic": t.Statistic}
	paths := []string{}
	for _, k := range e.effectOrder(op, effects) {
		values["details.effect_size."+k] = effects[k]
		paths = append(paths, "details.effect_size."+k)
	}
	paths = append(paths, "statistic")
	bandText, banded := e.band(&f, op, paths, values)
	text += bandText
	for _, k := range e.effectOrder(op, effects) {
		if "details.effect_size."+k == banded {
			continue
		}
		if v := effects[k]; finite(v) {
			text += " Its effect size " + tick(k) + " is " + fmtNum(v) + "."
		}
	}
	if shown != "" {
		e.noteOp(op, true)
	}
	e.addFinding(rr, f, text)
}

// regressionFindings reads one regression: a model finding, then one
// finding per predictor coefficient.
func (e *explainer) regressionFindings(rr *responseReader, n int, g *types.RegressionResult, spec *types.RegressionSpec) {
	op := string(g.Type)
	shown := e.shownOp(op)
	subject := g.Name
	words := ""
	if spec != nil {
		if d := strings.TrimPrefix(predicting(spec.Target, spec.Predictors), " "); d != "" {
			subject = d
			words = " " + d
		}
	} else if g.Name != "" {
		words = " labelled " + tick(g.Name)
	}
	slot := e.at + "regressions[" + strconv.Itoa(n-1) + "]"
	if subject == "" {
		subject = slot
	}
	lead := e.lead(stepIn{role: roleRegression, n: n}) + opParen(shown) + words
	model := descriptor.ExplainFinding{Slot: slot, Subject: subject, Operator: shown, Verdict: descriptor.VerdictDescriptive, Numbers: map[string]*float64{}}
	fit := map[string]float64{}
	for k, v := range map[string]float64{"r2": g.R2, "adj_r2": g.AdjR2, "pseudo_r2": g.PseudoR2, "deviance": g.Deviance,
		"null_deviance": g.NullDeviance, "residual_std_err": g.ResidualStdErr, "sum_weights": g.SumWeights, "n_eff": g.NEff} {
		if v != 0 {
			fit[k] = v
			setNum(model.Numbers, k, v)
		}
	}
	if g.NObs > 0 {
		setNum(model.Numbers, "n_obs", float64(g.NObs))
	}
	text := lead + " is fitted"
	if g.NObs > 0 {
		text += " on " + count(g.NObs, "row", "rows")
	}
	text += "."
	var fitKeys []string
	for _, k := range []string{"r2", "adj_r2", "pseudo_r2"} {
		if _, ok := fit[k]; ok {
			fitKeys = append(fitKeys, k)
		}
	}
	bandText, _ := e.band(&model, op, fitKeys, fit)
	text += bandText
	if shown != "" {
		e.noteOp(op, true)
	}
	e.addFinding(rr, model, text)

	for _, k := range sortedKeys(g.Coefficients) {
		if k == interceptKey {
			continue
		}
		f := descriptor.ExplainFinding{Slot: slot + ".coefficients." + k, Subject: subject + ": " + tick(k), Operator: shown, Numbers: map[string]*float64{}}
		setNum(f.Numbers, "coefficient", g.Coefficients[k])
		if se, ok := g.StdErrors[k]; ok {
			setNum(f.Numbers, "std_error", se)
		}
		clead := "In " + strings.ToLower(lead[:1]) + lead[1:] + ", the coefficient of " + tick(k)
		p, hasP := g.PValues[k]
		switch {
		case !hasP:
			f.Verdict = descriptive(g.Coefficients[k])
			text = clead + " is " + fmtNum(g.Coefficients[k]) + "; the model reports no p-value for it."
		case !finite(p):
			setNum(f.Numbers, "p_value", p)
			f.Verdict = descriptor.VerdictNotComputable
			text = clead + " has an undefined p-value, so no verdict is read from it."
		default:
			setNum(f.Numbers, "p_value", p)
			setNum(f.Numbers, "alpha", readAlpha)
			rr.regressionsP = true
			if p < readAlpha {
				f.Verdict = descriptor.VerdictEvidenceOfEffect
				text = clead + " is " + fmtNum(g.Coefficients[k]) + ": evidence that it differs from 0 at alpha " + fmtNum(readAlpha) +
					" (raw p = " + fmtNum(p) + ")."
			} else {
				f.Verdict = descriptor.VerdictNoEvidenceOfEffect
				text = clead + " is " + fmtNum(g.Coefficients[k]) + ": no evidence that it differs from 0 at alpha " + fmtNum(readAlpha) +
					" (raw p = " + fmtNum(p) + ")."
			}
			rr.inferential++
			if f.Verdict == descriptor.VerdictEvidenceOfEffect {
				rr.evidence++
			}
		}
		e.addFinding(rr, f, text)
	}
}

// descriptive is the verdict of a figure with no p-value: descriptive,
// or not_computable when the figure itself is undefined.
func descriptive(v float64) descriptor.Verdict {
	if finite(v) {
		return descriptor.VerdictDescriptive
	}
	return descriptor.VerdictNotComputable
}

// overlayFinding reads one overlay layer. An inferential kind's
// p-values sit where its Interpretation's p-value field says: one
// p-value (a summary or scalar slot) or one per cell / series entry.
func (e *explainer) overlayFinding(rr *responseReader, n int, l *types.OverlayLayer) {
	op := string(l.Kind)
	shown := e.shownOp(op)
	slot := e.at + "overlays[" + strconv.Itoa(n-1) + "]"
	subject := l.Name
	words := ""
	if subject != "" {
		words = " " + tick(subject)
	} else {
		subject = slot
	}
	f := descriptor.ExplainFinding{Slot: slot, Subject: subject, Operator: shown, Numbers: map[string]*float64{}}
	lead := e.lead(stepIn{role: roleOverlay, n: n}) + opParen(shown) + words
	e.layerWarnings(rr, lead, l)
	pField := ""
	for _, in := range e.interpretationsOf(op) {
		if in.Shared == descx.SharedPValue {
			pField = in.Field
			break
		}
	}
	if shown != "" {
		e.noteOp(op, pField != "")
	}
	if pField == "" {
		f.Verdict = descriptor.VerdictDescriptive
		text := lead + " describes the host's values; it carries no p-value."
		if v := l.Payload.Scalar; v != nil {
			setNum(f.Numbers, "scalar", *v)
			f.Verdict = descriptive(*v)
		}
		if s := l.Summary; s != nil && s.Statistic != nil {
			setNum(f.Numbers, "summary.statistic", *s.Statistic)
		}
		e.addFinding(rr, f, text)
		return
	}
	alpha := readAlpha
	if m := l.Multiplicity; m != nil {
		f.Multiplicity = &descriptor.ExplainMultiplicity{Method: string(m.Method), Family: string(m.Family), M: m.M}
		if m.Alpha > 0 {
			alpha = m.Alpha
		}
	} else if !slices.Contains(rr.overlayAlpha, op) {
		rr.overlayAlpha = append(rr.overlayAlpha, op)
	}
	setNum(f.Numbers, "alpha", alpha)
	class := e.classOf(op, overlayClass)
	ps, sig := overlayPValues(l, pField)
	if len(ps) == 1 && pField != "cells.value" && l.Payload.Series == nil {
		p := ps[0]
		setNum(f.Numbers, pField, p)
		if s := l.Summary; s != nil && s.Statistic != nil && pField != "summary.statistic" {
			setNum(f.Numbers, "summary.statistic", *s.Statistic)
		}
		text := ""
		switch {
		case !finite(p):
			f.Verdict = descriptor.VerdictNotComputable
			text = lead + " has an undefined p-value, so no verdict is read from it."
		case l.Multiplicity != nil:
			if l.Summary == nil || l.Summary.SignificantAdjusted == nil {
				f.Verdict = descriptor.VerdictNotComputable
				text = lead + " has an undefined adjusted p-value, so no verdict is read from it."
				break
			}
			f.Verdict = verdictOf(class, *l.Summary.SignificantAdjusted)
			if l.Summary.PAdjusted != nil {
				setNum(f.Numbers, "summary.p_adjusted", *l.Summary.PAdjusted)
			}
			text = lead + " finds " + verdictWords(f.Verdict) + " at alpha " + fmtNum(alpha) + " after the " +
				string(l.Multiplicity.Method) + " adjustment over its " + string(l.Multiplicity.Family) + " family of " +
				strconv.Itoa(l.Multiplicity.M) + " (raw p = " + fmtNum(p) + ")."
		default:
			f.Verdict = verdictOf(class, p < alpha)
			text = lead + " finds " + verdictWords(f.Verdict) + " at alpha " + fmtNum(alpha) + " (p = " + fmtNum(p) + ")."
			rr.uncorrected++
		}
		if f.Verdict != descriptor.VerdictNotComputable {
			rr.inferential++
			if isEvidence(f.Verdict) {
				rr.evidence++
			}
		}
		e.addFinding(rr, f, text)
		return
	}
	// One p-value per cell or series entry.
	defined, below := 0, 0
	for i, p := range ps {
		if !finite(p) {
			continue
		}
		defined++
		if l.Multiplicity != nil {
			if i < len(sig) && sig[i] {
				below++
			}
		} else if p < alpha {
			below++
		}
	}
	setNum(f.Numbers, "tests", float64(defined))
	text := ""
	switch {
	case defined == 0:
		f.Verdict = descriptor.VerdictNotComputable
		text = lead + " has no defined p-value, so no verdict is read from it."
	case l.Multiplicity != nil:
		setNum(f.Numbers, "below_alpha_adjusted", float64(below))
		f.Verdict = verdictOf(class, below > 0)
		text = lead + " runs " + count(defined, "test", "tests") + "; " + ofThemBelow(below) + " alpha " + fmtNum(alpha) +
			" after the " + string(l.Multiplicity.Method) + " adjustment over its " + string(l.Multiplicity.Family) + " family."
	default:
		setNum(f.Numbers, "below_alpha", float64(below))
		f.Verdict = verdictOf(class, below > 0)
		text = lead + " runs " + count(defined, "test", "tests") + "; " + ofThemBelow(below) + " alpha " + fmtNum(alpha) + ", unadjusted."
		rr.uncorrected += defined
	}
	if f.Verdict != descriptor.VerdictNotComputable {
		rr.inferential++
		if isEvidence(f.Verdict) {
			rr.evidence++
		}
	}
	e.addFinding(rr, f, text)
}

// overlayPValues collects a layer's p-values from pField, plus each
// one's adjusted significance where a correction ran (cells and series
// entries; a single summary p reads its own summary).
func overlayPValues(l *types.OverlayLayer, pField string) (ps []float64, sig []bool) {
	if ser := l.Payload.Series; ser != nil && strings.HasPrefix(pField, "summary.") {
		for _, en := range ser.Entries {
			ps = append(ps, summaryValue(&en.Summary, pField))
			sig = append(sig, en.Summary.SignificantAdjusted != nil && *en.Summary.SignificantAdjusted)
		}
		return ps, sig
	}
	switch pField {
	case "cells.value":
		if m := l.Payload.Matrix; m != nil {
			for _, row := range m.Cells {
				for _, c := range row {
					if c.Present {
						ps = append(ps, floatsOf(c.Value)...)
					}
				}
			}
		}
		if m := l.Payload.SignificantAdjusted; m != nil {
			for _, row := range m.Cells {
				for _, c := range row {
					if c.Present {
						sig = append(sig, boolsOf(c.Value)...)
					}
				}
			}
		}
		return ps, sig
	case "scalar":
		if v := l.Payload.Scalar; v != nil {
			return []float64{*v}, nil
		}
		return []float64{math.NaN()}, nil
	}
	if l.Summary == nil {
		return []float64{math.NaN()}, nil
	}
	return []float64{summaryValue(l.Summary, pField)}, nil
}

// ofThemBelow says how many of a layer's tests fall below alpha:
// "1 of them falls below", "2 of them fall below".
func ofThemBelow(n int) string {
	if n == 1 {
		return "1 of them falls below"
	}
	return strconv.Itoa(n) + " of them fall below"
}

func summaryValue(s *types.OverlaySummary, field string) float64 {
	var p *float64
	switch field {
	case "summary.p_value":
		p = s.PValue
	case "summary.statistic":
		p = s.Statistic
	}
	if p == nil {
		return math.NaN()
	}
	return *p
}

// matrixFinding reads one matrix result: descriptive, with its scalars,
// the strongest off-diagonal value when the primary values are banded,
// and the rows its components floor counts.
func (e *explainer) matrixFinding(rr *responseReader, i int, m *types.MatrixResult, comps *types.ResponseComponents) {
	op := string(m.Type)
	shown := e.shownOp(op)
	slot := e.at + "matrices[" + strconv.Itoa(i) + "]"
	subject := m.Name
	if subject == "" {
		subject = slot
	}
	f := descriptor.ExplainFinding{Slot: slot, Subject: subject, Operator: shown, Verdict: descriptor.VerdictDescriptive, Numbers: map[string]*float64{}}
	text := e.lead(stepIn{role: roleMatrix, n: i + 1}) + opParen(shown)
	if m.Primary != nil {
		text += " relates " + count(len(m.Primary.RowKeys), "field", "fields")
	}
	text += "."
	for _, k := range sortedKeys(m.Scalars) {
		setNum(f.Numbers, "scalars."+k, m.Scalars[k])
	}
	if m.Primary != nil {
		strongest, found := math.NaN(), false
		for r, row := range m.Primary.Values {
			for c, v := range row {
				if r == c || !finite(v) {
					continue
				}
				if !found || math.Abs(v) > math.Abs(strongest) {
					strongest, found = v, true
				}
			}
		}
		if found {
			setNum(f.Numbers, "primary.values.strongest", strongest)
			bandText, _ := e.band(&f, op, []string{"primary.values"}, map[string]float64{"primary.values": strongest})
			if bandText == "" {
				bandText = " Its strongest off-diagonal value is " + fmtNum(strongest) + "."
			}
			text += bandText
		}
	}
	if comps != nil && i < len(comps.Matrices) {
		mc := comps.Matrices[i]
		setNum(f.Numbers, "n", float64(mc.N))
		setNum(f.Numbers, "n_null", float64(mc.NNull))
		setNum(f.Numbers, "n_listwise_dropped", float64(mc.NListwiseDropped))
		text += " It read " + count(mc.N, "row", "rows") + " and left out " + strconv.Itoa(mc.NListwiseDropped) +
			" with a value missing in any of its fields."
		if mc.SumWeights != nil {
			setNum(f.Numbers, "sum_weights", *mc.SumWeights)
		}
	}
	if shown != "" {
		e.noteOp(op, false)
	}
	e.addFinding(rr, f, text)
}

// aggregationFindings reads the aggregations and returns the summary
// phrase for them. With the request, each aggregation is named by its
// operator and plain purpose and its weight is named; without it the
// response cannot say which operator ran or which field weighted it,
// so each is described by its label and counts only.
func (e *explainer) aggregationFindings(rr *responseReader, resp *types.Response, req *types.Request) string {
	var comps []types.AggregationComponents
	if resp.Components != nil {
		comps = resp.Components.Aggregations
	}
	rows := len(resp.Data)
	grouped := rows > 1 || (req != nil && len(req.Groups) > 0) || (resp.Components != nil && len(resp.Components.Groupers) > 0)
	slots := len(comps)
	if req != nil && len(req.Aggregations) > slots {
		slots = len(req.Aggregations)
	}
	if slots == 0 && rows == 0 {
		return ""
	}
	for i := 0; i < slots; i++ {
		var spec *types.Aggregation
		if req != nil && i < len(req.Aggregations) {
			spec = req.Aggregations[i]
		}
		var c *types.AggregationComponents
		if i < len(comps) {
			c = &comps[i]
		}
		label := ""
		switch {
		case c != nil && c.Label != "":
			label = c.Label
		case spec != nil && spec.Label != "":
			label = spec.Label
		}
		f := descriptor.ExplainFinding{Slot: e.at + "aggregations[" + strconv.Itoa(i) + "]", Verdict: descriptor.VerdictDescriptive, Numbers: map[string]*float64{}}
		switch {
		case label != "":
			f.Subject = label
		case spec != nil && spec.Field != "":
			f.Subject = spec.Field
		default:
			f.Subject = f.Slot
		}
		text := e.lead(stepIn{role: roleAgg, n: i + 1})
		if label != "" {
			text += " " + tick(label)
		}
		if spec != nil {
			op := string(spec.Type)
			f.Operator = e.shownOp(op)
			if f.Operator != "" {
				text += " runs " + f.Operator
			} else if op == "" {
				text += " runs the operator smart defaults chose"
			} else {
				text += " runs an operator this instance does not offer"
			}
			text += onFields(nonEmpty(spec.Field))
			if w := aggWeight(spec, req); w != "" {
				text += ", weighted by " + tick(w)
			}
			text += "."
			if f.Operator != "" {
				if p, ok := purposeOf(e.inst, f.Operator); ok {
					if plain := strings.TrimSpace(p.Plain); plain != "" {
						text += " " + plain
					}
				}
				e.noteOp(f.Operator, false)
			}
		} else {
			text += " is reported without its request, so its operator is not named."
		}
		if !grouped && rows == 1 && label != "" {
			if v, ok := numberOf(resp.Data[0][label]); ok {
				setNum(f.Numbers, "value", v)
				f.Verdict = descriptive(v)
				text += " Its value is " + fmtNum(v) + "."
			}
		}
		if c != nil {
			setNum(f.Numbers, "n", float64(c.N))
			setNum(f.Numbers, "n_null", float64(c.NNull))
			text += " It read " + count(c.N, "row", "rows") + " and left out " + strconv.Itoa(c.NNull) + " with no value."
			if c.SumWeights != nil {
				setNum(f.Numbers, "sum_weights", *c.SumWeights)
				if spec == nil || aggWeight(spec, req) == "" {
					text += " A row weight was applied; the response does not name it."
				}
			}
			if c.NEff != nil {
				setNum(f.Numbers, "n_eff", *c.NEff)
			}
			if c.NWeightInvalid != nil {
				setNum(f.Numbers, "n_weight_invalid", float64(*c.NWeightInvalid))
			}
		}
		e.addFinding(rr, f, text)
	}
	groups := 1
	if grouped {
		groups = rows
	}
	if req == nil {
		if _, filtered, ok := runCounts(resp); ok {
			return "aggregated output of " + strconv.FormatInt(filtered, 10) + " rows across " + count(groups, "group", "groups")
		}
		return "aggregated output across " + count(groups, "group", "groups")
	}
	phrase := count(slots, "aggregation", "aggregations")
	if grouped {
		phrase += " across " + count(groups, "group", "groups")
	}
	return phrase
}

// aggWeight names the field weighting spec: its own slot weight, or
// the request's when the slot is absent; "" when unweighted or opted
// out (null).
func aggWeight(spec *types.Aggregation, req *types.Request) string {
	if spec.Weight.IsNull() {
		return ""
	}
	if w := spec.Weight.Spec(); w != nil {
		return w.Field
	}
	if req != nil && req.Weight != nil {
		return req.Weight.Field
	}
	return ""
}

// runCounts returns the cohort's record count and how many passed the
// filters, from the Components Run floor, else Metadata.
func runCounts(resp *types.Response) (total, filtered int64, ok bool) {
	if c := resp.Components; c != nil && c.Run != nil {
		return c.Run.TotalRecords, c.Run.FilteredRecords, true
	}
	if m := resp.Metadata; m != nil {
		return m.TotalRows, m.FilteredRows, true
	}
	return 0, 0, false
}

// runNotes adds the Run floor's sentence (full detail).
func (e *explainer) runNotes(rr *responseReader, resp *types.Response) {
	if resp.Components == nil || resp.Components.Run == nil {
		return
	}
	r := resp.Components.Run
	s := e.whose("run") + " read " + strconv.FormatInt(r.TotalRecords, 10) + " records; " + strconv.FormatInt(r.FilteredRecords, 10) +
		" passed the filters and " + strconv.FormatInt(r.NullRecords, 10) + " were left out for a missing value"
	if r.ShardCount > 0 {
		s += ", across " + count(r.ShardCount, "shard", "shards")
	}
	rr.notes = append(rr.notes, s+".")
}

// finalCaveats adds the caveats about the whole result — the
// many-tests count, the alphas read for regressions and overlays, a
// partial reading (companion names the request that was absent) —
// then each result's own. They ride terse output too.
func (e *explainer) finalCaveats(rr *responseReader, companion string) {
	if rr.uncorrected >= 2 {
		e.caveat(strconv.Itoa(rr.uncorrected) + " p-values here carry no adjustment for multiple comparisons, so some may fall below alpha " +
			"by chance alone; this is the concern the " + "PULSE_ADVISORY_MANY_TESTS advisory raises.")
		e.caveat("Adding a multiplicity block (for example holm) adjusts them for multiple comparisons; then read p_adjusted beside each raw p-value.")
	}
	if rr.regressionsP {
		e.caveat("Regression p-values are raw: a regression carries no alpha of its own, so they are read here against " + fmtNum(readAlpha) +
			", and no adjustment for multiple comparisons covers them.")
	}
	for _, op := range rr.overlayAlpha {
		label := e.shownOp(op)
		if label == "" {
			label = "An overlay"
		}
		e.caveat(label + " carries no alpha of its own unless it is adjusted, so its p-values are read here against " + fmtNum(readAlpha) + ".")
	}
	if rr.partial {
		switch companion {
		case "chain":
			e.caveat("Partial reading: the chain request is absent and the response echoes none, so the aggregations and groupings are described by count only; " +
				"pass the chain request beside the response, or run the chain with its request echoed, to name their operators and weight.")
		case "request":
			e.caveat("Partial reading: the request is absent, so the aggregations and groupings are described by count only; " +
				"pass the request beside the response to name their operators and weight.")
		default:
			e.caveat("Partial reading: the " + companion + " request is absent, so the aggregations and groupings are described by count only; " +
				"pass it beside the response to name their operators and weight.")
		}
	}
	for _, c := range rr.later {
		e.caveat(c)
	}
}

// slotCaveats notes what one Process response's reading cannot vouch
// for, under the explainer's scope.
func (e *explainer) slotCaveats(rr *responseReader, resp *types.Response, req *types.Request) {
	hasAggs := len(resp.Data) > 0 || (resp.Components != nil && len(resp.Components.Aggregations) > 0)
	if req == nil && hasAggs {
		rr.partial = true
	}
	if resp.Components == nil && hasAggs {
		rr.later = append(rr.later, e.whose("response")+" carries no components, so the rows each aggregation left out are not reported.")
	}
	if resp.Components != nil && resp.Components.Run != nil && resp.Components.Run.PartialCohortReason != "" {
		rr.later = append(rr.later, e.whose("run")+" read only part of the cohort; components.run.partial_cohort_reason says why.")
	}
	if resp.Returned != nil {
		rr.later = append(rr.later, e.whose("response")+" was shaped by a return block, so a part it left out is not read here.")
	}
}

// whose names a part of the result being read: "The run", or
// "Request 2's run" under a scope.
func (e *explainer) whose(part string) string {
	if e.scope == "" {
		return "The " + part
	}
	return e.scope + "'s " + part
}

// layerWarnings notes an overlay layer's warnings by code; the
// messages are engine prose and stay in the layer's own slot.
func (e *explainer) layerWarnings(rr *responseReader, lead string, l *types.OverlayLayer) {
	if len(l.Warnings) == 0 {
		return
	}
	var codes []string
	for _, w := range l.Warnings {
		if w.Code != "" && !slices.Contains(codes, w.Code) {
			codes = append(codes, w.Code)
		}
	}
	sort.Strings(codes)
	s := lead + " carries " + count(len(l.Warnings), "warning", "warnings")
	if len(codes) > 0 {
		s += " (" + strings.Join(codes, ", ") + ")"
	}
	rr.later = append(rr.later, s+"; the layer's warnings slot holds them, and some of its values may be undefined.")
}

// addFinding records f and, for full detail, its sentence.
func (e *explainer) addFinding(rr *responseReader, f descriptor.ExplainFinding, text string) {
	e.res.Findings = append(e.res.Findings, f)
	rr.notes = append(rr.notes, text)
}

// band sets f's strength band from the first of paths whose
// Interpretation for op carries bands and whose value is banded, and
// returns the sentence naming it with its convention plus the banded
// path ("", "" when none).
func (e *explainer) band(f *descriptor.ExplainFinding, op string, paths []string, values map[string]float64) (string, string) {
	ins := e.interpretationsOf(op)
	for _, p := range paths {
		v, ok := values[p]
		if !ok {
			continue
		}
		for _, in := range ins {
			if in.Field != p {
				continue
			}
			if label, ok := descx.BandOf(in, v); ok {
				f.StrengthBand, f.Convention = label, in.Convention
				if p != "primary.values" {
					f.Numbers[p] = numPtr(v)
				}
				return " Its " + figureWords(p) + " is " + fmtNum(v) + ", " + label + " by the convention of " + in.Convention + ".", p
			}
		}
	}
	return "", ""
}

// figureWords names a figure path in a sentence.
func figureWords(p string) string {
	switch {
	case strings.HasPrefix(p, "details.effect_size."):
		return "effect size " + tick(strings.TrimPrefix(p, "details.effect_size."))
	case p == "primary.values":
		return "strongest off-diagonal value"
	}
	return tick(p)
}

// effectOrder is the order effect sizes are banded in: the family's
// declared keys first, then any other key the result carries, sorted.
func (e *explainer) effectOrder(op string, effects map[string]float64) []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range descx.EffectSizeKeysByTest()[op] {
		if _, ok := effects[k]; ok {
			out = append(out, k)
			seen[k] = true
		}
	}
	for _, k := range sortedKeys(effects) {
		if !seen[k] {
			out = append(out, k)
		}
	}
	return out
}

// interpretationsOf returns op's Interpretations: built-in, else the
// extension registration's.
func (e *explainer) interpretationsOf(op string) []descriptor.Interpretation {
	if ins, ok := descx.InterpretationsOf(op); ok {
		return ins
	}
	if x := e.inst.Extensions(); x != nil {
		return x.Interpretations[op]
	}
	return nil
}

// classOf returns what op's p-value is evidence of: the built-in
// table's class, else (an extension) association when its Purpose
// serves the relationship or drivers intent, difference otherwise.
func (e *explainer) classOf(op string, table map[string]string) string {
	if c, ok := table[op]; ok {
		return c
	}
	if p, ok := purposeOf(e.inst, op); ok {
		for _, id := range p.Intents {
			if id == "relationship" || id == "drivers" {
				return classAssociation
			}
		}
	}
	return classDifference
}

// shownOp returns op as a finding may name it: "" when empty or hidden
// on the instance.
func (e *explainer) shownOp(op string) string {
	if op == "" || e.inst.Hidden(op) {
		return ""
	}
	return op
}

func verdictOf(class string, evidence bool) descriptor.Verdict {
	switch {
	case class == classAssociation && evidence:
		return descriptor.VerdictEvidenceOfAssociation
	case class == classAssociation:
		return descriptor.VerdictNoEvidenceOfAssociation
	case evidence:
		return descriptor.VerdictEvidenceOfDifference
	}
	return descriptor.VerdictNoEvidenceOfDifference
}

func isEvidence(v descriptor.Verdict) bool {
	switch v {
	case descriptor.VerdictEvidenceOfDifference, descriptor.VerdictEvidenceOfAssociation, descriptor.VerdictEvidenceOfEffect:
		return true
	}
	return false
}

// verdictWords phrases a verdict. A result above alpha is "no evidence
// of" a difference — never evidence that there is none.
func verdictWords(v descriptor.Verdict) string {
	switch v {
	case descriptor.VerdictEvidenceOfDifference:
		return "evidence of a difference"
	case descriptor.VerdictNoEvidenceOfDifference:
		return "no evidence of a difference"
	case descriptor.VerdictEvidenceOfAssociation:
		return "evidence of an association"
	case descriptor.VerdictNoEvidenceOfAssociation:
		return "no evidence of an association"
	case descriptor.VerdictEvidenceOfEffect:
		return "evidence of an effect"
	case descriptor.VerdictNoEvidenceOfEffect:
		return "no evidence of an effect"
	}
	return "a descriptive result"
}

func opParen(op string) string {
	if op == "" {
		return ""
	}
	return " (" + op + ")"
}

func slotPath(r *role, n int) string {
	key := "tests"
	if r == rolePostTest {
		key = "post_tests"
	}
	return key + "[" + strconv.Itoa(n-1) + "]"
}

// effectSizes returns details.effect_size's numeric entries.
func effectSizes(details map[string]any) map[string]float64 {
	out := map[string]float64{}
	raw, ok := details["effect_size"]
	if !ok {
		return out
	}
	var m map[string]any
	switch v := raw.(type) {
	case map[string]any:
		m = v
	case map[string]float64:
		for k, x := range v {
			out[k] = x
		}
		return out
	default:
		return out
	}
	for k, x := range m {
		if f, ok := numberOf(x); ok {
			out[k] = f
		}
	}
	return out
}

// numberOf reads a JSON-ish number.
func numberOf(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	case int32:
		return float64(x), true
	case uint32:
		return float64(x), true
	}
	return 0, false
}

// floatsOf flattens a cell value: a number, or a panel cell's vector.
func floatsOf(v any) []float64 {
	switch x := v.(type) {
	case []float64:
		return x
	case []any:
		var out []float64
		for _, el := range x {
			if f, ok := numberOf(el); ok {
				out = append(out, f)
			} else {
				out = append(out, math.NaN())
			}
		}
		return out
	case nil:
		return []float64{math.NaN()}
	}
	if f, ok := numberOf(v); ok {
		return []float64{f}
	}
	return []float64{math.NaN()}
}

// boolsOf flattens an adjusted-significance cell value.
func boolsOf(v any) []bool {
	switch x := v.(type) {
	case bool:
		return []bool{x}
	case []bool:
		return x
	case []any:
		out := make([]bool, len(x))
		for i, el := range x {
			b, _ := el.(bool)
			out[i] = b
		}
		return out
	}
	return []bool{false}
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// numPtr is v as a Numbers value: nil (JSON null) when undefined.
func numPtr(v float64) *float64 {
	if !finite(v) {
		return nil
	}
	return &v
}

func setNum(m map[string]*float64, k string, v float64) { m[k] = numPtr(v) }

func fmtNum(v float64) string {
	if !finite(v) {
		return "undefined"
	}
	return strconv.FormatFloat(v, 'g', 3, 64)
}

func fmtPtr(v *float64) string {
	if v == nil {
		return "undefined"
	}
	return fmtNum(*v)
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
