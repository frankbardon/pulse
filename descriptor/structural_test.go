package descriptor

import (
	"os"
	"strings"
	"testing"
)

// TestPredictNoExecutionImports verifies that predict.go does not import
// the service package (which contains execution methods).
// This is a structural ban enforced by grep gate.
func TestPredictNoExecutionImports(t *testing.T) {
	data, err := os.ReadFile("predict.go")
	if err != nil {
		t.Fatalf("reading predict.go: %v", err)
	}

	source := string(data)

	// Must not import the service package.
	banned := []string{
		`"github.com/frankbardon/pulse/service"`,
		`"github.com/frankbardon/pulse/processing"`,
	}
	for _, b := range banned {
		if strings.Contains(source, b) {
			t.Errorf("predict.go imports banned package: %s", b)
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
		"inspect.go",
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
