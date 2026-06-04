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

func (g *categoryGrouper) KeyForRow(r *Record, field string) (string, bool, error) {
	v, ok := r.NumericValue(field)
	if !ok {
		return "", false, nil
	}
	f := g.schema.Field(field)
	if f != nil && f.Type.IsCategorical() && f.Dictionary != nil {
		key := f.Dictionary.Resolve(uint32(v))
		if key == "" {
			key = fmt.Sprintf("%d", uint32(v))
		}
		return key, true, nil
	}
	if v == math.Trunc(v) {
		return strconv.FormatInt(int64(v), 10), true, nil
	}
	return strconv.FormatFloat(v, 'f', -1, 64), true, nil
}

func (g *categoryGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	// Pass 1: compute keys (buffered to avoid recomputation) and per-key counts.
	keys := make([]string, len(records))
	used := make([]bool, len(records))
	counts := make(map[string]int)
	for i, r := range records {
		key, ok, err := g.KeyForRow(r, field)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
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

func (g *roundedGrouper) KeyForRow(r *Record, field string) (string, bool, error) {
	v, ok := r.NumericValue(field)
	if !ok {
		return "", false, nil
	}
	rounded := math.Floor(v/g.interval) * g.interval
	if rounded == math.Trunc(rounded) {
		return strconv.FormatInt(int64(rounded), 10), true, nil
	}
	return strconv.FormatFloat(rounded, 'f', -1, 64), true, nil
}

func (g *roundedGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	// Pass 1: compute keys (buffered) and per-key counts.
	keys := make([]string, len(records))
	used := make([]bool, len(records))
	counts := make(map[string]int)
	for i, r := range records {
		key, ok, err := g.KeyForRow(r, field)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
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

func (g *rangeGrouper) KeyForRow(r *Record, field string) (string, bool, error) {
	v, ok := r.NumericValue(field)
	if !ok {
		return "", false, nil
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
	return lowStr + "-" + highStr, true, nil
}

func (g *rangeGrouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	// Pass 1: compute keys (buffered) and per-key counts.
	keys := make([]string, len(records))
	used := make([]bool, len(records))
	counts := make(map[string]int)
	for i, r := range records {
		key, ok, err := g.KeyForRow(r, field)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
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
	Component    string `json:"component"`
	FiscalOffset *int   `json:"fiscal_offset,omitempty"`
}

var validDateGroupComponents = map[string]bool{
	"year": true, "quarter": true, "month": true,
	"week": true, "day": true, "day_of_week": true,
}

// fiscalComponents lists the only components that meaningfully accept a
// fiscal_offset. Month/week/day/day_of_week buckets do not shift under a
// fiscal calendar, so combining them with fiscal_offset is rejected at
// construction time.
var fiscalComponents = map[string]bool{
	"year": true, "quarter": true,
}

type dateGrouper struct {
	component    string
	fiscalOffset int // 0 = calendar; non-zero = FY starts at month (((offset%12)+12)%12)+1
}

func newDateGrouper(grp *types.Group, _ *encoding.Schema) (Grouper, error) {
	component := "month"
	fiscalOffset := 0
	if len(grp.Params) > 0 {
		var params dateGroupParams
		if err := json.Unmarshal(grp.Params, &params); err != nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("invalid GROUP_DATE params: %v", err))
		}
		if params.Component != "" {
			component = params.Component
		}
		if params.FiscalOffset != nil {
			fiscalOffset = *params.FiscalOffset
		}
	}

	if !validDateGroupComponents[component] {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("invalid date group component %q: must be one of year, quarter, month, week, day, day_of_week", component))
	}

	if fiscalOffset < -11 || fiscalOffset > 11 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("invalid GROUP_DATE fiscal_offset %d: must be in range [-11, 11]", fiscalOffset))
	}
	if fiscalOffset != 0 && !fiscalComponents[component] {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("GROUP_DATE fiscal_offset only applies to component=year or component=quarter, got %q", component))
	}

	return &dateGrouper{component: component, fiscalOffset: fiscalOffset}, nil
}

// fiscalYearQuarter returns the fiscal year (end-year convention) and the
// 1-indexed fiscal quarter for a calendar date under a given fiscal offset.
// Offset normalisation: start_month_1idx = ((offset%12)+12)%12 + 1, so
// offset=3 and offset=-9 both yield start_month=April; offset=9 and
// offset=-3 both yield start_month=October.
func fiscalYearQuarter(t time.Time, offset int) (int, int) {
	startMonth := ((offset%12)+12)%12 + 1
	calYear := t.Year()
	calMonth := int(t.Month())
	fy := calYear
	if startMonth != 1 && calMonth >= startMonth {
		fy = calYear + 1
	}
	elapsed := (calMonth - startMonth + 12) % 12
	fq := elapsed/3 + 1
	return fy, fq
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
			if g.fiscalOffset != 0 {
				fy, _ := fiscalYearQuarter(t, g.fiscalOffset)
				key = fmt.Sprintf("FY%d", fy)
			} else {
				key = fmt.Sprintf("%d", t.Year())
			}
		case "quarter":
			if g.fiscalOffset != 0 {
				fy, fq := fiscalYearQuarter(t, g.fiscalOffset)
				key = fmt.Sprintf("FY%d-Q%d", fy, fq)
			} else {
				key = fmt.Sprintf("%d-Q%d", t.Year(), (int(t.Month())-1)/3+1)
			}
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
