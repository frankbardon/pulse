# Privacy-safe data sharing with a synthetic twin

**Audience:** data-governance leads who must approve an external data share, and the analysts on
both sides of it.

**In one paragraph.** Kestrel Mobile, a fictional telecom, holds a 40,000-row customer table with
demographics, contract terms, products held, spend, support contacts, account dates and churn. A
regulator's research unit (the agency) wants to study what drives churn. Kestrel profiles the
table, generates a 40,000-row synthetic twin with Pulse, checks it three ways (Pulse's fidelity
report, business-rule violation counts, and the agency's own analysis run on both tables), and
checks that no synthetic row is a copy of a real customer. The result: every headline figure lands
between index 97.9 and 102.7 of the source value (source = 100), every churn driver points the
same way, no impossible rows remain once structural rules are applied, and no real record appears
in the twin. The churn effects are **weaker** than in the source. Strong drivers stay clearly
significant but shrink: the logistic model's pseudo-R² falls from 0.198 to 0.077. One moderate
driver, price plan, drops below significance. The twin is good enough for questions about which way
an effect points, but not for estimating how large it is.

---

## 1. The business question

> *Can we give an outside agency a dataset it can analyse freely, so that its conclusions about
> churn hold for our real customers, without handing over a single real customer record?*

Governance needs four answers before signing off:

| # | Question | How this walkthrough answers it |
|---|---|---|
| G1 | Does the twin look like the source, field by field? | Pulse fidelity report (KS / χ² per field, set-option frequencies, model recovery) |
| G2 | Does it obey our business logic, with no impossible customers? | Violation counts with Pulse expression attributes, with and without structural rules |
| G3 | Would the agency reach the same conclusions? | The same Compose requests run on both cohorts, with `OVERLAY_DELTA_VS_REF` / `OVERLAY_INDEX_VS_REF` comparing twin to source |
| G4 | Is any real customer in the twin? | Exact-record joins, and a profile-match rate compared against the rate between two halves of the real table |

## 2. The source table

`data/customers.csv` with the 40,000-row schema `data/customers.schema.json`, snapshot date 2026-06-30.

| Field | Pulse type | Meaning |
|---|---|---|
| `region` | `categorical_u8` | Sales region (North, South, East, West, Central) |
| `age` | `u8` | Account holder age, 18–85 |
| `plan` | `categorical_u8` | Basic, Standard, Premium, Unlimited |
| `contract` | `categorical_u8` | Monthly, One-Year, Two-Year |
| `tenure_months` | `u16` | Months from `start_date` to `end_date` (churned) or the snapshot (active) |
| `products_held` | `set_u8` | Multi-select: mobile, broadband, tv, landline, protection; empty = dormant account |
| `monthly_spend` | `f64` | Average monthly bill, USD; exactly 0 for dormant accounts |
| `support_contacts` | `u4` | Support calls/chats in the last 12 months |
| `frequent_caller` | `packed_bool` | Operational flag: `support_contacts ≥ 4` |
| `autopay` | `packed_bool` | Bill paid by automatic debit |
| `start_date` | `date` | Account opened |
| `end_date` | `date`, nullable | Account closed; null while active |
| `churned` | `packed_bool` | Closed between 2024-07-01 and 2026-06-30 |

**Built-in rules that hold on every source row:**

1. `end_date` is null exactly when `churned = 0`.
2. `end_date ≥ start_date`, and `tenure_months` matches the dates (to within half a month).
3. A dormant account (no products) has `monthly_spend = 0`.
4. `frequent_caller = 1` exactly when `support_contacts ≥ 4`.
5. TV is sold only over broadband: no customer has `tv` without `broadband`.

**Planted churn signal:** churn is driven by contract term (Monthly highest), short tenure, many
support contacts, no autopay, few products, and plan. Region and age have no effect. Contract
also drives tenure and autopay, plan and products drive spend, and older customers favour longer
contracts and landlines.

**Source facts** (`req/01_source_overview.json`, `pulse api process`):

| Metric | Value |
|---|---|
| Customers | 40,000 |
| Churn rate | 19.08% (7,633 closed; 32,367 open with no `end_date`) |
| Average monthly spend | $70.67 |
| Average tenure | 27.0 months |
| Average support contacts | 1.05 per year (1,147 frequent callers) |
| Autopay | 55.6% |
| Products per customer | 1.97 (mobile 35,205 · broadband 18,866 · protection 9,973 · tv 9,756 · landline 5,021) |
| Dormant accounts | 1,167 |
| Account dates | opened from 2014-08-11; closures 2024-07-01 to 2026-06-30 |

## 3. Methodology

```
customers.csv ──import──► customers.pulse ──profile create──► profile.json + suggested_rules.json
                                                                      │ (review: rules.json)
                                    synth from-profile --rules --fidelity-report --emit-spec
                                                                      │
                     fidelity.json  ◄──────── tagged.pulse   spec.json ──synth from-schema──► twin.pulse  (shared)
                                                                      │
             same Compose requests on customers.pulse vs twin.pulse, overlays = twin − source
```

* The profile is the only thing that leaves the secure zone and reaches the generator. It holds
  summary statistics, fitted model coefficients and correlations, and **no rows**.
* `synth from-profile` always writes a *tagged top-up*: the source rows plus the new rows, with a
  `_synthetic` flag. The fidelity report is computed on that file. The **shareable** file is
  regenerated from the emitted spec with `synth from-schema` at the same seed. It contains only
  synthetic rows and no `_synthetic` column, and step 5 shows it is exactly the scored rows.
* Every comparison is one Compose call: matching slots over `customers.pulse` and `twin.pulse`,
  with Compose overlays computing twin − source (percentage points) and twin ÷ source × 100.

All commands run from this directory. The full sequence is in `run.sh`. Responses land in `out/`.

---

## 4. Walkthrough

### Step 1: Import the source table

```bash
pulse import csv -i data/customers.csv -o customers.pulse --schema data/customers.schema.json --json
pulse api process -r req/01_source_overview.json --json
```

40,000 rows imported, 0 row errors. The overview figures are the table in section 2.

### Step 2: Capture the profile and let Pulse propose rules

```bash
pulse profile create -i customers.pulse -o synth/profile.json \
  --include-stats --include-correlations --conditional \
  --fit-models --residual-correlations --fit-shape \
  --suggest-rules synth/suggested_rules.json --seed 7 --json
```

* `--fit-models` fits one linear model per numeric field on the categorical levels and set options
  that explain at least 1% of its variance. It fitted 7 models: `monthly_spend` R² = 0.915 (plan +
  every product), `tenure_months` 0.214 (contract), `churned` 0.114 (contract), `autopay` 0.095
  (contract), `support_contacts` 0.084 (plan + products), `age` 0.073, `frequent_caller` 0.014.
* `--residual-correlations` captures how those models' residuals co-move (for example churn and tenure).
* `--fit-shape` replaced the plain normal with a two-component mixture for age, tenure, spend,
  support contacts and autopay.

`--suggest-rules` scanned the same pass and proposed **two** candidates (`synth/suggested_rules.json`):

| Detector | Proposed rule | Evidence |
|---|---|---|
| gating | `{"when": "round(churned) == 0", "set_null": ["end_date"], "owns_nulls": true}` | `end_date` null rate 1.0 when churned = 0 (32,367 rows), 0.0 when churned = 1 (7,633 rows) |
| dependency | `{"set_expr": {"frequent_caller": "round(support_contacts) >= 4"}}` | one value per level of `support_contacts` on all 40,000 rows, 0 exceptions (thin support warned: level 8 has 2 rows) |

These are business rules 1 and 4, found without being told. Rules 2 and 3 involve dates, an
`f64` and a set field, which the detectors do not examine. Rule 5 is a dependency inside one set
field, so no rule shape can express it without changing a marginal.

### Step 3: Write the structural rules (`synth/rules.json`)

Both suggestions were accepted, and three rules were written by hand:

```json
[
  {"when": "round(churned) == 0", "set_null": ["end_date"], "owns_nulls": true},
  {"set_expr": {"frequent_caller": "round(support_contacts) >= 4"}},
  {"set_expr": {"tenure_months": "round(tenure_months)"}},
  {"set_expr": {"start_date": "(round(churned) == 1 ? end_date : 20634) - round(tenure_months * 30.4375)"}},
  {"when": "!products_held[\"mobile\"] && !products_held[\"broadband\"] && !products_held[\"tv\"] && !products_held[\"landline\"] && !products_held[\"protection\"]",
   "set": {"monthly_spend": 0}}
]
```

Rule 4 rebuilds `start_date` from the account's end (or the snapshot, epoch day 20634 =
2026-06-30) minus its tenure. That makes `end_date ≥ start_date` and tenure-date consistency true
by construction. Rule 3 first rounds tenure to the whole month the file stores (rules read the
pre-rounding value).

### Step 4: Generate the twin, with and without rules

```bash
# baseline: no structural rules
pulse synth from-profile -p synth/profile.json --source customers.pulse -o tagged_norules.pulse \
  --rows 40000 --seed 11 --fidelity-report synth/fidelity_norules.json --emit-spec synth/spec_norules.json --json
# with the reviewed rules
pulse synth from-profile -p synth/profile.json --source customers.pulse -o tagged.pulse \
  --rows 40000 --seed 11 --rules synth/rules.json --fidelity-report synth/fidelity.json --emit-spec synth/spec.json --json
```

40,000 rows generated, 0 rejected. Pulse states what it did *not* model. The 20 generation
warnings include the expected ones, for example
`field "frequent_caller" is already claimed by structural rule 1 (set_expr); dropping linear model`.

**Fidelity report (`synth/fidelity.json`), 40,000 source vs 40,000 synthetic rows:**

| Field | Test | Statistic | p-value | Reading |
|---|---|---|---|---|
| region | χ² | 1.98 | 0.739 | indistinguishable |
| plan | χ² | 5.54 | 0.137 | indistinguishable |
| contract | χ² | 1.35 | 0.510 | indistinguishable |
| age | KS | D = 0.013 | 0.0014 | rejects at n = 80,000; max CDF gap 1.3 points |
| monthly_spend | KS | D = 0.025 | 1.1e−11 | rejects; max CDF gap 2.5 points |
| tenure_months | KS | D = 0.056 | 5.0e−54 | rejects; max CDF gap 5.6 points (the long right tail is smoothed) |

With 80,000 rows a KS test rejects for very small gaps, so read D (the largest gap between the
two cumulative distributions) rather than p. The headline table in step 7 shows what these gaps
mean in units.

| Set option | Source frequency | Synthetic frequency |
|---|---|---|
| mobile | 88.01% | 88.13% |
| broadband | 47.17% | 47.10% |
| tv | 24.39% | 24.17% |
| landline | 12.55% | 12.34% |
| protection | 24.93% | 25.01% |

**Model recovery** (does the captured conditioning survive?): 6 applied models were refitted on the
synthetic rows. For churn, the contract effects come back almost exactly on the probit-score
scale: Two-Year −0.768 captured vs −0.793 recovered, One-Year −0.584 vs −0.615. Tenure on
contract: 1.153 vs 1.074. One model is flagged, `monthly_spend`, where the latent coefficient for
mobile is 0.325 captured vs 0.634 recovered. That flag appears only in the run with rules (the
run without rules flags nothing for spend). It comes from rule 5 pinning dormant spend to exactly
$0. In dollars, spend by plan and by mobile ownership still lands at index 96.1–100.6 (step 7b).
Residual correlations: 3 of 15 pairs can be scored (mean |Δρ| 0.010). The 12 that involve a 0/1 or
small-count field cannot be scored and are listed as such.

### Step 5: Build the shareable file and prove it is the scored data

```bash
pulse synth from-schema -s synth/spec.json -o twin.pulse --seed 11 --json
pulse synth from-schema -s synth/spec.json -o twin_rerun.pulse --seed 11 --json   # byte-identical to twin.pulse
pulse api compose -r req/02_twin_equals_scored_rows.json --json
```

`twin.pulse` has the source's 13 fields, 40,000 rows, no `_synthetic` column, and no source rows.
Regenerating from the same spec and seed gives a byte-identical file, so anyone holding the spec can
reproduce or audit the release. Request 02 shows the shareable file is exactly the rows the fidelity
report scored. Both slots return:

| | `_synthetic == 1` rows of `tagged.pulse` | `twin.pulse` |
|---|---|---|
| rows | 40,000 | 40,000 |
| churn rate | 19.105% | 19.105% |
| total monthly spend | $2,770,586.25 | $2,770,586.25 |
| total tenure months | 1,109,889 | 1,109,889 |
| null `end_date` | 32,358 | 32,358 |
| tv / broadband holders | 9,669 / 18,839 | 9,669 / 18,839 |

### Step 6: Business-rule violations (G2)

```bash
pulse synth from-schema -s synth/spec_norules.json -o twin_norules.pulse --seed 11 --json
pulse api compose -r req/03_business_rule_violations.json --json
```

Each check is an `ATTR_FORMULA` that yields 1 on a violating row, summed with `AGG_SUM`, in three
Compose slots:

| Violation (rows) | Source | Twin, no rules | Twin, with rules |
|---|---|---|---|
| `end_date` before `start_date` | 0 | 660 | **0** |
| active (`churned = 0`) but has `end_date` | 0 | 6,122 | **0** |
| churned but no `end_date` | 0 | 6,145 | **0** |
| tenure disagrees with the dates by > ½ month | 0 | 39,732 | **0** |
| no products but spend > 0 | 0 | 1,240 | **0** |
| `frequent_caller` disagrees with `support_contacts` | 0 | 1,825 | **0** |
| tv without broadband | 0 | 5,155 | 5,113 |

Without rules, almost every synthetic row breaks at least one business rule. Statistical summaries
alone do not carry hard logic. With five rules, six of the seven checks reach zero. The remaining
one (tv ⇒ broadband) is a dependency between two options of the same set field, and the profile
does not capture it.

### Step 7: The agency's analysis, run on both tables (G3)

#### 7a. Headline metrics (`req/04_headline_metrics.json`, `req/04b_headline_twin_vs_source.json`)

Request 04 puts the figures side by side. Request 04b repeats each metric as a source/twin slot pair
with one all-customer bucket and adds `OVERLAY_INDEX_VS_REF` and `OVERLAY_DELTA_VS_REF`:

| Metric | Source | Twin | Index (source = 100) | Δ |
|---|---|---|---|---|
| Churn rate | 19.08% | 19.11% | 100.1 | +0.02 pp |
| Avg monthly spend | $70.67 | $69.26 | 98.0 | −$1.40 |
| Median monthly spend | $68.63 | $67.16 | 97.9 | −$1.46 |
| Avg tenure | 27.01 months | 27.75 months | 102.7 | +0.74 months |
| Median tenure | 16 months | 16 months | — | — |
| Avg age | 44.15 | 44.05 | 99.8 | −0.10 yr |
| Avg support contacts | 1.049 | 1.049 | 100.0 | +0.0001 |
| Autopay | 55.58% | 54.93% | 98.8 | −0.65 pp |
| Products per customer | 1.971 | 1.967 | 99.8 | −0.003 |

#### 7b. Segments, with Compose overlays (`req/05_segments_twin_vs_source.json`)

Fourteen slots (seven source/twin pairs), each with one grouped aggregation, plus 14 overlays:
`OVERLAY_DELTA_VS_REF` (twin − source) and `OVERLAY_INDEX_VS_REF` (twin ÷ source × 100), both
`scope: "group"`, with the source slot as reference.

**Churn rate by contract**

| Contract | Source | Twin | Δ (pp) | Index |
|---|---|---|---|---|
| Monthly | 33.7% | 29.9% | −3.9 | 88.5 |
| One-Year | 10.6% | 12.2% | +1.6 | 115.0 |
| Two-Year | 3.5% | 8.8% | +5.3 | 254.5 |

**Churn rate by autopay and by plan**

| Segment | Source | Twin | Δ (pp) |
|---|---|---|---|
| autopay = 0 | 27.0% | 23.6% | −3.4 |
| autopay = 1 | 12.7% | 15.4% | +2.7 |
| Basic | 22.1% | 19.9% | −2.2 |
| Standard | 17.9% | 18.7% | +0.9 |
| Premium | 15.8% | 18.9% | +3.1 |
| Unlimited | 21.8% | 18.7% | −3.1 |

**Churn rate by support contacts and by tenure band**

| Support contacts | 0 | 1 | 2 | 3 | 4 | 5 |
|---|---|---|---|---|---|---|
| Source | 14.5% | 18.1% | 23.8% | 28.5% | 35.4% | 41.7% |
| Twin | 15.3% | 18.8% | 23.5% | 24.8% | 28.2% | 35.4% |

| Tenure (months) | 0–12 | 12–24 | 24–36 | 36–48 | 48–60 | 60–72 | 72–84 | 96–108 |
|---|---|---|---|---|---|---|---|---|
| Source | 27.7% | 21.6% | 14.7% | 10.8% | 6.5% | 4.8% | 2.4% | 0.8% |
| Twin | 25.1% | 19.1% | 15.4% | 15.8% | 13.6% | 11.9% | 10.2% | 8.8% |

**Average monthly spend by plan and by product held** (spend is the best-modelled field, R² 0.915)

| Group | Source | Twin | Index |
|---|---|---|---|
| Basic | $51.09 | $50.54 | 98.9 |
| Standard | $66.00 | $64.83 | 98.2 |
| Premium | $86.85 | $83.45 | 96.1 |
| Unlimited | $106.32 | $104.19 | 98.0 |
| holds mobile | $71.19 | $71.60 | 100.6 |
| holds landline | $80.58 | $82.82 | 102.8 |
| holds broadband | $98.69 | $91.03 | 92.2 |
| holds protection | $87.25 | $79.27 | 90.9 |
| holds tv | $113.22 | $94.32 | 83.3 |

#### 7c. Plan × contract churn crosstab (`req/06_churn_crosstab_twin_vs_source.json`)

Two crosstab slots (cell = `AGG_AVERAGE churned`) and a cell-scope `OVERLAY_DELTA_VS_REF`:

| Plan | Source M / 1Y / 2Y | Twin M / 1Y / 2Y | Δ pp M / 1Y / 2Y |
|---|---|---|---|
| Basic | 39.3 / 13.5 / 4.6 | 30.5 / 13.2 / 9.2 | −8.8 / −0.3 / +4.6 |
| Standard | 31.9 / 9.9 / 3.0 | 29.5 / 11.6 / 8.7 | −2.4 / +1.7 / +5.7 |
| Premium | 28.2 / 8.0 / 2.8 | 29.6 / 11.9 / 8.6 | +1.4 / +3.9 / +5.9 |
| Unlimited | 36.8 / 10.4 / 3.1 | 29.9 / 11.9 / 8.3 | −6.9 / +1.5 / +5.3 |

In the twin, the contract gradient survives in every row of the crosstab, but plan makes almost
no difference within a contract.

#### 7d. Independence tests (`req/07_chisq_tests.json`)

Ten crosstab slots (field × churned counts), each with `OVERLAY_CHISQ_MATRIX`:

| Field × churned | Source χ² (df) | p | Twin χ² | p | Same conclusion at α = 0.05? |
|---|---|---|---|---|---|
| contract | 4,626.4 (2) | < 1e−300 | 2,394.9 | < 1e−300 | yes |
| autopay | 1,306.0 (1) | 5.5e−286 | 434.6 | 1.6e−96 | yes |
| frequent_caller | 244.6 (1) | 3.9e−55 | 94.4 | 2.6e−22 | yes |
| plan | 165.7 (3) | 1.1e−35 | 6.9 | 0.075 | **no** |
| region (a field with no effect) | 1.19 (4) | 0.88 | 1.04 | 0.90 | yes |

#### 7e. Logistic churn model (`req/08_churn_logistic_model.json`)

`REG_GLM` (binomial, logit) on `FEAT_ONE_HOT(contract)` dummies plus `ATTR_SET_POPCOUNT(products_held)`:

| Predictor | Source β | p | Twin β | p | Direction kept |
|---|---|---|---|---|---|
| contract One-Year | −1.209 | 9e−247 | −1.021 | 3e−205 | yes |
| contract Two-Year | −2.141 | 3e−273 | −1.249 | 3e−182 | yes |
| support_contacts (per contact) | +0.432 | 9e−244 | +0.237 | 9e−91 | yes |
| autopay | −0.536 | 3e−75 | −0.246 | 2e−19 | yes |
| tenure_months (per month) | −0.0206 | 3e−113 | −0.0058 | 3e−22 | yes |
| products held (per product) | −0.457 | 4e−201 | −0.042 | 0.004 | yes |
| age (no true effect) | −0.0014 | 0.18 | −0.0016 | 0.10 | n.s. in both |
| McFadden pseudo-R² | 0.198 | | 0.077 | | |

#### 7f. Correlation matrix (`req/09_correlation_matrix.json`)

`MAT_CORRELATION` over age, tenure, spend, support contacts, autopay and churned (`coerce: "binary"`):

| Pair | Source r | Twin r |
|---|---|---|
| tenure – churned | −0.220 | −0.134 |
| autopay – churned | −0.181 | −0.104 |
| support contacts – churned | +0.134 | +0.098 |
| spend – churned | −0.099 | −0.022 |
| tenure – autopay | +0.134 | +0.103 |
| spend – support contacts | +0.113 | +0.077 |
| age – spend | −0.073 | −0.076 |
| age – tenure | +0.073 | +0.062 |
| determinant | 0.855 | 0.935 |

Every pair keeps its sign. The determinant moves toward 1, meaning the twin's variables are less
inter-related overall.

### Step 8: Is any real customer in the twin? (G4)

**Exact copies** (`req/10_exact_record_match.json`). Each slot runs an inner join to
`customers.pulse` on all 11 non-null scalar columns. A post-join `FILTER_EXPRESSION` then compares
`products_held` and the nullable `end_date`.

| Slot | Matching row pairs |
|---|---|
| control: source joined to itself | 40,002 (each row matches itself; one pair of source rows is identical) |
| twin joined to source | **0** |

**Coincidental profile matches** (`req/11_profile_match_vs_chance.json`). Spend is continuous, so
an exact match is unlikely by design. The stricter test drops spend and asks how often a synthetic
customer shares *every other attribute* with a real one. The baseline is how often two *different
real* customers do. `pulse cohort filter` splits each table into halves of about 20,000 rows by
the parity of spend in cents:

| Comparison | Left rows | Right rows | Pairs matching on all 12 non-spend fields |
|---|---|---|---|
| real half A vs real half B | 20,591 | 19,409 | 95 |
| twin half A vs real half B | 20,710 | 19,409 | **46** |

With halves of similar size, synthetic customers coincide with real profiles 46 times, against 95
times between two sets of real customers. The twin does not memorise records. Its matches are common, low-detail profiles that
also recur among real customers.

### Step 9: Why `--fit-models` matters (comparison)

The same pipeline with a profile captured **without** `--fit-models` (`--conditional` only) keeps
just one driver per field. The first-listed categorical, `region`, claims every pair. Pulse warns,
for example `field "churned" is already claimed by categorical-numeric pair (region -> churned);
dropping categorical-numeric pair (contract -> churned)`, and the numeric correlations are dropped
the same way. On that twin (`req/08b_…`) the churn model has pseudo-R² **0.0002**, and contract,
tenure, support contacts and autopay are all non-significant (p ≥ 0.44). The signal is gone.
Read the generation warnings before trusting a twin.

---

## 5. Findings

1. **Headline figures match.** Churn rate 19.08% vs 19.11% (index 100.1), average spend index 98.0,
   tenure 102.7, support contacts 100.0. Set-option frequencies agree to within 0.22 pp, categorical marginals indistinguishable
   (χ² p = 0.14–0.74). The agency can describe the customer base from the twin.
2. **Every churn driver keeps its direction, and the no-effect fields stay null.** All six real
   drivers have the same sign in the twin's logistic model and correlation matrix. Region and age
   stay non-significant. Contract, autopay and frequent-caller χ² tests reach the same conclusion.
3. **Effect sizes are attenuated, so do not quote magnitudes from the twin.** Pseudo-R² falls from
   0.198 to 0.077. Two-Year customers churn at 8.8% in the twin vs 3.5% in reality (index 254.5).
   The tenure coefficient shrinks from −0.0206 to −0.0058 per month, and the products coefficient
   from −0.457 to −0.042 per product. Plan loses significance (p = 0.075 vs
   1e−35). The cause is how the profile captures churn. Churn is modelled from contract alone,
   because the other drivers either fall under the 1% variance-explained admission floor or are
   numeric. Tenure and support contacts reach churn only through residual correlations, and the
   fidelity report cannot score those for a 0/1 field. The fidelity report shows churn's
   captured-model coefficients as recovered, but that compares the twin with the captured model,
   not with the source. A side-by-side analysis like step 7 is needed to see the attenuation.
4. **Structural rules turn an implausible twin into a coherent one.** Without rules, 39,732 of
   40,000 synthetic rows break the tenure/date logic, and 6,122 + 6,145 break the churn/end-date
   link. Five
   rules (two proposed by Pulse) bring six of seven checks to zero. The one left, tv without broadband
   (5,113 rows), is a dependency inside one set field. It also distorts spend by product: tv holders
   average $94 in the twin vs $113 in the source.
5. **No real record leaks.** There are 0 exact matches, and 46 profile-level coincidences against a
   real-to-real baseline of 95. The release is reproducible byte for byte from `spec.json` and seed 11.

**Implications for the data-sharing agreement**

* Share `twin.pulse` together with this kind of report: fidelity table, violation counts and a
  side-by-side of the agreed analyses. The agency should treat the twin as **directional**. Use it
  to build and debug analyses and to rank drivers. Run the final effect-size estimates on the
  source inside the secure zone, using the same request files, which run unchanged against
  `customers.pulse`.
* Review `synth/profile.json` and `synth/spec.json` before release. They hold no rows, but they do
  carry exact minima, maxima and category frequencies. This pipeline adds no formal privacy
  guarantee (such as differential-privacy noise). The disclosure checks in step 8 are evidence, not
  proof.
* Keep rules under version control next to the spec. They encode the business logic the agency
  would otherwise "discover" as data-quality errors.

## 6. Pulse features used

| Feature | Where |
|---|---|
| `pulse import csv` with a typed schema (`set_u8`, `packed_bool`, nullable `date`) | Step 1 |
| `pulse profile create` with `--conditional --fit-models --residual-correlations --fit-shape` | Step 2 |
| `--suggest-rules` (gating and dependency detectors) | Step 2 |
| Structural rules (`set_null` + `owns_nulls`, `set_expr`, `set`, `when`) via `--rules` | Steps 3–4 |
| `pulse synth from-profile` with `--fidelity-report` and `--emit-spec` | Step 4 |
| `pulse synth from-schema` for seed-deterministic, byte-identical regeneration | Step 5 |
| `pulse api compose` with labelled slots | Steps 5–8 |
| `OVERLAY_DELTA_VS_REF`, `OVERLAY_INDEX_VS_REF` (group and cell scope) | Steps 7b, 7c |
| Crosstab with margins + `OVERLAY_CHISQ_MATRIX` | Steps 7c, 7d |
| `ATTR_FORMULA`, `ATTR_SET_POPCOUNT`, `FEAT_ONE_HOT` | Steps 6, 7e |
| `AGG_AVERAGE`, `AGG_MEDIAN`, `AGG_SUM`, `AGG_SET_FREQUENCY`, `AGG_NULL_COUNT` | Steps 1, 5, 7a |
| `GROUP_CATEGORY`, `GROUP_RANGE`, `GROUP_SET_PER_ELEMENT` | Step 7b |
| `REG_GLM` (binomial / logit) | Step 7e |
| `MAT_CORRELATION` with a binary-coerced vector | Step 7f |
| Inner hash join (`joins`) + `FILTER_EXPRESSION` | Step 8 |
| `pulse cohort filter`, `pulse cohort inspect` | Steps 5, 8 |

To reproduce every number above, see [REPRODUCE.md](REPRODUCE.md).
