package service

import (
	"math"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/groupedtests"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// multiplicity_fold.go is the multiple-comparison fold (U13): it
// gathers every p-value a resolved plan names into its correction
// family, adjusts each family once through the pure core
// (internal/processing/multiplicity) and writes the adjusted figures
// BESIDE the raw ones. Raw p-values, reject flags and every other
// figure are never touched.
//
// The fold is split in two so every host shares it:
//   - collection (collectRequestSites) visits one Response under its
//     plan and adds each member p as a multSite under a family key;
//   - adjustment (multFamilies.fold) corrects each keyed family once.
//
// A standalone Request (Process, every arm) collects and folds one
// response (foldRequestMultiplicity). A Compose batch collects every
// slot into ONE multFamilies with a per-slot scope so `request`
// families stay per slot while `compose` members pool across slots; a
// chain stage folds its own response like a standalone Process.

// multSite is one p-value joining a correction family. write receives
// the adjusted p (NaN when p is NaN), the family size m and the number
// of members excluded from m (NaN p-values), and records them beside the
// raw figure.
type multSite struct {
	p     float64
	write func(adjusted float64, m, excluded int)
}

// multFamily is one correction family: its resolved method and the
// p-sites corrected together, in collection order.
type multFamily struct {
	family types.MultiplicityFamily
	method types.MultiplicityMethod
	sites  []multSite
}

// multFamilies collects p-sites by family key, in first-seen key order
// so the fold is deterministic.
type multFamilies struct {
	order []string
	byKey map[string]*multFamily
}

func newMultFamilies() *multFamilies {
	return &multFamilies{byKey: map[string]*multFamily{}}
}

// multFamilyKey keys family inside scope. The `compose` family pools
// across every scope of a batch, so it ignores scope; every other
// family is local to the request (scope) that owns it.
func multFamilyKey(scope string, family types.MultiplicityFamily) string {
	if family == types.MultiplicityFamilyCompose {
		return string(family)
	}
	return scope + string(family)
}

// add files site under key. The resolver guarantees one method per
// family (PULSE_MULTIPLICITY_CONFLICT), so the first member's method
// is the family's.
func (f *multFamilies) add(key string, family types.MultiplicityFamily, method types.MultiplicityMethod, site multSite) {
	fam, ok := f.byKey[key]
	if !ok {
		fam = &multFamily{family: family, method: method}
		f.byKey[key] = fam
		f.order = append(f.order, key)
	}
	fam.sites = append(fam.sites, site)
}

// drop removes the family keyed key (a no-op when none was collected).
func (f *multFamilies) drop(key string) {
	if _, ok := f.byKey[key]; !ok {
		return
	}
	delete(f.byKey, key)
	f.order = slices.DeleteFunc(f.order, func(k string) bool { return k == key })
}

// fold adjusts every collected family once and writes each site's
// adjusted figure. NaN p-values are excluded from m, come back NaN and
// are counted as the family's excluded members.
func (f *multFamilies) fold() error {
	for _, key := range f.order {
		fam := f.byKey[key]
		ps := make([]float64, len(fam.sites))
		for i, s := range fam.sites {
			ps[i] = s.p
		}
		adj, err := multiplicity.Adjust(multiplicity.Method(fam.method), ps)
		if err != nil {
			// The resolver validated every method before dispatch.
			return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"multiplicity fold met an unresolved method",
				map[string]any{"family": string(fam.family), "method": string(fam.method)})
		}
		m := multiplicity.FamilySize(ps)
		excluded := len(ps) - m
		for i, s := range fam.sites {
			s.write(adj[i], m, excluded)
		}
	}
	return nil
}

// collectRequestSites adds every member p-site of resp under plan to
// fams, family keys prefixed with scope (empty for a standalone
// Request). Tests and post-tests contribute one headline p each
// (TestResult.PValue); each inferential overlay layer contributes its
// p-values per the p-site table (collectOverlaySites) — a `layer`
// family local to the layer, a `request` family pooled with the tests;
// each inferential matrix spec its results' off-diagonal p-values
// (collectMatrixResultSites) — a `matrix` family per result, or pooled into
// `request`; each member regression its member coefficients' p-values
// (collectRegressionSites), pooled into `request` or `compose`.
// A non-member slot (method none, the skipped Tukey HSD post-test, a
// descriptive overlay kind) contributes nothing. Nil plan or resp is a
// no-op.
func collectRequestSites(fams *multFamilies, scope string, plan *descx.MultiplicityPlan, resp *types.Response) {
	if plan == nil || resp == nil {
		return
	}
	collectTestSites(fams, scope, plan.Tests, resp.Tests)
	collectTestSites(fams, scope, plan.PostTests, resp.PostTests)
	collectOverlaySites(fams, scope, plan.Overlays, resp.Overlays)
	collectMatrixResultSites(fams, scope, plan.Matrices, resp.Matrices)
	collectRegressionSites(fams, scope, plan.Regressions, resp.Regressions)
}

// collectRegressionSites files each member regression's coefficient
// p-values under its slot's family, LAST (after the matrices), one site
// per member coefficient in the resolved order. Results pair with slots
// by index (one result per spec). A default member list skips a
// predictor the fit reports no p-value for (dropped by Selection); an
// explicit list keeps it as an undefined (NaN) member, excluded from m.
func collectRegressionSites(fams *multFamilies, scope string, slots []descx.ResolvedMultiplicity, results []*types.RegressionResult) {
	for i, r := range results {
		if i >= len(slots) || r == nil || r.Error != nil || !slots[i].Member {
			continue
		}
		rm := slots[i]
		method, family, alpha := rm.Method, rm.Family, rm.Alpha
		echo := &types.AppliedMultiplicity{Method: method, Family: family, Alpha: alpha}
		names := rm.Coefficients
		if rm.ResultCoefficients {
			names = pValuedCoefficients(r)
		}
		for _, name := range names {
			p, ok := r.PValues[name]
			if !ok {
				if !rm.ExplicitCoefficients {
					continue
				}
				p = math.NaN()
			}
			fams.add(multFamilyKey(scope, family), family, method, multSite{
				p: p,
				write: func(adjusted float64, m, excluded int) {
					writeRegressionAdjusted(r, echo, name, adjusted, m, excluded)
				},
			})
		}
		// The marginal effects (RegressionSpec.Margins) form a family of
		// their own, one per regression (keyed by the regression's index
		// in its scope, never pooled — a `compose` block pools the
		// coefficients only), so a coefficient's p_adjusted is the same
		// with margins or without. One site per term in the block's order;
		// an undefined (null) p is excluded from m. Its echo is the
		// margins block's own.
		if mg := r.Margins; mg != nil {
			mecho := &types.AppliedMultiplicity{Method: method, Family: types.MultiplicityFamilyMargins, Alpha: alpha}
			key := scope + "regressions[" + strconv.Itoa(i) + "]#" + string(types.MultiplicityFamilyMargins)
			for _, term := range mg.Terms {
				fams.add(key, types.MultiplicityFamilyMargins, method, multSite{
					p: mg.PValues[term],
					write: func(adjusted float64, m, excluded int) {
						writeMarginsAdjusted(mg, mecho, term, adjusted, m, excluded)
					},
				})
			}
		}
	}
}

// writeMarginsAdjusted records one marginal effect's adjusted p beside
// its raw one (RegressionMarginsResult.PAdjusted /
// SignificantAdjusted) and stamps the margins family's echo
// (RegressionMarginsResult.Multiplicity).
func writeMarginsAdjusted(mg *types.RegressionMarginsResult, echo *types.AppliedMultiplicity, term string, adjusted float64, m, excluded int) {
	if mg.PAdjusted == nil {
		mg.PAdjusted = map[string]float64{}
		mg.SignificantAdjusted = map[string]bool{}
	}
	mg.PAdjusted[term] = adjusted
	if !math.IsNaN(adjusted) {
		mg.SignificantAdjusted[term] = adjusted < echo.Alpha
	}
	echo.M, echo.Excluded = m, excluded
	mg.Multiplicity = echo
}

// pValuedCoefficients is the default family of an extension REG_* model
// (descx.ResolvedMultiplicity.ResultCoefficients): every coefficient of
// r whose p-value is defined, "(intercept)" excluded, in name order — a
// coefficient the model reports no p-value for (null on the wire) does
// not join.
func pValuedCoefficients(r *types.RegressionResult) []string {
	out := make([]string, 0, len(r.PValues))
	for name, p := range r.PValues {
		if name != regression.InterceptKey && !math.IsNaN(p) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// writeRegressionAdjusted records one member coefficient's adjusted p
// beside the raw PValues. significant_adjusted compares against the
// resolved multiplicity alpha and is absent for an undefined adjusted
// p. echo is shared by the regression's sites: every coefficient of one
// regression sits in one family, so m and excluded agree.
func writeRegressionAdjusted(r *types.RegressionResult, echo *types.AppliedMultiplicity, name string, adjusted float64, m, excluded int) {
	if r.PAdjusted == nil {
		r.PAdjusted = map[string]float64{}
		r.SignificantAdjusted = map[string]bool{}
	}
	r.PAdjusted[name] = adjusted
	if !math.IsNaN(adjusted) {
		r.SignificantAdjusted[name] = adjusted < echo.Alpha
	}
	echo.M, echo.Excluded = m, excluded
	r.Multiplicity = echo
}

// collectTestSites files each member test result under its slot's
// family. Results pair with slots by groupedtests.SpecIndex: one per
// slot ungrouped; on a grouped request one per (slot x group),
// test-major, every group's result joining its slot's family.
func collectTestSites(fams *multFamilies, scope string, slots []descx.ResolvedMultiplicity, results []*types.TestResult) {
	for k, r := range results {
		i := groupedtests.SpecIndex(k, len(slots), len(results))
		if i < 0 || r == nil || r.Error != nil {
			continue
		}
		rm := slots[i]
		if !rm.Member {
			continue
		}
		method, family := rm.Method, rm.Family
		fams.add(multFamilyKey(scope, family), family, method, multSite{
			p: r.PValue,
			write: func(adjusted float64, m, excluded int) {
				writeTestAdjusted(r, method, family, adjusted, m, excluded)
			},
		})
	}
}

// writeTestAdjusted records a test's adjusted p beside its raw one.
// significant_adjusted reads the test's own alpha with the same strict
// comparison as reject_null; it is absent when the adjusted p is
// undefined. The echo stamps the family's excluded (NaN-p) member count.
func writeTestAdjusted(r *types.TestResult, method types.MultiplicityMethod, family types.MultiplicityFamily, adjusted float64, m, excluded int) {
	r.PAdjusted = &adjusted
	r.SignificantAdjusted = nil
	if !math.IsNaN(adjusted) {
		sig := adjusted < r.Alpha
		r.SignificantAdjusted = &sig
	}
	r.Multiplicity = &types.AppliedMultiplicity{Method: method, Family: family, Alpha: r.Alpha, M: m, Excluded: excluded}
}

// foldRequestMultiplicity corrects one standalone Request's response in
// place under its resolved plan: every `request`-family member pooled
// into one family. A nil or inactive plan leaves resp untouched, so a
// request with no block (or only `none`) is byte-identical.
func foldRequestMultiplicity(plan *descx.MultiplicityPlan, resp *types.Response) error {
	if !plan.Active() || resp == nil {
		return nil
	}
	fams := newMultFamilies()
	collectRequestSites(fams, "", plan, resp)
	if err := fams.fold(); err != nil {
		return err
	}
	return refreshOverlayLetters(resp.Overlays)
}

// refreshOverlayLetters recomputes every pairwise layer's significance
// letters after the fold wrote its adjusted twins, so the letters read
// the corrected significance (processing.RefreshPairwiseLetters). A
// layer without letters is untouched.
func refreshOverlayLetters(layers []types.OverlayLayer) error {
	for i := range layers {
		if err := processing.RefreshPairwiseLetters(&layers[i]); err != nil {
			return err
		}
	}
	return nil
}

// composeSlotScope is the family-key scope of Compose slot i, so its
// `request` / `layer` / `row` / `column` families stay inside the slot.
func composeSlotScope(i int) string {
	return "requests[" + strconv.Itoa(i) + "]/"
}

// composeHostScope scopes the Compose-host overlay layers' own
// (`layer` / `row` / `column`) families.
const composeHostScope = "compose/"

// foldComposeMultiplicity corrects a whole Compose batch in place under
// its resolved plan, once, after the barrier: every slot response and
// every Compose-host layer is collected into ONE multFamilies — each
// slot under its own scope, so a slot's `request`, `layer`, `row` and
// `column` families stay inside that slot exactly as a standalone
// Process would correct them, while every `compose` member (slot tests,
// post-tests, request-host overlays and Compose-host layers) pools into
// one family — then each family is adjusted once. Collection walks slots
// in index order and then the Compose-host layers in spec order, never
// completion order, so Compose and ComposeParallel answer identically.
// A nil or inactive plan leaves out untouched (byte-identical).
//
// The fold runs only on a complete batch: both orchestrators return an
// error before the barrier when any slot fails, so a `compose` family
// is never corrected over a partial set of members.
func foldComposeMultiplicity(plan *descx.ComposeMultiplicityPlan, out *types.ComposedResponse) error {
	if plan == nil || out == nil {
		return nil
	}
	fams := newMultFamilies()
	for i, sub := range plan.Requests {
		if i < len(out.Responses) {
			collectRequestSites(fams, composeSlotScope(i), sub, out.Responses[i])
		}
	}
	collectOverlaySites(fams, composeHostScope, plan.Overlays, out.Overlays)
	collectComparisonSites(fams, plan.Comparisons, out.Comparisons)
	if err := fams.fold(); err != nil {
		return err
	}
	for _, resp := range out.Responses {
		if resp == nil {
			continue
		}
		if err := refreshOverlayLetters(resp.Overlays); err != nil {
			return err
		}
	}
	return nil
}

// collectComparisonSites files each member nested-model comparison's
// p-value (ComposedRequest.Compare) into the batch's `compose` family,
// in declaration order, after the slots and the Compose-host layers. A
// failed comparison (Error set) joins no family, like a failed test.
func collectComparisonSites(fams *multFamilies, slots []descx.ResolvedMultiplicity, results []types.FitComparisonResult) {
	for i := range results {
		r := &results[i]
		if i >= len(slots) || r.Error != nil || !slots[i].Member {
			continue
		}
		rm := slots[i]
		fams.add(multFamilyKey(composeHostScope, rm.Family), rm.Family, rm.Method, multSite{
			p: r.PValue,
			write: func(adjusted float64, m, excluded int) {
				writeComparisonAdjusted(r, rm, adjusted, m, excluded)
			},
		})
	}
}

// writeComparisonAdjusted records a comparison's adjusted p beside its
// raw one; significant_adjusted compares against the resolved alpha and
// is absent for an undefined adjusted p.
func writeComparisonAdjusted(r *types.FitComparisonResult, rm descx.ResolvedMultiplicity, adjusted float64, m, excluded int) {
	r.PAdjusted = &adjusted
	r.SignificantAdjusted = nil
	if !math.IsNaN(adjusted) {
		sig := adjusted < rm.Alpha
		r.SignificantAdjusted = &sig
	}
	r.Multiplicity = &types.AppliedMultiplicity{Method: rm.Method, Family: rm.Family, Alpha: rm.Alpha, M: m, Excluded: excluded}
}
