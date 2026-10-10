#!/usr/bin/env bash
# Prints the headline figures from ./out (written by run_all.sh) so they can be
# compared with the table in REPRODUCE.md. Read-only: it only quotes Pulse output.
set -euo pipefail
cd out
r4() { awk '{printf "%.4f\n", $1}'; }
echo "rows imported:                 $(jq '.data.RowsImported' 00_import_deliveries.json)"
echo "UTC Sunday deliveries (all):   $(jq '.data.crosstab.matrix as $m | ($m.column_keys|map(.[0])|index("Sunday")) as $i | $m.column_margins[$i].value' 01_weekday_volume_utc.json)"
echo "UTC Sunday deliveries SYD:     $(jq '.data.crosstab.matrix as $m | ($m.column_keys|map(.[0])|index("Sunday")) as $i | ($m.row_keys|map(.[0])|index("SYD")) as $r | $m.cells[$r][$i].value' 01_weekday_volume_utc.json)"
echo "local Sunday columns present:  $(jq '[.data.responses[].crosstab.matrix.column_keys[][0] | select(.=="Sunday")] | length' 02_weekday_volume_local.json)"
echo "SYD May 11 on-time UTC/local:  $(jq -r '[.data.responses[] | .data[] | select(.delivered_at=="2026-05-11") | .on_time_rate] | map(tostring) | join(" / ")' 03_syd_incident_week_utc_vs_local.json)"
echo "SYD incident on-time UTC/local:$(jq -r '[.data.responses[] | .data[] | select(.delivered_at=="2_incident") | .on_time_rate] | map(tostring) | join(" / ")' 04_syd_incident_periods.json)"
echo "SYD May 12 rolling z (local):  $(jq '.data.responses[5] as $r | ($r.data|map(.delivered_at)|index("2026-05-12")) as $k | $r.overlays[0].payload.series.entries[$k].summary.statistic' 05_daily_anomaly_screen.json | r4)"
echo "SYD May 12 rolling index:      $(jq '.data.responses[5] as $r | ($r.data|map(.delivered_at)|index("2026-05-12")) as $k | $r.overlays[1].payload.series.entries[$k].summary.statistic' 05_daily_anomaly_screen.json | r4)"
echo "SYD May 12 duration z:         $(jq '.data as $r | ($r.data|map(.delivered_at)|index("2026-05-12")) as $k | $r.overlays[0].payload.series.entries[$k].summary.statistic' 06_syd_daily_windows.json | r4)"
echo "SYD May 14 on-time z UTC:      $(jq '.data.responses[0] as $r | ($r.data|map(.delivered_at)|index("2026-05-14")) as $k | $r.overlays[0].payload.series.entries[$k].summary.statistic' 07_syd_screen_utc_vs_local.json | r4)"
echo "BLR weekly trend Z / p_holm:   $(jq -r '.data.responses[0].post_tests[0] | "\(.statistic) / \(.p_adjusted)"' 08_weekly_on_time_trend.json)"
echo "LON weekly trend Z / p:        $(jq -r '.data.responses[2].post_tests[0] | "\(.statistic) / \(.p_value)"' 08_weekly_on_time_trend.json)"
echo "BLR daily raw / MA6 trend Z:   $(jq -r '[.data.responses[0,1].post_tests[0].statistic] | map(tostring) | join(" / ")' 09_daily_trend_raw_vs_smoothed.json)"
echo "SYD Apr 5 night batches:       $(jq '.data.responses[3].data[] | select(.batch_closed_at=="2026-04-05") | .batches' 13_syd_dst_night.json)"
echo "SYD import (earlier) warning:  $(jq -c '.warnings[0].details' 13b_import_syd_earlier.json)"
echo "NYC import (default) error:    $(jq -r '.errors[0].code' 14a_import_nyc_default.json)"
