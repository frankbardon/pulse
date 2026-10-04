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

Regenerate (uv resolves the pinned versions above, PEP 723):

    cd internal/service/testdata/weight_reference
    uv run --no-project gen_weight_reference.py
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
  * AGG_MEDIAN / AGG_PERCENTILE, kind probability — no library ships
    Pulse's definition (probability weights rescaled to Σw = n, then
    expanded-index type 7 with x_(k) = the smallest value whose
    normalized cumulative weight EXCEEDS k). The figure is an exact
    rational (fractions.Fraction) transcription of that documented
    definition, cross-checked against numpy type 7 on the expansion
    wherever the normalized weights are integers. Hmisc
    wtd.quantile(normwt=TRUE) shares the normalization but takes the
    smallest value whose cumulative weight is >= the 1-based rank, so
    it differs on fractional cumulative weights (see README.md).
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

Row validity mirrors weighting.Classify: a weight that is negative,
NaN/Inf, or non-integer under kind frequency excludes its row (counted
in n_weight_invalid when the value is present); a zero weight is valid
and contributes nothing.
"""

import math
import os
import sys
from decimal import Decimal, getcontext
from fractions import Fraction

import numpy as np
import scipy
import scipy.stats
import statsmodels
from statsmodels.stats.weightstats import DescrStatsW

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


def pulse_normalized_quantile(x, w, q, guard=True):
    """Exact rational transcription of the documented probability rule."""
    pairs = sorted((Fraction(xi), Fraction(wi)) for xi, wi in zip(x, w) if wi > 0)
    n = len(pairs)
    total = sum(wi for _, wi in pairs)
    cum, acc = [], Fraction(0)
    for _, wi in pairs:
        acc += wi
        cum.append(acc * n / total)

    def at(k):
        for (xi, _), c in zip(pairs, cum):
            if c > k:
                return xi
        return pairs[-1][0]

    # Knife-edge guard: a normalized cumulative weight sitting on an
    # integer rank makes "exceeds k" turn on the last ulp of the float
    # sum — a fixture property, not a definition. Refuse such fixtures.
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
            qsrc = "exact Fraction transcription of the normalized expanded-index type 7"
            med = pulse_normalized_quantile(x, w, 50)
            pct = {q: pulse_normalized_quantile(x, w, q) for q in PERCENTILES}
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


def gofloat(v):
    if isinstance(v, float) and math.isnan(v):
        return "math.NaN()"
    r = repr(float(v))
    return r


def emit(out):
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
      f'scipy {scipy.__version__}, statsmodels {statsmodels.__version__}"')
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
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    here = os.path.dirname(os.path.abspath(__file__))
    dst = os.path.join(here, "..", "..", "weight_reference_values_test.go")
    with open(dst, "w") as fh:
        fh.write(emit(cases()))
    print("wrote", os.path.normpath(dst))
