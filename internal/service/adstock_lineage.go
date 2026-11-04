package service

import (
	"context"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// adstock_lineage.go records a media-mix fit's adstock lineage
// (model.AdstockLineage) where the fit runs: in Service.process, over
// the request's windows resolved exactly as the run resolved them (the
// same params fold, bound models, tables and functions —
// processing.ResolveWindowScalars), so a decay read through
// sweep_axis() or params.d is recorded as the number the window used.
// The records ride a regression `save` (model file feature adstock) and,
// through an adstockSink on ctx, a Compose fit's binding and its own
// `save`.

// adstockSink receives the adstock records of one Process call's
// regressions, index-aligned with its Request.Regressions (a nil entry:
// no record).
type adstockSink struct {
	records []map[string]model.Adstock
}

// at is regression i's records (nil for none). Nil-receiver-safe.
func (k *adstockSink) at(i int) map[string]model.Adstock {
	if k == nil || i >= len(k.records) {
		return nil
	}
	return k.records[i]
}

type adstockSinkKey struct{}

// withAdstockSink returns ctx carrying sink (nil clears any sink ctx
// carries, so a nested Process never fills its caller's).
func withAdstockSink(ctx context.Context, sink *adstockSink) context.Context {
	return context.WithValue(ctx, adstockSinkKey{}, sink)
}

// adstockSinkFrom returns the sink ctx carries, or nil.
func adstockSinkFrom(ctx context.Context) *adstockSink {
	k, _ := ctx.Value(adstockSinkKey{}).(*adstockSink)
	return k
}

// adstockLineage is the adstock records of req's regressions in resp,
// index-aligned with req.Regressions — computed only when a media-mix
// regression saves or want asks for them (nil otherwise, at no cost).
// ctx carries the request's bound models; s is the request's service
// (its params fold). A window the pass refuses — the run already
// applied it, so it cannot — leaves the records empty rather than
// failing a finished run.
func (s *Service) adstockLineage(ctx context.Context, req *types.Request, resp *types.Response, want bool) []map[string]model.Adstock {
	if resp == nil {
		return nil
	}
	need := false
	for _, spec := range req.Regressions {
		if spec != nil && (spec.Type == types.REG_MMM_HILL || spec.Type == types.REG_MMM_BAYES) && (want || spec.Save != "") {
			need = true
			break
		}
	}
	if !need {
		return nil
	}
	resolved := *req
	windows, err := processing.ResolveWindowScalars(s.extensions, model.SetFrom(ctx), req.Windows)
	if err != nil {
		windows = nil
	}
	resolved.Windows = windows
	out := make([]map[string]model.Adstock, len(req.Regressions))
	for i, spec := range req.Regressions {
		if i < len(resp.Regressions) {
			out[i] = model.AdstockLineage(&resolved, spec, resp.Regressions[i])
		}
	}
	return out
}
