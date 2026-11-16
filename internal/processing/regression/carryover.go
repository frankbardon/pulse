package regression

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/regorder"
	"github.com/frankbardon/pulse/types"
)

// carryover.go is the engine half of adstock carry-over (the static
// rules are internal/model mmm_carry.go): AdstockStateOf records an
// in-model adstocked fit's last periods of raw spend as
// outputs.adstock_state, and a Carrier adstocks a scored frame's raw
// spend with that state carried in — the engine holdout's held-out rows
// and a carried ATTR_REG_PREDICT's scoring rows alike, so both scoring
// paths compute one figure the same way.
//
// A scored row at period v of series p reads each channel c as
//
//	a_c(v) = Σ_{l=0..max_lag} w_l · spend_c(p, v − l·step)
//
// summed in lag order with every product barriered (WIN_DISTRIBUTED_LAG's
// loop), spend read from the scored rows themselves or, for a period
// none of them holds, the fit's tail; a period neither holds adds
// nothing. A null own spend scores null (as a null column did), as does
// a row without an order value or partition key.

// seriesKey is rec's partition key: "" without a partition; else the
// field's categorical label, or a number's decimal form (an unsigned
// integer code as REG_MMM_BAYES names a geo). ok=false for a null.
func seriesKey(rec Record, partition string) (string, bool) {
	if partition == "" {
		return "", true
	}
	if lr, ok := rec.(LabeledRecord); ok {
		if s, ok, err := lr.StringValueChecked(partition); err == nil && ok {
			return s, true
		}
	}
	v, ok := InputValue(rec, partition)
	if !ok {
		return "", false
	}
	if v >= 0 && v == math.Trunc(v) && v < 1<<63 {
		return strconv.FormatUint(uint64(v), 10), true
	}
	return strconv.FormatFloat(v, 'g', -1, 64), true
}

// AdstockStateOf is spec's adstock state over records — the rows its fit
// read: per series of spec's order (model.MMMOrder), the last max_lag
// rows by order value of each channel's raw spend. Nil when spec does
// not adstock in-model or no row can be placed.
func AdstockStateOf(spec *types.RegressionSpec, records []Record) *model.AdstockState {
	if !model.MMMInModelAdstock(spec) {
		return nil
	}
	field, partition := model.MMMOrder(spec)
	if field == "" {
		return nil
	}
	kernel, maxLag := model.MMMAdstockKernelName(spec)
	channels := model.MMMChannels(spec)
	if len(channels) == 0 {
		return nil
	}
	type row struct {
		v     int64
		spend []float64
	}
	bySeries := map[string][]row{}
	for _, rec := range records {
		fv, ok := InputValue(rec, field)
		if !ok {
			continue
		}
		key, ok := seriesKey(rec, partition)
		if !ok {
			continue
		}
		r := row{v: int64(fv), spend: make([]float64, len(channels))}
		for c, ch := range channels {
			s, ok := InputValue(rec, ch)
			if !ok {
				s = math.NaN()
			}
			r.spend[c] = s
		}
		bySeries[key] = append(bySeries[key], r)
	}
	if len(bySeries) == 0 {
		return nil
	}
	var step int64
	keys := make([]string, 0, len(bySeries))
	for k, rows := range bySeries {
		slices.SortStableFunc(rows, func(a, b row) int { return cmp.Compare(a.v, b.v) })
		for i := 1; i < len(rows); i++ {
			if d := rows[i].v - rows[i-1].v; d > 0 && (step == 0 || d < step) {
				step = d
			}
		}
		keys = append(keys, k)
	}
	if step == 0 {
		step = 1
	}
	slices.Sort(keys)
	st := &model.AdstockState{Field: field, Partition: partition, Kernel: kernel, MaxLag: maxLag, Step: step}
	for _, k := range keys {
		rows := bySeries[k]
		if len(rows) > maxLag {
			rows = rows[len(rows)-maxLag:]
		}
		sr := model.AdstockSeries{Key: k, Order: make([]int64, len(rows)), Spend: make(map[string][]model.Float, len(channels))}
		for c, ch := range channels {
			sp := make([]model.Float, len(rows))
			for i, r := range rows {
				sp[i] = model.Float(r.spend[c])
			}
			sr.Spend[ch] = sp
		}
		for i, r := range rows {
			sr.Order[i] = r.v
		}
		st.Series = append(st.Series, sr)
	}
	return st
}

// AttachAdstockState writes spec's adstock state over records into res's
// outputs (an in-model adstocked spec only; res untouched otherwise).
func AttachAdstockState(spec *types.RegressionSpec, res *types.RegressionResult, records []Record) error {
	if res == nil {
		return nil
	}
	st := AdstockStateOf(spec, records)
	if st == nil {
		return nil
	}
	v, err := st.Value()
	if err != nil {
		return err
	}
	if res.Outputs == nil {
		res.Outputs = map[string]any{}
	}
	res.Outputs[model.MMMAdstockStateKey] = v
	return nil
}

// Carrier adstocks a scored frame's raw spend with a fit's adstock state
// carried in. Add every scored row (the pre-pass), Finalize once, then
// Row wraps each row. It is not safe for concurrent use.
type Carrier struct {
	field, partition string
	step             int64
	channels         []string
	weights          [][]float64
	// spend is per series, per order value, each channel's raw spend
	// (NaN for null): the fit's tail, overwritten by the scored rows'
	// own periods.
	spend map[string]map[int64][]float64
	// frame are the scored rows' order values per series.
	frame map[string][]int64
	// slot names the scoring site in a refusal (details).
	slot map[string]any
}

// NewCarrier builds the carrier of st at the fit's coefficients coefs
// (the kernel, model.AdstockState.Weights), scoring rows ordered by
// field and split into series by partition (the scoring frame's names;
// the fit's own for a holdout). slot is merged into a refusal's details.
func NewCarrier(st *model.AdstockState, coefs map[string]float64, field, partition string, slot map[string]any) (*Carrier, error) {
	w, err := st.Weights(coefs)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_INTERNAL, "reading the fit's adstock kernel")
	}
	c := &Carrier{field: field, partition: partition, step: st.Step, channels: st.Channels(),
		spend: map[string]map[int64][]float64{}, frame: map[string][]int64{}, slot: slot}
	for _, ch := range c.channels {
		c.weights = append(c.weights, w[ch])
	}
	for _, sr := range st.Series {
		m := c.series(sr.Key)
		for i, v := range sr.Order {
			s := make([]float64, len(c.channels))
			for j, ch := range c.channels {
				s[j] = math.NaN()
				if sp, ok := sr.Spend[ch]; ok && i < len(sp) {
					s[j] = float64(sp[i])
				}
			}
			m[v] = s
		}
	}
	return c, nil
}

func (c *Carrier) series(key string) map[int64][]float64 {
	m, ok := c.spend[key]
	if !ok {
		m = map[int64][]float64{}
		c.spend[key] = m
	}
	return m
}

// place is rec's series key and order value; ok=false when either is
// null.
func (c *Carrier) place(rec Record) (string, int64, bool) {
	v, ok := InputValue(rec, c.field)
	if !ok {
		return "", 0, false
	}
	key, ok := seriesKey(rec, c.partition)
	if !ok {
		return "", 0, false
	}
	return key, int64(v), true
}

// Add folds one scored row's raw spend in (a row without an order value
// or key is skipped: it scores null).
func (c *Carrier) Add(rec Record) {
	key, v, ok := c.place(rec)
	if !ok {
		return
	}
	s := make([]float64, len(c.channels))
	for j, ch := range c.channels {
		x, ok := InputValue(rec, ch)
		if !ok {
			x = math.NaN()
		}
		s[j] = x
	}
	c.series(key)[v] = s
	c.frame[key] = append(c.frame[key], v)
}

// Finalize checks the scored rows' series: a period repeated within one
// series, or a step between consecutive periods that is not the fit's
// step, is PROCESSING_REGRESSION_ORDER (reason duplicate / gap) — the
// adstock would read an ambiguous or missing period. The first offence
// in series order, then period order, is reported.
func (c *Carrier) Finalize() error {
	keys := make([]string, 0, len(c.frame))
	for k := range c.frame {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		vs := slices.Clone(c.frame[k])
		slices.Sort(vs)
		for i := 1; i < len(vs); i++ {
			switch d := vs[i] - vs[i-1]; {
			case d == 0:
				return c.orderError(k, regorder.ReasonDuplicate,
					fmt.Sprintf("the scored rows repeat %s %d%s; a carried adstock reads one row per period", c.field, vs[i], c.in(k)),
					map[string]any{"value": vs[i]})
			case d != c.step:
				return c.orderError(k, regorder.ReasonGap,
					fmt.Sprintf("the scored rows jump from %s %d to %d%s, but the fit's period is %d: a carried adstock needs contiguous periods", c.field, vs[i-1], vs[i], c.in(k), c.step),
					map[string]any{"from": vs[i-1], "to": vs[i], "step": c.step})
			}
		}
	}
	return nil
}

func (c *Carrier) in(key string) string {
	if c.partition == "" {
		return ""
	}
	return " in series " + strconv.Quote(key)
}

func (c *Carrier) orderError(key, reason, msg string, extra map[string]any) error {
	details := map[string]any{"reason": reason, "field": c.field}
	if c.partition != "" {
		details["partition"] = key
	}
	for k, v := range c.slot {
		details[k] = v
	}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_REGRESSION_ORDER, msg, details)
}

// Row is rec with each channel read as its carried adstock (null when
// rec cannot be placed or its own spend is null); every other read is
// rec's. The row also holds each carried channel's spend at lags
// 0..max_lag, the very terms the sum read (NaN at a lag it skipped):
// model.AdstockLagRow, what a nonlinear fit's tangent plane reads for
// its kernel parameters' partials (U71 E2-S6).
func (c *Carrier) Row(rec Record) Record {
	vals := make(map[string]float64, len(c.channels))
	lags := make(map[string][]float64, len(c.channels))
	key, v, ok := c.place(rec)
	for j, ch := range c.channels {
		vals[ch] = math.NaN()
		if !ok {
			continue
		}
		own, ok := InputValue(rec, ch)
		if !ok {
			continue
		}
		m := c.spend[key]
		acc := 0.0
		ls := make([]float64, len(c.weights[j]))
		for l, w := range c.weights[j] {
			s := own
			if l > 0 {
				p, ok := m[v-int64(l)*c.step]
				if !ok || math.IsNaN(p[j]) {
					ls[l] = math.NaN()
					continue
				}
				s = p[j]
			}
			ls[l] = s
			acc += float64(w * s)
		}
		vals[ch] = acc
		lags[ch] = ls
	}
	return carriedRecord{Record: rec, vals: vals, lags: lags}
}
