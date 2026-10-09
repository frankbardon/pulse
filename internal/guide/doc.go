// Package guide holds the logic behind Pulse's guidance surfaces —
// Recommend (an intent to ranked draft requests) and, later, Explain.
//
// It is NO-EXECUTE, like internal/descriptor: it reads the instance
// snapshot (pruned ontology, instance manifest, Purposes) and never
// imports internal/service or internal/processing
// (TestGuideNoExecutionImports). Everything it says is derived from
// declared guidance metadata; nothing here runs a request.
package guide
