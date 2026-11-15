package pulse

import (
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/postfit"
	"github.com/frankbardon/pulse/types"
)

// extensions_postfit.go lifts a PostFitRegistration onto the Compose
// post-fit phase (processing.ExtensionRegistry.PostFits, run by
// internal/service) after validating it (validatePostFitRegistrations).
// The request rules and the output rule are internal/postfit's, one set
// for every caller.

// PostFitRegistration installs a custom POSTFIT_* operator, authored
// against extend.PostFitFunc: a computation over a Compose batch's
// FINISHED fits that returns named scalars and named tables, run by a
// ComposedRequest.PostFit entry once the optimize blocks have run and
// before the nested-model comparisons. The category is extension-only:
// Pulse ships no built-in post-fit operator. The name follows the
// extension policy (POSTFIT_<NAMESPACE>_<NAME>, the reserved namespaces
// refused).
//
// The operator reads no record — only the fits its entry names
// (coefficients, standard errors, vcov, adstock records, posterior draws
// under return_draws), the batch's slot results (the result() reader),
// the optimize solutions and its params. Its output is held to Outputs
// on every run (PULSE_EXTENSION_POSTFIT_OUTPUT_MISMATCH).
type PostFitRegistration struct {
	Name        types.PostFitType
	Description string
	// Run is the operator. Required (PULSE_EXTENSION_PARAM_INVALID,
	// reason missing_factory).
	Run extend.PostFitFunc
	// Outputs declares the scalars and tables every run returns.
	// Required and well formed (PULSE_EXTENSION_PARAM_INVALID, reasons
	// empty_output_schema, empty_output_name, duplicate_output_name,
	// empty_table, empty_column_name, duplicate_column_name,
	// invalid_column_kind).
	Outputs extend.PostFitSchema
	// Params declares the keys of an entry's params object. Declared, a
	// key outside the list is PULSE_REQUEST_UNKNOWN_FIELD and a wrong
	// type / missing Required / ExactlyOneOf breach
	// PULSE_REQUEST_PARAM_INVALID at post_fit[i].params; undeclared
	// (nil), params pass through unchecked. A post-fit param is never
	// expression-capable (reason expression_unsupported).
	Params []ParamMeta
}

// validatePostFitRegistrations is the shared naming / duplicate /
// params pass (no built-in name to collide with: the category ships
// none), then the category's own rules.
func validatePostFitRegistrations(regs []PostFitRegistration) error {
	if err := validateOperatorSlice(regs, categoryPostFit, map[string]struct{}{}, func(r PostFitRegistration) (string, []ParamMeta) {
		return string(r.Name), r.Params
	}, func(r PostFitRegistration) bool { return r.Run != nil }); err != nil {
		return err
	}
	for i, r := range regs {
		invalid := func(reason, what string) error {
			return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_PARAM_INVALID,
				fmt.Sprintf("extension post_fit %q %s", r.Name, what),
				map[string]any{"category": string(categoryPostFit), "name": string(r.Name), "index": i, "reason": reason})
		}
		for _, p := range r.Params {
			if p.Expression {
				return invalid("expression_unsupported", "param "+fmt.Sprintf("%q", p.Name)+" sets Expression; a post-fit param is never expression-capable")
			}
		}
		if reason, msg := postfit.CheckSchema(r.Outputs); reason != "" {
			return invalid(reason, msg)
		}
	}
	return nil
}

// adaptPostFits is the registrations as the runtime's operators (nil
// without any).
func adaptPostFits(regs []PostFitRegistration) map[types.PostFitType]*postfit.Operator {
	if len(regs) == 0 {
		return nil
	}
	out := make(map[types.PostFitType]*postfit.Operator, len(regs))
	for _, r := range regs {
		out[r.Name] = &postfit.Operator{
			Decl: postfit.Decl{Name: r.Name, Params: paramMetaSnapshot(r.Params), Outputs: clonePostFitSchema(r.Outputs)},
			Run:  r.Run,
		}
	}
	return out
}

// clonePostFitSchema copies a declaration so a later mutation of the
// registration never reaches the instance.
func clonePostFitSchema(s extend.PostFitSchema) extend.PostFitSchema {
	out := extend.PostFitSchema{Scalars: append([]string(nil), s.Scalars...)}
	for _, t := range s.Tables {
		out.Tables = append(out.Tables, extend.PostFitTableSchema{Name: t.Name, Columns: append([]extend.PostFitColumn(nil), t.Columns...)})
	}
	return out
}
