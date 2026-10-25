#!/usr/bin/env bash
# Regenerate the lavaan reference fits the lavaan-parity tests read
# (*_lavaan.json). Needs R with lavaan and jsonlite:
#   Rscript -e 'install.packages(c("lavaan", "jsonlite"), repos = "https://cloud.r-project.org")'
#   internal/sem/testdata/gen_lavaan.sh
# Every reference records the lavaan release and R version it was fitted
# with. brand_data.csv is generated, not simulated here: brand_spec.json
# and its seed through brandSurvey (go test ./internal/sem -run
# TestSEM_BrandSurveyRegenerates -regen-brand). CI never runs R.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
fit() { Rscript "$here/fit_lavaan.R" "$here/$1_data.csv" "$here/$2_model.txt" "$3" "${5:-}" "${6:-}" "${7:-}" "${8:-}" > "$here/$4_lavaan.json"; }
# data model meanstructure name [group] [group_equal] [group_partial] [level]
fit brand brand_ms TRUE brand_ms
fit brand brand_mx FALSE brand_mx
# NA and labelled first indicators, residual covariances
fit brand brand_res TRUE brand_res
fit brand brand_ms TRUE brand_mg_configural wave
# unequal groups (600 / 400: brand_data.csv's first 1000 rows), a misfit
# and a label shared across groups
fit brand_uneq brand_mgmis TRUE brand_mg_misfit wave
# the invariance ladder (group.equal) on the equal groups, and a
# partial-invariance scalar fit (group.partial)
fit brand brand_ms TRUE brand_mg_metric wave loadings
fit brand brand_ms TRUE brand_mg_scalar wave loadings,intercepts
fit brand brand_ms TRUE brand_mg_partial wave loadings,intercepts "v2~1,service=~s2"
# per-group modifiers c(...)*x: equal loadings and residual covariances
# with a freed marker and loading (group.partial), then every
# group.equal value at once
fit brand_uneq brand_mgmod TRUE brand_mg_modifiers wave loadings,residual.covariances "value=~v3,quality=~q1"
fit brand brand_mgmod TRUE brand_mg_strict wave loadings,intercepts,means,residuals,residual.covariances,lv.variances,lv.covariances,regressions
# lavaan defined parameters (:=): indirect / total effects and the
# grammar, with and without a mean structure (the covariance-only fit
# switches to lavaan conventions), and a cross-group contribution
# (a path times a latent mean change) under scalar invariance
fit brand brand_def TRUE brand_def_ms
fit brand brand_def FALSE brand_def_cov
fit brand brand_mgdef TRUE brand_mg_defined wave loadings,intercepts
# a non-default Wald level (lavaan's level = 0.90): params.ci_level sets
# ci_lower / ci_upper of every free and := row
fit brand brand_def TRUE brand_def_l90 "" "" "" 0.90
# lavaan's se = "bootstrap" (percentile intervals, seeded): the
# distributional references for params.resample "bootstrap" — a
# sampling tolerance, never the bits (fit_lavaan_boot.R)
boot() { Rscript "$here/fit_lavaan_boot.R" "$here/$1_data.csv" "$here/$2_model.txt" "$3" "${5:-}" "${6:-}" 1000 20261024 > "$here/$4_lavaan.json"; }
boot brand brand_def TRUE brand_boot_def
boot brand brand_mgdef TRUE brand_boot_mg wave loadings,intercepts
# lavaan's factor scores (lavPredict regression / Bartlett): the
# ATTR_SEM_SCORE oracle — a single-group mean structure, the same model
# under scalar invariance across the waves, an observed covariate with
# a free latent mean (lavaan's phantom latents), an indicator with an
# outgoing arrow, and a second-order factor (predict_lavaan.R; every
# 4th row recorded)
score() { Rscript "$here/predict_lavaan.R" "$here/$1_data.csv" "$here/$2_model.txt" "$3" "${5:-}" "${6:-}" "${7:-}" "${8:-}" "${9:-}" "${10:-}" > "$here/$4_lavaan.json"; }
score brand brand_ms TRUE brand_score_ms
score brand brand_ms TRUE brand_score_mg wave loadings,intercepts
score brand brand_mx TRUE brand_score_mx
score brand brand_xind TRUE brand_score_xind
score brand brand_ho TRUE brand_score_ho
# lavaan's empirical Bayes modal scores of the WLSMV fits (the nine
# ratings ordered; lavPredict's categorical default, and what method
# "regression" means there): the measurement model, the structural
# model with the numeric recommend outcome (mixed indicators), the
# latent regressions with residual covariances (Theta's diagonal alone
# enters), and the mixed model across the waves under thresholds,
# loadings and intercepts (wave-2 scale factors and latent means free)
q9s=q1,q2,q3,v1,v2,v3,s1,s2,s3
score brand brand_ord TRUE brand_score_ord "" "" "" "$q9s"
score brand brand_ms TRUE brand_score_ordmx "" "" "" "$q9s"
score brand brand_res TRUE brand_score_ordres "" "" "" "$q9s"
score brand brand_ms TRUE brand_score_ordmg wave thresholds,loadings,intercepts "" "$q9s"
# the observed mediator (numeric recommend predicted by two factors,
# predicting the service factor and its ordered s3; v3 numeric too):
# lavaan's dummy latents, single-group and across the waves under
# thresholds, loadings and intercepts (that fit tightened: BFGS at
# reltol 1e-14, lavaan's default stop ~1e-5 off the optimum)
q8s=q1,q2,q3,v1,v2,s1,s2,s3
score brand brand_ordmed TRUE brand_score_ordmed "" "" "" "$q8s"
score brand brand_ordmed TRUE brand_score_ordmedmg wave thresholds,loadings,intercepts "" "$q8s" WLSMV TRUE
# the two-step ordinal sample statistics (lavCor(..., ordered = ...)):
# thresholds, polychoric, polyserial and Pearson correlations, with an
# independent re-derivation and lavaan's casewise scores
# (fit_lavaan_polychoric.R; the edge datasets from
# gen_polychoric_data.R), and the bivariate-normal CDF reference table
# (bvn_reference.R)
twostep() { Rscript "$here/fit_lavaan_polychoric.R" "$here/$1_data.csv" "$2" "$3" "${5:-}" "${6:-}" "${7:-}" > "$here/$4_lavaan.json" 2>/dev/null; }
q9=q1,q2,q3,v1,v2,v3,s1,s2,s3
twostep brand "$q9,recommend" "$q9" brand_twostep "" "" 12
twostep brand "$q9,recommend" "$q9" brand_twostep_mg wave
twostep poly_extreme a,b,c,d,e,f,x,xr,y a,b,c,d,e,f poly_extreme "" "" 12
twostep poly_sparse p,q,r p,q,r poly_sparse "" "" 12
twostep poly_binary x1,x2,x3,x4,x5,x6 x1,x2,x3,x4,x5,x6 poly_binary "" "" 12
twostep poly_weighted q1,q2,v1,s1,recommend q1,q2,v1,s1 poly_weighted "" w
Rscript "$here/bvn_reference.R" > "$here/bvn_reference.json"
# lavaan's ordinal estimators (DWLS / WLSMV, delta parameterization) on
# the brand survey with the nine ratings ordered: the measurement model
# under both, the structural model with the numeric recommend outcome,
# the mediation model with defined parameters and lavaan's single-group
# rules, each with every estimator intermediate (fit_lavaan_wlsmv.R)
wls() { Rscript "$here/fit_lavaan_wlsmv.R" "$here/$1_data.csv" "$here/$2_model.txt" "$3" "$4" "${6:-}" "${7:-}" > "$here/$5_lavaan.json"; }
wls brand brand_ord "$q9" WLSMV brand_ord_wlsmv
wls brand brand_ord "$q9" DWLS brand_ord_dwls
wls brand brand_ms "$q9" WLSMV brand_ordms_wlsmv
wls brand brand_def "$q9" WLSMV brand_orddef_wlsmv
wls brand brand_res "$q9" WLSMV brand_ordres_wlsmv
# the multi-group ordinal fits across the waves (fit_lavaan_wlsmv.R with
# a group: its branch fit_lavaan_wlsmv_mg.R) — configural, lavaan's
# threshold + loading invariance (wave-2 scale factors and ordered
# intercepts free), and the structural model on the unequal waves under
# thresholds, loadings and intercepts (wave-2 latent means free)
wls brand brand_ord "$q9" WLSMV brand_ordmg_configural wave ""
wls brand brand_ord "$q9" WLSMV brand_ordmg_thload wave thresholds,loadings
wls brand_uneq brand_ms "$q9" WLSMV brand_ordmg_scalar wave thresholds,loadings,intercepts
# lavaan's bootstrap of the ordinal measurement model (500 replicates;
# DWLS — lavaan refuses se = "bootstrap" under WLSMV, whose estimates
# are DWLS's): the distributional reference of a resampled ordinal fit
Rscript "$here/fit_lavaan_boot.R" "$here/brand_data.csv" "$here/brand_ord_model.txt" TRUE "" "" 500 20261024 "$q9" DWLS > "$here/brand_boot_ord_lavaan.json"
