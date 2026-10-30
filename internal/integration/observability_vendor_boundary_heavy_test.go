//go:build heavy

package integration_test

import (
	"strings"
	"testing"
)

const pulseModulePath = "github.com/frankbardon/pulse"

// TestObservabilityVendorImportBoundaryModule walks
// `go list -tags heavy -deps -test ./...` and fails if any module package
// outside observabilityVendorAllowlist — a test variant included, unless
// it is recompiled for an allowlisted package's test binary — reaches an
// OpenTelemetry or Prometheus package, directly or transitively. It also
// asserts the allowlisted adapters DO reach one, so the walk cannot
// silently match nothing.
func TestObservabilityVendorImportBoundaryModule(t *testing.T) {
	t.Parallel()

	pkgs := goListDeps(t, "-tags", "heavy", "-test", "./...")
	adapterHit := map[string]bool{}
	for _, p := range pkgs {
		if p.Module == nil || p.Module.Path != pulseModulePath {
			continue
		}
		hits := vendorDeps(p)
		if len(hits) == 0 {
			continue
		}
		base := basePackage(p.ImportPath)
		if observabilityVendorAllowlist[base] || observabilityVendorAllowlist[p.ForTest] {
			adapterHit[base] = true
			continue
		}
		t.Errorf("%s reaches vendor observability packages %s; only observe/otel, observe/prom, internal/cli and cmd/pulse may import go.opentelemetry.io/* or github.com/prometheus/*",
			p.ImportPath, strings.Join(hits, ", "))
	}
	for _, a := range []string{pulseModulePath + "/observe/otel", pulseModulePath + "/observe/prom"} {
		if !adapterHit[a] {
			t.Errorf("adapter %s shows no vendor dependency in go list output; the walk is vacuous or the adapter moved", a)
		}
	}
}
