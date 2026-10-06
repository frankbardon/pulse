package returnplan

// The applier is how a result package that carries a plan in an
// unexported field (package types) lends its pruner to the engine
// without exporting a plan-typed symbol: types registers it at init,
// the facade calls Apply. This leaf stays stdlib-only either way.

// Applier prunes target (a *types.Response) under p in place, attaches
// p to it so its wire encoder honours the plan, and returns the Open
// include paths that matched no concrete node. ok is false when target
// is not a type the applier knows.
type Applier func(target any, p *Plan) (unmatched []Path, ok bool)

var applier Applier

// SetApplier installs the applier. Called once, from package types'
// init.
func SetApplier(a Applier) { applier = a }

// Apply runs the installed applier; ok is false when none is installed
// or it does not know target's type.
func Apply(target any, p *Plan) (unmatched []Path, ok bool) {
	if applier == nil {
		return nil, false
	}
	return applier(target, p)
}
