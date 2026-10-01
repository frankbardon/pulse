package pulse_test

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/buildinfo"
)

func TestVersion_ReflectsBuildinfo(t *testing.T) {
	t.Cleanup(buildinfo.SetForTest("v1.2.3-test"))
	if got := pulse.Version(); got != "v1.2.3-test" {
		t.Fatalf("pulse.Version() = %q, want injected %q", got, "v1.2.3-test")
	}
}

func TestVersion_FallbackNeverEmpty(t *testing.T) {
	t.Cleanup(buildinfo.SetForTest(""))
	got := pulse.Version()
	if got == "" {
		t.Fatal("pulse.Version() returned empty string")
	}
	// go test binaries carry no ldflags and report Main.Version "(devel)".
	if got != buildinfo.Devel && !strings.HasPrefix(got, buildinfo.Devel+"+") && !strings.HasPrefix(got, "v") {
		t.Fatalf("pulse.Version() = %q, want devel[+rev] or a module version", got)
	}
}
