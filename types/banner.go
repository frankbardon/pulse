package types

// BannerSpec is a Compose banner table (U47 #326): one base request, a
// list of stub SECTIONS (each its own rows, measures and filter, so KPIs
// with different bases sit in one table) and a list of column GROUPS
// (the banner: Gender, Age, Region, …), plus an optional Total block.
// Before any slot runs, Pulse expands it into ordinary crosstab slots —
// per section, the Total slot (a crosstab with no column grouper, the
// implicit Total column) when Total is set, then one slot per group in
// declared order — appended after the ComposedRequest's explicit
// Requests and any sweep slots. Each expanded slot then runs exactly
// like a hand-written one, and ComposedResponse.Banner stitches the
// finished slots back into one table per section.
//
// It rides as the optional `banner` slot on ComposedRequest, gated by
// capability:compose_banner. A nil spec is absent: the slot is
// `omitempty`, so a banner-free request is byte-identical on the wire
// and under CanonicalHash.
type BannerSpec struct {
	// Request is the base request every banner slot starts from: the
	// cohort, weight, multiplicity, `return`, filterers and — under
	// `crosstab` — the shared rows, cell(s), margins and normalize. It
	// may not set `label` (banner slots are labelled
	// `<section>:<group>`), crosstab `columns` (the groups own them) or
	// crosstab shape `long` (the stitch reads the matrix). Its `labels`
	// bindings are scoped per slot: a binding rides only the slots whose
	// crosstab rows or columns group by its field, so the Total and the
	// other groups' slots never warn PULSE_LABEL_NOT_APPLIED for it; a
	// binding no banner slot groups by stays on every slot and warns.
	Request *Request `json:"request"`

	// Sections are the stub sections, at least one, labels non-empty
	// and unique.
	Sections []BannerSection `json:"sections"`

	// Groups are the banner's column groups, labels non-empty and
	// unique; each needs at least one column grouper. May be empty only
	// when Total is set.
	Groups []BannerGroup `json:"groups,omitempty"`

	// Total adds the Total block — per section, one slot with no column
	// grouper — stitched first. The Total block takes no part in the
	// banner overlays, so it is compared to nothing and never lettered.
	Total bool `json:"total,omitempty"`

	// Overlays are request overlays appended to every GROUP slot after
	// the base request's own (the Total slot never receives them). A
	// pairwise kind carrying params.letters lets the stitch letter the
	// columns of each group (BannerSectionResult.Letters). A section
	// that sets its own Overlays uses those INSTEAD of these
	// (BannerSection.Overlays).
	Overlays []OverlaySpec `json:"overlays,omitempty"`
}

// BannerSection is one stub section of a banner: what each of its slots
// overrides on the base request.
type BannerSection struct {
	// Label names the section; the first half of its slots' labels.
	Label string `json:"label"`

	// Filterers are appended after the base request's own, so the
	// section can have its own base (all adults vs aided-aware).
	Filterers []*Filterer `json:"filterers,omitempty"`

	// Rows, when set, replaces the base crosstab's rows.
	Rows []*Group `json:"rows,omitempty"`

	// Cell / Cells, when either is set, replace the base crosstab's cell
	// AND cells (the CrosstabSpec rule: one measure, or several as stub
	// rows). Setting both is refused by the crosstab rules on the slot.
	Cell  *Aggregation   `json:"cell,omitempty"`
	Cells []*Aggregation `json:"cells,omitempty"`

	// Overlays, when non-empty, REPLACE the banner-level overlays
	// (BannerSpec.Overlays) on this section's group slots — so a mean
	// stub can run a means test while proportion stubs run a
	// proportions test — and the section's letters are stitched from
	// them (BannerLetters.Section). The base request's overlays still
	// lead every slot and the Total slot still receives neither. Empty
	// or absent inherits the banner-level overlays.
	Overlays []OverlaySpec `json:"overlays,omitempty"`
}

// BannerGroup is one column group of a banner: one crosstab slot per
// section with these column groupers.
type BannerGroup struct {
	// Label names the group; the second half of its slots' labels.
	Label string `json:"label"`

	// Columns is the group's column-axis grouper list (non-empty).
	Columns []*Group `json:"columns"`
}

// BannerTotalLabel is the Total block's label and the second half of
// its slots' labels (`<section>:Total`); a group may not take it when
// the banner has a Total block.
const BannerTotalLabel = CrosstabTotalColumnKey

// BannerSlotLabel is the compose slot label of section × group: the two
// labels joined by `:`.
func BannerSlotLabel(section, group string) string {
	return section + ":" + group
}

// BannerResult is ComposedResponse.Banner: one stitched table per
// banner section, in section order.
type BannerResult struct {
	Sections []BannerSectionResult `json:"sections"`
}

// BannerSectionResult is one section's stitched table: the section's
// slots' crosstab matrices side by side, column blocks concatenated in
// block order (Total first, then the groups). Rows are the union of the
// blocks' row keys in first-seen order; a row a block lacks (every
// record of it had a null column key there) is absent in that block's
// cells. A record reaches a block's margin only if it reached a CELL of
// that block, so row margins and grand totals are per block.
type BannerSectionResult struct {
	// Label is the section label.
	Label string `json:"label"`

	// RowHeader / RowKeys are the shared row axis.
	RowHeader AxisHeader `json:"row_header"`
	RowKeys   []AxisKey  `json:"row_keys"`

	// ColumnKeys concatenates every block's column keys; each key is
	// read against its block's ColumnHeader.
	ColumnKeys []AxisKey `json:"column_keys"`

	// Cells is [row][stitched column], each cell copied from its slot.
	Cells [][]MatrixCell `json:"cells"`

	// ColumnMargins concatenates the blocks' column margins
	// (single-measure tables with column margins only).
	ColumnMargins []MatrixCell `json:"column_margins,omitempty"`

	// CellLabel / CellLabels / NormalizeApplied echo the slots'.
	CellLabel        string            `json:"cell_label"`
	CellLabels       []string          `json:"cell_labels,omitempty"`
	NormalizeApplied CrosstabNormalize `json:"normalize_applied"`

	// Blocks locate each block's columns and carry its per-block figures.
	Blocks []BannerBlock `json:"blocks"`

	// Letters carries one entry per banner overlay whose layers carry a
	// `letters` part, on the stitched coordinates.
	Letters []BannerLetters `json:"letters,omitempty"`
}

// BannerBlock is one column block of a stitched section table.
type BannerBlock struct {
	// Label is the group label, or BannerTotalLabel for the Total block.
	Label string `json:"label"`
	// Slot is the compose slot label the block was read from.
	Slot string `json:"slot"`
	// Total marks the Total block.
	Total bool `json:"total,omitempty"`
	// Start / Count locate the block's columns in ColumnKeys.
	Start int `json:"start"`
	Count int `json:"count"`
	// ColumnHeader names the block's column-axis groupers.
	ColumnHeader AxisHeader `json:"column_header"`
	// RowMargins is the block's row margin per stitched row (absent
	// where the block lacks the row); omitted when the slot has none.
	RowMargins []MatrixCell `json:"row_margins,omitempty"`
	// GrandTotal is the block's grand total.
	GrandTotal MatrixCell `json:"grand_total"`
	// Measures is the slot's per-measure column margins and grand total
	// (multi-measure tables), in the block's own column order.
	Measures []CrosstabMeasureMargins `json:"measures,omitempty"`
}

// BannerLetters is one banner overlay's significance letters on the
// stitched table. Under COLUMN scope the letters are re-assigned across
// the banner — A, B, … running on through the group blocks in column
// order, never repeating — and Group numbers each block's comparison
// groups banner-wide, so letters still compare only within one group:
// a column is never compared across groups, and the Total block carries
// no letter cell at all (it is compared to nothing). Under ROW scope
// each cell is copied unchanged (the letters name rows, the same in
// every block).
type BannerLetters struct {
	// Overlay is the index into BannerSpec.Overlays — or, when Section
	// is set, into the section's own BannerSection.Overlays.
	Overlay int `json:"overlay"`
	// Section marks letters stitched from the section's own overlays
	// (BannerSection.Overlays), which replaced the banner-level ones.
	Section bool `json:"section,omitempty"`
	// Name / Kind / Scope echo the overlay spec.
	Name  string       `json:"name,omitempty"`
	Kind  OverlayKind  `json:"kind"`
	Scope OverlayScope `json:"scope"`
	// Cells is [row][stitched column], each present cell a
	// PairwiseLetterCell.
	Cells [][]MatrixCell `json:"cells"`
}
