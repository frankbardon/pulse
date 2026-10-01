// Package embeddersmoke is a test-only, out-of-module embedder of Pulse.
//
// It lives in its own Go module (see go.mod) with a replace directive
// pointing at the repository root, so it sees exactly what an external
// library sees: the exported, non-internal surface. Anything it needs
// that lives under github.com/frankbardon/pulse/internal/... fails to
// compile here, which is the point — the module proves the embedder
// migration guide's new spellings are reachable from outside.
//
// surface.go holds compile-only checks (signatures and names an
// embedder must be able to spell); the *_test.go files run cheap
// in-memory flows over an afero MemMap filesystem passed through
// pulse.Options.FS: ingest, query, inspect/predict, I/O conversion,
// raw .pulse writes, synthetic data and mounting the MCP catalog.
//
// The root module's ./... does not descend here (a nested go.mod
// excludes it); CI builds and tests it with `make smoke`.
package embeddersmoke
