package pulse

import (
	"context"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// The no-execute verdicts on the three non-Request roots. Each is the
// Data of the envelope its predict method returns.
type (
	// ComposePredictResult is PredictCompose's envelope Data: the
	// batch verdict, the rejected overlay pairs, the per-spec overlay
	// cost, the Compose-host p-value count and every slot's advisories.
	ComposePredictResult = descx.ComposeValidationResult
	// FacetPredictResult is PredictFacet's envelope Data.
	FacetPredictResult = descx.FacetValidationResult
	// ChainPredictResult is PredictChain's envelope Data: the verdict,
	// the source schema and each stage's inferred output columns.
	ChainPredictResult = descx.ChainValidationResult
	// ChainOverlaySchemaDivergence is one chain-overlay (ref, target)
	// pair ChainPredictResult rejects.
	ChainOverlaySchemaDivergence = descx.ChainOverlaySchemaDivergence
)

// PredictCompose validates a Compose batch without executing it: each
// slot is predicted over its own cohort (header and schema only, read
// through the runtime's opener) with the instance's options, then the
// batch overlays are checked. Each slot's advisories ride the result,
// the sidecar-fed ones included — every slot cohort's SPSS sidecar is
// read for its measure levels and weight variable.
//
// The returned envelope is whole: its Data is a *ComposePredictResult
// whose Valid mirrors env.Errors emptiness, and each refusal is a coded
// env.Errors entry. A returned error is a cancelled ctx. Under a
// feature profile a hidden operator or slot predicts exactly as a
// never-registered one.
func (p *Pulse) PredictCompose(ctx context.Context, req *ComposedRequest) (*descriptor.Envelope, error) {
	return observed(p, ctx, opSpec{kind: observe.OpPredict, req: composedHasher(req)}, func(ctx context.Context) (*descriptor.Envelope, error) {
		return p.predictCompose(ctx, req)
	})
}

func (p *Pulse) predictCompose(ctx context.Context, req *ComposedRequest) (*descriptor.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts := p.composePredictOptions(ctx)
	env := descx.ValidateComposeWithOptions(req, &opts)
	if req != nil {
		for _, slot := range req.Requests {
			if slot != nil && slot.Cohort != nil {
				p.touchManaged(ctx, resolveCohortPath(slot.Cohort))
			}
		}
	}
	return env, nil
}

// PredictFacet validates a facet request against its cohort without
// executing it — the cohort's header and schema only, never a record of
// a single-file cohort (a shard archive or anchored shard is read whole,
// as Predict reads it). The envelope's Data is a *FacetPredictResult.
//
// A returned error is a cancelled ctx, a nil request or cohort
// (SERVICE_VALIDATION), or a cohort that cannot be read (DATA_FILE;
// ENCODING_INVALID for a malformed header or schema). Every request
// fault is a coded env.Errors entry with Valid false.
func (p *Pulse) PredictFacet(ctx context.Context, req *FacetRequest) (*descriptor.Envelope, error) {
	return observed(p, ctx, facetOp(observe.OpPredict, req), func(ctx context.Context) (*descriptor.Envelope, error) {
		if req == nil {
			return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "predict: facet request is required")
		}
		return p.predictCohortRoot(ctx, req.Cohort, "predict", true, func(src *guideCohort, opts *descx.PredictOptions) *descriptor.Envelope {
			return descx.ValidateFacetWithOptions(src.rs, req, opts)
		})
	})
}

// PredictChain validates a ProcessChain request against its cohort
// without executing it: stage 0 over the cohort's schema, each later
// stage over the schema the one before it produces. It reads what
// PredictFacet reads and returns errors the same way; the envelope's
// Data is a *ChainPredictResult. A chain raises no advisories.
func (p *Pulse) PredictChain(ctx context.Context, req *ChainRequest) (*descriptor.Envelope, error) {
	return observed(p, ctx, chainOp(observe.OpPredict, req), func(ctx context.Context) (*descriptor.Envelope, error) {
		if req == nil {
			return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "predict: chain request is required")
		}
		return p.predictCohortRoot(ctx, req.Cohort, "predict", true, func(src *guideCohort, opts *descx.PredictOptions) *descriptor.Envelope {
			return descx.ValidateChainWithOptions(src.rs, req, opts)
		})
	})
}

// composePredictOptions is the option set a Compose predict runs with:
// the instance's predict options plus the SidecarLoader that reads each
// slot cohort's sidecar facts (a Compose has no single cohort, so the
// single-cohort sidecar fields stay empty). Shared by PredictCompose
// and Explain's Compose check.
func (p *Pulse) composePredictOptions(ctx context.Context) descx.PredictOptions {
	opts := p.predictOptions(ctx, sidecarFacts{})
	opts.SidecarLoader = p.sidecarLoader
	return opts
}

// sidecarLoader is PredictOptions.SidecarLoader over the facade's one
// sidecar read.
func (p *Pulse) sidecarLoader(path string) (string, map[string]string) {
	f := p.sidecarFacts(path)
	return f.weightVariable, f.measures
}

// predictCohortRoot opens cohort for a single-cohort predict and runs
// check over it with the instance's predict options and the cohort's
// sidecar facts; op ("predict", "explain") prefixes its error messages.
// touch slides the managed-import TTL after a successful open, as
// Predict does. Shared by PredictFacet, PredictChain and Explain.
func (p *Pulse) predictCohortRoot(ctx context.Context, cohort *types.Cohort, op string, touch bool, check func(*guideCohort, *descx.PredictOptions) *descriptor.Envelope) (*descriptor.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cohort == nil {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, op+": a cohort is required")
	}
	path := resolveCohortPath(cohort)
	src, err := p.openGuideCohort(path, op)
	if err != nil {
		return nil, err
	}
	defer src.close()
	opts := p.predictOptions(ctx, p.sidecarFacts(path))
	env := check(src, &opts)
	if touch {
		p.touchManaged(ctx, path)
	}
	return env, nil
}
