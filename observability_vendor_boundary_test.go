package pulse_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
)

// observabilityVendorPrefixes are the import-path prefixes of the vendor
// observability SDKs. Only the adapter packages (and the CLI that wires
// them into `pulse mcp`) may reach them; every other package in the
// module stays vendor-free so an embedder that never imports an adapter
// never builds OpenTelemetry or Prometheus.
var observabilityVendorPrefixes = []string{
	"go.opentelemetry.io/",
	"github.com/prometheus/",
}

// observabilityVendorAllowlist are the module packages permitted to
// import a vendor observability SDK, directly or transitively — their
// test variants included.
var observabilityVendorAllowlist = map[string]bool{
	"github.com/frankbardon/pulse/observe/otel": true,
	"github.com/frankbardon/pulse/observe/prom": true,
	"github.com/frankbardon/pulse/internal/cli": true,
	"github.com/frankbardon/pulse/cmd/pulse":    true,
}

// observabilityVendorFreeCore are the packages an embedder imports for
// the observability surface itself; each is checked by name in the fast
// tier so the module-wide heavy walk is not the only line of defence.
var observabilityVendorFreeCore = []string{
	"github.com/frankbardon/pulse",
	"github.com/frankbardon/pulse/observe",
	"github.com/frankbardon/pulse/internal/obsprom",
	"github.com/frankbardon/pulse/mcpserve",
}

func isObservabilityVendorPath(p string) bool {
	for _, pre := range observabilityVendorPrefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

// goListPackage is the subset of `go list -json` the boundary reads.
type goListPackage struct {
	ImportPath string
	ForTest    string
	Deps       []string
	Module     *struct{ Path string }
}

// goListDeps runs `go list -json -deps <args>` and decodes the stream.
func goListDeps(t *testing.T, args ...string) []goListPackage {
	t.Helper()
	full := append([]string{"list", "-json=ImportPath,ForTest,Deps,Module", "-deps"}, args...)
	cmd := exec.Command("go", full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(full, " "), err, stderr.String())
	}
	var pkgs []goListPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p goListPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decoding go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		t.Fatal("go list returned no packages; the check would be vacuous")
	}
	return pkgs
}

// basePackage strips a go list test-variant decoration: "p [q.test]" → p,
// "p_test" → p, "p.test" → p.
func basePackage(importPath string) string {
	if i := strings.Index(importPath, " ["); i >= 0 {
		importPath = importPath[:i]
	}
	importPath = strings.TrimSuffix(importPath, ".test")
	return strings.TrimSuffix(importPath, "_test")
}

// vendorDeps returns the vendor observability packages in p's build graph.
func vendorDeps(p goListPackage) []string {
	var hits []string
	if isObservabilityVendorPath(p.ImportPath) {
		hits = append(hits, p.ImportPath)
	}
	for _, d := range p.Deps {
		if isObservabilityVendorPath(d) {
			hits = append(hits, d)
		}
	}
	return hits
}

// TestObservabilityVendorImportBoundary (fast tier): the root pulse
// package, observe, internal/obsprom and mcpserve build without any
// OpenTelemetry or Prometheus package anywhere in their dependency graph.
// The module-wide walk (every package, test variants included) is the
// heavy twin TestObservabilityVendorImportBoundaryModule.
func TestObservabilityVendorImportBoundary(t *testing.T) {
	t.Parallel()

	pkgs := goListDeps(t, observabilityVendorFreeCore...)
	seen := map[string]bool{}
	for _, p := range pkgs {
		seen[p.ImportPath] = true
		if isObservabilityVendorPath(p.ImportPath) {
			t.Errorf("vendor package %s is in the build graph of the core packages %v; OpenTelemetry/Prometheus belong only in observe/otel, observe/prom and the CLI",
				p.ImportPath, observabilityVendorFreeCore)
		}
	}
	for _, c := range observabilityVendorFreeCore {
		if !seen[c] {
			t.Errorf("go list output omits %s; the check would be vacuous", c)
		}
	}
}
