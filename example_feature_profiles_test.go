package pulse

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/fs"
)

// wantExampleFeatureProfiles is the published example set. Examples are
// frozen once shipped: a new example adds a name here, it never replaces
// one.
var wantExampleFeatureProfiles = []string{"minimal", "read-only-analyst", "survey-crosstab"}

func TestExampleFeatureProfiles_Names(t *testing.T) {
	got := ExampleFeatureProfiles()
	if !slices.Equal(got, wantExampleFeatureProfiles) {
		t.Fatalf("ExampleFeatureProfiles() = %v, want %v", got, wantExampleFeatureProfiles)
	}
	// The embedded set is exactly the repo's examples/profiles/*.json.
	files, err := filepath.Glob(filepath.Join("examples", "profiles", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []string
	for _, f := range files {
		onDisk = append(onDisk, filepath.Base(f[:len(f)-len(".json")]))
	}
	sort.Strings(onDisk)
	if !slices.Equal(got, onDisk) {
		t.Fatalf("embedded examples %v != examples/profiles %v", got, onDisk)
	}
}

// TestExampleFeatureProfiles_ValidateInNew asserts every published
// example decodes strictly, passes pulse.New validation and enables
// exactly its list.
func TestExampleFeatureProfiles_ValidateInNew(t *testing.T) {
	for _, name := range ExampleFeatureProfiles() {
		t.Run(name, func(t *testing.T) {
			fp, err := ExampleFeatureProfile(name)
			if err != nil {
				t.Fatalf("ExampleFeatureProfile(%q): %v", name, err)
			}
			if fp.Profile != name {
				t.Errorf("profile label = %q, want %q", fp.Profile, name)
			}
			p, err := New(Options{FS: fs.NewMemMap().Fs(), FeatureProfile: fp})
			if err != nil {
				t.Fatalf("pulse.New rejected example %q: %v", name, err)
			}
			want := append([]string(nil), fp.Features...)
			sort.Strings(want)
			if got := p.svc.InstanceSnapshot().EnabledNames(); !slices.Equal(got, want) {
				t.Errorf("enabled = %v, want %v", got, want)
			}
		})
	}
}

// TestExampleFeatureProfiles_Shape pins the file form: strict JSON
// byte-identical to a canonical re-encode, written_with a release that
// reaches every listed feature, built-in names only, canonical order.
func TestExampleFeatureProfiles_Shape(t *testing.T) {
	for _, name := range ExampleFeatureProfiles() {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("examples", "profiles", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			fp, err := ParseFeatureProfile(raw)
			if err != nil {
				t.Fatalf("ParseFeatureProfile: %v", err)
			}
			if _, _, _, ok := descx.ParseSince(fp.WrittenWith); !ok {
				t.Fatalf("written_with %q is not major.minor.patch", fp.WrittenWith)
			}
			for _, f := range fp.Features {
				row, ok := descx.LookupFeature(f)
				if !ok {
					t.Errorf("%s is not a built-in feature", f)
					continue
				}
				if !descx.SinceReached(row.Since, fp.WrittenWith) {
					t.Errorf("%s (Since %s) is newer than written_with %s", f, row.Since, fp.WrittenWith)
				}
			}
			if sorted := descx.SortFeatureNames(fp.Features); !slices.Equal(fp.Features, sorted) {
				t.Errorf("features not in canonical order:\n got %v\nwant %v", fp.Features, sorted)
			}
			if fp.Behaviour != nil {
				t.Errorf("example carries behaviour %+v; examples list features only", fp.Behaviour)
			}
			type file struct {
				Profile     string   `json:"profile"`
				WrittenWith string   `json:"written_with"`
				Features    []string `json:"features"`
			}
			canon, err := json.MarshalIndent(file{fp.Profile, fp.WrittenWith, fp.Features}, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(raw, append(canon, '\n')) {
				t.Errorf("examples/profiles/%s.json is not in canonical form (2-space indent, profile/written_with/features, trailing newline)", name)
			}
		})
	}
}

// TestExampleFeatureProfiles_Content pins what each example promises.
func TestExampleFeatureProfiles_Content(t *testing.T) {
	load := func(name string) map[string]bool {
		t.Helper()
		fp, err := ExampleFeatureProfile(name)
		if err != nil {
			t.Fatal(err)
		}
		set := map[string]bool{}
		for _, f := range fp.Features {
			set[f] = true
		}
		return set
	}

	minimal := load("minimal")
	for _, f := range []string{"capability:process", "AGG_COUNT", "AGG_SUM", "GROUP_CATEGORY", "GROUP_RANGE"} {
		if !minimal[f] {
			t.Errorf("minimal lacks %s", f)
		}
	}

	survey := load("survey-crosstab")
	for _, f := range []string{
		"capability:crosstab", "capability:facet", "capability:labels", "capability:weighting",
		"TEST_CHISQ", "TEST_T", "OVERLAY_SHARE_OF_ROW", "OVERLAY_PAIRWISE_WELCH_T", "AGG_WELFORD",
	} {
		if !survey[f] {
			t.Errorf("survey-crosstab lacks %s", f)
		}
	}

	// read-only-analyst: every built-in feature as of its written_with,
	// minus the data-writing capabilities and the I/O formats only they
	// use.
	ro := load("read-only-analyst")
	excluded := map[string]bool{
		"capability:import": true, "capability:export": true, "capability:filter_to_file": true,
		"capability:dedup": true, "capability:widen": true, "capability:shard": true,
		"capability:index": true, "capability:synth": true,
	}
	fp, _ := ExampleFeatureProfile("read-only-analyst")
	for _, f := range descx.Features() {
		if !descx.SinceReached(f.Since, fp.WrittenWith) {
			continue
		}
		wantIn := !excluded[f.Name] && f.Kind != descx.FeatureKindIOFormat
		if ro[f.Name] != wantIn {
			t.Errorf("read-only-analyst: %s present=%v, want %v", f.Name, ro[f.Name], wantIn)
		}
	}
}

func TestExampleFeatureProfile_FreshCopy(t *testing.T) {
	a, err := ExampleFeatureProfile("minimal")
	if err != nil {
		t.Fatal(err)
	}
	a.Features[0] = "mutated"
	b, err := ExampleFeatureProfile("minimal")
	if err != nil {
		t.Fatal(err)
	}
	if b.Features[0] == "mutated" {
		t.Fatal("ExampleFeatureProfile shares state between calls")
	}
}

func TestExampleFeatureProfile_UnknownName(t *testing.T) {
	for _, name := range []string{"nope", "", "minimal.json", "../minimal", "MINIMAL"} {
		fp, err := ExampleFeatureProfile(name)
		if fp != nil || err == nil {
			t.Fatalf("ExampleFeatureProfile(%q) = (%v, %v), want an error", name, fp, err)
		}
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_FEATURE_PROFILE_INVALID {
			t.Fatalf("ExampleFeatureProfile(%q) error = %v, want PULSE_FEATURE_PROFILE_INVALID", name, err)
		}
		if ce.Details["reason"] != featureProfileReasonUnknownExample {
			t.Errorf("reason = %v, want %q", ce.Details["reason"], featureProfileReasonUnknownExample)
		}
		if ce.Details["name"] != name {
			t.Errorf("name detail = %v, want %q", ce.Details["name"], name)
		}
		if got, _ := ce.Details["examples"].([]string); !slices.Equal(got, wantExampleFeatureProfiles) {
			t.Errorf("examples detail = %v, want %v", ce.Details["examples"], wantExampleFeatureProfiles)
		}
	}
}
