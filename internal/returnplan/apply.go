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

// RowEncoder writes one result row (a streamed row, or one element of a
// Response's `data`) as its wire form under owner's plan — owner is the
// *types.Response the row belongs to (its plan is unexported). A nil
// owner, or one carrying no non-identity plan, writes the row exactly
// as the unshaped encoder does. Installed by package types at init,
// for the same reason as the Applier.
type RowEncoder func(owner any, row map[string]any) ([]byte, error)

var rowEncoder RowEncoder

// SetRowEncoder installs the row encoder. Called once, from package
// types' init.
func SetRowEncoder(e RowEncoder) { rowEncoder = e }

// EncodeRow runs the installed row encoder; ok is false when none is
// installed.
func EncodeRow(owner any, row map[string]any) (b []byte, ok bool, err error) {
	if rowEncoder == nil {
		return nil, false, nil
	}
	b, err = rowEncoder(owner, row)
	return b, true, err
}
