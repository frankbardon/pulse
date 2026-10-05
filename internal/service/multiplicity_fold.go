package service

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
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
// the adjusted p (NaN when p is NaN) and the family size m, and records
// them beside the raw figure.
type multSite struct {
	p     float64
	write func(adjusted float64, m int)
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

// fold adjusts every collected family once and writes each site's
// adjusted figure. NaN p-values are excluded from m and come back NaN.
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
		for i, s := range fam.sites {
			s.write(adj[i], m)
		}
	}
	return nil
}

// collectRequestSites adds every member p-site of resp under plan to
// fams, family keys prefixed with scope (empty for a standalone
// Request). Tests and post-tests contribute one headline p each
// (TestResult.PValue); each inferential overlay layer contributes its
// p-values per the p-site table (collectOverlaySites) — a `layer`
// family local to the layer, a `request` family pooled with the tests.
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
}

// collectTestSites files each member test result (index-aligned with
// its resolved slot) under its family.
func collectTestSites(fams *multFamilies, scope string, slots []descx.ResolvedMultiplicity, results []*types.TestResult) {
	for i, rm := range slots {
		if !rm.Member || i >= len(results) || results[i] == nil {
			continue
		}
		r := results[i]
		method, family := rm.Method, rm.Family
		fams.add(multFamilyKey(scope, family), family, method, multSite{
			p: r.PValue,
			write: func(adjusted float64, m int) {
				writeTestAdjusted(r, method, family, adjusted, m)
			},
		})
	}
}

// writeTestAdjusted records a test's adjusted p beside its raw one.
// significant_adjusted reads the test's own alpha with the same strict
// comparison as reject_null; it is absent when the adjusted p is
// undefined.
func writeTestAdjusted(r *types.TestResult, method types.MultiplicityMethod, family types.MultiplicityFamily, adjusted float64, m int) {
	r.PAdjusted = &adjusted
	r.SignificantAdjusted = nil
	if !math.IsNaN(adjusted) {
		sig := adjusted < r.Alpha
		r.SignificantAdjusted = &sig
	}
	r.Multiplicity = &types.AppliedMultiplicity{Method: method, Family: family, Alpha: r.Alpha, M: m}
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
	return fams.fold()
}
