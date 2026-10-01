package processing

import (
	"github.com/frankbardon/pulse/types"
)

// Per-slot COMPONENTS view for the COMPOSE-host overlay family.
//
// COMPOSE-host handlers have always read the MatrixPayload and nothing
// else: `ApplyComposeOverlays(specs, responses, labels)` already hands
// every handler the FULL per-slot `*types.Response`, and every Response
// carries `Components *ResponseComponents` with a
// `Crosstab *CrosstabComponents` inside. The data was already at the
// handler boundary — no plumbing was missing. What was missing is a
// view with nil-safe, bounds-checked accessors, so that each handler
// is not re-deriving the same four guards against a matrix of slices
// of maps.
//
// This file lands that view as a SHARED surface for the whole
// COMPOSE-host family, not a panel-private helper. Nothing here is
// wired into the dispatcher and no handler's behaviour changes: a
// handler opts in by constructing a ComposeHostView from the
// (reference, targets) it already receives, which is exactly why the
// `composeOverlayHandler` signature does not move.
//
// Relationship to CrosstabHostView. The per-slot accessors are
// deliberately NOT a second implementation. Each slot builds a
// CrosstabHostView over its own (payload, components) pair and the
// accessors below forward to it, so the COMPOSE arm and the MATRIX arm
// can never read a different number out of the same components block
// (TestComposeHostView_MatchesCrosstabHostView pins that). The reads
// this file implements directly — the generic margin / grand-total key
// lookups — are the ones CrosstabHostView does not expose; they use the
// same componentToFloat coercion, not a private copy.
//
// THE STATE TRI-STATE IS THE POINT OF THE TYPE. A slot whose
// components were DISABLED (pulse.Options.DisableComponents, or the
// per-request types.Request.DisableComponents override) is a
// CONFIGURATION condition the caller fixes by turning components back
// on. A slot that carries components but not the key a handler asked
// for is a DATA condition — that figure genuinely was not emitted for
// that coordinate. Collapsing both into one `ok=false` destroys the
// difference and forces every handler to answer both with the same
// diagnostic, which is how a fixable misconfiguration gets reported as
// a degenerate statistic. So: the per-read accessors keep the house
// two-value `(value, ok)` shape and answer the DATA question, while
// State() answers the CONFIGURATION question ONCE per slot, up front —
// the same posture CrosstabHostView.HasComponents() already
// establishes for the MATRIX arm, widened from a bool to a four-valued
// enum because a COMPOSE host has two more ways to have no components
// than a single crosstab does (a slot that is not a crosstab at all,
// and a slot index that resolves to nothing).
//
// Structural invariants: this file MUST NOT import service/ or
// descriptor/, and raises no errors — a view reports what it can see
// and leaves the choice of code to the handler.

// ComposeComponentsState classifies WHY a Compose slot can or cannot
// serve a components read. Handlers branch on it once, before any
// per-coordinate read, so a configuration failure and a data condition
// reach the wire as different diagnostics.
type ComposeComponentsState int

const (
	// ComposeComponentsSlotAbsent — there is no such slot. The index
	// is out of range, or the slot's *types.Response is nil (a slot
	// that failed under FailFast=false). Caller-side resolution
	// failure: no request knob changes it.
	ComposeComponentsSlotAbsent ComposeComponentsState = iota

	// ComposeComponentsDisabled — the slot produced a response but no
	// Components block at all. That is the DisableComponents opt-out
	// (engine-level pulse.Options.DisableComponents or the per-request
	// types.Request.DisableComponents override). A CONFIGURATION
	// condition: the figure exists, the run was told not to emit it.
	ComposeComponentsDisabled

	// ComposeComponentsNonCrosstab — the slot emitted components, but
	// no Crosstab block inside them. The slot is not a crosstab
	// (a grouped Process result, a scalar), so there are no per-cell
	// or per-margin figures to read however the knobs are set. Kept
	// distinct from Disabled because the fix is different: this one
	// needs a different REQUEST, not a different option.
	ComposeComponentsNonCrosstab

	// ComposeComponentsPresent — the slot carries a CrosstabComponents
	// block. Any accessor answering ok=false from here on is reporting
	// a DATA condition: that key, index or coordinate genuinely has no
	// figure.
	ComposeComponentsPresent
)

// Available reports whether the state permits a components read. Only
// ComposeComponentsPresent does.
func (s ComposeComponentsState) Available() bool {
	return s == ComposeComponentsPresent
}

// String names the state for diagnostics. Snake-case so a handler can
// drop it straight into an OverlayWarning Details map without
// reformatting.
func (s ComposeComponentsState) String() string {
	switch s {
	case ComposeComponentsSlotAbsent:
		return "slot_absent"
	case ComposeComponentsDisabled:
		return "components_disabled"
	case ComposeComponentsNonCrosstab:
		return "slot_not_crosstab"
	case ComposeComponentsPresent:
		return "present"
	}
	return "unknown"
}

// ComposeSlotView is the read-only per-slot window one COMPOSE-host
// overlay slot exposes to a handler: its state, its matrix host view,
// and the crosstab components accessors keyed to that slot's own axes.
//
// Every method is safe on a nil receiver and on every state, so a
// handler may read first and classify later. The zero value is not
// useful — construct through ComposeHostView.
type ComposeSlotView struct {
	// response is the finalised slot response. Read-only; nil for an
	// absent slot.
	response *types.Response

	// state is the up-front classification. See
	// ComposeComponentsState.
	state ComposeComponentsState

	// host is a CrosstabHostView over this slot's own (payload,
	// crosstab components) pair. Nil for an absent slot. Every
	// components accessor below forwards to it so the COMPOSE arm and
	// the MATRIX arm cannot drift.
	host *CrosstabHostView

	// components is the slot's crosstab components block, nil unless
	// state == ComposeComponentsPresent. Held directly for the generic
	// margin / grand-total key reads CrosstabHostView does not expose.
	components *types.CrosstabComponents
}

// composeSlotAbsent is the shared view every out-of-range or nil-slot
// lookup returns, so ComposeHostView.Slot never hands back nil and
// never allocates on the miss path. It is immutable — the struct has
// no exported fields and no method mutates it.
var composeSlotAbsent = &ComposeSlotView{state: ComposeComponentsSlotAbsent}

// ComposeHostView is the read-only window over the ordered per-slot
// responses a COMPOSE-host overlay fold operates on — the same slice
// ApplyComposeOverlays already holds, indexed the same way, so a
// handler's (refIdx, targetIdxs) pair addresses it directly.
type ComposeHostView struct {
	slots []*ComposeSlotView
}

// NewComposeHostView wraps the ordered per-slot responses as a host
// view. `responses` is the slice ApplyComposeOverlays received, in
// ComposedRequest.Requests order; nil entries (slots that failed under
// FailFast=false) classify as ComposeComponentsSlotAbsent. The view
// copies no payload and mutates nothing — callers must not mutate the
// responses during overlay execution.
//
// Handlers that address slots by their PANEL position rather than by
// request index build a second view over the panel ordering
// (reference first, then targets); the constructor is cheap enough for
// that — one struct per slot, no matrix walk.
func NewComposeHostView(responses []*types.Response) *ComposeHostView {
	if len(responses) == 0 {
		return &ComposeHostView{}
	}
	slots := make([]*ComposeSlotView, len(responses))
	for i, resp := range responses {
		slots[i] = newComposeSlotView(resp)
	}
	return &ComposeHostView{slots: slots}
}

// newComposeSlotView classifies one slot response and builds its view.
// The classification ladder is the whole disabled-vs-absent contract in
// one place: nil response, then nil Components (the opt-out), then nil
// Crosstab (a non-crosstab slot), then present.
func newComposeSlotView(resp *types.Response) *ComposeSlotView {
	if resp == nil {
		return composeSlotAbsent
	}
	var payload *types.MatrixPayload
	if resp.Crosstab != nil {
		payload = resp.Crosstab.Matrix
	}
	switch {
	case resp.Components == nil:
		return &ComposeSlotView{
			response: resp,
			state:    ComposeComponentsDisabled,
			host:     NewCrosstabHostView(payload),
		}
	case resp.Components.Crosstab == nil:
		return &ComposeSlotView{
			response: resp,
			state:    ComposeComponentsNonCrosstab,
			host:     NewCrosstabHostView(payload),
		}
	}
	comps := resp.Components.Crosstab
	return &ComposeSlotView{
		response:   resp,
		state:      ComposeComponentsPresent,
		host:       newCrosstabHostViewWithComponents(payload, comps),
		components: comps,
	}
}

// SlotCount returns the number of slots the view wraps. Zero on a nil
// view.
func (v *ComposeHostView) SlotCount() int {
	if v == nil {
		return 0
	}
	return len(v.slots)
}

// Slot returns the per-slot view at index i. NEVER nil: an index out
// of range (or a nil host view) yields the shared absent-state view
// whose every accessor answers ok=false. That is what lets a handler
// address slots by the (refIdx, targetIdxs) it was given without a
// bounds check at every call site — the bound is checked here, once.
func (v *ComposeHostView) Slot(i int) *ComposeSlotView {
	if v == nil || i < 0 || i >= len(v.slots) {
		return composeSlotAbsent
	}
	return v.slots[i]
}

// --- Slot-level classification --------------------------------------

// State returns the slot's components classification. Nil view ⇒
// ComposeComponentsSlotAbsent.
func (s *ComposeSlotView) State() ComposeComponentsState {
	if s == nil {
		return ComposeComponentsSlotAbsent
	}
	return s.state
}

// HasComponents reports whether the slot can serve a components read —
// the COMPOSE twin of CrosstabHostView.HasComponents. Handlers gate on
// this once up front; State() says WHY when it is false.
func (s *ComposeSlotView) HasComponents() bool {
	return s.State().Available()
}

// Response returns the underlying slot response, nil for an absent
// slot. Read-only.
func (s *ComposeSlotView) Response() *types.Response {
	if s == nil {
		return nil
	}
	return s.response
}

// Components returns the slot's CrosstabComponents block, nil unless
// the state is Present. Read-only.
func (s *ComposeSlotView) Components() *types.CrosstabComponents {
	if s == nil {
		return nil
	}
	return s.components
}

// Matrix returns the slot's CrosstabHostView so a handler that already
// speaks the MATRIX-arm vocabulary can use it directly. Nil for an
// absent slot; the view itself is nil-safe when the slot carries no
// matrix payload.
func (s *ComposeSlotView) Matrix() *CrosstabHostView {
	if s == nil {
		return nil
	}
	return s.host
}

// --- Axis shape (payload-side, independent of components) -----------

// RowCount returns the slot's row tuple count. Zero when the slot has
// no matrix.
func (s *ComposeSlotView) RowCount() int { return s.Matrix().RowCount() }

// ColumnCount returns the slot's column tuple count.
func (s *ComposeSlotView) ColumnCount() int { return s.Matrix().ColumnCount() }

// RowAxisDepth returns the slot's row-key tuple length — the count of
// row groupers this slot's crosstab declared. Slots in one Compose
// panel may declare DIFFERENT depths, which is why this is per slot.
func (s *ComposeSlotView) RowAxisDepth() int { return s.Matrix().RowAxisDepth() }

// ColumnAxisDepth returns the slot's column-key tuple length.
func (s *ComposeSlotView) ColumnAxisDepth() int { return s.Matrix().ColumnAxisDepth() }

// RowKey returns the raw row-key tuple at rowIdx, or nil when out of
// range / the slot carries no matrix. Positional dim access, not the
// joined label — prefix-scoped sample sizes need the tuple.
func (s *ComposeSlotView) RowKey(rowIdx int) types.AxisKey {
	return s.Matrix().rowKey(rowIdx)
}

// ColumnKey returns the raw column-key tuple at colIdx, or nil.
func (s *ComposeSlotView) ColumnKey(colIdx int) types.AxisKey {
	return s.Matrix().columnKey(colIdx)
}

// --- Cell components -------------------------------------------------

// CellComponentFloat reads a numeric key from the slot's
// CellComponents[rowIdx][colIdx]. (0, false) when components are
// unavailable for the slot, the coordinate is out of range or nil, or
// the key is absent / non-numeric.
func (s *ComposeSlotView) CellComponentFloat(rowIdx, colIdx int, key string) (float64, bool) {
	return s.Matrix().CellComponentFloat(rowIdx, colIdx, key)
}

// CellN reads the universal-floor "n" counter from the slot's
// CellComponents[rowIdx][colIdx].
func (s *ComposeSlotView) CellN(rowIdx, colIdx int) (int, bool) {
	return s.Matrix().CellN(rowIdx, colIdx)
}

// CellCount reads the slot's per-cell RECORD count from
// CrosstabComponents.CellCounts — the counted figure beside the cell
// aggregator's own components, not derived from them.
func (s *ComposeSlotView) CellCount(rowIdx, colIdx int) (int, bool) {
	c := s.Components()
	if c == nil || rowIdx < 0 || rowIdx >= len(c.CellCounts) {
		return 0, false
	}
	row := c.CellCounts[rowIdx]
	if colIdx < 0 || colIdx >= len(row) {
		return 0, false
	}
	return row[colIdx], true
}

// --- Margin components -----------------------------------------------

// RowMarginN reads the slot's per-row margin record count.
func (s *ComposeSlotView) RowMarginN(rowIdx int) (int, bool) {
	return s.Matrix().RowMarginN(rowIdx)
}

// ColumnMarginN reads the slot's per-column margin record count.
func (s *ComposeSlotView) ColumnMarginN(colIdx int) (int, bool) {
	return s.Matrix().ColumnMarginN(colIdx)
}

// RowMarginComponentFloat reads a numeric key out of the slot's
// RowMarginComponents[rowIdx]. The generic form behind the named
// margin readers — CrosstabHostView exposes only the distinct-key
// probe, so this is implemented here rather than forwarded, using the
// same componentToFloat coercion.
func (s *ComposeSlotView) RowMarginComponentFloat(rowIdx int, key string) (float64, bool) {
	c := s.Components()
	if c == nil || rowIdx < 0 || rowIdx >= len(c.RowMarginComponents) {
		return 0, false
	}
	return componentMapFloat(c.RowMarginComponents[rowIdx], key)
}

// ColumnMarginComponentFloat is RowMarginComponentFloat's column-axis
// twin, reading ColumnMarginComponents[colIdx].
func (s *ComposeSlotView) ColumnMarginComponentFloat(colIdx int, key string) (float64, bool) {
	c := s.Components()
	if c == nil || colIdx < 0 || colIdx >= len(c.ColumnMarginComponents) {
		return 0, false
	}
	return componentMapFloat(c.ColumnMarginComponents[colIdx], key)
}

// GrandTotalComponentFloat reads a numeric key out of the slot's
// GrandTotalComponents map.
func (s *ComposeSlotView) GrandTotalComponentFloat(key string) (float64, bool) {
	c := s.Components()
	if c == nil {
		return 0, false
	}
	return componentMapFloat(c.GrandTotalComponents, key)
}

// GrandTotalN reads the slot's grand-total RECORD count from
// CrosstabComponents.GrandTotalCount. ok=false only when components are
// unavailable — a genuine zero grand total is (0, true).
func (s *ComposeSlotView) GrandTotalN() (int, bool) {
	c := s.Components()
	if c == nil {
		return 0, false
	}
	return c.GrandTotalCount, true
}

// --- Distinct-key figures --------------------------------------------
//
// These forward to the MATRIX arm so the COMPOSE host reads distinct
// keys through the SAME ordered key probe (distinct_count, then
// cardinality) and the SAME exact-set cell-aggregator admission. A
// second spelling of either rule here is precisely how one arm would
// start reporting distinct VALUES of the measure field as a respondent
// count while the other reports respondents.

// RowMarginDistinctN reads the slot's per-row margin as the cell
// aggregator's distinct-KEY cardinality. ok=false when the margin was
// never emitted or carries no distinct key — deliberately NOT zero,
// because an unemitted margin is not a zero-sized one.
func (s *ComposeSlotView) RowMarginDistinctN(rowIdx int) (int, bool) {
	return s.Matrix().RowMarginDistinctN(rowIdx)
}

// ColumnMarginDistinctN is RowMarginDistinctN's column-axis twin.
func (s *ComposeSlotView) ColumnMarginDistinctN(colIdx int) (int, bool) {
	return s.Matrix().ColumnMarginDistinctN(colIdx)
}

// CellAggregatorIdentity classifies the slot's CELL aggregator from the
// component key set it emitted, by exact operator-key-set match.
// ok=false when components are unavailable or the key set matches no
// known distinct-bearing aggregator.
func (s *ComposeSlotView) CellAggregatorIdentity() (types.AggregationType, bool) {
	return s.Matrix().CellAggregatorIdentity()
}

// AdmitsDistinctKeyN reports whether the slot's cell aggregator carries
// a distinct-KEY cardinality, returning that aggregator and the
// component key its figure rides on.
func (s *ComposeSlotView) AdmitsDistinctKeyN() (types.AggregationType, string, bool) {
	return s.Matrix().AdmitsDistinctKeyN()
}

// componentMapFloat reads one numeric key out of a components map.
// (0, false) for a nil map, an absent key or a non-numeric value —
// the three DATA conditions, all of which the caller reads against a
// slot whose State() already said Present.
func componentMapFloat(comp map[string]any, key string) (float64, bool) {
	if comp == nil {
		return 0, false
	}
	v, ok := comp[key]
	if !ok {
		return 0, false
	}
	return componentToFloat(v)
}
