// Package guide holds the logic behind Pulse's guidance surfaces —
// Recommend (an intent to ranked draft requests) and Explain (a request
// to plain-language steps).
//
// It is NO-EXECUTE, like internal/descriptor: it reads the instance
// snapshot (pruned ontology, instance manifest, Purposes) and never
// imports internal/service or internal/processing
// (TestGuideNoExecutionImports). Everything it says is derived from
// declared guidance metadata; nothing here runs a request. Cohort-bound
// Recommend also reads the cohort's schema and validates each draft
// through the caller's Bound.Predict, and Explain checks a request
// through the caller's Checks — no-execute predicts over the header
// and schema, never a record.
package guide
