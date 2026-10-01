// Package spsssidecar names the SPSS metadata sidecar that sits beside a
// cohort. It is the one piece of the SPSS surface the root facade needs
// (sidecar discovery, CohortArtifacts, the table-directory exclusion list)
// without importing the SPSS adapter, and the adapter derives its own
// SidecarSuffix / SidecarPath from it so the two can never disagree.
package spsssidecar

// Suffix is appended to a cohort's path to form its SPSS metadata
// sidecar filename: "data.pulse" + Suffix == "data.pulse.spss.json".
//
// It is deliberately NOT the managed-import ".meta.json" suffix. A
// managed import writes that file for the same cohort, so sharing the
// suffix would have one artefact overwrite the other.
const Suffix = ".spss.json"

// Path derives the deterministic sidecar path for a cohort. Pure, no
// filesystem access.
func Path(cohortPath string) string { return cohortPath + Suffix }
