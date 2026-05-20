package pulse

import (
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// labelResolverAdapter wraps a processing.LabelResolver so it
// satisfies the io.LabelResolver interface. The shape mismatch is
// the Warnings() return type — processing returns []ResolverWarning
// (typed Code), io returns []LabelWarning (string Code). The adapter
// translates.
type labelResolverAdapter struct {
	r *processing.LabelResolver
}

func (a labelResolverAdapter) Has(field string) bool {
	return a.r.Has(field)
}

func (a labelResolverAdapter) Mode(field string) types.LabelMode {
	return a.r.Mode(field)
}

func (a labelResolverAdapter) Apply(field, raw string) (out, sibling string, ok bool) {
	return a.r.Apply(field, raw)
}

func (a labelResolverAdapter) FieldsWithAugment() []string {
	return a.r.FieldsWithAugment()
}

func (a labelResolverAdapter) Warnings() []pio.LabelWarning {
	src := a.r.Warnings()
	if len(src) == 0 {
		return nil
	}
	out := make([]pio.LabelWarning, len(src))
	for i, w := range src {
		out[i] = pio.LabelWarning{
			Code:    string(w.Code),
			Message: w.Message,
			Details: w.Details,
		}
	}
	return out
}

// newIOLabelResolver builds a processing.LabelResolver from the
// request bindings + the Pulse Service's runtime registry and wraps
// it in the io-package adapter. Returns nil on nil/empty bindings so
// the caller path stays a single nil-check.
func (p *Pulse) newIOLabelResolver(bindings []*types.LabelBinding) (pio.LabelResolver, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	r, err := processing.BuildLabelResolver(bindings, p.svc.Extensions())
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, nil
	}
	return labelResolverAdapter{r: r}, nil
}
