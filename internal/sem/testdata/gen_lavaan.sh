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
