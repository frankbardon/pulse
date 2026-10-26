package regmmm_test

import (
	"context"
	"encoding/json"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// fixture_test.go is the REG_MMM_HILL tests' generated media-mix cohort: per
// geo, 104 weeks of bursty TV flights, always-on search, an optional
// display channel and a price control; spend adstocked by the
// generator's own Weibull kernels (equal to WIN_DISTRIBUTED_LAG's, which
// the tests run upstream and materialize), y from a known truth plus
// Gaussian noise. Every product feeding an addition is float64()-wrapped
// and the transcendental calls are linalg/detmath's deterministic ports, so the
// cohort's bits are a function of the seed alone on every architecture.

// truth is the generator's ground truth. A display β of 0 is a channel
// with no effect.
type truth struct {
	intercept       float64
	tvBeta, tvK     float64
	tvS             float64
	srBeta, srK     float64
	srS             float64
	dispBeta, dispK float64
	dispS           float64
	price, noise    float64
}

var defaultTruth = truth{
	intercept: 50, tvBeta: 12, tvK: 80, tvS: 2, srBeta: 6, srK: 30, srS: 1.6,
	dispBeta: 0, dispK: 20, dispS: 1, price: -3, noise: 0.15,
}

var geos = []string{"east", "north", "west"}

const weeks = 104

// lagKernel is the normalised Weibull lag weights (op-win-distributed-lag's
// formulas): pdf at t = l+1, or the survival curve at t = l.
func lagKernel(pdf bool, maxLag int, shape, scale float64) []float64 {
	w := make([]float64, maxLag+1)
	sum := 0.0
	for l := range w {
		if pdf {
			t := float64(l + 1)
			w[l] = float64(float64(shape/scale*detmath.Pow(t/scale, shape-1)) * detmath.Exp(-detmath.Pow(t/scale, shape)))
		} else {
			w[l] = detmath.Exp(-detmath.Pow(float64(l)/scale, shape))
		}
		sum += w[l]
	}
	for l := range w {
		w[l] /= sum
	}
	return w
}

func convolve(x, w []float64) []float64 {
	out := make([]float64, len(x))
	for i := range x {
		for l, wl := range w {
			if i-l >= 0 {
				out[i] += float64(wl * x[i-l])
			}
		}
	}
	return out
}

// row is one generated row with the generator's own adstock.
type row struct {
	geo                     string
	week                    int
	tv, search, disp, price float64
	tvAd, searchAd, dispAd  float64
	y                       float64
}

func generate(tr truth, seed uint64) []row {
	rng := rand.New(rand.NewPCG(seed, 0x4d4d4d))
	tvW, srW, dpW := lagKernel(false, 8, 2, 3), lagKernel(true, 6, 1.5, 2), lagKernel(false, 4, 1, 1.5)
	var rows []row
	for _, g := range geos {
		tv, sr, dp, pr := make([]float64, weeks), make([]float64, weeks), make([]float64, weeks), make([]float64, weeks)
		for w := range tv {
			if rng.Float64() < 0.5 {
				tv[w] = float64(rng.Float64() * 320)
			}
			if rng.Float64() < 0.6 {
				sr[w] = float64(rng.Float64() * 120)
			}
			if rng.Float64() < 0.7 {
				dp[w] = float64(rng.Float64() * 60)
			}
			pr[w] = float64(rng.Float64()*2) - 1
		}
		tvAd, srAd, dpAd := convolve(tv, tvW), convolve(sr, srW), convolve(dp, dpW)
		for w := range tv {
			y := tr.intercept + float64(tr.tvBeta*regmmm.HillCurve(tvAd[w], tr.tvK, tr.tvS)) +
				float64(tr.srBeta*regmmm.HillCurve(srAd[w], tr.srK, tr.srS)) +
				float64(tr.dispBeta*regmmm.HillCurve(dpAd[w], tr.dispK, tr.dispS)) +
				float64(tr.price*pr[w]) + float64(tr.noise*rng.NormFloat64())
			rows = append(rows, row{geo: g, week: w, tv: tv[w], search: sr[w], disp: dp[w], price: pr[w],
				tvAd: tvAd[w], searchAd: srAd[w], dispAd: dpAd[w], y: y})
		}
	}
	return rows
}

func schema() encoding.Schema {
	geo := encoding.NewDictionary()
	for _, g := range geos {
		_, _ = geo.Add(g)
	}
	fields := []encoding.Field{
		{Name: "geo", Type: encoding.FieldTypeCategoricalU8, Dictionary: geo},
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "tv", Type: encoding.FieldTypeF64},
		{Name: "search", Type: encoding.FieldTypeF64},
		{Name: "display", Type: encoding.FieldTypeF64},
		{Name: "price", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}
	s := encoding.Schema{Fields: fields}
	off := 0
	for i := range s.Fields {
		s.Fields[i].CsvColumnIdx = i
		s.Fields[i].ByteOffset = off
		s.Fields[i].Description = "Media-mix test column " + s.Fields[i].Name + "."
		off += s.FieldWidth(i)
	}
	return s
}

// adstockWindows adstocks every channel per geo over week with the
// generator's kernels.
func adstockWindows() []*types.Window {
	w := func(field, label, params string) *types.Window {
		return &types.Window{Type: types.WIN_DISTRIBUTED_LAG, Field: field, Label: label, PartitionBy: []string{"geo"},
			OrderBy: []types.OrderKey{{Field: "week"}}, Params: json.RawMessage(params)}
	}
	return []*types.Window{
		w("tv", "tv_ad", `{"kernel":"weibull_cdf","max_lag":8,"shape":2,"scale":3}`),
		w("search", "search_ad", `{"kernel":"weibull_pdf","max_lag":6,"shape":1.5,"scale":2}`),
		w("display", "display_ad", `{"kernel":"weibull_cdf","max_lag":4,"shape":1,"scale":1.5}`),
	}
}

// instance opens a stock instance over fsys — REG_MMM_HILL is built in,
// so nothing is registered; mut adjusts the options.
func instance(t testing.TB, fsys afero.Fs, mut func(*pulse.Options)) *pulse.Pulse {
	t.Helper()
	opts := pulse.Options{FS: fsys, DisablePanicRecovery: true}
	if mut != nil {
		mut(&opts)
	}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p
}

// cohort writes the generated mmm.pulse and materializes its
// WIN_DISTRIBUTED_LAG columns into mmm_ad.pulse, the cohort every fit
// reads.
func cohort(t testing.TB, p *pulse.Pulse, tr truth, seed uint64) []row {
	t.Helper()
	ctx := context.Background()
	rows := generate(tr, seed)
	b, err := p.NewCohortBuilder(ctx, "mmm.pulse", schema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		if err := b.Append(pulse.CohortRow{r.geo, uint64(r.week), r.tv, r.search, r.disp, r.price, r.y}); err != nil {
			t.Fatalf("Append row %d: %v", i, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Materialize(ctx, &types.Request{Cohort: &types.Cohort{Filename: "mmm.pulse"}, Windows: adstockWindows()},
		"mmm_ad.pulse", pulse.MaterializeOptions{}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	return rows
}

// spec is a REG_MMM_HILL slot over target y.
func spec(predictors []string, params string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: regmmm.Name, Name: "mmm", Target: "y", Predictors: predictors, Params: json.RawMessage(params)}
}

var (
	twoChannels   = []string{"tv_ad", "search_ad", "price"}
	threeChannels = []string{"tv_ad", "search_ad", "display_ad", "price"}
)

const (
	paramsTwo   = `{"channels":["tv_ad","search_ad"]}`
	paramsThree = `{"channels":["tv_ad","search_ad","display_ad"]}`
)

// process runs one request and returns its response.
func process(t testing.TB, p *pulse.Pulse, req *types.Request) *types.Response {
	t.Helper()
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp
}

// fit fits s over mmm_ad.pulse and returns its (successful) result.
func fit(t testing.TB, p *pulse.Pulse, s *types.RegressionSpec) *types.RegressionResult {
	t.Helper()
	resp := process(t, p, &types.Request{Cohort: &types.Cohort{Filename: "mmm_ad.pulse"}, Regressions: []*types.RegressionSpec{s}})
	if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil {
		t.Fatalf("regressions = %s", mustJSON(t, resp.Regressions))
	}
	return resp.Regressions[0]
}

func mustJSON(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// relClose reports |got − want| ≤ tol·|want|.
func relClose(got, want, tol float64) bool { return math.Abs(got-want) <= tol*math.Abs(want) }
