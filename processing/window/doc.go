// Package window implements the WIN_* window operators for Pulse.
//
// Window operations evaluate over post-aggregate rows ([]map[string]any),
// inserted into the processing pipeline after grouped aggregation:
//
//	records → filter → attribute → group → aggregate → window → response
//
// The Apply entry point sorts rows once per distinct (PartitionBy, OrderBy)
// tuple, partitions them, and runs each window's WindowComputer to mutate
// rows with the operator's output column.
//
// Validation of window specs (frame matrix, alpha bounds, orderable types)
// belongs to descriptor/predict_window.go and must run before Apply. Apply
// trusts that req.Windows has been validated; runtime errors here surface as
// PROCESSING_RUNTIME / PROCESSING_CONFIG.
package window
