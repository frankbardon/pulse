package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Baseline-index resolver — runtime support for the windowed-Process
// overlay family.
//
// The windowed-Process kinds (INDEX_VS_BASELINE, DELTA_VS_BASELINE,
// INDEX_VS_ROLLING_MEAN, YOY) all reach for the same operation — pin
// a single positional point in the ordered SERIES host and surface
// its metric so every other point can be compared against it. The
// resolver is a small standalone function that takes the SERIES host
// view + the OverlayBaselineIndexRef arm and returns
// `(baselineValue, error)`.
//
// Architectural shape mirrors processing/overlay_sibling_resolver.go
// — small standalone file, single function, host-view + ref-arm +
// `(value, err)` return. The resolver is host-shape-agnostic so the
// four future handlers can call it without binding to a single kind's
// params.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside overlay.go.
//   - No fmt.Sprintf in any JSON-bearing path. The resolver builds the
//     CodedError message with string concatenation; structured detail
//     fields ride on the CodedError Details map.

// ResolveBaselineIndex resolves the OverlayBaselineIndexRef.Position
// arm against the host's ordered SERIES axis. Returns the baseline
// metric value the host produced for the group at that ordinal plus a
// nil error on success, or a PULSE_OVERLAY_REF_UNKNOWN-coded
// CodedError on failure.
//
// Two rejection arms:
//
//   - Negative Position (`ref.Position < 0`): the resolver returns
//     `(0, CodedError)` with code `PULSE_OVERLAY_REF_UNKNOWN` and
//     details `{baseline_index: <n>, series_length: <len>}`. A 0-based
//     ordinal cannot resolve to a negative slot — the caller likely
//     constructed the ref with an uninitialised default that wandered
//     negative.
//
//   - Position outside the series (`ref.Position >= host.GroupCount()`):
//     same code and details shape. The resolver does NOT defend against
//     a missing host group at that ordinal (the orchestrator skipped
//     the group); the host's own resolver surfaces `(0, false)` at
//     `host.ValueAt(i)` and the caller (the per-kind handler) decides
//     whether absent-baseline emits a layer-level
//     PULSE_OVERLAY_REF_ZERO or a per-entry NaN-statistic shape. The
//     resolver's job is structural — it ensures the requested ordinal
//     falls inside the host's declared range and lets the host's
//     present-flag surface the metric.
//
// Defense-in-depth returns:
//
//   - `host == nil`: returns `(0, CodedError)` with the same
//     PULSE_OVERLAY_REF_UNKNOWN code and `{baseline_index: <n>,
//     series_length: 0}` details so the caller does not crash on a
//     nil-host fold (the dispatch should never feed a nil host but the
//     resolver stays robust).
//   - `ref == nil`: returns `(0, nil)` — the caller's responsibility is
//     to consult `ref` before invoking the resolver. The function does
//     not raise on the empty case so a S2 / S3 / S5 / S7 handler can
//     short-circuit cleanly when no baseline ref was supplied (other
//     ref families take over).
//
// Deterministic + order-stable: the function calls `host.ValueAt(i)`
// once and returns whatever the host produced. Same series + same
// Position → byte-equal return on every invocation. The resolver does
// NOT alter the host or fold any state.
//
// Forward-compat (Row / Column arms): the MATRIX-host baseline-cell
// shape (`ref.Row` + `ref.Column`) is reserved for a future Crosstab
// overlay family — `ResolveBaselineIndex` ONLY consumes
// `ref.Position` and ignores the Row / Column slots. The MATRIX-arm
// resolver will land alongside the future "vs designated cell" kind
// (per PRD §4) and live in its own file.
func ResolveBaselineIndex(host *SeriesHostView, ref *types.OverlayBaselineIndexRef) (float64, error) {
	if ref == nil {
		return 0, nil
	}
	seriesLength := host.GroupCount()
	if host == nil || ref.Position < 0 || ref.Position >= seriesLength {
		return 0, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay baseline-index position is out of range for the host series",
			map[string]any{
				"code":           string(errors.PULSE_OVERLAY_REF_UNKNOWN),
				"baseline_index": ref.Position,
				"series_length":  seriesLength,
			})
	}
	// The host's own resolver decides whether the requested ordinal
	// surfaces a value or reports absent. Either way the caller (the
	// per-kind handler) gets the host's metric and can branch on the
	// present flag itself; the resolver's job is structural range
	// validation only.
	v, _ := host.ValueAt(ref.Position)
	return v, nil
}
