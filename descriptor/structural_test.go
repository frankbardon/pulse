package descriptor

import (
	"os"
	"strings"
	"testing"
)

// TestPredictNoExecutionImports verifies that the predict source files
// do not import the service or processing packages (both contain
// execution paths). The no-execute structural ban keeps predict header-
// and schema-only.
func TestPredictNoExecutionImports(t *testing.T) {
	files := []string{
		"predict.go",
		"predict_window.go",
		"predict_feature.go",
		"predict_suggestions.go",
		"defaults.go",
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}

		source := string(data)

		// Must not import the service package.
		banned := []string{
			`"github.com/frankbardon/pulse/service"`,
			`"github.com/frankbardon/pulse/processing"`,
		}
		for _, b := range banned {
			if strings.Contains(source, b) {
				t.Errorf("%s imports banned package: %s", file, b)
			}
		}
	}
}

// TestDescriptorNoFmtSprintf verifies that no source file in the descriptor
// package uses fmt.Sprintf to construct JSON or output strings.
func TestDescriptorNoFmtSprintf(t *testing.T) {
	files := []string{
		"envelope.go",
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
