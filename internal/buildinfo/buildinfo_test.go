package buildinfo

import (
	"go/build"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

const fullRev = "0123456789abcdef0123456789abcdef01234567"

func stubBuildInfo(t *testing.T, info *debug.BuildInfo, ok bool) {
	t.Helper()
	prev := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) { return info, ok }
	t.Cleanup(func() { readBuildInfo = prev })
}

func withSettings(mainVersion string, kv ...string) *debug.BuildInfo {
	info := &debug.BuildInfo{GoVersion: "go1.99.0", Main: debug.Module{Path: ModulePath, Version: mainVersion}}
	for i := 0; i+1 < len(kv); i += 2 {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
	}
	return info
}

func TestVersionChain(t *testing.T) {
	cases := []struct {
		name    string
		ldflags string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{"ldflags wins over module version", "v1.2.3", withSettings("v9.9.9", "vcs.revision", fullRev), true, "v1.2.3"},
		{"ldflags wins with no build info", "v1.2.3-4-gabc-dirty", nil, false, "v1.2.3-4-gabc-dirty"},
		{"real module version", "", withSettings("v0.40.0", "vcs.revision", fullRev), true, "v0.40.0"},
		{"(devel) falls to devel+short revision", "", withSettings("(devel)", "vcs.revision", fullRev), true, "devel+" + fullRev[:12]},
		{"empty module version falls to devel+short revision", "", withSettings("", "vcs.revision", fullRev), true, "devel+" + fullRev[:12]},
		{"short revision kept whole", "", withSettings("(devel)", "vcs.revision", "abc123"), true, "devel+abc123"},
		{"(devel) without revision is bare devel", "", withSettings("(devel)"), true, "devel"},
		{"no build info is bare devel", "", nil, false, "devel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubBuildInfo(t, tc.info, tc.ok)
			t.Cleanup(SetForTest(tc.ldflags))
			if got := Version(); got != tc.want {
				t.Fatalf("Version() = %q, want %q", got, tc.want)
			}
		})
	}
}

// embedderInfo models Pulse linked into an embedder binary: Main is the
// embedder at embedderVersion, Pulse appears (or not) among deps.
func embedderInfo(embedderVersion string, deps ...*debug.Module) *debug.BuildInfo {
	return &debug.BuildInfo{
		GoVersion: "go1.99.0",
		Main:      debug.Module{Path: "example.com/embedder", Version: embedderVersion},
		Deps:      deps,
		Settings:  []debug.BuildSetting{{Key: "vcs.revision", Value: fullRev}},
	}
}

func TestVersionResolvesPulseModuleNotEmbedder(t *testing.T) {
	other := &debug.Module{Path: "example.com/other", Version: "v5.0.0"}
	cases := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"pulse is main", withSettings("v1.4.0", "vcs.revision", fullRev), "v1.4.0"},
		{"pulse is a versioned dep", embedderInfo("v0.3.0", other, &debug.Module{Path: ModulePath, Version: "v1.2.0"}), "v1.2.0"},
		{"pulse dep replaced with a version", embedderInfo("v0.3.0", &debug.Module{Path: ModulePath, Version: "v1.2.0", Replace: &debug.Module{Path: "example.com/fork", Version: "v1.2.1-fork"}}), "v1.2.1-fork"},
		{"pulse dep replaced by local path is devel", embedderInfo("v0.3.0", &debug.Module{Path: ModulePath, Version: "v1.2.0", Replace: &debug.Module{Path: "../pulse"}}), "devel+" + fullRev[:12]},
		{"pulse absent from deps is devel", embedderInfo("v3.0.0", other), "devel+" + fullRev[:12]},
		{"embedder version never returned with no deps", embedderInfo("v0.3.0"), "devel+" + fullRev[:12]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubBuildInfo(t, tc.info, true)
			t.Cleanup(SetForTest(""))
			got := Version()
			if got != tc.want {
				t.Fatalf("Version() = %q, want %q", got, tc.want)
			}
			if tc.info.Main.Path != ModulePath && got == tc.info.Main.Version {
				t.Fatalf("Version() returned the embedder's Main.Version %q", got)
			}
		})
	}
}

func TestCommitAndCommitTime(t *testing.T) {
	stubBuildInfo(t, withSettings("(devel)", "vcs.revision", fullRev, "vcs.time", "2026-10-01T12:00:00Z"), true)
	if got := Commit(); got != fullRev {
		t.Errorf("Commit() = %q, want %q", got, fullRev)
	}
	if got := CommitTime(); got != "2026-10-01T12:00:00Z" {
		t.Errorf("CommitTime() = %q", got)
	}

	stubBuildInfo(t, withSettings("(devel)"), true)
	if Commit() != "" || CommitTime() != "" {
		t.Errorf("missing settings must yield empty strings, got %q / %q", Commit(), CommitTime())
	}

	stubBuildInfo(t, nil, false)
	if Commit() != "" || CommitTime() != "" {
		t.Errorf("no build info must yield empty strings, got %q / %q", Commit(), CommitTime())
	}
}

func TestGoVersion(t *testing.T) {
	stubBuildInfo(t, withSettings("(devel)"), true)
	if got := GoVersion(); got != "go1.99.0" {
		t.Errorf("GoVersion() = %q, want BuildInfo.GoVersion", got)
	}
	stubBuildInfo(t, nil, false)
	if got := GoVersion(); got != runtime.Version() {
		t.Errorf("GoVersion() = %q, want runtime.Version() %q", got, runtime.Version())
	}
}

func TestSetForTestRestores(t *testing.T) {
	stubBuildInfo(t, nil, false)
	restore := SetForTest("v7.7.7")
	if got := Version(); got != "v7.7.7" {
		t.Fatalf("Version() = %q after SetForTest", got)
	}
	restore()
	if got := Version(); got != Devel {
		t.Fatalf("Version() = %q after restore, want %q", got, Devel)
	}
}

// TestStdlibOnlyImports keeps buildinfo a leaf so descriptor/ can import it
// without breaking TestPredictNoExecutionImports.
func TestStdlibOnlyImports(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		first := strings.SplitN(imp, "/", 2)[0]
		if strings.Contains(first, ".") {
			t.Errorf("buildinfo imports non-stdlib package %q", imp)
		}
	}
}
