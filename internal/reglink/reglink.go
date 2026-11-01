// Package reglink is the one rule over a REG_GLM (family, link) pair
// (types.RegressionSpec Family / Link), shared by the no-execute predict
// pass (internal/descriptor), the manifest's link capability and the GLM
// engine (internal/processing/regression) so the three agree on which
// pairs exist and refuse the same requests with the same code, message
// and details. It reads the spec only and imports neither side.
//
// Each family has three links, its canonical one first (the default
// when Link is empty) — R's glm families:
//
//	binomial  logit (canonical), probit, cloglog
//	poisson   log (canonical), identity, sqrt
//	gamma     inverse (canonical), log, identity
package reglink

import (
	"slices"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// families lists the REG_GLM error families, in their documented order.
var families = []string{"binomial", "poisson", "gamma"}

// links maps each family to its links, canonical first.
var links = map[string][]string{
	"binomial": {"logit", "probit", "cloglog"},
	"poisson":  {"log", "identity", "sqrt"},
	"gamma":    {"inverse", "log", "identity"},
}

// Families lists the REG_GLM families (binomial, poisson, gamma).
func Families() []string { return slices.Clone(families) }

// Links lists family's links, its canonical link first; nil for an
// unknown family.
func Links(family string) []string { return slices.Clone(links[family]) }

// Canonical is family's canonical link (the default when Link is
// empty); "" for an unknown family.
func Canonical(family string) string {
	if l := links[family]; len(l) > 0 {
		return l[0]
	}
	return ""
}

// Resolve is the link a fit of family runs: link itself, or the
// family's canonical link when link is empty.
func Resolve(family, link string) string {
	if link == "" {
		return Canonical(family)
	}
	return link
}

// ByFamily is the manifest's family → links table (each list canonical
// first), a fresh map.
func ByFamily() map[string][]string {
	out := make(map[string][]string, len(links))
	for f, l := range links {
		out[f] = slices.Clone(l)
	}
	return out
}

// All is every link any family accepts, sorted: the link param's enum.
func All() []string {
	var out []string
	for _, l := range links {
		for _, v := range l {
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Check returns the PROCESSING_REGRESSION_INVALID_LINK refusal of a
// REG_GLM spec whose Link is not one of its Family's links, or nil.
// Every other type passes, and so does an empty Link (the canonical
// default) and an unknown family (the family check's refusal, never
// this one).
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Type != types.REG_GLM {
		return nil
	}
	return CheckPair(spec.Family, spec.Link)
}

// CheckPair is Check on a bare (family, link) pair.
func CheckPair(family, link string) *errors.CodedError {
	allowed, ok := links[family]
	if !ok || link == "" || slices.Contains(allowed, link) {
		return nil
	}
	return errors.NewCodedErrorWithDetails(
		errors.PROCESSING_REGRESSION_INVALID_LINK,
		"REG_GLM Family="+family+" requires Link in {"+strings.Join(allowed, ", ")+"}; got "+link,
		map[string]any{"family": family, "link": link, "allowed": slices.Clone(allowed)},
	)
}
