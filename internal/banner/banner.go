// Package banner is the one rule set for a Compose banner table
// (types.BannerSpec, the `banner` slot of a ComposedRequest): its
// structural check (Validate), its slot count (Count) and its expansion
// into ordinary crosstab slots (ExpandCompose, which also runs the
// sweep expansion so every Compose surface consumes ONE effective
// request).
//
// It is NO-EXECUTE and sits below both callers that must agree on it:
// the runtime (internal/service, Compose / ComposeParallel) and predict
// (internal/descriptor, PredictCompose), which may not import the
// engine (TestPredictNoExecutionImports). It imports only the standard
// library, errors, types and internal/sweep (TestBanner_ImportBoundary).
//
// Validate's rules (the contract), checked in this order, the first
// fault returned as PULSE_BANNER_INVALID with details `field` (the
// banner-rooted path), `reason` and, where one applies, `label`:
//
//   - request is set (request_missing), sets no `label`
//     (request_label_set), and its crosstab, when set, carries no
//     `columns` (columns_set) and no shape `long` (shape_long);
//   - sections is non-empty (sections_empty); every section label is
//     non-empty (label_empty) and unique (label_duplicate);
//   - at least one group or total (groups_empty); every group label is
//     non-empty, unique, and not `Total` beside the Total block
//     (label_reserved); every group has a column grouper
//     (columns_empty).
//
// Everything else — rows, cells, filterers, overlays — is judged on the
// expanded slots by the ordinary request rules, so a banner slot is
// refused exactly as its hand-written twin would be.
package banner

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

// The reason values PULSE_BANNER_INVALID carries under details.reason.
const (
	ReasonRequestMissing  = "request_missing"
	ReasonRequestLabelSet = "request_label_set"
	ReasonColumnsSet      = "columns_set"
	ReasonShapeLong       = "shape_long"
	ReasonSectionsEmpty   = "sections_empty"
	ReasonGroupsEmpty     = "groups_empty"
	ReasonLabelEmpty      = "label_empty"
	ReasonLabelDuplicate  = "label_duplicate"
	ReasonLabelReserved   = "label_reserved"
	ReasonColumnsEmpty    = "columns_empty"
	// ReasonRequestEncode: the base request could not be copied (an
	// invariant violation — a typed request always round-trips).
	ReasonRequestEncode = "request_encode"
)

// Validate reports the first structural fault of spec as a
// *errors.CodedError (PULSE_BANNER_INVALID), or nil. A nil spec is
// valid (no banner).
func Validate(spec *types.BannerSpec) *errors.CodedError {
	if spec == nil {
		return nil
	}
	if spec.Request == nil {
		return invalid("banner.request", ReasonRequestMissing, "",
			"banner has no base request; set `request` to the cohort, weight and shared crosstab every banner slot starts from")
	}
	if spec.Request.Label != "" {
		return invalid("banner.request.label", ReasonRequestLabelSet, "",
			"banner base request sets `label`; banner slots are labelled `<section>:<group>` — drop it")
	}
	if ct := spec.Request.Crosstab; ct != nil {
		if len(ct.Columns) > 0 {
			return invalid("banner.request.crosstab.columns", ReasonColumnsSet, "",
				"banner base request crosstab sets `columns`; the banner groups own the columns — move them into banner.groups[*].columns")
		}
		if ct.Shape == types.CrosstabShapeLong {
			return invalid("banner.request.crosstab.shape", ReasonShapeLong, "",
				"banner base request crosstab has shape `long`; the banner stitches the matrix — drop `shape` or set it to `matrix`")
		}
	}
	if len(spec.Sections) == 0 {
		return invalid("banner.sections", ReasonSectionsEmpty, "",
			"banner declares no sections; a banner needs at least one stub section")
	}
	seen := map[string]int{}
	for i, sec := range spec.Sections {
		at := "banner.sections[" + strconv.Itoa(i) + "].label"
		if ce := labelFault(at, "section", sec.Label, seen, i); ce != nil {
			return ce
		}
	}
	if len(spec.Groups) == 0 && !spec.Total {
		return invalid("banner.groups", ReasonGroupsEmpty, "",
			"banner declares no groups and no Total block; add a column group or set `total: true`")
	}
	seen = map[string]int{}
	for i, g := range spec.Groups {
		at := "banner.groups[" + strconv.Itoa(i) + "]"
		if ce := labelFault(at+".label", "group", g.Label, seen, i); ce != nil {
			return ce
		}
		if spec.Total && g.Label == types.BannerTotalLabel {
			return invalid(at+".label", ReasonLabelReserved, g.Label,
				"banner group label "+strconv.Quote(g.Label)+" is the Total block's; rename the group or drop `total`")
		}
		if len(g.Columns) == 0 {
			return invalid(at+".columns", ReasonColumnsEmpty, g.Label,
				"banner group "+strconv.Quote(g.Label)+" has no column grouper; list at least one under `columns`")
		}
	}
	return nil
}

func labelFault(at, noun, label string, seen map[string]int, i int) *errors.CodedError {
	if label == "" {
		return invalid(at, ReasonLabelEmpty, "", "banner "+noun+" at "+at+" has no label; every "+noun+" needs a non-empty label")
	}
	if _, dup := seen[label]; dup {
		return invalid(at, ReasonLabelDuplicate, label,
			"banner "+noun+" label "+strconv.Quote(label)+" is declared twice; "+noun+" labels must be unique")
	}
	seen[label] = i
	return nil
}

func invalid(field, reason, label, msg string) *errors.CodedError {
	details := map[string]any{"field": field, "reason": reason}
	if label != "" {
		details["label"] = label
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_BANNER_INVALID, msg, details)
}

// Count returns how many slots a structurally valid spec expands to:
// sections × (groups + 1 with the Total block), saturating at
// math.MaxInt; 0 for a nil spec.
func Count(spec *types.BannerSpec) int {
	if spec == nil {
		return 0
	}
	per := len(spec.Groups)
	if spec.Total {
		per++
	}
	n := len(spec.Sections)
	if per != 0 && n > math.MaxInt/per {
		return math.MaxInt
	}
	return n * per
}

// Block locates one banner slot: its section, block label and Total
// flag, and its index in the effective Requests.
type Block struct {
	// Label is the group label, or types.BannerTotalLabel.
	Label string
	// Slot is the slot label (`<section>:<block>`).
	Slot string
	// Total marks the Total block.
	Total bool
	// Index is the slot's index in the effective request's Requests.
	Index int
}

// Section is one banner section's blocks, in stitch order.
type Section struct {
	Label  string
	Blocks []Block
}

// Layout is where the banner slots sit in the effective request, read
// by the stitch (ComposedResponse.Banner).
type Layout struct {
	// Sections are the banner sections in order.
	Sections []Section
	// BaseOverlays is the base request's overlay count: a group slot's
	// banner overlay j is its layer BaseOverlays+j.
	BaseOverlays int
	// Overlays are the banner overlay specs (the group slots' trailing
	// layers).
	Overlays []types.OverlaySpec
	// Start is the index of the first banner slot.
	Start int
}

// Expansion is a ComposedRequest with its sweep and banner expanded:
// the effective request every Compose surface runs, predicts and
// label-checks.
type Expansion struct {
	// Composed is the effective request: explicit slots, then sweep
	// slots, then banner slots; Sweep and Banner nil. Without either it
	// is the input pointer itself.
	Composed *types.ComposedRequest
	// Explicit is the number of explicit slots.
	Explicit int
	// SweepLabels are the sweep slots' labels (nil without a sweep).
	SweepLabels []string
	// Banner is the banner layout (nil without a banner).
	Banner *Layout
}

// ExpandCompose turns c into its effective request — the ONE expansion
// every Compose surface consumes. Order: banner Validate; then the sweep
// expansion (sweep.Expand) whose MaxComposeSlots preflight is handed
// explicit + sweep + banner slots, so no banner bypasses the limit and
// nothing renders past it; then one slot per section × block, appended
// in section order (Total first, then the groups). Each slot is a deep
// copy of the base request with the slot label, the section's
// filterers appended, its rows / cell(s) replacing the base crosstab's,
// the group's columns, and — on a group slot — the banner overlays
// appended.
func ExpandCompose(c *types.ComposedRequest, preflight func(slots int) error) (*Expansion, error) {
	if c == nil {
		return &Expansion{}, nil
	}
	spec := c.Banner
	if spec == nil {
		exp, err := sweep.Expand(c, preflight)
		if err != nil {
			return nil, err
		}
		return &Expansion{Composed: exp.Composed, Explicit: exp.Explicit, SweepLabels: exp.Labels}, nil
	}
	if ce := Validate(spec); ce != nil {
		return nil, ce
	}
	n := Count(spec)
	exp, err := sweep.Expand(c, func(slots int) error {
		if preflight == nil {
			return nil
		}
		total := slots
		if n > math.MaxInt-total {
			total = math.MaxInt
		} else {
			total += n
		}
		return preflight(total)
	})
	if err != nil {
		return nil, err
	}
	base, err := json.Marshal(spec.Request)
	if err != nil {
		return nil, encodeFault(err)
	}
	out := *exp.Composed
	out.Banner = nil
	requests := make([]*types.Request, 0, len(out.Requests)+n)
	requests = append(requests, out.Requests...)
	layout := &Layout{BaseOverlays: len(spec.Request.Overlays), Overlays: spec.Overlays, Start: len(requests)}
	for _, sec := range spec.Sections {
		ls := Section{Label: sec.Label}
		add := func(label string, cols []*types.Group, total bool) error {
			req, err := slotRequest(base, sec, cols, total, spec.Overlays)
			if err != nil {
				return err
			}
			req.Label = types.BannerSlotLabel(sec.Label, label)
			ls.Blocks = append(ls.Blocks, Block{Label: label, Slot: req.Label, Total: total, Index: len(requests)})
			requests = append(requests, req)
			return nil
		}
		if spec.Total {
			if err := add(types.BannerTotalLabel, nil, true); err != nil {
				return nil, err
			}
		}
		for _, g := range spec.Groups {
			if err := add(g.Label, g.Columns, false); err != nil {
				return nil, err
			}
		}
		layout.Sections = append(layout.Sections, ls)
	}
	out.Requests = requests
	return &Expansion{Composed: &out, Explicit: exp.Explicit, SweepLabels: exp.Labels, Banner: layout}, nil
}

// slotRequest builds one banner slot from the encoded base request.
// Every part it sets is a fresh copy (decoded from JSON), so no two
// slots — and no slot and the caller's spec — share a pointer the
// engine might normalise in place.
func slotRequest(base []byte, sec types.BannerSection, cols []*types.Group, total bool, overlays []types.OverlaySpec) (*types.Request, error) {
	req := new(types.Request)
	if err := json.Unmarshal(base, req); err != nil {
		return nil, encodeFault(err)
	}
	var over struct {
		Filterers []*types.Filterer    `json:"filterers"`
		Rows      []*types.Group       `json:"rows"`
		Cell      *types.Aggregation   `json:"cell"`
		Cells     []*types.Aggregation `json:"cells"`
		Columns   []*types.Group       `json:"columns"`
		Overlays  []types.OverlaySpec  `json:"overlays"`
	}
	raw, err := json.Marshal(map[string]any{
		"filterers": sec.Filterers, "rows": sec.Rows, "cell": sec.Cell, "cells": sec.Cells,
		"columns": cols, "overlays": overlays,
	})
	if err != nil {
		return nil, encodeFault(err)
	}
	if err := json.Unmarshal(raw, &over); err != nil {
		return nil, encodeFault(err)
	}
	if len(over.Filterers) > 0 {
		req.Filterers = append(req.Filterers, over.Filterers...)
	}
	if req.Crosstab == nil {
		req.Crosstab = &types.CrosstabSpec{}
	}
	ct := req.Crosstab
	if len(over.Rows) > 0 {
		ct.Rows = over.Rows
	}
	if over.Cell != nil || len(over.Cells) > 0 {
		ct.Cell, ct.Cells = over.Cell, over.Cells
	}
	ct.Columns = over.Columns
	if !total && len(over.Overlays) > 0 {
		req.Overlays = append(req.Overlays, over.Overlays...)
	}
	return req, nil
}

func encodeFault(err error) *errors.CodedError {
	ce := invalid("banner.request", ReasonRequestEncode, "", "banner base request could not be copied: "+err.Error())
	ce.Cause = err
	return ce
}
