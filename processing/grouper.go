package processing

import (
	"fmt"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// --- Category Grouper ---

type categoryGrouper struct {
	schema *encoding.Schema
}

func newCategoryGrouper(_ *types.Group, schema *encoding.Schema) (Grouper, error) {
	return &categoryGrouper{schema: schema}, nil
}

func (g *categoryGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	groups := make(map[string][]*Record)

	f := g.schema.Field(field)
	isCategorical := f != nil && f.Type.IsCategorical() && f.Dictionary != nil

	for _, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			continue // skip null values
		}

		var key string
		if isCategorical {
			key = f.Dictionary.Resolve(uint32(v))
			if key == "" {
				key = fmt.Sprintf("%d", uint32(v))
			}
		} else {
			// Format numeric value as key
			if v == math.Trunc(v) {
				key = strconv.FormatInt(int64(v), 10)
			} else {
				key = strconv.FormatFloat(v, 'f', -1, 64)
			}
		}

		groups[key] = append(groups[key], r)
	}
	return groups, nil
}

// --- Rounded Grouper ---

type roundedGrouper struct {
	interval float64
}

func newRoundedGrouper(grp *types.Group, _ *encoding.Schema) (Grouper, error) {
	if grp.Interval <= 0 {
		grp.Interval = 1 // default to 1
	}
	return &roundedGrouper{interval: grp.Interval}, nil
}

func (g *roundedGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	groups := make(map[string][]*Record)

	for _, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			continue
		}

		// Round down to nearest interval
		rounded := math.Floor(v/g.interval) * g.interval
		var key string
		if rounded == math.Trunc(rounded) {
			key = strconv.FormatInt(int64(rounded), 10)
		} else {
			key = strconv.FormatFloat(rounded, 'f', -1, 64)
		}

		groups[key] = append(groups[key], r)
	}
	return groups, nil
}
