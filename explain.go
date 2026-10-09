package pulse

import (
	"context"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/guide"
	"github.com/frankbardon/pulse/types"
)

// Explain says, in plain words, what a request will do — without
// running it — or what a result found. req names exactly one request
// root (Request, Composed, Chain, Facet or Sample), or exactly one
// result root (Response, ComposedResponse, ChainResponse or
// FacetResult) with at most its own request companion (Request,
// Composed, Chain or Facet respectively); anything else, or a Detail
// other than terse / full, is SERVICE_VALIDATION.
//
// Response mode reads the response into Findings — one per test,
// regression coefficient and fit, overlay layer, matrix and
// aggregation — each with a closed verdict read at the result's own
// alpha (adjusted when a correction ran), a strength band only where
// the figure's Interpretation names a convention, and the figures it
// rests on (undefined ones null). Without the companion an aggregation
// is described by count only and a caveat says the reading is partial.
// A ComposedResponse is read slot by slot then its batch overlays, a
// ChainResponse stage by stage with the request it echoes (else its
// companion), a FacetResult field by field with the overlays that
// decorate each; every finding's Slot is the wire path it reads, and
// the many-tests caveat counts the whole result. Response mode opens no
// cohort.
//
// The result's Steps describe each slot from its structure and its
// operator's plain purpose. When the root names a cohort Explain
// predicts it in-process with the instance's options and the cohort's
// sidecar facts — header, schema and sidecar only, never a record of a
// single-file cohort (a shard archive or anchored shard is read whole,
// as Predict reads it) — to name each operator smart defaults infer
// (DefaultsApplied, and the step marked Defaulted), the advisories the
// request raises and predict's verdict (Valid, with each refusal a
// caveat). Without a cohort nothing is checked, and a caveat says so. A
// cohort that cannot be read is DATA_FILE (ENCODING_INVALID for a
// malformed header or schema). A Compose root predicts each slot over
// its own cohort and validates the batch; a chain root predicts its
// first stage and validates the chain; a Sample root names no operator
// and is never predicted.
//
// Terse detail (the default) returns the summary sentence and the
// structured parts; full detail adds the narrative sentences, the
// glossary terms the operators link, their declared follow-ups and the
// assumptions of the request's tests and models. Under a feature
// profile no hidden operator or capability is named. Explain is not an
// observed operation: its predicts run in-process and fire no predict
// hooks or metrics.
func (p *Pulse) Explain(ctx context.Context, req descriptor.ExplainRequest) (*descriptor.ExplainResult, error) {
	if _, _, err := guide.ValidateExplainRequest(req); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return guide.Explain(p.svc.InstanceSnapshot(), req, guide.Checks{
		Request: func(r *types.Request) (*descriptor.Envelope, error) {
			return p.explainCheck(ctx, r.Cohort, func(src *guideCohort, opts *descx.PredictOptions) *descriptor.Envelope {
				return descx.Predict(src.rs, r, opts)
			})
		},
		Compose: func(c *types.ComposedRequest) (*descriptor.Envelope, error) {
			opts := p.predictOptions(ctx, sidecarFacts{})
			opts.SidecarLoader = func(path string) (string, map[string]string) {
				f := p.sidecarFacts(path)
				return f.weightVariable, f.measures
			}
			return descx.ValidateComposeWithOptions(c, &opts), nil
		},
		Chain: func(c *types.ChainRequest) (*descriptor.Envelope, error) {
			return p.explainCheck(ctx, c.Cohort, func(src *guideCohort, opts *descx.PredictOptions) *descriptor.Envelope {
				return descx.ValidateChainWithOptions(src.rs, c, opts)
			})
		},
		Facet: func(f *types.FacetRequest) (*descriptor.Envelope, error) {
			return p.explainCheck(ctx, f.Cohort, func(src *guideCohort, opts *descx.PredictOptions) *descriptor.Envelope {
				return descx.ValidateFacetWithOptions(src.rs, f, opts)
			})
		},
	})
}

// explainCheck opens cohort and runs check over it with the facade's
// predict options and the cohort's sidecar facts.
func (p *Pulse) explainCheck(ctx context.Context, cohort *types.Cohort, check func(*guideCohort, *descx.PredictOptions) *descriptor.Envelope) (*descriptor.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := resolveCohortPath(cohort)
	src, err := p.openGuideCohort(path, "explain")
	if err != nil {
		return nil, err
	}
	defer src.close()
	opts := p.predictOptions(ctx, p.sidecarFacts(path))
	return check(src, &opts), nil
}
