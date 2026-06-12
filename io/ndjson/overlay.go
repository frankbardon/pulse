package ndjson

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// NDJSON overlay embedding implementation per
// research/export-embedding-shape.md § 6.
//
// Response.Overlays land as ONE trailing line after the last host-
// record line. The trailing block carries a single key `_overlays`
// whose value is the []*OverlayLayer slice serialised via
// encoding/json — the wire shape mirrors descriptor.Envelope.Data.Overlays
// exactly so a reader can `json.Unmarshal` the line straight into
// []*types.OverlayLayer. The `_overlays` key has a leading underscore
// to reserve the namespace from any host field name (the underscore
// prefix is already reserved for the `_margin` axis-key tag per the
// Crosstab long-form rule).
//
// Streaming semantics:
//
//   - Trailer ONLY lands when SetOverlays handed the writer a non-empty
//     []*OverlayLayer slice. nil OR empty leaves the NDJSON file byte-
//     identical to a pre-overlay export (no trailer line, no extra
//     bytes).
//   - The trailer is the LAST line of the file. Streaming consumers
//     that do not understand overlays can ignore it (the `_overlays`
//     key signals "this is not a host record"); overlay-aware consumers
//     buffer until EOF and re-read the last line.
//   - The streaming-export path (pulse api process --stream) does NOT
//     invoke this trailer (matches the descriptor.Envelope rule:
//     streaming output skips the envelope by construction).
//
// Round-trip rule: ReadOverlays scans the file line-by-line and returns
// the slice carried by the LAST line whose first key is `_overlays`.
// Returns nil when no trailer is present — matches the Arrow / Excel /
// Parquet "absent means nil, not error" contract.

// OverlayTrailerKey is the JSON object key the trailing sidecar line
// carries. Reserved namespace — pulse cohort schemas never produce a
// field with this name (the underscore prefix is reserved per the
// Crosstab long-form `_margin` precedent). Readers detect the trailer
// by checking whether a line's parsed object contains this key.
const OverlayTrailerKey = "_overlays"

// overlayTrailer is the wire shape of the trailing line:
//
//	{"_overlays": [...OverlayLayer...]}
//
// Marshalled via encoding/json per CLAUDE.md "Structural defense bans"
// — no fmt.Sprintf-built JSON.
type overlayTrailer struct {
	Overlays []*types.OverlayLayer `json:"_overlays"`
}

// SetOverlays records the Response.Overlays layers the export pipeline
// wants the writer to embed in the NDJSON file. Each layer lands inside
// a single trailing line `{"_overlays": [...]}` appended after the last
// host-record line per research/export-embedding-shape.md § 6. nil or
// empty layers leave the NDJSON output byte-identical to a pre-overlay
// export (no trailer line lands). Implements pio.OverlayAwareWriter.
//
// Emission happens at Close() time so the host record stream stays
// untouched until the file is finalised. Must be called BEFORE Close.
func (w *Writer) SetOverlays(layers []*types.OverlayLayer) {
	w.overlays = layers
}

// flushOverlayTrailer appends the trailing `{"_overlays": [...]}` block
// to the writer's internal buffer when overlays were supplied. No-op
// when SetOverlays was never called, was called with a nil slice, or
// was called with an empty slice — matches the byte-identity contract.
// Idempotent: a second call after the trailer landed is a no-op so
// Close() can be invoked multiple times.
func (w *Writer) flushOverlayTrailer() error {
	if w.overlaysWritten {
		return nil
	}
	if len(w.overlays) == 0 {
		// No-op marks the writer as "done" so a follow-up SetOverlays +
		// Close cannot retroactively append a trailer onto an already-
		// finalised buffer. The contract is "SetOverlays BEFORE Close".
		w.overlaysWritten = true
		return nil
	}
	trailer := overlayTrailer{Overlays: w.overlays}
	encoded, err := json.Marshal(trailer)
	if err != nil {
		return fmt.Errorf("ndjson.Writer: marshalling overlay trailer: %w", err)
	}
	w.buf.Write(encoded)
	w.buf.WriteByte('\n')
	w.overlaysWritten = true
	return nil
}

// ReadOverlays walks the NDJSON source and returns the overlay layers
// carried by the trailing `{"_overlays": [...]}` line, if present.
// Returns nil (not error) when no trailer is found — matches the
// Arrow / Excel / Parquet "absent means nil" contract.
//
// The reader scans every line; if multiple trailing-shaped lines are
// present (a defensive case the writer never produces) the LAST one
// wins. Lines whose first parsed key is not the trailer key are skipped
// silently — host record lines remain consumable through ReadRows.
//
// ReadOverlays is independent of ReadHeader / ReadRows — the underlying
// byte source (data or afero path) is re-read so calling ReadOverlays
// before, after, or instead of ReadRows is safe.
func (r *Reader) ReadOverlays() ([]*types.OverlayLayer, error) {
	data, err := r.bytesForOverlayScan()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Match the host-record path: large lines may carry sizeable matrix
	// payloads, so widen the scanner buffer to the same ceiling
	// bufio.Scanner uses on demand. Stay below MaxInt32 so 32-bit
	// platforms do not overflow.
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	var lastTrailer []*types.OverlayLayer
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if !lineIsOverlayTrailer(line) {
			continue
		}
		var trailer overlayTrailer
		if err := json.Unmarshal(line, &trailer); err != nil {
			return nil, fmt.Errorf("ndjson.Reader: unmarshalling overlay trailer: %w", err)
		}
		lastTrailer = trailer.Overlays
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ndjson.Reader: scanning for overlay trailer: %w", err)
	}
	return lastTrailer, nil
}

// bytesForOverlayScan returns the underlying NDJSON byte source for the
// trailing-line scan. Mirrors Reader.init's source resolution but does
// NOT mutate r.scanner / r.header so ReadOverlays composes with
// ReadHeader / ReadRows in any order.
func (r *Reader) bytesForOverlayScan() ([]byte, error) {
	if r.data != nil {
		return r.data, nil
	}
	if r.fs == nil {
		return nil, nil
	}
	data, err := afero.ReadFile(r.fs, r.path)
	if err != nil {
		return nil, fmt.Errorf("ndjson.Reader: reading %s for overlay trailer: %w", r.path, err)
	}
	return data, nil
}

// lineIsOverlayTrailer cheaply detects whether the JSON object starting
// at `line` has OverlayTrailerKey as its FIRST key. The trailer is
// emitted by this package with the key in slot 0 (the only key), so the
// check parses just the opening `{ "_overlays"` prefix instead of
// unmarshalling the whole payload — important when the trailer carries
// a large matrix overlay.
func lineIsOverlayTrailer(line []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(line))
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return false
	}
	if !dec.More() {
		return false
	}
	tok, err = dec.Token()
	if err != nil {
		return false
	}
	key, ok := tok.(string)
	if !ok {
		return false
	}
	return key == OverlayTrailerKey
}
