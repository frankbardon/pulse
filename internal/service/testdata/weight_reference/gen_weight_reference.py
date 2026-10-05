# /// script
# requires-python = "==3.12.*"
# dependencies = [
#   "numpy==2.3.3",
#   "scikit-learn==1.7.2",
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
  * AGG_CI_LOWER / AGG_CI_UPPER (weighting-inferential E5-S1) — the
    normal-critical bound mean ∓ z·√(s²/N*), z = qnorm(1 − α/2), s² on
    w* = w·N*/Σw. Kind frequency: stock R on the rep()-expanded rows
    (ci_reference.R: mean ∓ qnorm·sd/√n). Kind probability: that closed
    form with N* = Kish n_eff, cross-checked against statsmodels
    DescrStatsW(w*).zconfint_mean; the same closed form must equal R on
    the frequency column (1e-12) first.
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

Weighted regressions and regression attributes (weighting-inferential
E4-S3) — their own fixture REG_ROWS; R lm / glm on the expansion
(reg_reference.R) for kind frequency, the w* closed form cross-checked
with statsmodels for kind probability, scikit-learn for the penalised β,
the closed-form NIG posterior for Bayes linear (see "weighted
regressions" below).

ATTR_ZSCORE / ATTR_TSCORE and GROUP_QUANTILE (weighting-inferential
E5-S2) — on the same fixture. Scores: (y − μ_w)/σ_w with the POPULATION
sd √(Σw(y − μ_w)²/Σw); frequency = R mean / population sd of the
expanded y, probability = that closed form cross-checked with
statsmodels DescrStatsW(ddof=0) (scale-free, so kind-free). Quantile
buckets on yg (distinct values): the order statistic opening each bucket
is pinned to R on the sorted expansion (frequency) or Hmisc
wtd.quantile(normwt=TRUE) (probability); counts and highs follow from an
exact rational transcription of Pulse's cut ⌊⌊C − 1⌋·k/W⌋.

Per-group N*: the split t / Welch / z, ANOVA F (c_g = N*_g/Σw_g, so
N* = Σ_g N*_g — review WS-01), Welch ANOVA and prop-z read each group's
own n_eff; Pearson, paired and χ² read one n_eff over the contributing
rows (the w* scale c = N*/Σw is whole-sample there).
Confidence bounds are pinned everywhere (since weighting-inferential
E5-S1 replaced the approximate inverse-erf normal critical value): the
t bounds invert the t distribution, the z / prop-z Wald bounds and the
Pearson Fisher-z bounds use qnorm(0.975) — R's own conf.int for prop.test
and cor.test, the Welch standard error × qnorm for the z test;
probability: the same formulas on N* (statsmodels zconfint_diff and
confint_proportions_2indep(method="wald") cross-check the z and prop-z
bounds).

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
import warnings
from decimal import Decimal, getcontext
from fractions import Fraction

import numpy as np
import scipy
import scipy.special
import scipy.stats
import sklearn
import sklearn.linear_model
import statsmodels
from statsmodels.genmod import families as sm_families
from statsmodels.genmod.generalized_linear_model import GLM as SmGLM
from statsmodels.regression.linear_model import OLS as SmOLS
from statsmodels.tools.sm_exceptions import DomainWarning
from statsmodels.stats.proportion import confint_proportions_2indep, proportions_ztest
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


def ci_closed_form(x, w, prob, conf):
    """The normal-critical CI on w*: mean ∓ z·√(s²/N*)."""
    x = np.asarray(x, dtype=float)
    w = np.asarray(w, dtype=float)
    keep = w > 0
    x, w = x[keep], w[keep]
    sw = float(w.sum())
    nstar = kish(w) if prob else sw
    c = nstar / sw
    mean = float(np.sum(w * x) / sw)
    m2 = float(np.sum(w * (x - mean) ** 2))
    s2 = c * m2 / (nstar - 1)
    se = math.sqrt(s2 / nstar)
    z = float(scipy.stats.norm.isf((1 - conf) / 2))
    figs = dict(mean=mean, stderr=se, t_critical=z, lower=mean - z * se, upper=mean + z * se)
    lo, hi = DescrStatsW(x, weights=w * c).zconfint_mean(alpha=1 - conf)
    assert close(lo, figs["lower"], 1e-12) and close(hi, figs["upper"], 1e-12), (lo, hi, figs)
    return figs


def r_ci_reference(x, f, conf):
    """ci_reference.R on the frequency rows: (version, {figure: value})."""
    here = os.path.dirname(os.path.abspath(__file__))
    csv = lambda vs: ",".join(repr(float(v)) for v in vs)
    lines = subprocess.run(
        ["Rscript", os.path.join(here, "ci_reference.R"), repr(conf), csv(x), csv(f)],
        check=True, capture_output=True, text=True).stdout.strip().split("\n")
    return lines[0].strip(), {ln.split()[0]: float(ln.split()[1]) for ln in lines[1:]}


def ci_reference(x, w, kind, conf):
    """AGG_CI_* reference figures and their source."""
    prob = kind == "probability"
    figs = ci_closed_form(x, w, prob, conf)
    if prob:
        return figs, (f"closed form on w* = w·n_eff/Σw (scipy {scipy.__version__} norm.isf), "
                      f"cross-checked with statsmodels {statsmodels.__version__} DescrStatsW(w*).zconfint_mean")
    rver, rfigs = r_ci_reference(x, w, conf)
    for k, v in rfigs.items():
        assert close(figs[k], v, 1e-12), (k, figs[k], v)
    return rfigs, f"{rver} stats qnorm on rep()-expanded rows (ci_reference.R)"


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

        for op, bound, conf, params in (("AGG_CI_LOWER", "lower", 0.9, '{"confidence":0.9}'),
                                        ("AGG_CI_UPPER", "upper", 0.95, "")):
            figs, src = ci_reference(x, w, kind, conf)
            add(op, {"": figs[bound]}, params=params,
                comp={k: figs[k] for k in ("mean", "stderr", "t_critical", bound)}, src=src)

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
    # Rank tests (E2-S1): frequency-only — FREQUENCY_ONLY below.
    ("mann_whitney", {"type": "TEST_MANN_WHITNEY_U", "field": "x", "split_by": "h"}),
    ("wilcoxon_sr", {"type": "TEST_WILCOXON_SR", "field": "x", "field2": "y"}),
    ("kruskal", {"type": "TEST_KRUSKAL_WALLIS", "field": "x", "split_by": "k"}),
    ("spearman", {"type": "TEST_SPEARMAN_R", "field": "x", "field2": "y"}),
    ("kendall", {"type": "TEST_KENDALL_TAU", "field": "x", "field2": "y"}),
    # Exact / distribution / spread tests (E2-S2): frequency-only too.
    # Fisher reads the 2×2 h × o table with o = "maybe" filtered out.
    ("fisher", {"type": "TEST_FISHER_EXACT", "rows": "h", "cols": "o"}),
    ("ks", {"type": "TEST_KS", "field": "x", "split_by": "h"}),
    ("brown_forsythe", {"type": "TEST_BROWN_FORSYTHE", "field": "x", "split_by": "k"}),
]

# Request-level slots a case adds beside its tests[0] slot.
TEST_EXTRA = {
    "fisher": {"filterers": [{"type": "FILTER_EXCLUDE", "field": "o", "values": ["maybe"]}]},
}
FISHER_OUT = "maybe"

# Cases with no probability-weighted form (refused under that kind): a
# frequency reference only, = the unweighted test on the expansion.
FREQUENCY_ONLY = {"mann_whitney", "wilcoxon_sr", "kruskal", "spearman", "kendall",
                  "fisher", "ks", "brown_forsythe"}


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
            half = float(scipy.stats.norm.isf(0.025)) * math.sqrt(va + vb)
            zl, zh = cm.zconfint_diff(alpha=0.05, usevar="unequal")
            assert close(zl, diff - half) and close(zh, diff + half), case
            out.update(statistic=stat, p_value=p, diff=diff,
                       ci_low=diff - half, ci_high=diff + half)
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
        # Per-group scale c_g = N*_g/Σw_g (review WS-01): W_g = c_g·Σw_g
        # = N*_g, N* = Σ_g N*_g, grand mean Σ N*_g·m_g / N*. Under
        # frequency c_g = 1, so this is the expansion's one-way ANOVA.
        gs = [Sample([r[0] for r in xr if r[3] == g], [r[5] for r in xr if r[3] == g], prob)
              for g in K_LEVELS]
        nstar = sum(sg.nstar for sg in gs)
        grand = sum(sg.nstar * sg.mean for sg in gs) / nstar
        ssb = ssw = 0.0
        for g, sg in zip(K_LEVELS, gs):
            ssb += sg.nstar * (sg.mean - grand) ** 2
            ssw += sg.c * sg.m2
            out[f"group_means[{g}]"] = sg.mean
            out[f"n[{g}]"] = sg.n
            out[f"sum_weights[{g}]"] = sg.sw
            if prob:
                out[f"n_eff[{g}]"] = kish(sg.w)
        k = len(K_LEVELS)
        dfw = nstar - k
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
        assert sx.n >= 4 and sx.nstar > 3, case
        zh = float(scipy.stats.norm.isf(0.025)) / math.sqrt(sx.nstar - 3)
        out.update(ci_low=math.tanh(math.atanh(r) - zh), ci_high=math.tanh(math.atanh(r) + zh))
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
        diff = phat[0] - phat[1]
        half = float(scipy.stats.norm.isf(0.025)) * math.sqrt(
            phat[0] * (1 - phat[0]) / ns[0] + phat[1] * (1 - phat[1]) / ns[1])
        wl, wh = confint_proportions_2indep(mass[0], ns[0], mass[1], ns[1], method="wald", compare="diff")
        assert close(wl, diff - half) and close(wh, diff + half), case
        out.update(ci_low=diff - half, ci_high=diff + half)
        out.update(statistic=z, p_value=p, pooled=pooled, diff=phat[0] - phat[1],
                   **{"effect_size.cohens_h": 2 * math.asin(math.sqrt(phat[0]))
                      - 2 * math.asin(math.sqrt(phat[1]))})
        return out
    if case in FREQUENCY_ONLY:
        assert not prob, case
        if case in ("fisher", "ks", "brown_forsythe"):
            return exact_closed_form(case, rows, xr)
        return rank_closed_form(case, xr)
    raise ValueError(case)


def rank_closed_form(case, xr):
    """A frequency-weighted rank test = scipy's unweighted test on the
    np.repeat-expanded rows (raw n / Σw reported per Pulse's details).
    R on the same expansion must agree (test_cases); scipy's Kendall p
    omits the continuity correction Pulse and R apply, so only tau is
    cross-checked there."""
    out = {}
    if case in ("mann_whitney", "kruskal"):
        col, levels = (2, H_LEVELS) if case == "mann_whitney" else (3, K_LEVELS)
        groups = []
        for g in levels:
            gx = np.array([r[0] for r in xr if r[col] == g])
            gw = np.array([r[5] for r in xr if r[col] == g])
            groups.append(np.repeat(gx, gw.astype(int)))
            out[f"n[{g}]"] = len(gx)
            out[f"sum_weights[{g}]"] = float(gw.sum())
        if case == "mann_whitney":
            res = scipy.stats.mannwhitneyu(groups[0], groups[1], use_continuity=True,
                                           alternative="two-sided", method="asymptotic")
            na, nb = len(groups[0]), len(groups[1])
            ua = float(res.statistic)
            out.update(statistic=min(ua, na * nb - ua), p_value=float(res.pvalue), u_a=ua,
                       u_min=min(ua, na * nb - ua),
                       **{"effect_size.rank_biserial": 2 * ua / (na * nb) - 1})
        else:
            res = scipy.stats.kruskal(*groups)
            ranks = scipy.stats.rankdata(np.concatenate(groups))
            off = 0
            for g, gx in zip(levels, groups):
                out[f"rank_sums[{g}]"] = float(ranks[off:off + len(gx)].sum())
                off += len(gx)
            n = sum(len(g) for g in groups)
            out.update(statistic=float(res.statistic), df=len(levels) - 1, p_value=float(res.pvalue),
                       **{"effect_size.epsilon_squared": float(res.statistic) / (n - 1)})
        return out
    x = np.array([r[0] for r in xr])
    y = np.array([r[1] for r in xr])
    w = np.array([r[5] for r in xr])
    if case == "wilcoxon_sr":
        nz = (x - y) != 0
        d = np.repeat((x - y)[nz], w[nz].astype(int))
        res = scipy.stats.wilcoxon(d, zero_method="wilcox", correction=True, method="asymptotic")
        r = scipy.stats.rankdata(np.abs(d))
        wp, wm = float(r[d > 0].sum()), float(r[d < 0].sum())
        out.update(statistic=min(wp, wm), p_value=float(res.pvalue), w_plus=wp, w_minus=wm,
                   n=int(nz.sum()), sum_weights=float(w[nz].sum()),
                   **{"effect_size.rank_biserial": (wp - wm) / (wp + wm)})
        assert close(res.statistic, min(wp, wm)), case
        return out
    ex, ey = np.repeat(x, w.astype(int)), np.repeat(y, w.astype(int))
    n = len(ex)
    out.update(n=len(x), sum_weights=float(w.sum()))
    if case == "spearman":
        res = scipy.stats.spearmanr(ex, ey)
        rho = float(res.statistic)
        out.update(statistic=rho, p_value=float(res.pvalue), df=n - 2,
                   t=rho * math.sqrt((n - 2) / (1 - rho * rho)))
        return out
    if case == "kendall":
        res = scipy.stats.kendalltau(ex, ey, variant="b", method="asymptotic")
        out.update(statistic=float(res.statistic))
        return out
    raise ValueError(case)


def exact_closed_form(case, rows, xr):
    """Fisher / KS / Brown-Forsythe on the np.repeat-expanded rows (raw
    n / Σw reported per Pulse's details). R on the same expansion must
    agree (test_cases).

    - fisher: scipy fisher_exact's two-sided p (tables no more likely
      than observed) on the Σw table; the statistic is the SAMPLE odds
      ratio ad/bc in first-seen row / column order, as Pulse reports it
      (R's estimate is the conditional MLE, so only R's p is compared).
    - ks: scipy ks_2samp's D (ties handled at value boundaries, as R);
      Pulse's p is the Stephens-corrected Kolmogorov asymptotic,
      Q_KS((√en + 0.12 + 0.11/√en)·D) with en = n₁n₂/(n₁+n₂) on the
      expanded sizes — scipy.special.kolmogorov — not R's uncorrected
      asymptotic, so R pins D only.
    - brown_forsythe: scipy levene(center="median") on the expansion =
      one-way ANOVA on |x − group median|."""
    out = {}
    if case == "fisher":
        fr = [r for r in rows if r[4] != FISHER_OUT]
        rord, cord, cells = [], [], {}
        for r in fr:
            if r[2] not in rord:
                rord.append(r[2])
            if r[4] not in cord:
                cord.append(r[4])
            cells[(r[2], r[4])] = cells.get((r[2], r[4]), 0) + int(r[5])
        assert len(rord) == 2 and len(cord) == 2, (rord, cord)
        (a, b), (c, d) = [[cells.get((ri, ci), 0) for ci in cord] for ri in rord]
        assert b > 0 and c > 0 and a > 0 and d > 0, "fixture must avoid a zero cell (U36 #205 OR)"
        res = scipy.stats.fisher_exact([[a, b], [c, d]], alternative="two-sided")
        orr = a * d / (b * c)
        out.update(statistic=orr, odds_ratio=orr, p_value=float(res.pvalue),
                   n=len(fr), sum_weights=float(a + b + c + d))
        return out
    if case == "ks":
        groups, sizes = [], []
        for g in H_LEVELS:
            gx = np.array([r[0] for r in xr if r[2] == g])
            gw = np.array([r[5] for r in xr if r[2] == g])
            groups.append(np.repeat(gx, gw.astype(int)))
            out[f"n[{g}]"] = len(gx)
            out[f"sum_weights[{g}]"] = float(gw.sum())
            sizes.append(float(gw.sum()))
        D = float(scipy.stats.ks_2samp(groups[0], groups[1], method="asymp").statistic)
        en = math.sqrt(sizes[0] * sizes[1] / (sizes[0] + sizes[1]))
        out.update(statistic=D, p_value=float(scipy.special.kolmogorov((en + 0.12 + 0.11 / en) * D)))
        return out
    if case == "brown_forsythe":
        groups, meds, devs = [], [], []
        for g in K_LEVELS:
            gx = np.array([r[0] for r in xr if r[3] == g])
            gw = np.array([r[5] for r in xr if r[3] == g])
            e = np.repeat(gx, gw.astype(int))
            groups.append(e)
            m = float(np.median(e))
            meds.append(m)
            devs.append(np.abs(e - m))
            out[f"n[{g}]"] = len(gx)
            out[f"sum_weights[{g}]"] = float(gw.sum())
            out[f"group_medians[{g}]"] = m
            out[f"abs_dev_means[{g}]"] = float(devs[-1].mean())
        res = scipy.stats.levene(*groups, center="median")
        alld = np.concatenate(devs)
        grand = float(alld.mean())
        ssb = float(sum(len(z) * (z.mean() - grand) ** 2 for z in devs))
        ssw = float(sum(((z - z.mean()) ** 2).sum() for z in devs))
        k, n = len(K_LEVELS), len(alld)
        F = (ssb / (k - 1)) / (ssw / (n - k))
        assert close(F, res.statistic), (F, res.statistic)
        out.update(statistic=F, df=k - 1, df_between=k - 1, df_within=n - k,
                   p_value=float(res.pvalue), ss_between=ssb, ss_within=ssw)
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
    "z": ("z", {"statistic": "statistic", "p_value": "p_value",
                "ci_low": "ci_low", "ci_high": "ci_high"}),
    "paired": ("paired", {"statistic": "statistic", "df": "df", "p_value": "p_value",
                          "mean_diff": "mean_diff", "ci_low": "ci_low", "ci_high": "ci_high"}),
    "anova_f": ("anova_f", {"statistic": "statistic", "df_between": "df_between",
                            "df_within": "df_within", "p_value": "p_value",
                            "ss_between": "ss_between", "ss_within": "ss_within",
                            "mean_p": "group_means[p]", "mean_q": "group_means[q]",
                            "mean_r": "group_means[r]"}),
    "anova_welch": ("anova_welch", {"statistic": "statistic", "df_between": "df_between",
                                    "df_within": "df_within", "p_value": "p_value"}),
    "pearson": ("pearson", {"r": "statistic", "t": "t", "df": "df", "p_value": "p_value",
                            "ci_low": "ci_low", "ci_high": "ci_high"}),
    "chisq": ("chisq", {"statistic": "statistic", "df": "df", "p_value": "p_value",
                        "expected_min": "expected_min", "cramers_v": "effect_size.cramers_v"}),
    "prop_z": ("prop_z", {"statistic": "statistic", "p_value": "p_value",
                          "proportion_a": "proportion[a]", "proportion_b": "proportion[b]",
                          "pooled": "pooled", "ci_low": "ci_low", "ci_high": "ci_high"}),
}
R_FIGURES.update({
    "mann_whitney": ("mann_whitney", {"u_min": "statistic", "p_value": "p_value", "u_a": "u_a",
                                      "rank_biserial": "effect_size.rank_biserial"}),
    "wilcoxon_sr": ("wilcoxon_sr", {"statistic": "statistic", "p_value": "p_value",
                                    "w_plus": "w_plus", "w_minus": "w_minus",
                                    "rank_biserial": "effect_size.rank_biserial"}),
    "kruskal": ("kruskal", {"statistic": "statistic", "df": "df", "p_value": "p_value",
                            "rank_sum_p": "rank_sums[p]", "rank_sum_q": "rank_sums[q]",
                            "rank_sum_r": "rank_sums[r]"}),
    "spearman": ("spearman", {"rho": "statistic", "p_value": "p_value", "t": "t", "df": "df"}),
    # scipy's Kendall p has no continuity correction: z / p are R's only.
    "kendall": ("kendall", {"tau": "statistic", "z": "z", "p_value": "p_value"}),
    # R's fisher.test estimate is the conditional MLE (not Pulse's
    # sample OR) and its ks.test p is the uncorrected asymptotic: R pins
    # Fisher's p and KS's D; Brown-Forsythe is car::leveneTest's own
    # anova(lm(|x − median| ~ k)) in base R.
    "fisher": ("fisher", {"p_value": "p_value"}),
    "ks": ("ks", {"statistic": "statistic"}),
    "brown_forsythe": ("brown_forsythe", {"statistic": "statistic", "df_between": "df",
                                          "df_within": "df_within", "p_value": "p_value",
                                          "ss_between": "ss_between", "ss_within": "ss_within",
                                          "median_p": "group_medians[p]", "median_q": "group_medians[q]",
                                          "median_r": "group_medians[r]"}),
})
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
            if prob and case in FREQUENCY_ONLY:
                continue
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
                    if fname in figs:
                        assert close(figs[fname], rv, 1e-9), (case, fname, figs[fname], rv)
                    else:
                        assert case == "kendall", (case, fname)  # R-only z / p
                    figs[fname] = rv
                src = f"{RTEST[0]} stats on rep()-expanded rows (test_reference.R)"
            out.append(dict(weight=wname, kind=kind, name=case, spec=spec, figs=figs, src=src))
    return out


# --- weighted inferential overlays (weighting-inferential E3-S3) --------
#
# One reference case per (host, weight configuration) over the same
# TEST_ROWS fixture. Frequency: stock R on the rep()-expanded rows
# (test_reference.R, "ov_*" cases, which print the Go figure keys
# directly). Probability: the closed form of the one formula rule on the
# overlay's legs — a cell / slot table scaled by c = Kish n_eff / Σw of
# the rows it covers (χ² matrix: the whole table; row / column: that
# row / column; CHISQ_VS_REF: the target table; proportions: the row
# base), a mean leg read as Sample(w*) with N* = n_eff. The same closed
# form must reproduce R on the frequency configuration (1e-9) before a
# row is written. Figure keys: "<layer>/<path>" — a wire path from the
# layer root, "cell[<row>|<col>]" a matrix cell by its comma-joined axis
# keys ("#i" an element of a vector cell), "entry[<key>]" a series entry.

OV_SUB = "y > 4"
OV_SUB2 = "y < 6"
OV_SERIES_PARAMS = {"variance_target": 2.5, "variance_ref": 1.75,
                    "sample_size_target": 40, "sample_size_ref": 55}


def ov_xt(rows, cols, cell, extra=None):
    x = {"rows": [{"type": "GROUP_CATEGORY", "field": rows}],
         "columns": [{"type": "GROUP_CATEGORY", "field": cols}],
         "cell": cell, "margins": {"rows": True, "columns": True, "grand": True}}
    if extra:
        x.update(extra)
    return x


OV_COUNT = {"type": "AGG_COUNT", "field": "o", "label": "cell"}
OV_WELFORD = {"type": "AGG_WELFORD", "field": "x", "label": "cell"}


def ov_slot(label, filt, body):
    s = {"label": label, "cohort": {"filename": "ref_tests.pulse"}}
    if filt:
        s["filterers"] = [{"type": "FILTER_EXPRESSION", "expression": filt}]
    s.update(body)
    return s


def ov_request(case, kind):
    """The case's request fragment (Process) or ComposedRequest."""
    if case == "ov_chisq":
        return {"crosstab": ov_xt("h", "o", OV_COUNT), "overlays": [
            {"name": "m", "kind": "OVERLAY_CHISQ_MATRIX", "scope": "matrix"},
            {"name": "r", "kind": "OVERLAY_CHISQ_ROW", "scope": "row"},
            {"name": "c", "kind": "OVERLAY_CHISQ_COL", "scope": "column"}]}
    if case == "ov_fisher":
        return {"crosstab": ov_xt("h", "o", OV_COUNT), "overlays": [
            {"name": "f", "kind": "OVERLAY_FISHER_EXACT_CELL", "scope": "cell"}]}
    if case == "ov_pairwise_welch":
        return {"crosstab": ov_xt("k", "h", OV_WELFORD), "overlays": [
            {"name": "pw", "kind": "OVERLAY_PAIRWISE_WELCH_T", "scope": "column"}]}
    if case == "ov_pairwise_prop":
        return {"attributes": [{"type": "ATTR_FORMULA", "field": "o", "label": "yes",
                                "expression": 'o == "yes" ? 100 : 0'}],
                "crosstab": ov_xt("h", "k", {"type": "AGG_AVERAGE", "field": "yes", "label": "cell"}),
                "overlays": [{"name": "pz", "kind": "OVERLAY_PAIRWISE_PROP_Z", "scope": "column"}]}
    if case == "ov_pairwise_weighted_z":
        basis = "kish" if kind == "probability" else "weights"
        return {"crosstab": ov_xt("k", "h", {"type": "AGG_AVERAGE", "field": "y", "label": "cell"}),
                "overlays": [{"name": "wz", "kind": "OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z",
                              "scope": "column", "params": {"n_basis": basis}}]}
    if case == "ov_compose_means":
        body = {"crosstab": ov_xt("k", "h", OV_WELFORD)}
        return {"requests": [ov_slot("total", None, body), ov_slot("sub", OV_SUB, body)], "overlays": [
            {"name": "t", "kind": "OVERLAY_T_CELL", "scope": "cell", "reference": "total", "targets": ["sub"]},
            {"name": "z", "kind": "OVERLAY_Z_CELL", "scope": "cell", "reference": "total", "targets": ["sub"]}]}
    if case == "ov_compose_series":
        body = {"groups": [{"type": "GROUP_CATEGORY", "field": "k"}],
                "aggregations": [{"type": "AGG_AVERAGE", "field": "x", "label": "cell"}]}
        return {"requests": [ov_slot("total", None, body), ov_slot("sub", OV_SUB, body)], "overlays": [
            {"name": "t", "kind": "OVERLAY_T_VS_REF", "scope": "group", "reference": "total",
             "targets": ["sub"], "params": OV_SERIES_PARAMS},
            {"name": "z", "kind": "OVERLAY_Z_VS_REF", "scope": "group", "reference": "total",
             "targets": ["sub"], "params": OV_SERIES_PARAMS}]}
    if case == "ov_compose_props":
        body = {"crosstab": ov_xt("h", "o", OV_COUNT)}
        return {"requests": [ov_slot("total", None, body), ov_slot("sub", OV_SUB, body),
                             ov_slot("sub2", OV_SUB2, body)], "overlays": [
            {"name": "cv", "kind": "OVERLAY_CHISQ_VS_REF", "scope": "matrix", "reference": "total", "targets": ["sub"]},
            {"name": "pc", "kind": "OVERLAY_PROP_Z_CELL", "scope": "cell", "reference": "total", "targets": ["sub"]},
            {"name": "pp", "kind": "OVERLAY_PROP_Z_PANEL", "scope": "cell", "reference": "total",
             "targets": ["sub", "sub2"]}]}
    raise ValueError(case)


OV_CASES = ["ov_chisq", "ov_fisher", "ov_pairwise_welch", "ov_pairwise_prop",
            "ov_pairwise_weighted_z", "ov_compose_means", "ov_compose_series", "ov_compose_props"]
OV_FREQUENCY_ONLY = {"ov_fisher"}


def ov_table(rows):
    """h × o Σw and Σw² tables (every row counts: o is never null)."""
    T = np.zeros((len(H_LEVELS), len(O_LEVELS)))
    W2 = np.zeros_like(T)
    for r in rows:
        i, j = H_LEVELS.index(r[2]), O_LEVELS.index(r[4])
        T[i, j] += r[5]
        W2[i, j] += r[5] * r[5]
    return T, W2


def ov_scale(sw, sww, prob):
    """c = N*/Σw: Kish n_eff / Σw under probability, else 1."""
    return (sw * sw / sww) / sw if prob else 1.0


def pearson(obs, exp):
    return float(sum((o - e) ** 2 / e for o, e in zip(obs, exp) if e > 0))


def two_prop_p(x1, n1, x2, n2):
    pooled = (x1 + x2) / (n1 + n2)
    se = math.sqrt(pooled * (1 - pooled) * (1 / n1 + 1 / n2))
    z = (x1 / n1 - x2 / n2) / se
    return float(2 * scipy.stats.norm.sf(abs(z)))


def welch_p(a, b, normal=False):
    va, vb = a.var / a.nstar, b.var / b.nstar
    t = (a.mean - b.mean) / math.sqrt(va + vb)
    if normal:
        return float(2 * scipy.stats.norm.sf(abs(t)))
    df = (va + vb) ** 2 / (va * va / (a.nstar - 1) + vb * vb / (b.nstar - 1))
    return t_two_sided(t, df)


def ov_filter(rows, expr):
    if expr == OV_SUB:
        return [r for r in rows if r[1] > 4]
    if expr == OV_SUB2:
        return [r for r in rows if r[1] < 6]
    raise ValueError(expr)


def ov_cell_sample(rows, field, prob, **match):
    idx = {"x": 0, "y": 1}[field]
    sel = [r for r in rows if r[idx] is not NULL and all(r[{"h": 2, "k": 3}[k]] == v for k, v in match.items())]
    return Sample([r[idx] for r in sel], [r[5] for r in sel], prob)


def ov_closed_form(case, rows, prob):
    """{figure key: value}, expected overlay warning codes."""
    rows = [r for r in rows if r[5] > 0]
    out, warn = {}, set()
    if case in ("ov_chisq", "ov_fisher"):
        T, W2 = ov_table(rows)
        N, R, C = T.sum(), T.sum(1), T.sum(0)
        if case == "ov_fisher":
            for i, h in enumerate(H_LEVELS):
                for j, o in enumerate(O_LEVELS):
                    a = T[i, j]
                    tb = [[a, R[i] - a], [C[j] - a, N - R[i] - C[j] + a]]
                    out[f"0/cell[{h}|{o}]"] = float(scipy.stats.fisher_exact(tb).pvalue)
                    ex = [R[i] * C[j] / N, R[i] * (N - C[j]) / N, (N - R[i]) * C[j] / N,
                          (N - R[i]) * (N - C[j]) / N]
                    if min(ex) < 1 or sum(e < 5 for e in ex) >= 0.2 * 4:
                        warn.add("PULSE_OVERLAY_EXPECTED_LOW")
            out["0/summary.parameters.sum_weights"] = float(N)
            return out, warn
        c = ov_scale(N, W2.sum(), prob)
        Ts = c * T
        E = np.outer(Ts.sum(1), Ts.sum(0)) / Ts.sum()
        stat = pearson(Ts.ravel(), E.ravel())
        res = scipy.stats.chi2_contingency(Ts, correction=False)
        assert close(res.statistic, stat), case
        if E.min() < 5:
            warn.add("PULSE_OVERLAY_EXPECTED_LOW")
        out.update({"0/summary.statistic": stat, "0/summary.p_value": float(scipy.stats.chi2.sf(stat, 2)),
                    "0/summary.parameters.df": 2.0, "0/summary.parameters.sum_weights": float(N)})
        if prob:
            out["0/summary.parameters.n_eff"] = float(N * N / W2.sum())
        for i, h in enumerate(H_LEVELS):
            ci = ov_scale(R[i], W2[i].sum(), prob)
            exp = [ci * R[i] * C[j] / N for j in range(len(O_LEVELS))]
            st = pearson([ci * v for v in T[i]], exp)
            if min(exp) < 5:
                warn.add("PULSE_OVERLAY_EXPECTED_LOW")
            out[f"1/entry[{h}].summary.statistic"] = st
            out[f"1/entry[{h}].summary.p_value"] = float(scipy.stats.chi2.sf(st, len(O_LEVELS) - 1))
            out[f"1/entry[{h}].summary.parameters.sum_weights"] = float(R[i])
            if prob:
                out[f"1/entry[{h}].summary.parameters.n_eff"] = float(R[i] ** 2 / W2[i].sum())
        for j, o in enumerate(O_LEVELS):
            cj = ov_scale(C[j], W2[:, j].sum(), prob)
            exp = [cj * C[j] * R[i] / N for i in range(len(H_LEVELS))]
            st = pearson([cj * v for v in T[:, j]], exp)
            if min(exp) < 5:
                warn.add("PULSE_OVERLAY_EXPECTED_LOW")
            out[f"2/entry[{o}].summary.statistic"] = st
            out[f"2/entry[{o}].summary.p_value"] = float(scipy.stats.chi2.sf(st, len(H_LEVELS) - 1))
        return out, warn
    if case == "ov_pairwise_welch":
        for k in K_LEVELS:
            a = ov_cell_sample(rows, "x", prob, k=k, h="a")
            b = ov_cell_sample(rows, "x", prob, k=k, h="b")
            out[f"0/cell[{k}|a,b]"] = welch_p(a, b)
        return out, warn
    if case == "ov_pairwise_weighted_z":
        for k in K_LEVELS:
            a = ov_cell_sample(rows, "y", prob, k=k, h="a")
            b = ov_cell_sample(rows, "y", prob, k=k, h="b")
            out[f"0/cell[{k}|a,b]"] = welch_p(a, b, normal=True)
        return out, warn
    if case == "ov_pairwise_prop":
        allw = [r[5] for r in rows]
        for h in H_LEVELS:
            legs = {}
            for k in K_LEVELS:
                ws = [r[5] for r in rows if r[2] == h and r[3] == k]
                yes = sum(r[5] for r in rows if r[2] == h and r[3] == k and r[4] == "yes")
                sw = float(np.sum(ws))
                n = kish(ws) if prob else sw
                legs[k] = (yes / sw, n)
            for k1, k2 in [("p", "q"), ("p", "r"), ("q", "r")]:
                (p1, n1), (p2, n2) = legs[k1], legs[k2]
                out[f"0/cell[{h}|{k1},{k2}]"] = two_prop_p(p1 * n1, n1, p2 * n2, n2)
        out["0/summary.parameters.sum_weights"] = float(np.sum(allw))
        if prob:
            out["0/summary.parameters.n_eff"] = kish(allw)
        return out, warn
    if case == "ov_compose_means":
        sub = ov_filter(rows, OV_SUB)
        for k in K_LEVELS:
            for h in H_LEVELS:
                a = ov_cell_sample(sub, "x", prob, k=k, h=h)
                b = ov_cell_sample(rows, "x", prob, k=k, h=h)
                out[f"0/cell[{k}|{h}]"] = welch_p(a, b)
                out[f"1/cell[{k}|{h}]"] = welch_p(a, b, normal=True)
        return out, warn
    if case == "ov_compose_series":
        sub = ov_filter(rows, OV_SUB)
        P = OV_SERIES_PARAMS
        vt, vr = P["variance_target"] / P["sample_size_target"], P["variance_ref"] / P["sample_size_ref"]
        df = (vt + vr) ** 2 / (vt * vt / (P["sample_size_target"] - 1) + vr * vr / (P["sample_size_ref"] - 1))
        for k in K_LEVELS:
            # The weight reaches these kinds only through the slots'
            # weighted means (Σw·x / Σw, kind-free).
            d = ov_cell_sample(sub, "x", prob, k=k).mean - ov_cell_sample(rows, "x", prob, k=k).mean
            t = d / math.sqrt(vt + vr)
            out[f"0/entry[k={k}].summary.statistic"] = t_two_sided(t, df)
            out[f"1/entry[k={k}].summary.statistic"] = float(2 * scipy.stats.norm.sf(abs(t)))
        return out, warn
    if case == "ov_compose_props":
        slots = [rows, ov_filter(rows, OV_SUB), ov_filter(rows, OV_SUB2)]
        tabs = [ov_table(s) for s in slots]
        # CHISQ_VS_REF: the target (sub) table scaled to its Kish n_eff;
        # the reference is a distribution.
        (T, W2), (Rf, _) = tabs[1], tabs[0]
        c = ov_scale(T.sum(), W2.sum(), prob)
        Ts = c * T
        exp = (Rf * (Ts.sum() / Rf.sum())).ravel()
        st = pearson(Ts.ravel(), exp)
        dfv = float(sum(e > 0 for e in exp) - 1)
        if min(exp) < 5:
            warn.add("PULSE_OVERLAY_EXPECTED_LOW")
        out.update({"0/summary.statistic": st, "0/summary.p_value": float(scipy.stats.chi2.sf(st, dfv)),
                    "0/summary.parameters.df": dfv, "0/summary.parameters.sum_weights": float(T.sum())})
        if prob:
            out["0/summary.parameters.n_eff"] = float(T.sum() ** 2 / W2.sum())

        def leg(s, i, j):
            Tt, Ww = tabs[s]
            ci = ov_scale(Tt[i].sum(), Ww[i].sum(), prob)
            return ci * Tt[i, j], ci * Tt[i].sum()

        for i, h in enumerate(H_LEVELS):
            for j, o in enumerate(O_LEVELS):
                out[f"1/cell[{h}|{o}]"] = two_prop_p(*leg(1, i, j), *leg(0, i, j))
                k = 0
                for u in range(3):
                    for v in range(u + 1, 3):
                        out[f"2/cell[{h}|{o}]#{k}"] = two_prop_p(*leg(u, i, j), *leg(v, i, j))
                        k += 1
        return out, warn
    raise ValueError(case)


def overlay_cases():
    out = []
    for wname, kind in CONFIGS:
        ws = TEST_WEIGHTS[wname]
        prob = kind == "probability"
        good = [(r[0], r[1], r[2], r[3], r[4], ws[i]) for i, r in enumerate(TEST_ROWS) if valid(ws[i], kind)]
        rfigs = None
        if not prob:
            _, rfigs = r_test_reference([g[:5] + (int(g[5]),) for g in good])
        for case in OV_CASES:
            if prob and case in OV_FREQUENCY_ONLY:
                continue
            figs, warn = ov_closed_form(case, good, prob)
            if prob:
                src = ("closed form on w* = w·n_eff/Σw per leg / table (scipy " + scipy.__version__ + " tails)")
            else:
                rf = rfigs[case]
                for key, rv in rf.items():
                    assert key in figs, (case, key)
                    assert close(figs[key], rv, 1e-9), (case, key, figs[key], rv)
                    figs[key] = rv
                missing = {k for k in figs if k not in rf and "parameters.sum_weights" not in k}
                assert not missing, (case, missing)
                src = f"{RTEST[0]} stats on rep()-expanded rows (test_reference.R)"
            out.append(dict(weight=wname, kind=kind, name=case, request=ov_request(case, kind),
                            figs=figs, warn=sorted(warn), src=src))
    return out


# --- weighted regressions and regression attributes (E4-S3) -------------
#
# Their own fixture REG_ROWS: (id, x1, x2, y, yb, yp, yg, w, f) — x2
# nullable; y a linear response, yb / yp / yg binomial / poisson / gamma
# targets of the same predictors; weights as the other fixtures (w
# fractional probability with a zero / negative / NaN, f integer
# frequency with zeros / negative / NaN / non-integer, p = c·f).
#
#   * REG_OLS — frequency: R lm on the rep()-expanded rows
#     (reg_reference.R). Probability: the closed form on w* (β from the
#     weighted normal equations, σ̂² = Σw*e²/(n_eff − q), t on n_eff − q
#     df), its SEs cross-checked against statsmodels GLM(Gaussian,
#     freq_weights = w*) — whose df_resid is Σw* − q = n_eff − q. NOT
#     lm(weights = w) / WLS: those read the row count as the df.
#   * REG_OLS penalised (l2 / l1 / elasticnet) — β only, from
#     scikit-learn: Ridge(alpha = λ·Σw) (Pulse adds Σw·λ to the centered
#     Gram, unstandardized); Lasso / ElasticNet(alpha = λ, l1_ratio) on
#     predictors divided by their weighted population SD, β = coef/σ —
#     Pulse's coordinate descent minimizes the same
#     (1/2Σw)·Σw(y − Xβ)² + λ(α‖β_std‖₁ + (1 − α)/2‖β_std‖²).
#     Frequency on the np.repeat expansion, unweighted; probability with
#     sample_weight = w (β is kind-free: the penalty scales by Σw). The
#     ridge β is also checked against its closed form.
#   * REG_GLM (binomial / poisson / gamma, dispersion fixed at 1) —
#     frequency: R glm on the expansion; probability: IRLS on w* in
#     numpy, cross-checked against statsmodels GLM(freq_weights = w*,
#     scale = 1). The same IRLS on f must equal R (1e-9).
#   * REG_BAYES_LINEAR — frequency-only. No stock package fits Pulse's
#     scalar-precision Normal-Inverse-Gamma prior, so the reference is
#     that conjugate posterior in closed form evaluated on the expanded
#     rows (np.repeat), scipy t quantiles for the credible intervals.
#   * ATTR_REG_FITTED / RESIDUAL / LEVERAGE — frequency: R lm on the
#     expansion (predict(); leverage = f × one copy's hatvalues());
#     probability: the closed form (β and hᵢᵢ = wᵢ(1/Σw + dxᵀM2_w⁻¹dx)
#     are invariant to a weight scale), the leverage cross-checked
#     against statsmodels OLS influence on the √w-whitened design. A row with a
#     null predictor emits 0; a zero / invalid weight keeps the row out
#     of the refit and gives it leverage 0.

REG_PREDICTORS = ["x1", "x2"]
REG_TERMS = ["(intercept)", "x1", "x2"]
REG_GLM_TARGETS = {"glm_binomial": ("binomial", "yb"), "glm_poisson": ("poisson", "yp"),
                   "glm_gamma": ("gamma", "yg")}
REG_ALPHA = {"ridge": 0.3, "lasso": 0.05, "elasticnet": 0.05}
REG_L1_RATIO = 0.5
# Coordinate descent runs to these so its β sits ~1e-10 from the optimum.
REG_CD = {"tol": 1e-13, "max_iters": 100000}
REG_GLM_CONTROL = {"tol": 1e-14, "max_iters": 100}


def _reg_spec(extra):
    return {"target": "y", "predictors": REG_PREDICTORS, **extra}


REG_SPECS = [
    ("ols", _reg_spec({"type": "REG_OLS"})),
    ("ridge", _reg_spec({"type": "REG_OLS", "penalty": "l2", "alpha": REG_ALPHA["ridge"]})),
    ("lasso", _reg_spec({"type": "REG_OLS", "penalty": "l1", "alpha": REG_ALPHA["lasso"], **REG_CD})),
    ("elasticnet", _reg_spec({"type": "REG_OLS", "penalty": "elasticnet", "alpha": REG_ALPHA["elasticnet"],
                              "l1_ratio": REG_L1_RATIO, **REG_CD})),
    *[(case, {"type": "REG_GLM", "family": fam, "target": tgt, "predictors": REG_PREDICTORS, **REG_GLM_CONTROL})
      for case, (fam, tgt) in REG_GLM_TARGETS.items()],
    ("bayes", _reg_spec({"type": "REG_BAYES_LINEAR"})),
]
REG_FREQUENCY_ONLY = {"bayes"}

# (case, attribute type, label): one per-row figure each, read back as
# an unweighted AGG_SUM per id group.
ATTR_SPECS = [("attr_fitted", "ATTR_REG_FITTED"), ("attr_residual", "ATTR_REG_RESIDUAL"),
              ("attr_leverage", "ATTR_REG_LEVERAGE")]


def build_reg_rows():
    rng = np.random.default_rng(20261005)
    rows = []
    for i in range(40):
        x1 = round(float(rng.uniform(-2.0, 2.5)), 2)
        x2 = round(float(rng.normal(0.0, 1.0)), 2)
        y = round(1.5 + 0.8 * x1 - 1.2 * x2 + float(rng.normal(0.0, 0.9)), 2)
        pb = 1.0 / (1.0 + math.exp(-(-0.3 + 0.9 * x1 - 0.7 * x2)))
        yb = float(rng.uniform() < pb)
        yp = float(rng.poisson(math.exp(0.4 + 0.3 * x1 + 0.2 * x2)))
        mu = 1.0 / (0.5 + 0.08 * x1 + 0.05 * x2)
        yg = round(float(rng.gamma(4.0, mu / 4.0)), 3)
        w = round(float(rng.uniform(0.2, 3.0)), 3)
        f = int(rng.integers(1, 5))
        rows.append([x1, x2, y, yb, yp, yg, w, f])
    rows += [
        [0.7, NULL, 2.4, 1.0, 2.0, 1.9, 1.1, 2],      # null predictor: dropped listwise
        [1.3, -0.4, 3.1, 1.0, 3.0, 2.2, 0.0, 0],      # zero weights
        [-1.1, 0.6, 0.2, 0.0, 1.0, 2.6, 0.9, 0],      # zero frequency weight only
        [2.0, 1.5, 9.0, 0.0, 9.0, 9.0, -0.5, -1],     # negative weights
        [-1.8, -1.6, -7.0, 1.0, 0.0, 0.1, NAN, NAN],  # NaN weights
        [0.4, 0.3, 2.0, 1.0, 1.0, 1.5, 1.6, 1.5],     # non-integer f
    ]
    return [(f"r{i:02d}", *r) for i, r in enumerate(rows)]


REG_ROWS = build_reg_rows()
REG_WEIGHTS = {
    "w": [r[7] for r in REG_ROWS],
    "f": [float(r[8]) for r in REG_ROWS],
    "p": [p_of(float(r[8])) for r in REG_ROWS],
}
REG_COL = {"x1": 1, "x2": 2, "y": 3, "yb": 4, "yp": 5, "yg": 6}


def reg_fit_rows(ws, kind):
    """Indices of the rows a fit uses: predictors present, weight valid
    and positive (a zero weight contributes nothing and is not in n)."""
    return [i for i, r in enumerate(REG_ROWS)
            if r[2] is not NULL and valid(ws[i], kind) and ws[i] > 0]


def reg_design(idx):
    return np.array([[1.0, REG_ROWS[i][1], REG_ROWS[i][2]] for i in idx])


def reg_floor(ws, idx, prob):
    w = np.array([ws[i] for i in idx])
    out = {"n_obs": len(idx), "sum_weights": float(w.sum())}
    if prob:
        out["n_eff"] = kish(w)
    return out


def ols_closed_form(ws, kind):
    prob = kind == "probability"
    idx = reg_fit_rows(ws, kind)
    X = reg_design(idx)
    y = np.array([REG_ROWS[i][3] for i in idx])
    w = np.array([ws[i] for i in idx])
    sw = float(w.sum())
    nstar = kish(w) if prob else sw
    wstar = w * nstar / sw
    q = X.shape[1]
    xtwx = X.T @ (wstar[:, None] * X)
    beta = np.linalg.solve(xtwx, X.T @ (wstar * y))
    e = y - X @ beta
    df = nstar - q
    rss = float(np.sum(wstar * e * e))
    ybar = float(np.sum(w * y) / sw)
    tss = float(np.sum(wstar * (y - ybar) ** 2))
    sigma2 = rss / df
    cov = sigma2 * np.linalg.inv(xtwx)
    out = {}
    for j, t in enumerate(REG_TERMS):
        se = math.sqrt(cov[j, j])
        out[f"coefficients.{t}"] = float(beta[j])
        out[f"std_errors.{t}"] = se
        out[f"p_values.{t}"] = t_two_sided(beta[j] / se, df)
    r2 = 1 - rss / tss
    out.update(r2=r2, adj_r2=1 - (1 - r2) * (nstar - 1) / df, residual_std_err=math.sqrt(sigma2))
    # statsmodels' frequency-weighted Gaussian GLM reads df_resid =
    # Σw* − q = n_eff − q: the Kish SE, independently assembled.
    sm = SmGLM(y, X, family=sm_families.Gaussian(), freq_weights=wstar).fit()
    assert close(sm.df_resid, df) and np.allclose(sm.params, beta, rtol=1e-10), kind
    for j, t in enumerate(REG_TERMS):
        assert close(sm.bse[j], out[f"std_errors.{t}"], 1e-9), (kind, t, sm.bse[j])
    out.update(reg_floor(ws, idx, prob))
    return out


def weighted_scales(X, w):
    """Weighted means and population SDs of the predictor columns."""
    mu = (w[:, None] * X).sum(0) / w.sum()
    sd = np.sqrt((w[:, None] * (X - mu) ** 2).sum(0) / w.sum())
    return mu, sd


def penalised_beta(case, ws, kind):
    """β from scikit-learn (see the section comment)."""
    idx = reg_fit_rows(ws, kind)
    X = np.array([[REG_ROWS[i][1], REG_ROWS[i][2]] for i in idx])
    y = np.array([REG_ROWS[i][3] for i in idx])
    w = np.array([ws[i] for i in idx])
    if kind == "frequency":
        reps = w.astype(int)
        X, y, w = np.repeat(X, reps, axis=0), np.repeat(y, reps), np.ones(int(reps.sum()))
        sample_weight = None
    else:
        sample_weight = w
    sw = float(w.sum())
    lam = REG_ALPHA[case]
    if case == "ridge":
        m = sklearn.linear_model.Ridge(alpha=lam * sw, fit_intercept=True, solver="cholesky", tol=1e-14)
        m.fit(X, y, sample_weight=sample_weight)
        coef, icpt = m.coef_, m.intercept_
        mu, _ = weighted_scales(X, w)
        xc = X - mu
        yc = y - float(np.sum(w * y) / sw)
        closed = np.linalg.solve(xc.T @ (w[:, None] * xc) + sw * lam * np.eye(2), xc.T @ (w * yc))
        assert np.allclose(coef, closed, rtol=1e-10, atol=0), (kind, coef, closed)
    else:
        mu, sd = weighted_scales(X, w)
        cls = sklearn.linear_model.Lasso if case == "lasso" else sklearn.linear_model.ElasticNet
        kw = {} if case == "lasso" else {"l1_ratio": REG_L1_RATIO}
        m = cls(alpha=lam, fit_intercept=True, tol=1e-15, max_iter=10_000_000, **kw)
        m.fit(X / sd, y, sample_weight=sample_weight)
        coef = m.coef_ / sd
        icpt = float(np.sum(w * y) / sw) - float(coef @ mu)
        assert close(icpt, m.intercept_, 1e-9), (case, kind, icpt, m.intercept_)
    out = {f"coefficients.{t}": float(v) for t, v in zip(REG_TERMS, [icpt, *coef])}
    return out


def glm_irls(X, y, w, family):
    """Dispersion-1 IRLS fit: (β, SE, deviance, null deviance)."""
    if family == "binomial":
        inv = lambda eta: 1 / (1 + np.exp(-eta))
        dmu = lambda eta, mu: mu * (1 - mu)
        var = lambda mu: mu * (1 - mu)
        mu0 = (w * y + 0.5) / (w + 1)
        eta0 = np.log(mu0 / (1 - mu0))

        def dev(mu):
            a = np.where(y > 0, y * np.log(np.where(y > 0, y, 1) / mu), 0.0)
            b = np.where(y < 1, (1 - y) * np.log(np.where(y < 1, 1 - y, 1) / (1 - mu)), 0.0)
            return 2 * float(np.sum(w * (a + b)))
    elif family == "poisson":
        inv = np.exp
        dmu = lambda eta, mu: mu
        var = lambda mu: mu
        eta0 = np.log(y + 0.1)

        def dev(mu):
            a = np.where(y > 0, y * np.log(np.where(y > 0, y, 1) / mu), 0.0)
            return 2 * float(np.sum(w * (a - (y - mu))))
    else:  # gamma, inverse link
        inv = lambda eta: 1 / eta
        dmu = lambda eta, mu: -mu * mu
        var = lambda mu: mu * mu
        eta0 = 1 / y

        def dev(mu):
            return 2 * float(np.sum(w * (-np.log(y / mu) + (y - mu) / mu)))
    eta = eta0
    beta = None
    prev = math.inf
    for _ in range(500):
        mu = inv(eta)
        g = dmu(eta, mu)
        z = eta + (y - mu) / g
        ww = w * g * g / var(mu)
        beta = np.linalg.solve(X.T @ (ww[:, None] * X), X.T @ (ww * z))
        eta = X @ beta
        d = dev(inv(eta))
        if abs(d - prev) <= 1e-15 * (abs(d) + 0.1):
            break
        prev = d
    mu = inv(eta)
    g = dmu(eta, mu)
    ww = w * g * g / var(mu)
    se = np.sqrt(np.diag(np.linalg.inv(X.T @ (ww[:, None] * X))))
    ybar = float(np.sum(w * y) / w.sum())
    return beta, se, dev(inv(eta)), dev(np.full_like(y, ybar))


def glm_closed_form(case, ws, kind):
    prob = kind == "probability"
    family, tgt = REG_GLM_TARGETS[case]
    idx = reg_fit_rows(ws, kind)
    X = reg_design(idx)
    y = np.array([REG_ROWS[i][REG_COL[tgt]] for i in idx])
    w = np.array([ws[i] for i in idx])
    sw = float(w.sum())
    wstar = w * ((kish(w) if prob else sw) / sw)
    beta, se, dev, null_dev = glm_irls(X, y, wstar, family)
    sm_fam = {"binomial": sm_families.Binomial(),
              "poisson": sm_families.Poisson(),
              "gamma": sm_families.Gamma(sm_families.links.InversePower())}[family]
    with warnings.catch_warnings():
        # Gamma's inverse link is R's default too; the fit stays in-domain.
        warnings.simplefilter("ignore", DomainWarning)
        sm = SmGLM(y, X, family=sm_fam, freq_weights=wstar).fit(scale=1.0, tol=1e-14, maxiter=200)
    assert np.allclose(sm.params, beta, rtol=1e-8) and np.allclose(sm.bse, se, rtol=1e-8), (case, kind)
    assert close(sm.deviance, dev, 1e-8) and close(sm.null_deviance, null_dev, 1e-8), (case, kind)
    out = {}
    for j, t in enumerate(REG_TERMS):
        out[f"coefficients.{t}"] = float(beta[j])
        out[f"std_errors.{t}"] = float(se[j])
        out[f"p_values.{t}"] = float(2 * scipy.stats.norm.sf(abs(beta[j] / se[j])))
    out.update(deviance=dev, null_deviance=null_dev, pseudo_r2=1 - dev / null_dev)
    out.update(reg_floor(ws, idx, prob))
    return out


def bayes_closed_form(ws):
    """The NIG posterior (Pulse's default prior) on the expanded rows."""
    idx = reg_fit_rows(ws, "frequency")
    reps = np.array([int(ws[i]) for i in idx])
    X = np.repeat(reg_design(idx), reps, axis=0)
    y = np.repeat(np.array([REG_ROWS[i][3] for i in idx]), reps)
    n, q = X.shape
    eps, a0, b0, level = 1e-3, 1e-3, 1e-3, 0.95
    lam = X.T @ X + eps * np.eye(q)
    mun = np.linalg.solve(lam, X.T @ y)
    an = a0 + n / 2
    bn = b0 + 0.5 * (float(y @ y) - float(mun @ lam @ mun))
    s2 = bn / an
    se = np.sqrt(s2 * np.diag(np.linalg.inv(lam)))
    tq = float(scipy.stats.t.ppf(0.5 + level / 2, 2 * an))
    e = y - X @ mun
    rss = float(e @ e)
    tss = float(np.sum((y - y.mean()) ** 2))
    r2 = 1 - rss / tss
    out = {}
    for j, t in enumerate(REG_TERMS):
        out[f"coefficients.{t}"] = float(mun[j])
        out[f"std_errors.{t}"] = float(se[j])
        out[f"credible_intervals.{t}[0]"] = float(mun[j] - tq * se[j])
        out[f"credible_intervals.{t}[1]"] = float(mun[j] + tq * se[j])
    out.update(r2=r2, adj_r2=1 - (1 - r2) * (n - 1) / (n - q), residual_std_err=math.sqrt(s2))
    out.update(reg_floor(ws, idx, False))
    return out


def attr_closed_form(ws, kind):
    """Per row id: fitted, residual, leverage (see the section comment)."""
    idx = reg_fit_rows(ws, kind)
    X = reg_design(idx)
    y = np.array([REG_ROWS[i][3] for i in idx])
    w = np.array([ws[i] for i in idx])
    beta = np.linalg.solve(X.T @ (w[:, None] * X), X.T @ (w * y))
    mu, _ = weighted_scales(X[:, 1:], w)
    xc = X[:, 1:] - mu
    m2inv = np.linalg.inv(xc.T @ (w[:, None] * xc))
    sw = float(w.sum())
    # WLS hat diagonal = the OLS hat diagonal of the √w-whitened design.
    rw = np.sqrt(w)
    infl = SmOLS(rw * y, rw[:, None] * X).fit().get_influence().hat_matrix_diag
    fit_pos = {i: k for k, i in enumerate(idx)}
    out = {"attr_fitted": {}, "attr_residual": {}, "attr_leverage": {}}
    for i, r in enumerate(REG_ROWS):
        rid = r[0]
        if r[2] is NULL:
            fv = rv = lv = 0.0
        else:
            fv = float(beta @ np.array([1.0, r[1], r[2]]))
            rv = r[3] - fv
            lv = 0.0
            if i in fit_pos:
                dx = np.array([r[1], r[2]]) - mu
                lv = ws[i] * (1 / sw + float(dx @ m2inv @ dx))
                assert close(lv, infl[fit_pos[i]], 1e-9), (kind, rid, lv, infl[fit_pos[i]])
        out["attr_fitted"][rid] = fv
        out["attr_residual"][rid] = rv
        out["attr_leverage"][rid] = lv
    return out


SCORE_SPECS = [("attr_zscore", "ATTR_ZSCORE"), ("attr_tscore", "ATTR_TSCORE")]
QUANTILE_KS = [4, 10]


def score_closed_form(ws, kind):
    """ATTR_ZSCORE / ATTR_TSCORE per row id: (y − μ_w)/σ_w with the
    weighted mean and POPULATION sd √(Σw(y − μ_w)²/Σw) over the rows whose
    weight is valid and positive; every row is scored (statsmodels
    DescrStatsW(ddof=0) cross-check). Scale-free, so kind-free."""
    idx = [i for i in range(len(REG_ROWS)) if valid(ws[i], kind) and ws[i] > 0]
    y = np.array([REG_ROWS[i][3] for i in idx])
    w = np.array([ws[i] for i in idx])
    sw = float(w.sum())
    mean = float(np.sum(w * y) / sw)
    sd = math.sqrt(float(np.sum(w * (y - mean) ** 2)) / sw)
    d0 = DescrStatsW(y, weights=w, ddof=0)
    assert close(d0.mean, mean, 1e-13) and close(d0.std, sd, 1e-13), (kind, d0.mean, mean, d0.std, sd)
    out = {"attr_zscore": {}, "attr_tscore": {}}
    for r in REG_ROWS:
        z = (r[3] - mean) / sd
        out["attr_zscore"][r[0]] = z
        out["attr_tscore"][r[0]] = z * 10 + 50
    return out


def quantile_buckets(ws, kind, k):
    """GROUP_QUANTILE on yg, exact rational transcription of Pulse's cut:
    rows sorted by value; C = the cumulative weight through a row (valid,
    positive weights only; probability weights rescaled so Σw = the
    contributing row count n); the row's bucket ⌊⌊C − 1⌋·k / W⌋ (≥ 0).
    Returns [(bucket, low, high, count)] for the non-empty buckets, in
    order. The opening row of a bucket b ≥ 1 is the order statistic at
    0-based rank ⌈b·W/k⌉ — what the external references pin."""
    vals = [r[6] for r in REG_ROWS]
    assert len(set(vals)) == len(vals), "GROUP_QUANTILE fixture column has ties"
    order = sorted(range(len(REG_ROWS)), key=lambda i: vals[i])
    contrib = [i for i in order if valid(ws[i], kind) and ws[i] > 0]
    n = len(contrib)
    total = sum(Fraction(ws[i]) for i in contrib)
    acc, cum = Fraction(0), []
    for i in order:
        if i in contrib:
            acc += Fraction(ws[i])
        cum.append(acc)
    if kind == "probability":
        cum = [c * n / total for c in cum]
        total = Fraction(n)
    for c in cum:
        # Pulse snaps a cumulative weight within 1e-9 of an integer to
        # it; an exact value that close to (but off) an integer would
        # disagree with this transcription — refuse such a fixture.
        off = abs(c - round(c))
        assert off == 0 or off > Fraction(1, 10**8), ("knife-edge cumulative weight", float(c))
    buckets = {}
    for i, c in zip(order, cum):
        b = max(0, math.floor(math.floor(c - 1) * k / total))
        buckets.setdefault(b, []).append(vals[i])
    return [(b, min(v), max(v), len(v)) for b, v in sorted(buckets.items())], n, total


def quantile_hmisc_cuts(ws, kind, k, nonempty):
    """Probability: the order statistic opening each non-empty bucket
    b ≥ 1, from Hmisc wtd.quantile(normwt=TRUE) at probs ⌈b·n/k⌉/(n − 1)
    (order 1 + (n − 1)·p is that 1-based rank + 1, so the figure is that
    order statistic up to an ulp-sized interpolation weight)."""
    vals = [r[6] for r in REG_ROWS]
    contrib = [i for i in range(len(REG_ROWS)) if valid(ws[i], kind) and ws[i] > 0]
    n = len(contrib)
    bs = [b for b in nonempty if b >= 1]
    qs = [100 * math.ceil(Fraction(b * n, k)) / (n - 1) for b in bs]
    rver, hq = hmisc_quantiles([vals[i] for i in contrib], [ws[i] for i in contrib], qs)
    HMISC[0] = HMISC[0] or rver
    return rver, dict(zip(bs, hq))


def r_reg_reference(ws):
    """Run reg_reference.R on the frequency configuration."""
    import csv
    import tempfile
    here = os.path.dirname(os.path.abspath(__file__))
    with tempfile.NamedTemporaryFile("w", suffix=".csv", delete=False, newline="") as fh:
        wr = csv.writer(fh)
        wr.writerow(["id", "x1", "x2", "y", "yb", "yp", "yg", "f"])
        for i, r in enumerate(REG_ROWS):
            copies = int(ws[i]) if valid(ws[i], "frequency") else 0
            wr.writerow([r[0], repr(r[1]), "NA" if r[2] is NULL else repr(r[2]),
                         repr(r[3]), repr(r[4]), repr(r[5]), repr(r[6]), copies])
        path = fh.name
    try:
        lines = subprocess.run(["Rscript", os.path.join(here, "reg_reference.R"), path],
                               check=True, capture_output=True, text=True).stdout.strip().split("\n")
    finally:
        os.unlink(path)
    figs = {}
    for ln in lines[1:]:
        case, fig, v = ln.split()
        figs.setdefault(case, {})[fig] = float(v)
    return lines[0].strip(), figs


RREG = [None]  # "R <ver>", set by the frequency configuration


def adopt_r(case, figs, rfigs):
    """Frequency: the closed form must equal stock R on the expansion
    (1e-9); R's figure is then the one pinned."""
    for fig, rv in rfigs.items():
        assert fig in figs, (case, fig)
        assert close(figs[fig], rv, 1e-9) or abs(figs[fig] - rv) <= 1e-12, (case, fig, figs[fig], rv)
        figs[fig] = rv


def regression_cases():
    out = []
    for wname, kind in CONFIGS:
        ws = REG_WEIGHTS[wname]
        prob = kind == "probability"
        rfigs = None
        if not prob:
            RREG[0], rfigs = r_reg_reference(ws)
        for case, spec in REG_SPECS:
            if prob and case in REG_FREQUENCY_ONLY:
                continue
            if case == "ols":
                figs = ols_closed_form(ws, kind)
                if prob:
                    src = ("closed form on w* = w·n_eff/Σw, df n_eff − q (scipy " + scipy.__version__ +
                           " t tail), SEs cross-checked with statsmodels " + statsmodels.__version__ +
                           " GLM(freq_weights = w*)")
                else:
                    adopt_r(case, figs, rfigs[case])
                    src = f"{RREG[0]} lm on rep()-expanded rows (reg_reference.R)"
            elif case in REG_ALPHA:
                figs = penalised_beta(case, ws, kind)
                figs.update(reg_floor(ws, reg_fit_rows(ws, kind), prob))
                how = "np.repeat expansion" if not prob else "sample_weight = w"
                src = f"scikit-learn {sklearn.__version__} on the {how}"
            elif case in REG_GLM_TARGETS:
                figs = glm_closed_form(case, ws, kind)
                if prob:
                    src = ("IRLS on w* = w·n_eff/Σw, dispersion 1, cross-checked with statsmodels " +
                           statsmodels.__version__ + " GLM(freq_weights = w*, scale = 1)")
                else:
                    adopt_r(case, figs, rfigs[case])
                    src = f"{RREG[0]} glm on rep()-expanded rows, summary(dispersion = 1) (reg_reference.R)"
            else:
                figs = bayes_closed_form(ws)
                src = ("NIG conjugate posterior (default prior) in closed form on the np.repeat "
                       "expansion; scipy " + scipy.__version__ + " t quantiles — no stock reference")
            out.append(dict(weight=wname, kind=kind, name=case,
                            request={"regressions": [spec]},
                            figs={f"regressions[0].{k}": v for k, v in figs.items()}, src=src))
        attrs = attr_closed_form(ws, kind)
        for case, typ in ATTR_SPECS:
            figs = attrs[case]
            if prob:
                src = ("closed form (β and hᵢᵢ invariant to a weight scale), leverage cross-checked with "
                       "statsmodels " + statsmodels.__version__ + " OLS influence on the √w-whitened design")
            else:
                adopt_r(case, figs, rfigs[case])
                src = f"{RREG[0]} lm on rep()-expanded rows: predict() / f × hatvalues() (reg_reference.R)"
            req = {"attributes": [{"type": typ, "label": "attr", "target": "y", "predictors": REG_PREDICTORS}],
                   "groups": [{"type": "GROUP_CATEGORY", "field": "id"}],
                   "aggregations": [{"type": "AGG_SUM", "field": "attr", "label": "v", "weight": None}]}
            out.append(dict(weight=wname, kind=kind, name=case, request=req,
                            figs={f"data[{rid}].v": v for rid, v in figs.items()}, src=src))
        scores = score_closed_form(ws, kind)
        for case, typ in SCORE_SPECS:
            figs = scores[case]
            if prob:
                src = ("closed form: weighted mean and population sd √(Σw(y − μ)²/Σw), cross-checked with "
                       "statsmodels " + statsmodels.__version__ + " DescrStatsW(ddof=0)")
            else:
                adopt_r(case, figs, rfigs[case])
                src = f"{RREG[0]} mean / population sd of the rep()-expanded y (reg_reference.R)"
            req = {"attributes": [{"type": typ, "field": "y", "label": "attr"}],
                   "groups": [{"type": "GROUP_CATEGORY", "field": "id"}],
                   "aggregations": [{"type": "AGG_SUM", "field": "attr", "label": "v", "weight": None}]}
            out.append(dict(weight=wname, kind=kind, name=case, request=req,
                            figs={f"data[{rid}].v": v for rid, v in figs.items()}, src=src))
        for k in QUANTILE_KS:
            case = f"group_quantile_k{k}"
            buckets, n, total = quantile_buckets(ws, kind, k)
            vals = [r[6] for r in REG_ROWS]
            if prob:
                rver, cuts = quantile_hmisc_cuts(ws, kind, k, [b for b, *_ in buckets])
                for b, low, *_ in buckets:
                    if b >= 1:
                        assert close(cuts[b], low, 1e-12), (wname, k, b, cuts[b], low)
                src = (f"{rver} wtd.quantile(normwt=TRUE) order statistic opening each bucket; "
                       "count / high: exact rational transcription of the cut")
            else:
                for b, low, *_ in buckets:
                    rv = rfigs[case][f"cut{b}"]
                    # Bucket 0 opens at the smallest VALUE, which may be a
                    # row carrying no weight (absent from the expansion).
                    assert b == 0 or rv == low, (wname, k, b, rv, low)
                src = (f"{RREG[0]} order statistic of the sorted rep()-expanded yg opening each bucket "
                       "(reg_reference.R); count / high: exact rational transcription of the cut")
            figs = {}
            for j, (b, low, high, count) in enumerate(buckets):
                pre = f"components.groupers[0].operator.buckets[{j}]"
                figs[pre + ".low"] = low
                figs[pre + ".high"] = high
                figs[pre + ".count"] = float(count)
            req = {"groups": [{"type": "GROUP_QUANTILE", "field": "yg", "interval": k}],
                   "aggregations": [{"type": "AGG_COUNT", "field": "yg", "label": "n", "weight": None}]}
            out.append(dict(weight=wname, kind=kind, name=case, request=req, figs=figs, src=src))
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


def emit(out, tout, oout, rout):
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
        req = json.dumps({"tests": [c["spec"]], **TEST_EXTRA.get(c["name"], {})}, separators=(",", ":"))
        figs = ", ".join(f'"tests[0].{figure_path(k)}": {gofloat(v)}' for k, v in sorted(c["figs"].items()))
        w(f'\t{{weight: "{c["weight"]}", kind: "{c["kind"]}", name: "{c["name"]}", '
          f'request: `{req}`, figures: map[string]float64{{{figs}}}, source: "{c["src"]}"}},')
    w("}")
    w("")
    w("// weightRefOverlayCases: one host per case, figures keyed by layer / path.")
    w("var weightRefOverlayCases = []weightRefOverlayCase{")
    for c in oout:
        req = json.dumps(c["request"], separators=(",", ":"))
        figs = ", ".join(f'"{k}": {gofloat(v)}' for k, v in sorted(c["figs"].items()))
        warn = ", ".join(f'"{x}"' for x in c["warn"])
        w(f'\t{{weight: "{c["weight"]}", kind: "{c["kind"]}", name: "{c["name"]}", '
          f'request: `{req}`, figures: map[string]float64{{{figs}}}, warnings: []string{{{warn}}}, '
          f'source: "{c["src"]}"}},')
    w("}")
    w("")
    w(f"// weightRefRegProvenance records the regression toolchain.")
    w(f'const weightRefRegProvenance = "{RREG[0]} (stats); python {sys.version.split()[0]}, numpy {np.__version__}, '
      f'scipy {scipy.__version__}, statsmodels {statsmodels.__version__}, scikit-learn {sklearn.__version__}"')
    w("")
    w("// weightRefRegRows is the regression fixture.")
    w("var weightRefRegRows = []weightRefRegRow{")
    for r in REG_ROWS:
        rid, x1, x2, y, yb, yp, yg, wv, f = r
        f = float(f)
        fields = [
            f'id: "{rid}"',
            f"x1: {gofloat(x1)}",
            f"x2: {gofloat(x2) if x2 is not NULL else '0'}",
            f"x2Null: {'true' if x2 is NULL else 'false'}",
            f"y: {gofloat(y)}, yb: {gofloat(yb)}, yp: {gofloat(yp)}, yg: {gofloat(yg)}",
            f"w: {gofloat(wv)}",
            f"f: {gofloat(f)}",
            f"p: {gofloat(p_of(f))}",
        ]
        w("\t{" + ", ".join(fields) + "},")
    w("}")
    w("")
    w("// weightRefRegCases: one regression or regression-attribute slot per case,")
    w("// figures keyed by wire path (data[<id>] the row of that id group).")
    w("var weightRefRegCases = []weightRefInferCase{")
    for c in rout:
        req = json.dumps(c["request"], separators=(",", ":"), ensure_ascii=False)
        figs = ", ".join(f'"{k}": {gofloat(v)}' for k, v in sorted(c["figs"].items()))
        w(f'\t{{weight: "{c["weight"]}", kind: "{c["kind"]}", name: "{c["name"]}", '
          f'request: `{req}`, figures: map[string]float64{{{figs}}}, source: "{c["src"]}"}},')
    w("}")
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    here = os.path.dirname(os.path.abspath(__file__))
    dst = os.path.join(here, "..", "..", "weight_reference_values_test.go")
    with open(dst, "w") as fh:
        fh.write(emit(cases(), test_cases(), overlay_cases(), regression_cases()))
    print("wrote", os.path.normpath(dst))
