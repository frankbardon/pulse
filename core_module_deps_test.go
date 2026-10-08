package pulse_test

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// vendorObservabilityPrefixes are the module / import-path prefixes the
// core module must never depend on. OpenTelemetry and Prometheus
// adapters ship as the separate contrib/otelpulse and contrib/prompulse
// modules; the core module (and the internal/obsprom exporter) stays
// vendor-free so an embedder that never asks for them never builds them.
var vendorObservabilityPrefixes = []string{
	"go.opentelemetry.io/",
	"github.com/prometheus/",
	"github.com/frankbardon/pulse/contrib/",
}

func hasVendorObservabilityPrefix(path string) bool {
	for _, p := range vendorObservabilityPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// goModRequires returns every module path a go.mod `require` names,
// direct or indirect, in both the single-line and block forms.
func goModRequires(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()
	var mods []string
	inBlock := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		switch {
		case line == "":
			continue
		case inBlock && line == ")":
			inBlock = false
		case inBlock:
			mods = append(mods, strings.Fields(line)[0])
		case line == "require (":
			inBlock = true
		case strings.HasPrefix(line, "require "):
			mods = append(mods, strings.Fields(strings.TrimPrefix(line, "require "))[0])
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return mods
}

// TestCoreModuleNoObservabilityDeps: the root module never requires an
// OpenTelemetry or Prometheus module, and no root package (tests
// included) imports one — nor a contrib/ module — anywhere in its build
// graph. go.sum is deliberately not checked: it legitimately carries
// otel hashes from the go.mod graph of grpc (via arrow-go) without any
// otel package ever being built.
func TestCoreModuleNoObservabilityDeps(t *testing.T) {
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
	cmd := exec.Command(goBin, "list", "-deps", "-test", "./...")
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
