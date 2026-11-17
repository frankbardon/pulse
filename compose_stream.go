package pulse

import (
	"io"

	"github.com/frankbardon/pulse/internal/composestream"
)

// ComposeStreamOptions shapes WriteComposeStream's NDJSON. The zero
// value is the default stream `pulse api compose --stream` prints.
type ComposeStreamOptions struct {
	// TerminalFits ends the stream with one {"fits":{...}} line — the
	// buffered ComposedResponse.Fits object, keyed by fit name (a
	// sweep-bound fit included, a failed fit as its identity + error) —
	// after every row and every other trailing line, the sweep ranking
	// line included. Written only when the request declared fits. Off
	// (the default) the stream is byte-identical to one written without
	// the option. CLI: --stream-fits.
	TerminalFits bool
}

// WriteComposeStream writes a finished Compose run — resp, the response
// Compose or ComposeParallel returned for req — to w as NDJSON, the
// stream `pulse api compose --stream` prints: one {"index":N,"row":{...}}
// line per slot row in slot order (each row shaped by its slot's
// `return`), then one line each, when present, for the run-time
// warnings, a sampling sweep's sweep_seed, a ranked sweep's ranking
// (+ ranking_profile), a banner, the optimize blocks and the
// comparisons, then the opt-in terminal fits line. Overlays and the
// post-fit results are buffered-only. Undefined figures are JSON null.
// It reads resp only; nothing is executed.
func WriteComposeStream(w io.Writer, req *ComposedRequest, resp *ComposedResponse, opts ComposeStreamOptions) error {
	return composestream.Write(w, req, resp, composestream.Options{TerminalFits: opts.TerminalFits})
}
