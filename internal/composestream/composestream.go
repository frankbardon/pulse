// Package composestream writes a finished Compose run as the NDJSON
// stream `pulse api compose --stream` prints and pulse.WriteComposeStream
// hands an embedder: one {"index":N,"row":{...}} line per slot row (slot
// order, each row shaped by its slot's `return`), then the trailing
// records — warnings, sweep_seed, ranking (+ ranking_profile,
// sweep_trace), banner,
// optimizations, comparisons — and, opt-in (Options.TerminalFits), one
// final {"fits":{...}} record. Overlays surface only in the buffered
// response, never on the stream.
package composestream

import (
	"encoding/json"
	"io"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/returnshape"
	"github.com/frankbardon/pulse/internal/runwarn"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

// Options shapes the stream. The zero value is the default stream.
type Options struct {
	// TerminalFits ends the stream with one {"fits":{...}} record: the
	// buffered ComposedResponse.Fits object (keyed by fit name, a failed
	// fit as its identity + error), written after every row and every
	// other record. Written only when the request declared fits; off,
	// the stream is byte-identical to the pre-option one.
	TerminalFits bool
}

// Write writes resp — the response Compose / ComposeParallel returned
// for req — to w as NDJSON. A nil resp writes only the records req
// alone decides (an empty ranking for a ranked sweep).
func Write(w io.Writer, req *types.ComposedRequest, resp *types.ComposedResponse, opts Options) error {
	enc := json.NewEncoder(w)
	if resp != nil {
		for i, sub := range resp.Responses {
			if sub == nil {
				continue
			}
			for _, row := range sub.Data {
				// Compose already pruned each slot's rows under its
				// `return`; the row encoder adds the slot's precision.
				// Unshaped slot: the row is written exactly as before.
				body, err := returnshape.MarshalRow(sub, row)
				if err != nil {
					return err
				}
				if err := encode(enc, map[string]any{"index": i, "row": json.RawMessage(body)}); err != nil {
					return err
				}
			}
		}
	}
	// A batch that warned adds one {"warnings":[...]} record after the
	// rows (slot entries located by details.request).
	if ws := runwarn.Entries(resp); len(ws) > 0 {
		if err := encode(enc, map[string][]*descriptor.EnvelopeEntry{"warnings": ws}); err != nil {
			return err
		}
	}
	// A sampling sweep (random / lhs) adds one {"sweep_seed":N} record —
	// the effective seed, so the streamed run can be replayed.
	if resp != nil && resp.SweepSeed != nil {
		if err := encode(enc, map[string]any{"sweep_seed": *resp.SweepSeed}); err != nil {
			return err
		}
	}
	if req == nil {
		return nil
	}
	// A ranked sweep ends the stream with one {"ranking":[...]} record,
	// even when every slot was left out (an empty list).
	if req.Sweep != nil && req.Sweep.Rank != nil {
		ranking := []types.RankEntry{}
		if resp != nil && resp.Ranking != nil {
			ranking = resp.Ranking
		}
		line := map[string]any{"ranking": ranking}
		// rank.profile adds the per-axis profile to the same record.
		if req.Sweep.Rank.Profile {
			profile := map[string][]types.RankProfileEntry{}
			if resp != nil && resp.RankingProfile != nil {
				profile = resp.RankingProfile
			}
			line["ranking_profile"] = profile
		}
		// An adaptive mode (de / bo) adds its round trace to the same
		// record.
		if sweep.IsAdaptive(req.Sweep.Mode) {
			trace := []types.SweepTraceEntry{}
			if resp != nil && resp.SweepTrace != nil {
				trace = resp.SweepTrace
			}
			line["sweep_trace"] = trace
		}
		if err := encode(enc, line); err != nil {
			return err
		}
	}
	// A banner ends the stream with one {"banner":{...}} record: the
	// stitched tables are a matrix, never rows.
	if req.Banner != nil && resp != nil && resp.Banner != nil {
		if err := encode(enc, map[string]any{"banner": resp.Banner}); err != nil {
			return err
		}
	}
	// Optimize blocks end the stream with one {"optimizations":[...]}
	// record: each block's optimum is one solve, never rows, so it is
	// written whole (a failed block as its {name, direction, error}).
	if len(req.Optimize) > 0 && resp != nil && len(resp.Optimizations) > 0 {
		if err := encode(enc, map[string]any{"optimizations": resp.Optimizations}); err != nil {
			return err
		}
	}
	// Nested-model comparisons end the stream with one
	// {"comparisons":[...]} record: each is one test over two fits,
	// never rows (a failed one as its identity and error).
	if len(req.Compare) > 0 && resp != nil && len(resp.Comparisons) > 0 {
		if err := encode(enc, map[string]any{"comparisons": resp.Comparisons}); err != nil {
			return err
		}
	}
	// Opt-in: the fits end the stream with one {"fits":{...}} record —
	// the buffered `fits` object, so a streamed run keeps the models its
	// slots scored against. Last, after every other record.
	if opts.TerminalFits && len(req.Fits) > 0 {
		fits := map[string]*types.FitResult{}
		if resp != nil && resp.Fits != nil {
			fits = resp.Fits
		}
		if err := encode(enc, map[string]any{"fits": fits}); err != nil {
			return err
		}
	}
	return nil
}

// encode writes v under the wire rule for undefined figures: a
// non-finite float is JSON null (types.MarshalFinite), never a
// `json: unsupported value` failure.
func encode(enc *json.Encoder, v any) error {
	body, err := types.MarshalFinite(v)
	if err != nil {
		return err
	}
	return enc.Encode(json.RawMessage(body))
}
