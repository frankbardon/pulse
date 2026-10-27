package pulse_test

import (
	"bufio"
	"os"
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
