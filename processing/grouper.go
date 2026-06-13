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

// formatRoundedNumeric formats a float64 in the same shape every
// numeric-key grouper uses today — integer-valued floats render via
// strconv.FormatInt, fractional values via FormatFloat with -1
// precision. Lifted out of categoryGrouper / roundedGrouper /
// rangeGrouper so the KeyFor and Group paths share a single
// canonicalisation site.
func formatRoundedNumeric(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// groupKeyResult collects the per-record bucket key plus a "used"
// flag used by every streamable grouper's Group implementation. The
// underlying KeyFor returns an ErrGrouperKeyNull sentinel for null
// rows; we translate that to used=false so Group continues to skip
// them silently (matching its long-standing contract).
func keyForOrSkip(g StreamableGrouper, r *Record) (string, bool, error) {
	key, err := g.KeyFor(r)
	if err != nil {
		if isGrouperKeyNull(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return key, true, nil
}

// isGrouperKeyNull recognises the ErrGrouperKeyNull sentinel without
// pulling stderrors into every grouper file.
func isGrouperKeyNull(err error) bool {
	return err == ErrGrouperKeyNull
}

// buildIncludeFilter materialises Group.Include into a membership set.
// Empty / nil Include returns nil — callers MUST treat nil as
// "accept-all" to preserve byte-identity against the pre-Include
// bucket set.
func buildIncludeFilter(include []string) map[string]struct{} {
	if len(include) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(include))
	for _, v := range include {
		out[v] = struct{}{}
	}
	return out
}

// includeAccepts reports whether key passes the include filter. nil
// filter accepts every key; non-nil rejects keys outside the set.
func includeAccepts(filter map[string]struct{}, key string) bool {
	if filter == nil {
		return true
	}
	_, ok := filter[key]
	return ok
}

// streamableGroup is the shared Group() implementation for streamable
// groupers — pass-1 computes per-record keys via KeyFor, pass-2 fills
// pre-sized per-key slices. Lifted so categoryGrouper / roundedGrouper /
// rangeGrouper / dateGrouper share the exact same partition shape and
// no key-format logic is duplicated.
func streamableGroup(g StreamableGrouper, records []*Record) (map[string][]*Record, error) {
	keys := make([]string, len(records))
	used := make([]bool, len(records))
	counts := make(map[string]int)
	for i, r := range records {
		key, ok, err := keyForOrSkip(g, r)
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
	groups := make(map[string][]*Record, len(counts))
	for k, n := range counts {
		groups[k] = make([]*Record, 0, n)
	}
	for i, r := range records {
		if !used[i] {
			continue
		}
		groups[keys[i]] = append(groups[keys[i]], r)
	}
	return groups, nil
}

// --- Category Grouper ---

type categoryGrouper struct {
	schema  *encoding.Schema
	field   string
	include map[string]struct{}
}

func newCategoryGrouper(grp *types.Group, schema *encoding.Schema) (Grouper, error) {
	return &categoryGrouper{
		schema:  schema,
		field:   grp.Field,
		include: buildIncludeFilter(grp.Include),
	}, nil
}

// KeyFor implements StreamableGrouper.KeyFor. Categorical fields resolve
// the wire-format dictionary index to its label; non-categorical numeric
// fields stringify the value via formatRoundedNumeric so the output
// matches the historical Group() partition key byte-for-byte.
func (g *categoryGrouper) KeyFor(r *Record) (string, error) {
	v, ok := r.NumericValue(g.field)
	if !ok {
		return "", ErrGrouperKeyNull
	}
	var key string
	if g.schema != nil {
		if f := g.schema.Field(g.field); f != nil && f.Type.IsCategorical() && f.Dictionary != nil {
			key = f.Dictionary.Resolve(uint32(v))
			if key == "" {
				key = strconv.FormatUint(uint64(uint32(v)), 10)
			}
		}
	}
	if key == "" {
		key = formatRoundedNumeric(v)
	}
	if !includeAccepts(g.include, key) {
		return "", ErrGrouperKeyNull
	}
	return key, nil
}

// KeyForRow delegates to KeyFor — single source of truth for the
// category bucket key format. The field argument is accepted for
// StreamingGrouper compatibility; runtime callers always pass
// grp.Field which matches g.field by construction.
func (g *categoryGrouper) KeyForRow(r *Record, _ string) (string, bool, error) {
	return keyForOrSkip(g, r)
}

func (g *categoryGrouper) Group(records []*Record, _ string) (map[string][]*Record, error) {
	return streamableGroup(g, records)
}

// --- Rounded Grouper ---

type roundedGrouper struct {
	field    string
	interval float64
}

func newRoundedGrouper(grp *types.Group, _ *encoding.Schema) (Grouper, error) {
	if grp.Interval <= 0 {
		grp.Interval = 1 // default to 1
	}
	return &roundedGrouper{field: grp.Field, interval: grp.Interval}, nil
}

// KeyFor implements StreamableGrouper.KeyFor. Floor-rounds the value to
// the configured interval and emits the stringified rounded value via
// formatRoundedNumeric — identical to today's Group() partition key.
func (g *roundedGrouper) KeyFor(r *Record) (string, error) {
	v, ok := r.NumericValue(g.field)
	if !ok {
		return "", ErrGrouperKeyNull
	}
	rounded := math.Floor(v/g.interval) * g.interval
	return formatRoundedNumeric(rounded), nil
}

func (g *roundedGrouper) KeyForRow(r *Record, _ string) (string, bool, error) {
	return keyForOrSkip(g, r)
}

func (g *roundedGrouper) Group(records []*Record, _ string) (map[string][]*Record, error) {
	return streamableGroup(g, records)
}

// --- Range Grouper ---

type rangeGrouper struct {
	field    string
	interval float64
}

func newRangeGrouper(grp *types.Group, _ *encoding.Schema) (Grouper, error) {
	if grp.Interval <= 0 {
		grp.Interval = 1 // default to 1
	}
	return &rangeGrouper{field: grp.Field, interval: grp.Interval}, nil
}

// KeyFor implements StreamableGrouper.KeyFor. Emits a "lo-hi" range
// string covering the half-open bin [low, low+interval). low+interval
// can drift due to FP error on non-power-of-two intervals; the format
// matches the historical Group() output exactly because both call into
// formatRoundedNumeric on identical inputs.
func (g *rangeGrouper) KeyFor(r *Record) (string, error) {
	v, ok := r.NumericValue(g.field)
	if !ok {
		return "", ErrGrouperKeyNull
	}
	low := math.Floor(v/g.interval) * g.interval
	high := low + g.interval
	return formatRoundedNumeric(low) + "-" + formatRoundedNumeric(high), nil
}

func (g *rangeGrouper) KeyForRow(r *Record, _ string) (string, bool, error) {
	return keyForOrSkip(g, r)
}

func (g *rangeGrouper) Group(records []*Record, _ string) (map[string][]*Record, error) {
	return streamableGroup(g, records)
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

// quantileGrouper deliberately does NOT implement StreamableGrouper.
// Bucket assignment depends on a sorted view of the full record set
// (each row's rank is set by every other row's value), so per-record
// KeyFor would lie. The fused crosstab path detects the missing
// interface via type assertion and falls back to the buffered
// PartitionByAxis route.

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
	field        string
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

	return &dateGrouper{field: grp.Field, component: component, fiscalOffset: fiscalOffset}, nil
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

// formatDateKey is the shared bucket-key formatter for dateGrouper. Both
// the buffered Group() path and the streaming KeyFor / KeyForRow paths
// call into it so the date / fiscal_offset formatting lives in one
// place. Returns the canonical bucket key for the configured component.
func (g *dateGrouper) formatDateKey(t time.Time) string {
	switch g.component {
	case "year":
		if g.fiscalOffset != 0 {
			fy, _ := fiscalYearQuarter(t, g.fiscalOffset)
			return fmt.Sprintf("FY%d", fy)
		}
		return fmt.Sprintf("%d", t.Year())
	case "quarter":
		if g.fiscalOffset != 0 {
			fy, fq := fiscalYearQuarter(t, g.fiscalOffset)
			return fmt.Sprintf("FY%d-Q%d", fy, fq)
		}
		return fmt.Sprintf("%d-Q%d", t.Year(), (int(t.Month())-1)/3+1)
	case "month":
		return t.Format("2006-01")
	case "week":
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	case "day":
		return t.Format("2006-01-02")
	case "day_of_week":
		return t.Weekday().String()
	}
	// Construction rejects unknown components, so this is unreachable
	// in normal use. Return empty string to keep the partition stable
	// if the contract ever drifts.
	return ""
}

// KeyFor implements StreamableGrouper.KeyFor. Per-record date
// classification is fully local — the same calendar conversion the
// buffered Group() path runs, lifted into formatDateKey. GROUP_DATE
// itself remains marked non-streamable for the Process orchestrator
// because grouped streaming Process is not wired through today; the
// crosstab fused path consumes KeyFor directly.
func (g *dateGrouper) KeyFor(r *Record) (string, error) {
	v, ok := r.NumericValue(g.field)
	if !ok {
		return "", ErrGrouperKeyNull
	}
	t := time.Unix(int64(v)*86400, 0).UTC()
	return g.formatDateKey(t), nil
}

// KeyForRow is the StreamingGrouper-shape adapter on top of KeyFor.
// Allows downstream code that consults StreamingGrouper to drive
// dateGrouper per record without buffering.
func (g *dateGrouper) KeyForRow(r *Record, _ string) (string, bool, error) {
	return keyForOrSkip(g, r)
}

func (g *dateGrouper) Group(records []*Record, _ string) (map[string][]*Record, error) {
	return streamableGroup(g, records)
}
