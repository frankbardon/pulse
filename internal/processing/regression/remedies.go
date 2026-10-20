package regression

import "github.com/frankbardon/pulse/internal/regbayes"

// Remedy clauses: the pieces of regression refusal prose that point the
// caller at ANOTHER regression type. Each is a pure function of the
// instance's offered predicate (nil = everything offered); the nil
// rendering is the historical literal, so a refusal built from
// remedy(nil) is byte-identical on an instance without a feature
// profile. processing.ExtensionRegistry.ScopeRefusal swaps each default
// rendering for the instance's, so a refusal never names a regression
// type the instance hides. The REG_BAYES_LINEAR clauses live with the
// rule they complete (internal/regbayes, shared with predict, which
// renders the instance's clause directly).

// Remedies lists every regression remedy clause, for
// processing.ExtensionRegistry.ScopeRefusal.
func Remedies() []func(offered func(string) bool) string {
	return regbayes.Remedies()
}
