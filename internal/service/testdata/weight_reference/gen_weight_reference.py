# /// script
# requires-python = "==3.12.*"
# dependencies = [
#   "numpy==2.3.3",
#   "scipy==1.16.2",
#   "statsmodels==0.14.5",
# ]
# ///
"""Reference generator for the weighted descriptive aggregators.

Writes ../../weight_reference_values_test.go (package service): the
fixture rows and, per (weight column, kind, operator), the figure the
external reference computes. CI never runs Python; the Go literals ARE
the pinned oracle (weighting-descriptive E2-S4,
.claude/reference/weighting.md "Reference fixtures").

Regenerate (uv resolves the pinned versions above, PEP 723; the
probability quantiles shell out to Rscript hmisc_quantile.R, which needs
R 4.6.1 with Hmisc 5.3.0 on R_LIBS — see that script's header):

    cd internal/service/testdata/weight_reference
    R_LIBS=<lib with Hmisc 5.3.0> uv run --no-project gen_weight_reference.py
    gofmt -w ../../weight_reference_values_test.go

Reference per family, and how each maps onto Pulse's documented
definition:

  * AGG_COUNT / AGG_SUM / AGG_AVERAGE / AGG_WEIGHTED_MEAN / AGG_VARIANCE /
    AGG_STDDEV / AGG_WELFORD — statsmodels DescrStatsW. Pulse's
    AGG_VARIANCE / AGG_STDDEV are POPULATION figures (m2_w / Σw) =
    DescrStatsW(ddof=0).var / .std; AGG_WELFORD's variance is
    m2_w / (Σw − 1) = DescrStatsW(ddof=1).var (statsmodels divides by
    sum_weights − ddof). AGG_COUNT = .sum_weights, AGG_SUM = .sum.
  * AGG_MEDIAN / AGG_PERCENTILE, kind frequency — numpy.percentile
    (method="linear", Hyndman–Fan type 7) on np.repeat-expanded data.
  * AGG_MEDIAN / AGG_PERCENTILE, kind probability — R Hmisc 5.3.0
    wtd.quantile(x, w, probs, type="quantile", normwt=TRUE), via
    hmisc_quantile.R: weights rescaled to Σw = n, x_(k) = the smallest
    value whose cumulative weight is >= the 1-based rank k + 1, linear
    interpolation. Cross-checked against an exact rational
    (fractions.Fraction) transcription of the same rule (1e-12
    relative: Hmisc interpolates as (1-f)·lo + f·hi, Pulse as
    lo + f·(hi-lo)), and that transcription against numpy type 7 on the
    expansion wherever the normalized weights are integers.
  * AGG_SKEWNESS / AGG_KURTOSIS — population (biased) moments:
    scipy.stats.skew(bias=True) / kurtosis(fisher=True, bias=True) on
    the expanded data for kind frequency, and for the probability twin
    p = c·f (both are invariant under a common weight scale). For the
    fractional column w no library takes weights, so the figure is the
    exact rational weighted moment ratio M3/(W·var^1.5),
    M4/(W·var^2) − 3 (var = M2/W) evaluated in 50-digit Decimal.
  * AGG_MODE / AGG_MODE_COUNT / AGG_FREQUENCY / AGG_RATIO / AGG_SET_* —
    numpy weighted sums (np.unique + np.bincount(weights=...), masked
    np.sum): Σw per value, Σw_match / Σw, Σw·num / Σw·den, Σw per
    member, Σw·popcount, Σw·popcount / Σw.

Weighted significance tests (weighting-inferential E1-S4) — their own
generated fixture weightRefTestRows (x, y numeric; h two-level, k
three-level, o three-level categoricals; weights w / f / p as above),
one reference row per (test, weight configuration):

  * kind frequency — STOCK R on the rep()-expanded rows, via
    test_reference.R (base R only): t.test (one-sample, Welch
    var.equal = FALSE, paired), oneway.test (var.equal TRUE / FALSE) +
    aov for the sums of squares, cor.test, chisq.test(correct = FALSE),
    prop.test(correct = FALSE) (z = sign(p_a − p_b)·√X²); the z test is
    the Welch standard error with a pnorm tail on R's mean / var.
  * kind probability — the closed form of the one formula rule
    (.claude/reference/weighting.md, Weighted inference): N* = Kish
    n_eff, w* = w·N*/Σw, every test = its frequency formula on w*
    (two-pass sums; scipy t / F / χ² / normal tails, fractional df). The
    moment step is cross-checked against statsmodels: DescrStatsW(w*)
    (sum_weights = n_eff, ddof=1 var, ttest_mean df = sum_weights − 1),
    CompareMeans.ttest_ind(usevar="unequal") / ztest_ind,
    DescrStatsW(w*).corrcoef, proportions_ztest on the scaled counts and
    scipy chi2_contingency(correction=False) on T* = n_eff·p_ij. The
    SAME closed form is run on the frequency configuration with w* = f
    and must equal R there (1e-9), which is what licenses it as the
    probability oracle.

Per-group N*: the split t / Welch / z, Welch ANOVA and prop-z read each
group's own n_eff; ANOVA F, Pearson, paired and χ² read one n_eff over
the contributing rows (the w* scale c = N*/Σw is whole-sample there).
Confidence bounds are pinned only where Pulse inverts the t
distribution (statdist); the z / prop-z Wald bounds and the Pearson
Fisher-z bounds still use an approximate inverse-erf and are left out
until that is replaced.

Row validity mirrors weighting.Classify: a weight that is negative,
NaN/Inf, or non-integer under kind frequency excludes its row (counted
in n_weight_invalid when the value is present); a zero weight is valid
and contributes nothing.
"""

import json
import math
import os
import subprocess
import sys
from decimal import Decimal, getcontext
from fractions import Fraction

import numpy as np
import scipy
import scipy.stats
import statsmodels
from statsmodels.stats.proportion import proportions_ztest
from statsmodels.stats.weightstats import CompareMeans, DescrStatsW

getcontext().prec = 50
NAN = float("nan")
NULL = None

MEMBERS = ["a", "b", "c", "d", "e"]
P_SCALE = 0.37  # p = P_SCALE·f: non-integer probability twin of f

# (x, y, s, w, f) — x nullable numeric value; y ratio denominator; s a
# set_u8 mask over MEMBERS (None = null); w fractional probability
# weights (a zero, a negative, a NaN); f integer frequency weights
# (zeros, a negative, a NaN, a non-integer 1.5).
ROWS = [
    (3.5, 2.0, 0b00011, 0.8, 2),
    (1.25, 0.5, 0b00101, 1.7, 1),
    (7.0, 1.5, 0b00000, 0.35, 0),
    (3.5, 3.0, 0b11001, 2.2, 3),
    (2.0, 2.25, 0b00010, 0.0, 1),
    (9.75, 1.0, NULL, 1.15, 2),
    (NULL, 4.0, 0b00100, 0.9, 1),
    (1.25, 0.75, 0b10100, 0.6, 4),
    (3.5, 1.5, 0b00001, 1.3, 1),
    (5.0, 2.5, 0b01111, 2.7, 3),
    (1000.0, 1.0, 0b11111, -0.5, -1),
    (2000.0, 1.0, 0b11111, NAN, NAN),
    (4.0, 3.25, 0b01000, 0.45, 0),
    (6.5, 2.0, 0b00110, 0.95, 1.5),
]


def p_of(f):
    """The probability twin: P_SCALE·f, with f's exclusions mirrored."""
    if isinstance(f, float) and math.isnan(f):
        return NAN
    if f != int(f):
        return -P_SCALE * f  # non-integer f is invalid under frequency
    return P_SCALE * f


WEIGHTS = {
    "w": [r[3] for r in ROWS],
    "f": [float(r[4]) for r in ROWS],
    "p": [p_of(float(r[4])) for r in ROWS],
}

CONFIGS = [("w", "probability"), ("f", "frequency"), ("p", "probability")]
PERCENTILES = [37.5, 90.0]
FREQ_VALUE = 3.5


def valid(w, kind):
    if math.isnan(w) or math.isinf(w) or w < 0:
        return False
    if kind == "frequency" and w != int(w):
        return False
    return True


def present_rows(col):
    if col == "x":
        return [i for i, r in enumerate(ROWS) if r[0] is not NULL]
    if col == "s":
        return [i for i, r in enumerate(ROWS) if r[2] is not NULL]
    raise ValueError(col)


def floor(col, wname, kind):
    """sum_weights, n_eff (probability only), n_weight_invalid."""
    ws = WEIGHTS[wname]
    rows = present_rows(col)
    good = [ws[i] for i in rows if valid(ws[i], kind)]
    bad = sum(1 for i in rows if not valid(ws[i], kind))
    sw = float(np.sum(good))
    neff = None
    if kind == "probability":
        neff = float(np.sum(good) ** 2 / np.sum(np.square(good)))
    return sw, neff, bad


def xw(wname, kind):
    """Value / weight arrays over the contributing x rows (w valid)."""
    ws = WEIGHTS[wname]
    idx = [i for i in present_rows("x") if valid(ws[i], kind)]
    return np.array([ROWS[i][0] for i in idx]), np.array([ws[i] for i in idx])


def type7_expanded(x, f, q):
    return float(np.percentile(np.repeat(x, f.astype(int)), q, method="linear"))


def hmisc_quantiles(x, w, qs):
    """Hmisc wtd.quantile(normwt=TRUE) via Rscript: (versions, values)."""
    here = os.path.dirname(os.path.abspath(__file__))
    csv = lambda vs: ",".join(repr(float(v)) for v in vs)
    out = subprocess.run(
        ["Rscript", os.path.join(here, "hmisc_quantile.R"),
         csv([q / 100 for q in qs]), csv(x), csv(w)],
        check=True, capture_output=True, text=True).stdout.split("\n")
    return out[0].strip(), [float(v) for v in out[1:1 + len(qs)]]


def pulse_normalized_quantile(x, w, q, guard=True):
    """Exact rational transcription of the probability rule (Hmisc's)."""
    pairs = sorted((Fraction(xi), Fraction(wi)) for xi, wi in zip(x, w) if wi > 0)
    n = len(pairs)
    total = sum(wi for _, wi in pairs)
    cum, acc = [], Fraction(0)
    for _, wi in pairs:
        acc += wi
        cum.append(acc * n / total)

    def at(k):
        for (xi, _), c in zip(pairs, cum):
            if c >= k + 1:
                return xi
        return pairs[-1][0]

    # Knife-edge guard: a normalized cumulative weight sitting on an
    # integer rank makes ">= the rank" turn on the last ulp of the float
    # sum. Pulse snaps such a weight to the integer (the exact answer);
    # Hmisc does not, so its figure there is a float artifact and not a
    # usable reference. Refuse such fixtures.
    for c in cum[:-1] if guard else []:
        if abs(c - round(c)) < Fraction(1, 10**6):
            raise SystemExit(f"knife-edge fixture: normalized cumulative weight {float(c)!r}")

    h = Fraction(q) / 100 * (n - 1)
    lo, hi = math.floor(h), math.ceil(h)
    a, b = at(lo), at(hi)
    return float(a + (h - lo) * (b - a))


def exact_shape(x, w):
    xs = [Fraction(v) for v in x]
    ws = [Fraction(v) for v in w]
    W = sum(ws)
    mean = sum(a * b for a, b in zip(ws, xs)) / W
    m2 = sum(b * (a - mean) ** 2 for a, b in zip(xs, ws))
    m3 = sum(b * (a - mean) ** 3 for a, b in zip(xs, ws))
    m4 = sum(b * (a - mean) ** 4 for a, b in zip(xs, ws))
    var = m2 / W

    def dec(fr):
        return Decimal(fr.numerator) / Decimal(fr.denominator)

    skew = dec(m3 / W) / (dec(var).sqrt() ** 3)
    kurt = dec(m4 / (W * var * var)) - 3
    return float(skew), float(kurt)


HMISC = [None]  # "R <ver>; Hmisc <ver>", set by the first probability config


def cases():
    out = []
    for wname, kind in CONFIGS:
        x, w = xw(wname, kind)
        fl = floor("x", wname, kind)
        d0 = DescrStatsW(x, weights=w, ddof=0)
        d1 = DescrStatsW(x, weights=w, ddof=1)

        def add(op, data, params="", col="x", comp=None, floor_=fl, src=""):
            out.append(dict(weight=wname, kind=kind, op=op, params=params, col=col,
                            data=data, comp=comp or {}, floor=floor_, src=src))

        sm = f"statsmodels {statsmodels.__version__} DescrStatsW"
        add("AGG_COUNT", {"": d0.sum_weights}, src=sm + ".sum_weights")
        add("AGG_SUM", {"": d0.sum}, src=sm + ".sum")
        add("AGG_AVERAGE", {"": d0.mean}, src=sm + ".mean")
        add("AGG_WEIGHTED_MEAN", {"": d0.mean}, src=sm + ".mean")
        add("AGG_VARIANCE", {"": d0.var}, src=sm + "(ddof=0).var")
        add("AGG_STDDEV", {"": d0.std}, src=sm + "(ddof=0).std")
        add("AGG_WELFORD", {"mean": d1.mean, "variance": d1.var}, src=sm + "(ddof=1).mean/.var")

        if kind == "frequency":
            qsrc = f"numpy {np.__version__} percentile(method=linear) on np.repeat"
            med = type7_expanded(x, w, 50)
            pct = {q: type7_expanded(x, w, q) for q in PERCENTILES}
        else:
            qs = [50.0] + PERCENTILES
            rver, hq = hmisc_quantiles(x, w, qs)
            HMISC[0] = rver
            for q, v in zip(qs, hq):
                exact = pulse_normalized_quantile(x, w, q)
                assert abs(v - exact) <= 1e-12 * abs(exact), (wname, q, v, exact)
            qsrc = f"{rver} wtd.quantile(normwt=TRUE)"
            med = hq[0]
            pct = dict(zip(PERCENTILES, hq[1:]))
        add("AGG_MEDIAN", {"": med}, src=qsrc)
        for q in PERCENTILES:
            add("AGG_PERCENTILE", {"": pct[q]}, params=f'{{"percentile":{q}}}', src=qsrc)

        vals, inv = np.unique(x, return_inverse=True)
        per = np.bincount(inv, weights=w)
        top = per.max()
        mode = float(vals[np.flatnonzero(per == top)[0]])  # ties → smallest
        nsrc = f"numpy {np.__version__} weighted sums"
        add("AGG_MODE", {"": mode}, src=nsrc + " (np.unique + np.bincount)")
        add("AGG_MODE_COUNT", {"": float(top)}, src=nsrc + " (np.unique + np.bincount)")

        match = float(np.sum(w[x == FREQ_VALUE]))
        add("AGG_FREQUENCY", {"": match}, params=f'{{"value":"{FREQ_VALUE}"}}',
            comp={"match_count": match, "share": match / float(np.sum(w))}, src=nsrc)

        ws = WEIGHTS[wname]
        ridx = [i for i in present_rows("x") if valid(ws[i], kind)]
        num = float(np.sum([ws[i] * ROWS[i][0] for i in ridx]))
        den = float(np.sum([ws[i] * ROWS[i][1] for i in ridx]))
        add("AGG_RATIO", {"": num / den},
            params='{"numerator_field":"x","denominator_field":"y"}',
            comp={"numerator": num, "denominator": den}, src=nsrc)

        if kind == "frequency":
            e = np.repeat(x, w.astype(int))
            skew = float(scipy.stats.skew(e, bias=True))
            kurt = float(scipy.stats.kurtosis(e, fisher=True, bias=True))
            ssrc = f"scipy {scipy.__version__} stats.skew/kurtosis(bias=True) on np.repeat"
        elif wname == "p":
            e = np.repeat(x, np.rint(w / P_SCALE).astype(int))
            skew = float(scipy.stats.skew(e, bias=True))
            kurt = float(scipy.stats.kurtosis(e, fisher=True, bias=True))
            ssrc = (f"scipy {scipy.__version__} stats.skew/kurtosis(bias=True) on np.repeat "
                    "of p/c (moment ratios are weight-scale invariant)")
        else:
            skew, kurt = exact_shape(x, w)
            ssrc = "exact Fraction weighted population moments, 50-digit Decimal"
        add("AGG_SKEWNESS", {"": skew}, src=ssrc)
        add("AGG_KURTOSIS", {"": kurt}, src=ssrc)

        sidx = [i for i in present_rows("s") if valid(ws[i], kind)]
        sw = np.array([ws[i] for i in sidx])
        masks = [ROWS[i][2] for i in sidx]
        card = np.array([bin(m).count("1") for m in masks], dtype=float)
        members = {}
        for b, name in enumerate(MEMBERS):
            sel = np.array([(m >> b) & 1 == 1 for m in masks]) & (sw > 0)
            if sel.any():
                members[name] = float(np.sum(sw[sel]))
        sfl = floor("s", wname, kind)
        add("AGG_SET_FREQUENCY", members, col="s", floor_=sfl, src=nsrc + " per member")
        csum = float(np.sum(sw * card))
        add("AGG_SET_CARDINALITY_SUM", {"": csum}, col="s", floor_=sfl, src=nsrc + " Σw·popcount")
        add("AGG_SET_CARDINALITY_AVG", {"": csum / float(np.sum(sw))}, col="s", floor_=sfl,
            src=nsrc + " Σw·popcount / Σw")

    # Cross-check: where the normalized probability weights are
    # integers the transcription must equal numpy type 7 on the
    # expansion (unit weights scaled by 0.37 normalize to ones).
    xs = np.array([1.5, 4.0, 2.25, 9.0, 4.0, 7.5])
    for q in [50.0] + PERCENTILES:
        a = pulse_normalized_quantile(xs, [0.37] * len(xs), q, guard=False)
        b = type7_expanded(xs, np.ones(len(xs)), q)
        assert a == b, (q, a, b)
    return out


# --- weighted significance tests (weighting-inferential E1-S4) ----------

TEST_MU = 5.5
TEST_SUCCESS = "yes"
H_LEVELS = ["a", "b"]
K_LEVELS = ["p", "q", "r"]
O_LEVELS = ["maybe", "no", "yes"]


def build_test_rows():
    """(x, y, h, k, o, w, f): 66 regular rows plus edge rows.

    x depends on h and k (so the split tests and ANOVAs see real but
    moderate effects), y on x (Pearson / paired), o on h (χ² / prop-z).
    Edge rows: a null x (still counted by χ² / prop-z), zero
    frequency weights, a negative and a NaN weight, and a non-integer f
    (invalid under frequency; its p twin is made negative to match)."""
    rng = np.random.default_rng(20261004)
    shift_h = {"a": 0.0, "b": 0.7}
    shift_k = {"p": -0.5, "q": 0.2, "r": 0.8}
    rows = []
    for i in range(66):
        h = H_LEVELS[i % 2]
        k = K_LEVELS[(i // 2) % 3]
        x = round(5.0 + shift_h[h] + shift_k[k] + float(rng.normal(0, 1.4)), 2)
        y = round(0.5 * x + 2.9 + float(rng.normal(0, 1.8)), 2)
        probs = [0.3, 0.4, 0.3] if h == "a" else [0.25, 0.3, 0.45]
        o = O_LEVELS[int(rng.choice(3, p=probs))]
        w = round(float(rng.uniform(0.2, 3.0)), 3)
        f = int(rng.integers(1, 5))
        rows.append((x, y, h, k, o, w, f))
    rows += [
        (NULL, 4.4, "a", "q", "yes", 1.25, 2),
        (6.1, 4.9, "b", "r", "no", 0.75, 0),
        (4.8, 3.6, "a", "p", "maybe", 0.0, 1),
        (99.0, 1.0, "b", "p", "yes", -0.5, -1),
        (-99.0, 1.0, "a", "r", "no", NAN, NAN),
        (5.9, 4.1, "b", "q", "yes", 1.6, 1.5),
    ]
    return rows


TEST_ROWS = build_test_rows()
TEST_WEIGHTS = {
    "w": [r[5] for r in TEST_ROWS],
    "f": [float(r[6]) for r in TEST_ROWS],
    "p": [p_of(float(r[6])) for r in TEST_ROWS],
}

# (case, spec JSON, R case) — spec is the request's tests[0] slot.
TEST_SPECS = [
    ("t_one_sample", {"type": "TEST_T", "field": "x", "params": {"mu": TEST_MU}}),
    ("t_split", {"type": "TEST_T", "field": "x", "split_by": "h"}),
    ("welch", {"type": "TEST_WELCH", "field": "x", "split_by": "h"}),
    ("z", {"type": "TEST_Z_TWO_SAMPLE", "field": "x", "split_by": "h"}),
    ("paired", {"type": "TEST_PAIRED_T", "field": "x", "field2": "y"}),
    ("anova_f", {"type": "TEST_ANOVA_F", "field": "x", "split_by": "k"}),
    ("anova_welch", {"type": "TEST_ANOVA_WELCH", "field": "x", "split_by": "k"}),
    ("pearson", {"type": "TEST_PEARSON_R", "field": "x", "field2": "y"}),
    ("chisq", {"type": "TEST_CHISQ", "rows": "h", "cols": "o"}),
    ("prop_z", {"type": "TEST_PROP_Z", "field": "o", "split_by": "h",
                "params": {"success": TEST_SUCCESS}}),
]


def kish(w):
    w = np.asarray(w, dtype=float)
    return float(w.sum() ** 2 / np.square(w).sum())


class Sample:
    """Closed-form w* summary of one sample (two-pass sums)."""

    def __init__(self, x, w, prob):
        self.x = np.asarray(x, dtype=float)
        self.w = np.asarray(w, dtype=float)
        self.n = len(self.x)
        self.sw = float(self.w.sum())
        self.nstar = kish(self.w) if prob else self.sw
        self.c = self.nstar / self.sw
        self.ws = self.w * self.c  # w*
        self.mean = float(np.sum(self.w * self.x) / self.sw)
        self.m2 = float(np.sum(self.w * (self.x - self.mean) ** 2))
        self.var = self.c * self.m2 / (self.nstar - 1)


def t_two_sided(t, df):
    return float(2 * scipy.stats.t.sf(abs(t), df))


def t_crit(df, alpha=0.05):
    return float(scipy.stats.t.ppf(1 - alpha / 2, df))


def test_closed_form(case, rows, prob):
    """The probability (and, as a check, frequency) closed form.

    rows: (x, y, h, k, o, w) tuples with a VALID weight. Returns
    {figure: value} in the reference's figure vocabulary (see FIGURES).
    A zero weight is valid but contributes nothing: it is dropped here,
    as Pulse keeps it out of every sum and of n."""
    rows = [r for r in rows if r[5] > 0]
    xr = [r for r in rows if r[0] is not NULL]
    out = {}
    if case == "t_one_sample":
        s = Sample([r[0] for r in xr], [r[5] for r in xr], prob)
        t = (s.mean - TEST_MU) / math.sqrt(s.var / s.nstar)
        df = s.nstar - 1
        half = t_crit(df) * math.sqrt(s.var / s.nstar)
        d = DescrStatsW(s.x, weights=s.ws, ddof=1)
        tt, pp, dd = d.ttest_mean(TEST_MU)
        assert close(d.sum_weights, s.nstar) and close(d.var, s.var) and close(dd, df), case
        assert close(tt, t) and close(pp, t_two_sided(t, df), 1e-9), case
        out.update(statistic=t, df=df, p_value=t_two_sided(t, df), mean=s.mean,
                   variance=s.var, ci_low=s.mean - half, ci_high=s.mean + half,
                   sum_weights=s.sw, n=s.n)
        if prob:
            out["n_eff"] = s.nstar
        return out
    if case in ("t_split", "welch", "z"):
        sa = Sample([r[0] for r in xr if r[2] == "a"], [r[5] for r in xr if r[2] == "a"], prob)
        sb = Sample([r[0] for r in xr if r[2] == "b"], [r[5] for r in xr if r[2] == "b"], prob)
        va, vb = sa.var / sa.nstar, sb.var / sb.nstar
        diff = sa.mean - sb.mean
        stat = diff / math.sqrt(va + vb)
        df = (va + vb) ** 2 / (va * va / (sa.nstar - 1) + vb * vb / (sb.nstar - 1))
        cm = CompareMeans(DescrStatsW(sa.x, weights=sa.ws), DescrStatsW(sb.x, weights=sb.ws))
        if case == "z":
            zz, zp = cm.ztest_ind(usevar="unequal")
            p = float(2 * scipy.stats.norm.sf(abs(stat)))
            assert close(zz, stat) and close(zp, p, 1e-9), case
            out.update(statistic=stat, p_value=p, diff=diff)
        else:
            tt, tp, td = cm.ttest_ind(usevar="unequal")
            p = t_two_sided(stat, df)
            assert close(tt, stat) and close(td, df) and close(tp, p, 1e-9), case
            half = t_crit(df) * math.sqrt(va + vb)
            out.update(statistic=stat, df=df, p_value=p, diff=diff,
                       ci_low=diff - half, ci_high=diff + half)
        out.update({"mean[a]": sa.mean, "mean[b]": sb.mean,
                    "variance[a]": sa.var, "variance[b]": sb.var,
                    "sum_weights[a]": sa.sw, "sum_weights[b]": sb.sw,
                    "n[a]": sa.n, "n[b]": sb.n})
        if prob:
            out.update({"n_eff[a]": sa.nstar, "n_eff[b]": sb.nstar})
        return out
    if case == "paired":
        s = Sample([r[0] - r[1] for r in xr], [r[5] for r in xr], prob)
        t = s.mean / math.sqrt(s.var / s.nstar)
        df = s.nstar - 1
        half = t_crit(df) * math.sqrt(s.var / s.nstar)
        tt, _, dd = DescrStatsW(s.x, weights=s.ws, ddof=1).ttest_mean(0)
        assert close(tt, t) and close(dd, df), case
        out.update(statistic=t, df=df, p_value=t_two_sided(t, df), mean_diff=s.mean,
                   variance=s.var, ci_low=s.mean - half, ci_high=s.mean + half,
                   sum_weights=s.sw, n=s.n)
        if prob:
            out["n_eff"] = s.nstar
        return out
    if case == "anova_f":
        allw = Sample([r[0] for r in xr], [r[5] for r in xr], prob)
        ssb = ssw = 0.0
        for g in K_LEVELS:
            sg = Sample([r[0] for r in xr if r[3] == g], [r[5] for r in xr if r[3] == g], False)
            ssb += allw.c * sg.sw * (sg.mean - allw.mean) ** 2
            ssw += allw.c * sg.m2
            out[f"group_means[{g}]"] = sg.mean
            out[f"n[{g}]"] = sg.n
            out[f"sum_weights[{g}]"] = sg.sw
            if prob:
                out[f"n_eff[{g}]"] = kish(sg.w)
        k = len(K_LEVELS)
        dfw = allw.nstar - k
        F = (ssb / (k - 1)) / (ssw / dfw)
        out.update(statistic=F, df=k - 1, df_between=k - 1, df_within=dfw,
                   p_value=float(scipy.stats.f.sf(F, k - 1, dfw)),
                   ss_between=ssb, ss_within=ssw,
                   **{"effect_size.eta_squared": ssb / (ssb + ssw)})
        return out
    if case == "anova_welch":
        gs = []
        for g in K_LEVELS:
            sg = Sample([r[0] for r in xr if r[3] == g], [r[5] for r in xr if r[3] == g], prob)
            gs.append(sg)
            out[f"group_means[{g}]"] = sg.mean
        k = len(gs)
        v = [sg.nstar / sg.var for sg in gs]
        W = sum(v)
        mt = sum(vi * sg.mean for vi, sg in zip(v, gs)) / W
        num = sum(vi * (sg.mean - mt) ** 2 for vi, sg in zip(v, gs)) / (k - 1)
        tail = sum((1 - vi / W) ** 2 / (sg.nstar - 1) for vi, sg in zip(v, gs))
        F = num / (1 + 2 * (k - 2) / (k * k - 1) * tail)
        df2 = (k * k - 1) / (3 * tail)
        out.update(statistic=F, df=k - 1, df_between=k - 1, df_within=df2,
                   p_value=float(scipy.stats.f.sf(F, k - 1, df2)))
        return out
    if case == "pearson":
        w = np.array([r[5] for r in xr])
        sx = Sample([r[0] for r in xr], w, prob)
        sy = Sample([r[1] for r in xr], w, prob)
        cxy = float(np.sum(w * (sx.x - sx.mean) * (sy.x - sy.mean)))
        r = cxy / math.sqrt(sx.m2 * sy.m2)
        rr = DescrStatsW(np.c_[sx.x, sy.x], weights=sx.ws).corrcoef[0, 1]
        assert close(rr, r), case
        df = sx.nstar - 2
        t = r * math.sqrt(df / (1 - r * r))
        out.update(statistic=r, df=df, p_value=t_two_sided(t, df), t=t,
                   mean_x=sx.mean, mean_y=sy.mean, sum_weights=sx.sw, n=sx.n)
        if prob:
            out["n_eff"] = sx.nstar
        return out
    if case == "chisq":
        w = np.array([r[5] for r in rows])
        nstar = kish(w) if prob else float(w.sum())
        tab = np.zeros((len(H_LEVELS), len(O_LEVELS)))
        for r in rows:
            tab[H_LEVELS.index(r[2]), O_LEVELS.index(r[4])] += r[5]
        scaled = nstar * tab / tab.sum()
        stat, p, df, expected = scipy.stats.chi2_contingency(scaled, correction=False)
        out.update(statistic=float(stat), df=float(df), p_value=float(p),
                   expected_min=float(expected.min()), sum_weights=float(w.sum()), n=len(rows),
                   **{"effect_size.cramers_v": math.sqrt(stat / (nstar * (min(tab.shape) - 1)))})
        if prob:
            out["n_eff"] = nstar
        return out
    if case == "prop_z":
        ns, mass, phat = [], [], []
        for g in H_LEVELS:
            s = Sample([1.0 if r[4] == TEST_SUCCESS else 0.0 for r in rows if r[2] == g],
                       [r[5] for r in rows if r[2] == g], prob)
            ns.append(s.nstar)
            phat.append(s.mean)
            mass.append(s.mean * s.nstar)
            out[f"proportion[{g}]"] = s.mean
            out[f"successes[{g}]"] = float(np.sum(s.w * s.x))
            out[f"sum_weights[{g}]"] = s.sw
            out[f"n[{g}]"] = s.n
            if prob:
                out[f"n_eff[{g}]"] = s.nstar
        pooled = sum(mass) / sum(ns)
        z = (phat[0] - phat[1]) / math.sqrt(pooled * (1 - pooled) * (1 / ns[0] + 1 / ns[1]))
        sz, sp = proportions_ztest(np.array(mass), np.array(ns))
        p = float(2 * scipy.stats.norm.sf(abs(z)))
        assert close(sz, z) and close(sp, p, 1e-9), case
        out.update(statistic=z, p_value=p, pooled=pooled, diff=phat[0] - phat[1],
                   **{"effect_size.cohens_h": 2 * math.asin(math.sqrt(phat[0]))
                      - 2 * math.asin(math.sqrt(phat[1]))})
        return out
    raise ValueError(case)


def close(a, b, rel=1e-10):
    a, b = float(a), float(b)
    return abs(a - b) <= rel * max(abs(b), 1e-300) or a == b


# R figure name → reference figure name, per case (test_reference.R).
R_FIGURES = {
    "t_one_sample": ("t_one", {"statistic": "statistic", "df": "df", "p_value": "p_value",
                               "mean": "mean", "variance": "variance",
                               "ci_low": "ci_low", "ci_high": "ci_high"}),
    "t_split": ("welch", {"statistic": "statistic", "df": "df", "p_value": "p_value",
                          "mean_a": "mean[a]", "mean_b": "mean[b]", "variance_a": "variance[a]",
                          "variance_b": "variance[b]", "ci_low": "ci_low", "ci_high": "ci_high"}),
    "z": ("z", {"statistic": "statistic", "p_value": "p_value"}),
    "paired": ("paired", {"statistic": "statistic", "df": "df", "p_value": "p_value",
                          "mean_diff": "mean_diff", "ci_low": "ci_low", "ci_high": "ci_high"}),
    "anova_f": ("anova_f", {"statistic": "statistic", "df_between": "df_between",
                            "df_within": "df_within", "p_value": "p_value",
                            "ss_between": "ss_between", "ss_within": "ss_within",
                            "mean_p": "group_means[p]", "mean_q": "group_means[q]",
                            "mean_r": "group_means[r]"}),
    "anova_welch": ("anova_welch", {"statistic": "statistic", "df_between": "df_between",
                                    "df_within": "df_within", "p_value": "p_value"}),
    "pearson": ("pearson", {"r": "statistic", "t": "t", "df": "df", "p_value": "p_value"}),
    "chisq": ("chisq", {"statistic": "statistic", "df": "df", "p_value": "p_value",
                        "expected_min": "expected_min", "cramers_v": "effect_size.cramers_v"}),
    "prop_z": ("prop_z", {"statistic": "statistic", "p_value": "p_value",
                          "proportion_a": "proportion[a]", "proportion_b": "proportion[b]",
                          "pooled": "pooled"}),
}
R_FIGURES["welch"] = R_FIGURES["t_split"]

# Top-level TestResult fields; everything else lives under details.
TOP_LEVEL = {"statistic", "df", "p_value"}


def r_test_reference(rows):
    """Run test_reference.R on the frequency rows: (version, {case: {fig: v}})."""
    import csv
    import tempfile
    here = os.path.dirname(os.path.abspath(__file__))
    with tempfile.NamedTemporaryFile("w", suffix=".csv", delete=False, newline="") as fh:
        wr = csv.writer(fh)
        wr.writerow(["x", "y", "h", "k", "o", "f"])
        for x, y, h, k, o, f in rows:
            wr.writerow(["NA" if x is NULL else repr(float(x)), repr(float(y)), h, k, o, int(f)])
        path = fh.name
    try:
        lines = subprocess.run(["Rscript", os.path.join(here, "test_reference.R"), path, repr(TEST_MU)],
                               check=True, capture_output=True, text=True).stdout.strip().split("\n")
    finally:
        os.unlink(path)
    figs = {}
    for ln in lines[1:]:
        case, fig, v = ln.split()
        figs.setdefault(case, {})[fig] = float(v)
    return lines[0].strip(), figs


RTEST = [None]  # "R <ver>", set by the frequency configuration


def test_cases():
    out = []
    for wname, kind in CONFIGS:
        ws = TEST_WEIGHTS[wname]
        prob = kind == "probability"
        good = [(r[0], r[1], r[2], r[3], r[4], ws[i]) for i, r in enumerate(TEST_ROWS) if valid(ws[i], kind)]
        rfigs = None
        if not prob:
            RTEST[0], rfigs = r_test_reference([g[:5] + (int(g[5]),) for g in good])
        for case, spec in TEST_SPECS:
            figs = test_closed_form(case, good, prob)
            if case == "chisq":
                # The fixture must clear the expected-count guard under
                # every configuration (a guard warning is a different test).
                assert figs["expected_min"] >= 5, (wname, figs["expected_min"])
            if prob:
                src = ("closed form on w* = w·n_eff/Σw (scipy " + scipy.__version__ +
                       " tails), moments cross-checked with statsmodels " +
                       statsmodels.__version__)
            else:
                rcase, names = R_FIGURES[case]
                for rname, fname in names.items():
                    rv = rfigs[rcase][rname]
                    # The closed form IS stock R on the expansion here.
                    assert close(figs[fname], rv, 1e-9), (case, fname, figs[fname], rv)
                    figs[fname] = rv
                src = f"{RTEST[0]} stats on rep()-expanded rows (test_reference.R)"
            out.append(dict(weight=wname, kind=kind, name=case, spec=spec, figs=figs, src=src))
    return out


def figure_path(name):
    """Reference figure name → JSON path from the TestResult root."""
    if name in TOP_LEVEL:
        return name
    return "details." + name


def gofloat(v):
    if isinstance(v, float) and math.isnan(v):
        return "math.NaN()"
    r = repr(float(v))
    return r


def emit(out, tout):
    lines = []
    w = lines.append
    w("// Code generated by testdata/weight_reference/gen_weight_reference.py; DO NOT EDIT.")
    w("")
    w("package service")
    w("")
    w('import "math"')
    w("")
    w("// weightRefProvenance records the generating toolchain.")
    w(f'const weightRefProvenance = "python {sys.version.split()[0]}, numpy {np.__version__}, '
      f'scipy {scipy.__version__}, statsmodels {statsmodels.__version__}; {HMISC[0]}"')
    w("")
    w(f"// weightRefPScale is c in the probability twin p = c·f.")
    w(f"const weightRefPScale = {P_SCALE!r}")
    w("")
    w("var weightRefRows = []weightRefRow{")
    for r in ROWS:
        x, y, s, wv, f = r
        f = float(f)
        fields = [
            f"x: {gofloat(x) if x is not NULL else '0'}",
            f"xNull: {'true' if x is NULL else 'false'}",
            f"y: {gofloat(y)}",
            f"s: {s if s is not NULL else 0}",
            f"sNull: {'true' if s is NULL else 'false'}",
            f"w: {gofloat(wv)}",
            f"f: {gofloat(f)}",
            f"p: {gofloat(p_of(f))}",
        ]
        w("\t{" + ", ".join(fields) + "},")
    w("}")
    w("")
    w("var weightRefCases = []weightRefCase{")
    for c in out:
        sw, neff, bad = c["floor"]
        data = ", ".join(f'"{k}": {gofloat(v)}' for k, v in c["data"].items())
        comp = ", ".join(f'"{k}": {gofloat(v)}' for k, v in c["comp"].items())
        neff_s = gofloat(neff) if neff is not None else "0"
        w(f'\t{{weight: "{c["weight"]}", kind: "{c["kind"]}", op: "{c["op"]}", field: "{c["col"]}", '
          f'params: `{c["params"]}`, data: map[string]float64{{{data}}}, '
          f'components: map[string]float64{{{comp}}}, sumWeights: {gofloat(sw)}, '
          f'nEff: {neff_s}, hasNEff: {"true" if neff is not None else "false"}, '
          f'nWeightInvalid: {bad}, source: "{c["src"]}"}},')
    w("}")
    w("")
    w(f"// weightRefTestProvenance records the significance-test toolchain.")
    w(f'const weightRefTestProvenance = "{RTEST[0]} (stats); python {sys.version.split()[0]}, '
      f'scipy {scipy.__version__}, statsmodels {statsmodels.__version__}"')
    w("")
    w("// weightRefTestRows is the significance-test fixture.")
    w("var weightRefTestRows = []weightRefTestRow{")
    for r in TEST_ROWS:
        x, y, h, k, o, wv, f = r
        f = float(f)
        fields = [
            f"x: {gofloat(x) if x is not NULL else '0'}",
            f"xNull: {'true' if x is NULL else 'false'}",
            f"y: {gofloat(y)}",
            f'h: "{h}", k: "{k}", o: "{o}"',
            f"w: {gofloat(wv)}",
            f"f: {gofloat(f)}",
            f"p: {gofloat(p_of(f))}",
        ]
        w("\t{" + ", ".join(fields) + "},")
    w("}")
    w("")
    w("// weightRefTestCases: one slot per case, figures keyed by wire path.")
    w("var weightRefTestCases = []weightRefInferCase{")
    for c in tout:
        req = json.dumps({"tests": [c["spec"]]}, separators=(",", ":"))
        figs = ", ".join(f'"tests[0].{figure_path(k)}": {gofloat(v)}' for k, v in sorted(c["figs"].items()))
        w(f'\t{{weight: "{c["weight"]}", kind: "{c["kind"]}", name: "{c["name"]}", '
          f'request: `{req}`, figures: map[string]float64{{{figs}}}, source: "{c["src"]}"}},')
    w("}")
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    here = os.path.dirname(os.path.abspath(__file__))
    dst = os.path.join(here, "..", "..", "weight_reference_values_test.go")
    with open(dst, "w") as fh:
        fh.write(emit(cases(), test_cases()))
    print("wrote", os.path.normpath(dst))
