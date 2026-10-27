//go:build heavy

package pulse_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCoreModuleNoObservabilityDeps: the root module never requires an
// OpenTelemetry or Prometheus module, and no root package (tests
// included) imports one — nor a contrib/ module — anywhere in its build
// graph. go.sum is deliberately not checked: it legitimately carries
// otel hashes from the go.mod graph of grpc (via arrow-go) without any
// otel package ever being built.
func TestCoreModuleNoObservabilityDeps(t *testing.T) {
	t.Parallel()

	mods := goModRequires(t, "go.mod")
	if len(mods) == 0 {
		t.Fatal("parsed no require lines from go.mod — parser broken?")
	}
	for _, m := range mods {
		if hasVendorObservabilityPrefix(m + "/") {
			t.Errorf("root go.mod requires %s; OpenTelemetry/Prometheus belong in contrib/otelpulse or contrib/prompulse, never the core module", m)
		}
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go toolchain not on PATH: %v", err)
	}
	cmd := exec.Command(goBin, "list", "-tags", "heavy", "-deps", "-test", "./...")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list -deps -test ./...: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("go list -deps -test ./...: %v", err)
	}
	pkgs := strings.Fields(string(out))
	if len(pkgs) == 0 {
		t.Fatal("go list returned no packages")
	}
	for _, p := range pkgs {
		if hasVendorObservabilityPrefix(p) {
			t.Errorf("root build graph contains %s; the core module must stay vendor-free (adapters live in contrib/)", p)
		}
	}
}
