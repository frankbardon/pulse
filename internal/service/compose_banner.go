package service

import (
	"encoding/json"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/banner"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// applyComposeBanner fills out.Banner from the banner slots
// (banner.Layout): one stitched table per section, the section's slots'
// crosstab matrices side by side in block order. It runs after every
// fold — the multiplicity barrier included, so letters already read the
// corrected significance — and before the facade shapes anything, so
// each slot is read unshaped (the banner slots ran under the rank veto).
// Nil layout leaves out untouched. The stitch only copies: no figure is
// recomputed, so every cell equals its slot's.
func applyComposeBanner(layout *banner.Layout, out *types.ComposedResponse) error {
	if layout == nil {
		return nil
	}
	res := &types.BannerResult{Sections: make([]types.BannerSectionResult, 0, len(layout.Sections))}
	for _, sec := range layout.Sections {
		table, err := stitchBannerSection(layout, sec, out.Responses)
		if err != nil {
			return err
		}
		res.Sections = append(res.Sections, table)
	}
	out.Banner = res
	return nil
}

// stitchBannerSection builds one section's table.
func stitchBannerSection(layout *banner.Layout, sec banner.Section, responses []*types.Response) (types.BannerSectionResult, error) {
	table := types.BannerSectionResult{Label: sec.Label}
	mats := make([]*types.MatrixPayload, len(sec.Blocks))
	for b, blk := range sec.Blocks {
		if blk.Index >= len(responses) || responses[blk.Index] == nil ||
			responses[blk.Index].Crosstab == nil || responses[blk.Index].Crosstab.Matrix == nil {
			return table, bannerInternal(blk.Slot, "the slot carries no crosstab matrix")
		}
		mats[b] = responses[blk.Index].Crosstab.Matrix
	}

	// The row axis: the union of the blocks' row keys, first seen first.
	rowIndex := map[string]int{}
	rowOf := make([][]int, len(mats)) // block row -> stitched row
	for b, m := range mats {
		rowOf[b] = make([]int, len(m.RowKeys))
		for r, key := range m.RowKeys {
			k, err := axisKeyString(key)
			if err != nil {
				return table, bannerInternal(sec.Blocks[b].Slot, err.Error())
			}
			idx, ok := rowIndex[k]
			if !ok {
				idx = len(table.RowKeys)
				rowIndex[k] = idx
				table.RowKeys = append(table.RowKeys, key)
				if len(m.CellLabels) == len(m.RowKeys) {
					table.CellLabels = append(table.CellLabels, m.CellLabels[r])
				}
			}
			rowOf[b][r] = idx
		}
	}
	first := mats[0]
	table.RowHeader = first.RowHeader
	table.CellLabel = first.CellLabel
	table.NormalizeApplied = first.NormalizeApplied
	if table.RowKeys == nil {
		table.RowKeys = []types.AxisKey{}
	}
	if len(table.CellLabels) != len(table.RowKeys) {
		table.CellLabels = nil
	}

	rows := len(table.RowKeys)
	starts := make([]int, len(mats))
	cols := 0
	anyColMargins := false
	for b, m := range mats {
		starts[b] = cols
		cols += len(m.ColumnKeys)
		if len(m.ColumnMargins) > 0 {
			anyColMargins = true
		}
	}
	table.Cells = make([][]types.MatrixCell, rows)
	for r := range table.Cells {
		table.Cells[r] = make([]types.MatrixCell, cols)
	}
	table.ColumnKeys = make([]types.AxisKey, 0, cols)
	if anyColMargins {
		table.ColumnMargins = make([]types.MatrixCell, cols)
	}
	for b, m := range mats {
		blk := sec.Blocks[b]
		table.ColumnKeys = append(table.ColumnKeys, m.ColumnKeys...)
		for r, row := range m.Cells {
			copy(table.Cells[rowOf[b][r]][starts[b]:], row)
		}
		if anyColMargins {
			copy(table.ColumnMargins[starts[b]:], m.ColumnMargins)
		}
		block := types.BannerBlock{
			Label: blk.Label, Slot: blk.Slot, Total: blk.Total,
			Start: starts[b], Count: len(m.ColumnKeys),
			ColumnHeader: m.ColumnHeader, GrandTotal: m.GrandTotal, Measures: m.Measures,
		}
		if len(m.RowMargins) > 0 {
			block.RowMargins = make([]types.MatrixCell, rows)
			for r, cell := range m.RowMargins {
				if r < len(rowOf[b]) {
					block.RowMargins[rowOf[b][r]] = cell
				}
			}
		}
		table.Blocks = append(table.Blocks, block)
	}

	for j, spec := range sec.Overlays {
		letters, err := stitchBannerLetters(layout, sec, j, spec, responses, rowOf, starts, rows, cols)
		if err != nil {
			return table, err
		}
		if letters != nil {
			letters.Section = sec.OwnOverlays
			table.Letters = append(table.Letters, *letters)
		}
	}
	return table, nil
}

// stitchBannerLetters places section overlay j's (Section.Overlays: the
// section's own, else the banner-level ones) `letters` part of every
// GROUP block on the stitched coordinates, or returns nil when no block's
// layer carries one. The Total block never received the banner overlays,
// so its columns hold no letter cell: the Total is compared to nothing.
// Under COLUMN scope the letters are re-assigned banner-wide (see
// types.BannerLetters) — each block's local letter and comparison group
// mapped to the next unused banner letter and group in column order, its
// Higher / Above lists through the same map — so letters stay unique
// across the stitched table and still compare only within one group.
func stitchBannerLetters(layout *banner.Layout, sec banner.Section, j int, spec types.OverlaySpec,
	responses []*types.Response, rowOf [][]int, starts []int, rows, cols int) (*types.BannerLetters, error) {
	var out *types.BannerLetters
	nextLetter, nextGroup := 0, 0
	for b, blk := range sec.Blocks {
		if blk.Total {
			continue
		}
		resp := responses[blk.Index]
		li := layout.BaseOverlays + j
		if li >= len(resp.Overlays) {
			return nil, bannerInternal(blk.Slot, "the slot is missing section overlay "+strconv.Itoa(j))
		}
		layer := resp.Overlays[li]
		part := layer.Payload.Parts[processing.PairwiseLettersPart]
		if part == nil || part.Matrix == nil {
			continue
		}
		if out == nil {
			out = &types.BannerLetters{Overlay: j, Name: layer.Name, Kind: spec.Kind, Scope: layer.Scope,
				Cells: make([][]types.MatrixCell, rows)}
			for r := range out.Cells {
				out.Cells[r] = make([]types.MatrixCell, cols)
			}
		}
		column := layer.Scope != types.OverlayScopeRow
		type local struct {
			group  int
			letter string
		}
		letterMap := map[local]string{}
		groupMap := map[int]int{}
		cellAt := func(r, c int) (types.PairwiseLetterCell, bool, error) {
			cell := part.Matrix.Cells[r][c]
			if !cell.Present {
				return types.PairwiseLetterCell{}, false, nil
			}
			v, ok := cell.Value.(types.PairwiseLetterCell)
			if !ok {
				return v, false, bannerInternal(blk.Slot, "a letters cell does not hold a PairwiseLetterCell")
			}
			return v, true, nil
		}
		if column {
			// Banner letters and groups, in column order.
			ncols := 0
			if len(part.Matrix.Cells) > 0 {
				ncols = len(part.Matrix.Cells[0])
			}
			for c := 0; c < ncols; c++ {
				for r := range part.Matrix.Cells {
					v, ok, err := cellAt(r, c)
					if err != nil {
						return nil, err
					}
					if !ok {
						continue
					}
					key := local{v.Group, v.Letter}
					if _, seen := letterMap[key]; !seen {
						letterMap[key] = processing.PairwiseLetterName(nextLetter)
						nextLetter++
					}
					if _, seen := groupMap[v.Group]; !seen {
						groupMap[v.Group] = nextGroup
						nextGroup++
					}
					break
				}
			}
		}
		for r := range part.Matrix.Cells {
			if r >= len(rowOf[b]) {
				return nil, bannerInternal(blk.Slot, "the letters part does not match the host rows")
			}
			for c := range part.Matrix.Cells[r] {
				v, ok, err := cellAt(r, c)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
				if column {
					mapped, err := remapLetterCell(v, letterMap, groupMap, func(l string) local { return local{v.Group, l} })
					if err != nil {
						return nil, bannerInternal(blk.Slot, err.Error())
					}
					v = mapped
				}
				out.Cells[rowOf[b][r]][starts[b]+c] = types.MatrixCell{Present: true, Value: v}
			}
		}
	}
	return out, nil
}

// remapLetterCell rewrites one letters cell through the block's banner
// letter and group maps.
func remapLetterCell[K comparable](v types.PairwiseLetterCell, letters map[K]string, groups map[int]int, key func(string) K) (types.PairwiseLetterCell, error) {
	conv := func(l string) (string, error) {
		m, ok := letters[key(l)]
		if !ok {
			return "", &letterMapError{letter: l}
		}
		return m, nil
	}
	out := types.PairwiseLetterCell{Group: groups[v.Group], Higher: []string{}, Above: []string{}}
	var err error
	if out.Letter, err = conv(v.Letter); err != nil {
		return out, err
	}
	for _, l := range v.Higher {
		m, err := conv(l)
		if err != nil {
			return out, err
		}
		out.Higher = append(out.Higher, m)
	}
	for _, l := range v.Above {
		m, err := conv(l)
		if err != nil {
			return out, err
		}
		out.Above = append(out.Above, m)
	}
	return out, nil
}

type letterMapError struct{ letter string }

func (e *letterMapError) Error() string {
	return "letter " + strconv.Quote(e.letter) + " names no column of its comparison group"
}

// axisKeyString is the identity of an axis key for the row union.
func axisKeyString(key types.AxisKey) (string, error) {
	b, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func bannerInternal(slot, reason string) error {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
		"banner stitch, slot "+strconv.Quote(slot)+": "+reason, map[string]any{"slot": slot})
}
