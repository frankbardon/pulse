package processing

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
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
	f := g.schema.Field(field)
	isCategorical := f != nil && f.Type.IsCategorical() && f.Dictionary != nil

	// Pass 1: compute keys (buffered to avoid recomputation) and per-key counts.
	keys := make([]string, len(records))
	used := make([]bool, len(records))
	counts := make(map[string]int)
	for i, r := range records {
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

		keys[i] = key
		used[i] = true
		counts[key]++
	}

	// Pass 2: pre-allocate per-key slices to known final size, then append.
	groups := make(map[string][]*Record, len(counts))
	for k, n := range counts {
		groups[k] = make([]*Record, 0, n)
	}
	for i, r := range records {
		if !used[i] {
			continue
		}
		k := keys[i]
		groups[k] = append(groups[k], r)
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
	// Pass 1: compute keys (buffered) and per-key counts.
	keys := make([]string, len(records))
	used := make([]bool, len(records))
	counts := make(map[string]int)
	for i, r := range records {
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

		keys[i] = key
		used[i] = true
		counts[key]++
	}

	// Pass 2: pre-allocate per-key slices, then append.
	groups := make(map[string][]*Record, len(counts))
	for k, n := range counts {
		groups[k] = make([]*Record, 0, n)
	}
	for i, r := range records {
		if !used[i] {
			continue
		}
		k := keys[i]
		groups[k] = append(groups[k], r)
	}
	return groups, nil
}

// --- Range Grouper ---

type rangeGrouper struct {
	interval float64
}

func newRangeGrouper(grp *types.Group, _ *encoding.Schema) (Grouper, error) {
	if grp.Interval <= 0 {
		grp.Interval = 1 // default to 1
	}
	return &rangeGrouper{interval: grp.Interval}, nil
}

func (g *rangeGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	// Pass 1: compute keys (buffered) and per-key counts.
	keys := make([]string, len(records))
	used := make([]bool, len(records))
	counts := make(map[string]int)
	for i, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			continue
		}

		low := math.Floor(v/g.interval) * g.interval
		high := low + g.interval

		var lowStr, highStr string
		if low == math.Trunc(low) {
			lowStr = strconv.FormatInt(int64(low), 10)
		} else {
			lowStr = strconv.FormatFloat(low, 'f', -1, 64)
		}
		if high == math.Trunc(high) {
			highStr = strconv.FormatInt(int64(high), 10)
		} else {
			highStr = strconv.FormatFloat(high, 'f', -1, 64)
		}

		key := lowStr + "-" + highStr
		keys[i] = key
		used[i] = true
		counts[key]++
	}

	// Pass 2: pre-allocate per-key slices, then append.
	groups := make(map[string][]*Record, len(counts))
	for k, n := range counts {
		groups[k] = make([]*Record, 0, n)
	}
	for i, r := range records {
		if !used[i] {
			continue
		}
		k := keys[i]
		groups[k] = append(groups[k], r)
	}
	return groups, nil
}

// --- Quantile Grouper ---

type quantileGrouper struct {
	buckets int
}

func newQuantileGrouper(grp *types.Group, _ *encoding.Schema) (Grouper, error) {
	buckets := int(grp.Interval)
	if buckets <= 0 {
		buckets = 4
	}
	return &quantileGrouper{buckets: buckets}, nil
}

type indexedValue struct {
	record *Record
	value  float64
}

func (g *quantileGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	groups := make(map[string][]*Record)

	// Collect non-null (record, value) pairs.
	items := make([]indexedValue, 0, len(records))
	for _, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			continue
		}
		items = append(items, indexedValue{record: r, value: v})
	}

	n := len(items)
	if n == 0 {
		return groups, nil
	}

	// Sort by value.
	sort.Slice(items, func(i, j int) bool {
		return items[i].value < items[j].value
	})

	// Determine key prefix.
	prefix := "B"
	switch g.buckets {
	case 4:
		prefix = "Q"
	case 10:
		prefix = "D"
	case 100:
		prefix = "P"
	}

	// Pass 1: count records per bucket.
	bucketCounts := make([]int, g.buckets)
	for rank := range n {
		bucket := rank * g.buckets / n
		if bucket >= g.buckets {
			bucket = g.buckets - 1
		}
		bucketCounts[bucket]++
	}

	// Pre-allocate per-key slices to known final size.
	bucketKeys := make([]string, g.buckets)
	for b := 0; b < g.buckets; b++ {
		if bucketCounts[b] == 0 {
			continue
		}
		key := prefix + strconv.Itoa(b+1)
		bucketKeys[b] = key
		groups[key] = make([]*Record, 0, bucketCounts[b])
	}

	// Pass 2: assign each item to its pre-sized bucket.
	for rank, item := range items {
		bucket := rank * g.buckets / n
		if bucket >= g.buckets {
			bucket = g.buckets - 1
		}
		key := bucketKeys[bucket]
		groups[key] = append(groups[key], item.record)
	}

	return groups, nil
}

// --- Date Grouper ---

type dateGroupParams struct {
	Component string `json:"component"`
}

var validDateGroupComponents = map[string]bool{
	"year": true, "quarter": true, "month": true,
	"week": true, "day": true, "day_of_week": true,
}

type dateGrouper struct {
	component string
}

func newDateGrouper(grp *types.Group, _ *encoding.Schema) (Grouper, error) {
	component := "month"
	if len(grp.Params) > 0 {
		var params dateGroupParams
		if err := json.Unmarshal(grp.Params, &params); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("invalid GROUP_DATE params: %v", err))
		}
		if params.Component != "" {
			component = params.Component
		}
	}

	if !validDateGroupComponents[component] {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("invalid date group component %q: must be one of year, quarter, month, week, day, day_of_week", component))
	}

	return &dateGrouper{component: component}, nil
}

func (g *dateGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	// Pass 1: compute keys (buffered to avoid recomputing time formatting) and per-key counts.
	keys := make([]string, len(records))
	used := make([]bool, len(records))
	counts := make(map[string]int)
	for i, r := range records {
		v, ok := r.NumericValue(field)
		if !ok {
			continue // skip null values
		}

		t := time.Unix(int64(v)*86400, 0).UTC()

		var key string
		switch g.component {
		case "year":
			key = fmt.Sprintf("%d", t.Year())
		case "quarter":
			key = fmt.Sprintf("%d-Q%d", t.Year(), (int(t.Month())-1)/3+1)
		case "month":
			key = t.Format("2006-01")
		case "week":
			y, w := t.ISOWeek()
			key = fmt.Sprintf("%d-W%02d", y, w)
		case "day":
			key = t.Format("2006-01-02")
		case "day_of_week":
			key = t.Weekday().String()
		}

		keys[i] = key
		used[i] = true
		counts[key]++
	}

	// Pass 2: pre-allocate per-key slices, then append.
	groups := make(map[string][]*Record, len(counts))
	for k, n := range counts {
		groups[k] = make([]*Record, 0, n)
	}
	for i, r := range records {
		if !used[i] {
			continue
		}
		k := keys[i]
		groups[k] = append(groups[k], r)
	}
	return groups, nil
}
