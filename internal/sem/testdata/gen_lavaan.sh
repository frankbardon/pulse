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
fit() { Rscript "$here/fit_lavaan.R" "$here/$1_data.csv" "$here/$2_model.txt" "$3" > "$here/$4_lavaan.json"; }
# name data model meanstructure
fit brand brand_ms TRUE brand_ms
fit brand brand_mx FALSE brand_mx
