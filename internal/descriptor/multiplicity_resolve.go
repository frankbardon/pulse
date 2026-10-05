package descriptor

import (
	"fmt"
	"slices"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// multiplicity_resolve.go is the single multiple-comparison resolution
// pass (U13). The runtime (internal/service: Process, Compose and
// ComposeParallel, every ProcessChain stage, FacetSchema) and predict
// (Predict and the Compose / chain / facet validators) both call it, so
// a `multiplicity` block is accepted or refused identically by both.
// It reads no schema and no record: every rule is about the request
// shape, the overlay catalog and the instance.

// ResolvedMultiplicity is one slot's resolved correction: the method,
// family and alpha in force after the field-by-field fall-through
// (slot → request → Compose request → pulse.Options.DefaultMultiplicity
// → none / surface default).
type ResolvedMultiplicity struct {
	// Slot is the slot path inside its request ("tests[0]",
	// "post_tests[1]", "overlays[2]"); a Compose-host overlay is
	// "overlays[i]" of the ComposedRequest.
	Slot string
	// Operator is the slot's test type or overlay kind as authored.
	Operator string
	// Method is the resolved method; MultiplicityMethodNone when
	// nothing names one.
	Method types.MultiplicityMethod
	// Family is the resolved family; the surface default when nothing
	// names one, or when an inherited family is not one the slot's
	// surface offers.
	Family types.MultiplicityFamily
	// Alpha is the resolved overlay significance level
	// (types.DefaultMultiplicityAlpha when nothing sets one). Zero on a
	// test: a test's adjusted flag reads its own Test.Alpha.
	Alpha float64
	// Member reports whether the slot's p-values join Family under
	// Method: Method is not none, the slot emits p-values (a test, or
	// an inferential overlay kind the instance offers), and it is not
	// skipped (an inherited correction never reaches the Tukey HSD
	// post-test, which is already corrected).
	Member bool
}

// MultiplicityPlan is the resolved correction of one Request, each
// list index-aligned with the request slot it resolves.
type MultiplicityPlan struct {
	Tests     []ResolvedMultiplicity
	PostTests []ResolvedMultiplicity
	Overlays  []ResolvedMultiplicity
}

// Active reports whether any slot of the plan is a family member.
func (p *MultiplicityPlan) Active() bool {
	if p == nil {
		return false
	}
	for _, list := range [][]ResolvedMultiplicity{p.Tests, p.PostTests, p.Overlays} {
		for _, r := range list {
			if r.Member {
				return true
			}
		}
	}
	return false
}

// ComposeMultiplicityPlan is the resolved correction of one
// ComposedRequest: one plan per slot request (index-aligned; nil when
// nothing applies to that slot) plus one entry per Compose-host
// overlay.
type ComposeMultiplicityPlan struct {
	Requests []*MultiplicityPlan
	Overlays []ResolvedMultiplicity
}

// multSurface is the kind of slot a block sits on; it decides the
// families the slot offers and its default family.
type multSurface int

const (
	surfaceTest multSurface = iota
	surfaceRequestOverlay
	surfaceComposeOverlay
	surfaceFacetOverlay
)

// defaultFamily is the family a slot of surface takes when nothing (or
// only an inapplicable inherited family) names one.
func (s multSurface) defaultFamily() types.MultiplicityFamily {
	if s == surfaceTest {
		return types.MultiplicityFamilyRequest
	}
	return types.MultiplicityFamilyLayer
}

// families returns the families surface offers, in the canonical
// order. inCompose admits `compose` on the surfaces that can carry it;
// matrix admits `row` / `column` on an overlay surface.
func (s multSurface) families(inCompose, matrix bool) []types.MultiplicityFamily {
	var out []types.MultiplicityFamily
	for _, f := range types.AllMultiplicityFamilies() {
		ok := false
		switch f {
		case types.MultiplicityFamilyLayer:
			ok = s != surfaceTest
		case types.MultiplicityFamilyRow, types.MultiplicityFamilyColumn:
			ok = s != surfaceTest && matrix
		case types.MultiplicityFamilyRequest:
			ok = s == surfaceTest || s == surfaceRequestOverlay
		case types.MultiplicityFamilyCompose:
			ok = inCompose && s != surfaceFacetOverlay
		}
		if ok {
			out = append(out, f)
		}
	}
	return out
}

// Refusal reasons (details.reason).
const (
	multReasonUnknownMethod  = "unknown method"
	multReasonUnknownFamily  = "unknown family"
	multReasonAlphaRange     = "alpha must lie in the open interval (0, 1)"
	multReasonTestAlpha      = "a test reads its own alpha; set the test's alpha instead of multiplicity.alpha"
	multReasonComposeOutside = "the compose family exists only inside Compose"
	multReasonSurface        = "family not offered on this slot"
	multReasonNotMatrix      = "row and column families need an overlay kind with a MATRIX payload"
	multReasonTukey          = "the Tukey HSD post-test is already corrected for its own comparisons; an explicit correction would correct it twice"
)

func stringsOf[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}

// multInvalid builds PULSE_MULTIPLICITY_INVALID for key on the block at
// where (a slot path ending in ".multiplicity", or
// "Options.DefaultMultiplicity").
func multInvalid(where, key string, value any, reason string, valid []string) *errors.CodedError {
	details := map[string]any{
		"slot":   where,
		"key":    key,
		"value":  value,
		"reason": reason,
	}
	msg := fmt.Sprintf("%s.%s %v: %s", where, key, value, reason)
	if valid != nil {
		details["valid"] = valid
		msg += fmt.Sprintf(" (valid: %v)", valid)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MULTIPLICITY_INVALID, msg, details)
}

// ValidateMultiplicitySpec checks a block's own values — a known method
// and family and an alpha inside (0, 1) — with where naming it in the
// refusal ("Options.DefaultMultiplicity", "tests[0].multiplicity"). A
// nil block is valid. pulse.New applies it to
// Options.DefaultMultiplicity; the resolver runs it on every block
// before the per-surface rules.
func ValidateMultiplicitySpec(m *types.Multiplicity, where string) error {
	if m == nil {
		return nil
	}
	if m.Method != "" && !slices.Contains(types.AllMultiplicityMethods(), m.Method) {
		return multInvalid(where, "method", string(m.Method), multReasonUnknownMethod, stringsOf(types.AllMultiplicityMethods()))
	}
	if m.Family != "" && !slices.Contains(types.AllMultiplicityFamilies(), m.Family) {
		return multInvalid(where, "family", string(m.Family), multReasonUnknownFamily, stringsOf(types.AllMultiplicityFamilies()))
	}
	if m.Alpha != 0 && !(m.Alpha > 0 && m.Alpha < 1) {
		return multInvalid(where, "alpha", m.Alpha, multReasonAlphaRange, nil)
	}
	return nil
}

// multChain is the inherited blocks behind a slot's own block, nearest
// first: the request's, the Compose request's, the instance default.
type multChain []*types.Multiplicity

func (c multChain) method(own *types.Multiplicity) types.MultiplicityMethod {
	if own != nil && own.Method != "" {
		return own.Method
	}
	for _, m := range c {
		if m != nil && m.Method != "" {
			return m.Method
		}
	}
	return types.MultiplicityMethodNone
}

func (c multChain) alpha(own *types.Multiplicity) float64 {
	if own != nil && own.Alpha != 0 {
		return own.Alpha
	}
	for _, m := range c {
		if m != nil && m.Alpha != 0 {
			return m.Alpha
		}
	}
	return types.DefaultMultiplicityAlpha
}

// inheritedFamily is the nearest inherited family, or "".
func (c multChain) inheritedFamily() types.MultiplicityFamily {
	for _, m := range c {
		if m != nil && m.Family != "" {
			return m.Family
		}
	}
	return ""
}

// multResolver carries the context one resolution runs in.
type multResolver struct {
	inst      *InstanceSnapshot
	inCompose bool
}

// family resolves a slot's family: its own (refused when the surface
// does not offer it), else the nearest inherited one the surface
// offers, else the surface default.
func (r multResolver) family(where string, s multSurface, own *types.Multiplicity, chain multChain, matrix bool) (types.MultiplicityFamily, error) {
	offered := s.families(r.inCompose, matrix)
	if own != nil && own.Family != "" {
		if slices.Contains(offered, own.Family) {
			return own.Family, nil
		}
		reason := multReasonSurface
		switch {
		case own.Family == types.MultiplicityFamilyCompose && !r.inCompose && s != surfaceFacetOverlay:
			reason = multReasonComposeOutside
		case (own.Family == types.MultiplicityFamilyRow || own.Family == types.MultiplicityFamilyColumn) && s != surfaceTest && !matrix:
			reason = multReasonNotMatrix
		}
		return "", multInvalid(where, "family", string(own.Family), reason, stringsOf(offered))
	}
	if f := chain.inheritedFamily(); f != "" && slices.Contains(offered, f) {
		return f, nil
	}
	return s.defaultFamily(), nil
}

// test resolves one tier-1 test or post-test.
func (r multResolver) test(slot string, t *types.Test, chain multChain) (ResolvedMultiplicity, error) {
	rm := ResolvedMultiplicity{Slot: slot, Method: types.MultiplicityMethodNone, Family: types.MultiplicityFamilyRequest}
	if t == nil {
		return rm, nil
	}
	rm.Operator = string(t.Type)
	own := t.Multiplicity
	where := slot + ".multiplicity"
	if err := ValidateMultiplicitySpec(own, where); err != nil {
		return rm, err
	}
	if own != nil && own.Alpha != 0 {
		return rm, multInvalid(where, "alpha", own.Alpha, multReasonTestAlpha, nil)
	}
	family, err := r.family(where, surfaceTest, own, chain, false)
	if err != nil {
		return rm, err
	}
	rm.Family = family
	method := chain.method(own)
	// The Tukey HSD post-test is already corrected: an explicit
	// correction on its own block is refused, an inherited one skips
	// it. A hidden one routes as never-registered (an ordinary name).
	if opRoute(r.inst, t.Type) == types.TEST_TUKEY_HSD {
		if own != nil && own.Method != "" && own.Method != types.MultiplicityMethodNone {
			return rm, multInvalid(where, "method", string(own.Method), multReasonTukey, []string{string(types.MultiplicityMethodNone)})
		}
		return rm, nil
	}
	rm.Method = method
	rm.Member = method != types.MultiplicityMethodNone
	return rm, nil
}

// overlay resolves one overlay slot on surface s.
func (r multResolver) overlay(slot string, s multSurface, kind types.OverlayKind, own *types.Multiplicity, chain multChain) (ResolvedMultiplicity, error) {
	rm := ResolvedMultiplicity{Slot: slot, Operator: string(kind), Method: types.MultiplicityMethodNone, Family: types.MultiplicityFamilyLayer}
	where := slot + ".multiplicity"
	if err := ValidateMultiplicitySpec(own, where); err != nil {
		return rm, err
	}
	// The kind's payload shape and inferential flag come from the
	// overlay catalog. A kind the catalog does not know — or the
	// instance hides (never-registered) — has neither: its own refusal
	// belongs to the overlay validators, so the family is judged
	// without the MATRIX rule and the slot joins no family.
	matrix, inferential, known := false, false, false
	if routed := opRoute(r.inst, kind); routed != "" {
		if c := overlayCapabilityFor(routed); c.Kind != "" && len(c.Shapes) > 0 {
			known, inferential = true, c.Inferential
			matrix = true
			for _, sh := range c.Shapes {
				if sh != types.OverlayShapeMatrix {
					matrix = false
				}
			}
		}
	}
	family, err := r.family(where, s, own, chain, matrix || !known)
	if err != nil {
		return rm, err
	}
	if !known && (family == types.MultiplicityFamilyRow || family == types.MultiplicityFamilyColumn) {
		// Inherited row / column on an unknown kind: no payload to read
		// rows from, so the surface default.
		if own == nil || own.Family == "" {
			family = s.defaultFamily()
		}
	}
	rm.Family = family
	rm.Method = chain.method(own)
	rm.Alpha = chain.alpha(own)
	rm.Member = inferential && rm.Method != types.MultiplicityMethodNone
	return rm, nil
}

// request resolves one Request's tests, post-tests and overlays, then
// refuses a `request` family whose members disagree on the method.
// composed is the ComposedRequest's block (nil outside Compose).
func (r multResolver) request(req *types.Request, composed, def *types.Multiplicity) (*MultiplicityPlan, error) {
	if own := req.Multiplicity; own != nil {
		if err := ValidateMultiplicitySpec(own, "multiplicity"); err != nil {
			return nil, err
		}
		if own.Family == types.MultiplicityFamilyCompose && !r.inCompose {
			return nil, multInvalid("multiplicity", "family", string(own.Family), multReasonComposeOutside,
				stringsOf(slices.DeleteFunc(types.AllMultiplicityFamilies(), func(f types.MultiplicityFamily) bool {
					return f == types.MultiplicityFamilyCompose
				})))
		}
	}
	chain := multChain{req.Multiplicity, composed, def}
	plan := &MultiplicityPlan{}
	for _, tier := range []struct {
		prefix string
		tests  []*types.Test
		out    *[]ResolvedMultiplicity
	}{{"tests", req.Tests, &plan.Tests}, {"post_tests", req.PostTests, &plan.PostTests}} {
		for i, t := range tier.tests {
			rm, err := r.test(tier.prefix+"["+strconv.Itoa(i)+"]", t, chain)
			if err != nil {
				return nil, err
			}
			*tier.out = append(*tier.out, rm)
		}
	}
	for i := range req.Overlays {
		o := &req.Overlays[i]
		rm, err := r.overlay("overlays["+strconv.Itoa(i)+"]", surfaceRequestOverlay, o.Kind, o.Multiplicity, chain)
		if err != nil {
			return nil, err
		}
		plan.Overlays = append(plan.Overlays, rm)
	}
	var members []ResolvedMultiplicity
	for _, list := range [][]ResolvedMultiplicity{plan.Tests, plan.PostTests, plan.Overlays} {
		members = append(members, list...)
	}
	if err := familyConflict(types.MultiplicityFamilyRequest, members, nil); err != nil {
		return nil, err
	}
	return plan, nil
}

// familyConflict refuses family when its members (Member entries whose
// Family is family) resolve to more than one method. prefix, when
// non-nil, maps a member's index in members to a slot-path prefix.
func familyConflict(family types.MultiplicityFamily, members []ResolvedMultiplicity, prefix func(int) string) error {
	first := map[types.MultiplicityMethod]string{}
	for i, m := range members {
		if !m.Member || m.Family != family {
			continue
		}
		if _, seen := first[m.Method]; !seen {
			slot := m.Slot
			if prefix != nil {
				slot = prefix(i) + slot
			}
			first[m.Method] = slot
		}
	}
	if len(first) < 2 {
		return nil
	}
	methods := make([]string, 0, len(first))
	for m := range first {
		methods = append(methods, string(m))
	}
	sort.Strings(methods)
	slots := make([]string, len(methods))
	for i, m := range methods {
		slots[i] = first[types.MultiplicityMethod(m)]
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MULTIPLICITY_CONFLICT,
		fmt.Sprintf("the %s correction family mixes methods %v (first members: %v); a family is corrected by one method", family, methods, slots),
		map[string]any{"family": string(family), "methods": methods, "slots": slots})
}

// requestNamesMultiplicity reports whether anything reaching req names
// a block: the request's own, any test's or overlay's, or one of the
// inherited blocks.
func requestNamesMultiplicity(req *types.Request, inherited ...*types.Multiplicity) bool {
	for _, m := range inherited {
		if m != nil {
			return true
		}
	}
	if req == nil {
		return false
	}
	if req.Multiplicity != nil {
		return true
	}
	for _, tier := range [][]*types.Test{req.Tests, req.PostTests} {
		for _, t := range tier {
			if t != nil && t.Multiplicity != nil {
				return true
			}
		}
	}
	for i := range req.Overlays {
		if req.Overlays[i].Multiplicity != nil {
			return true
		}
	}
	return false
}

// ResolveMultiplicity is the single multiplicity-resolution pass for a
// standalone Request (Process, ProcessStream, every ProcessChain
// stage). The runtime and predict both call it, so a request is refused
// or accepted identically by both.
//
// Precedence per slot and per field (method, family and alpha fall
// through independently): the slot's own block → req.Multiplicity →
// def (pulse.Options.DefaultMultiplicity) → none. Rules, in evaluation
// order (PULSE_MULTIPLICITY_INVALID unless noted):
//
//   - def, the request's block and every slot block must name a known
//     method and family, and an alpha inside (0, 1);
//   - the request's block may not name the `compose` family (Compose
//     only);
//   - a test's or post-test's own family must be `request`; its own
//     block may not set alpha (the test's own Test.Alpha governs);
//   - an explicit non-none method on the Tukey HSD post-test is
//     refused; an inherited method skips it (Member false);
//   - an overlay's own family must be `layer`, `request`, or — for a
//     kind whose payload is MATRIX — `row` / `column`;
//   - an inherited family the slot's surface does not offer falls back
//     to the surface default (`request` for tests, `layer` for
//     overlays), never refused;
//   - members of the `request` family (tests, post-tests and
//     inferential overlays whose method is not none) must agree on one
//     method, else PULSE_MULTIPLICITY_CONFLICT.
//
// The plan is nil when nothing — the request, any slot, or def — names
// a block. inst routes hidden test types and overlay kinds as
// never-registered; nil hides nothing. It never mutates req.
func ResolveMultiplicity(req *types.Request, def *types.Multiplicity, inst *InstanceSnapshot) (*MultiplicityPlan, error) {
	if req == nil || !requestNamesMultiplicity(req, def) {
		return nil, nil
	}
	if err := ValidateMultiplicitySpec(def, "Options.DefaultMultiplicity"); err != nil {
		return nil, err
	}
	return multResolver{inst: inst}.request(req, nil, def)
}

// ResolveComposeMultiplicity is ResolveMultiplicity for a whole
// ComposedRequest: each slot request resolves with the Compose
// request's block between its own and def (and may use the `compose`
// family), a refusal located with details.request; then each
// Compose-host overlay resolves (own → Compose request → def; families
// `layer`, `row` / `column` for a MATRIX kind, `compose` — never
// `request`); then every `compose`-family member, across slots and
// Compose-host overlays, must agree on one method
// (PULSE_MULTIPLICITY_CONFLICT, member slots prefixed
// "requests[i]."). Nil when nothing names a block.
func ResolveComposeMultiplicity(req *types.ComposedRequest, def *types.Multiplicity, inst *InstanceSnapshot) (*ComposeMultiplicityPlan, error) {
	if req == nil {
		return nil, nil
	}
	named := def != nil || req.Multiplicity != nil
	for i := 0; !named && i < len(req.Overlays); i++ {
		named = req.Overlays[i].Multiplicity != nil
	}
	for i := 0; !named && i < len(req.Requests); i++ {
		named = requestNamesMultiplicity(req.Requests[i])
	}
	if !named {
		return nil, nil
	}
	if err := ValidateMultiplicitySpec(def, "Options.DefaultMultiplicity"); err != nil {
		return nil, err
	}
	if err := ValidateMultiplicitySpec(req.Multiplicity, "multiplicity"); err != nil {
		return nil, err
	}
	r := multResolver{inst: inst, inCompose: true}
	out := &ComposeMultiplicityPlan{Requests: make([]*MultiplicityPlan, len(req.Requests))}
	var members []ResolvedMultiplicity
	var owners []int // members[i]'s slot request index, -1 for a Compose overlay
	for i, sub := range req.Requests {
		if sub == nil {
			continue
		}
		plan, err := r.request(sub, req.Multiplicity, def)
		if err != nil {
			return nil, RefusalAt(err, "request", i)
		}
		out.Requests[i] = plan
		for _, list := range [][]ResolvedMultiplicity{plan.Tests, plan.PostTests, plan.Overlays} {
			for _, m := range list {
				members = append(members, m)
				owners = append(owners, i)
			}
		}
	}
	chain := multChain{req.Multiplicity, def}
	for i := range req.Overlays {
		o := &req.Overlays[i]
		rm, err := r.overlay("overlays["+strconv.Itoa(i)+"]", surfaceComposeOverlay, o.Kind, o.Multiplicity, chain)
		if err != nil {
			return nil, err
		}
		out.Overlays = append(out.Overlays, rm)
		members = append(members, rm)
		owners = append(owners, -1)
	}
	prefix := func(i int) string {
		if owners[i] < 0 {
			return ""
		}
		return "requests[" + strconv.Itoa(owners[i]) + "]."
	}
	if err := familyConflict(types.MultiplicityFamilyCompose, members, prefix); err != nil {
		return nil, err
	}
	return out, nil
}

// ResolveFacetMultiplicity resolves a FacetRequest's overlays: own
// block → def → none, families `layer` (default) and `row` / `column`
// for a MATRIX kind. A FacetRequest has no block of its own. Index-
// aligned with req.Overlays; nil when nothing names a block.
func ResolveFacetMultiplicity(req *types.FacetRequest, def *types.Multiplicity, inst *InstanceSnapshot) ([]ResolvedMultiplicity, error) {
	if req == nil {
		return nil, nil
	}
	named := def != nil
	for i := 0; !named && i < len(req.Overlays); i++ {
		named = req.Overlays[i].Multiplicity != nil
	}
	if !named {
		return nil, nil
	}
	if err := ValidateMultiplicitySpec(def, "Options.DefaultMultiplicity"); err != nil {
		return nil, err
	}
	r := multResolver{inst: inst}
	chain := multChain{def}
	out := make([]ResolvedMultiplicity, 0, len(req.Overlays))
	for i := range req.Overlays {
		o := &req.Overlays[i]
		rm, err := r.overlay("overlays["+strconv.Itoa(i)+"]", surfaceFacetOverlay, o.Kind, o.Multiplicity, chain)
		if err != nil {
			return nil, err
		}
		out = append(out, rm)
	}
	return out, nil
}
