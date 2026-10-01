package descriptor

import (
	"os"
	"strings"
	"testing"
)

// publicDir is the public descriptor package, which keeps the result and
// envelope types this internal twin builds (paths are relative to this
// package directory, the test's working directory).
const publicDir = "../../descriptor/"

// TestPredictNoExecutionImports verifies that the predict source files
// do not import the service or processing packages (both contain
// execution paths). The no-execute structural ban keeps predict header-
// and schema-only. It covers the predict implementation here and the
// public predict result types in descriptor/.
func TestPredictNoExecutionImports(t *testing.T) {
	files := []string{
		"predict.go",
		"predict_window.go",
		"predict_feature.go",
		"predict_suggestions.go",
		"defaults.go",
		publicDir + "predict.go",
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}

		source := string(data)

		// Must not import the service package or any part of the engine.
		// The processing entry is an unterminated prefix so the
		// feature/, window/, regression/ and arena/ subpackages are
		// banned along with the root engine package.
		banned := []string{
			`"github.com/frankbardon/pulse/internal/service"`,
			`"github.com/frankbardon/pulse/internal/processing`,
		}
		for _, b := range banned {
			if strings.Contains(source, b) {
				t.Errorf("%s imports banned package: %s", file, b)
			}
		}
	}
}

// TestDescriptorNoFmtSprintf verifies that no source file in the descriptor
// packages (this twin and the public descriptor/) uses fmt.Sprintf to
// construct JSON or output strings.
func TestDescriptorNoFmtSprintf(t *testing.T) {
	files := []string{
		publicDir + "envelope.go",
		publicDir + "manifest.go",
		publicDir + "predict.go",
		publicDir + "inspect.go",
		"manifest.go",
		"predict.go",
		"predict_window.go",
		"predict_feature.go",
		"predict_suggestions.go",
		"inspect.go",
		"defaults.go",
	}

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}

		source := string(data)
		if strings.Contains(source, "fmt.Sprintf") {
			t.Errorf("%s uses fmt.Sprintf (use encoding/json or string concatenation instead)", file)
		}
	}
}
