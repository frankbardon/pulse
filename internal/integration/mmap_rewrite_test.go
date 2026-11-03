package integration_test

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// mmap_rewrite_test.go pins U77 E2-S1's rewrite audit: a cohort Pulse
// has just read through its mmap fast path (a DataDir instance, so the
// read resolves a real path and maps the file — on Windows too, since
// mmap_windows.go) can then be rewritten in place by every atomic
// rewrite that targets a single-file cohort, and every archive rewrite
// runs after an archive read. A mapping Pulse took for a read is
// released before the operation returns, so it can never hold the
// rename that publishes a rewrite. On unix a rename over a mapped file
// succeeds anyway, so the test bites on the Windows leg, where a view
// still mapped refuses the rename (mmap_held_view_windows_test.go pins
// what such a held view does).

const mmapRewriteRows = 64

// mmapRewriteSchema: id u32, a constant categorical (so dedup has a
// constant to elide) and a set_u8 (so widen has a rung to climb).
func mmapRewriteSchema() encoding.Schema {
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "tags", Type: encoding.FieldTypeSetU8},
	}}
}

// mmapWriteCohort writes (or overwrites) a single-file cohort of rows
// records at name.
func mmapWriteCohort(t *testing.T, p *pulse.Pulse, name string, rows int) {
	t.Helper()
	b, err := p.NewCohortBuilder(context.Background(), name, mmapRewriteSchema(), pulse.CohortBuilderOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("NewCohortBuilder(%s): %v", name, err)
	}
	tags := [][]string{{}, {"a"}, {"a", "b"}, {"c"}}
	for i := range rows {
		if err := b.Append(pulse.CohortRow{uint64(i), "north", tags[i%len(tags)]}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatalf("Close(%s): %v", name, err)
	}
}

// mmapCount runs a Process count over name: on a DataDir instance the
// read maps a single-file cohort (stream.go's mmap fast path).
func mmapCount(t *testing.T, p *pulse.Pulse, name string) int64 {
	t.Helper()
	resp, err := p.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: name},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
	})
	if err != nil {
		t.Fatalf("Process(%s): %v", name, err)
	}
	return zepCount(t, resp.Data[0]["n"])
}

// mmapRewriteCase is one atomic rewrite: setup writes its inputs under a
// fresh DataDir, target is the single-file cohort the rewrite replaces
// ("" for an archive rewrite), run performs it, check verifies the
// result and refused is the code its rename refusal carries.
type mmapRewriteCase struct {
	name   string
	target string
	setup  func(t *testing.T, p *pulse.Pulse)
	run    func(ctx context.Context, p *pulse.Pulse) error
	check  func(t *testing.T, p *pulse.Pulse)
	// refused is the code the rewrite's rename refusal carries when
	// another reader holds a view of target (Windows).
	refused perr.Code
}

func mmapRewriteCases() []mmapRewriteCase {
	one := func(name string) func(t *testing.T, p *pulse.Pulse) {
		return func(t *testing.T, p *pulse.Pulse) { mmapWriteCohort(t, p, name, mmapRewriteRows) }
	}
	two := func(a, b string) func(t *testing.T, p *pulse.Pulse) {
		return func(t *testing.T, p *pulse.Pulse) {
			mmapWriteCohort(t, p, a, mmapRewriteRows)
			mmapWriteCohort(t, p, b, mmapRewriteRows/2)
		}
	}
	countIs := func(name string, want int64) func(t *testing.T, p *pulse.Pulse) {
		return func(t *testing.T, p *pulse.Pulse) {
			t.Helper()
			if got := mmapCount(t, p, name); got != want {
				t.Errorf("%s count = %d, want %d", name, got, want)
			}
		}
	}
	archive := func(t *testing.T, p *pulse.Pulse) {
		mmapWriteCohort(t, p, "s1.pulse", mmapRewriteRows)
		mmapWriteCohort(t, p, "s2.pulse", mmapRewriteRows/2)
		mmapCount(t, p, "s1.pulse")
		mmapCount(t, p, "s2.pulse")
		if _, err := p.CreateShardArchive(context.Background(), "arch.pulse", []string{"s1.pulse", "s2.pulse"}); err != nil {
			t.Fatalf("CreateShardArchive: %v", err)
		}
		mmapWriteCohort(t, p, "s3.pulse", 8)
	}
	return []mmapRewriteCase{
		{
			name: "widen_set_field", target: "c.pulse", refused: perr.ENCODING_IO, setup: one("c.pulse"),
			run: func(ctx context.Context, p *pulse.Pulse) error {
				_, err := p.WidenSetField(ctx, "c.pulse", "tags", "set_u16")
				return err
			},
			check: func(t *testing.T, p *pulse.Pulse) {
				c, err := p.Open(context.Background(), "c.pulse")
				if err != nil {
					t.Fatal(err)
				}
				if got := c.Field("tags").Type; got != encoding.FieldTypeSetU16 {
					t.Errorf("tags type = %v after widen, want set_u16", got)
				}
			},
		},
		{
			name: "dedup_in_place", target: "c.pulse", refused: perr.ENCODING_IO, setup: one("c.pulse"),
			run: func(ctx context.Context, p *pulse.Pulse) error {
				_, err := p.Dedup(ctx, "c.pulse", pulse.DedupOptions{ElideConstants: true})
				return err
			},
			check: countIs("c.pulse", mmapRewriteRows),
		},
		{
			name: "materialize_overwrite", target: "c.pulse", refused: perr.ENCODING_IO, setup: two("src.pulse", "c.pulse"),
			run: func(ctx context.Context, p *pulse.Pulse) error {
				_, err := p.Materialize(ctx, &types.Request{
					Cohort:     &types.Cohort{Filename: "src.pulse"},
					Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "id", Label: "z"}},
				}, "c.pulse", pulse.MaterializeOptions{Overwrite: true})
				return err
			},
			check: countIs("c.pulse", mmapRewriteRows),
		},
		{
			name: "materialize_in_place", target: "c.pulse", refused: perr.ENCODING_IO, setup: one("c.pulse"),
			run: func(ctx context.Context, p *pulse.Pulse) error {
				_, err := p.Materialize(ctx, &types.Request{
					Cohort:     &types.Cohort{Filename: "c.pulse"},
					Attributes: []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "id", Label: "z"}},
				}, "c.pulse", pulse.MaterializeOptions{Overwrite: true})
				return err
			},
			check: countIs("c.pulse", mmapRewriteRows),
		},
		{
			name: "builder_overwrite", target: "c.pulse", refused: perr.ENCODING_IO, setup: one("c.pulse"),
			run: func(ctx context.Context, p *pulse.Pulse) error {
				b, err := p.NewCohortBuilder(ctx, "c.pulse", mmapRewriteSchema(), pulse.CohortBuilderOptions{Overwrite: true})
				if err != nil {
					return err
				}
				if err := b.Append(pulse.CohortRow{uint64(7), "south", []string{"b"}}); err != nil {
					_ = b.Abort()
					return err
				}
				_, err = b.Close()
				return err
			},
			check: countIs("c.pulse", 1),
		},
		{
			name: "filter_to_file_overwrite", target: "c.pulse", refused: perr.SERVICE_RESOURCE, setup: two("src.pulse", "c.pulse"),
			run: func(ctx context.Context, p *pulse.Pulse) error {
				_, err := p.FilterToFile(ctx, "src.pulse", "c.pulse", "id < 10")
				return err
			},
			check: countIs("c.pulse", 10),
		},
		{
			name: "filter_to_file_in_place", target: "c.pulse", refused: perr.SERVICE_RESOURCE, setup: one("c.pulse"),
			run: func(ctx context.Context, p *pulse.Pulse) error {
				_, err := p.FilterToFile(ctx, "c.pulse", "c.pulse", "id < 10")
				return err
			},
			check: countIs("c.pulse", 10),
		},
		{
			name: "build_then_drop_index", target: "", setup: one("c.pulse"),
			run: func(ctx context.Context, p *pulse.Pulse) error {
				if _, err := p.BuildIndex(ctx, "c.pulse", []string{"id"}); err != nil {
					return err
				}
				return p.DropIndex(ctx, "c.pulse", []string{"id"})
			},
			check: countIs("c.pulse", mmapRewriteRows),
		},
		{
			name: "shard_add_remove_compact", target: "", setup: archive,
			run: func(ctx context.Context, p *pulse.Pulse) error {
				if _, err := p.AddShard(ctx, "arch.pulse", "s3.pulse"); err != nil {
					return err
				}
				if _, err := p.RemoveShard(ctx, "arch.pulse", "s1.pulse"); err != nil {
					return err
				}
				return p.CompactShardArchive(ctx, "arch.pulse")
			},
			check: countIs("arch.pulse", mmapRewriteRows/2+8),
		},
	}
}

// mmapDirListing is dir's entry names, sorted: a failed rewrite must
// leave no stage, spool or temp file behind.
func mmapDirListing(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	sort.Strings(out)
	return out
}

func mmapNewDataDir(t *testing.T) (*pulse.Pulse, string) {
	t.Helper()
	dir := t.TempDir()
	p, err := pulse.New(pulse.Options{DataDir: dir})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, dir
}

// mmapRewriteAfterMap runs the named case: the rewrite succeeds right
// after Pulse read (and mapped) the cohort it replaces, and leaves no
// staging file behind. On Windows, falsified by dropping the
// UnmapViewOfFile from mmap_windows.go's cleanup (or the iterator's
// Close): the rename over the still-mapped cohort is refused
// (TestMmapHeldView_* shows that refusal). One top-level test per case
// keeps each inside the fast tier's budget on the Windows leg.
func mmapRewriteAfterMap(t *testing.T, name string) {
	t.Helper()
	var c *mmapRewriteCase
	for _, k := range mmapRewriteCases() {
		if k.name == name {
			c = &k
		}
	}
	if c == nil {
		t.Fatalf("no rewrite case %q", name)
	}
	p, dir := mmapNewDataDir(t)
	c.setup(t, p)
	if c.target != "" {
		mmapCount(t, p, c.target)
	}
	before := mmapDirListing(t, dir)
	if err := c.run(context.Background(), p); err != nil {
		t.Fatalf("rewrite after a mapped read: %v", err)
	}
	c.check(t, p)
	after := mmapDirListing(t, dir)
	for _, name := range after {
		if filepath.Ext(name) != ".pulse" && filepath.Ext(name) != ".json" {
			t.Errorf("rewrite left %q behind (before %v, after %v)", name, before, after)
		}
	}
}

// mmapRewriteCaseTests names every case's top-level test below;
// TestMmapRewrite_EveryCaseHasATest keeps the two lists in step.
var mmapRewriteCaseTests = []string{
	"widen_set_field", "dedup_in_place", "materialize_overwrite", "materialize_in_place",
	"builder_overwrite", "filter_to_file_overwrite", "filter_to_file_in_place",
	"build_then_drop_index", "shard_add_remove_compact",
}

func TestMmapRewrite_EveryCaseHasATest(t *testing.T) {
	t.Parallel()
	var names []string
	for _, c := range mmapRewriteCases() {
		names = append(names, c.name)
	}
	if !slices.Equal(names, mmapRewriteCaseTests) {
		t.Errorf("rewrite cases %v, but the per-case tests cover %v", names, mmapRewriteCaseTests)
	}
}

func TestMmapRewrite_AfterMapWiden(t *testing.T) {
	t.Parallel()
	mmapRewriteAfterMap(t, "widen_set_field")
}

func TestMmapRewrite_AfterMapDedup(t *testing.T) {
	t.Parallel()
	mmapRewriteAfterMap(t, "dedup_in_place")
}

func TestMmapRewrite_AfterMapMaterializeOverwrite(t *testing.T) {
	t.Parallel()
	mmapRewriteAfterMap(t, "materialize_overwrite")
}

func TestMmapRewrite_AfterMapMaterializeInPlace(t *testing.T) {
	t.Parallel()
	mmapRewriteAfterMap(t, "materialize_in_place")
}

func TestMmapRewrite_AfterMapBuilderOverwrite(t *testing.T) {
	t.Parallel()
	mmapRewriteAfterMap(t, "builder_overwrite")
}

func TestMmapRewrite_AfterMapFilterToFileOverwrite(t *testing.T) {
	t.Parallel()
	mmapRewriteAfterMap(t, "filter_to_file_overwrite")
}

func TestMmapRewrite_AfterMapFilterToFileInPlace(t *testing.T) {
	t.Parallel()
	mmapRewriteAfterMap(t, "filter_to_file_in_place")
}

func TestMmapRewrite_AfterMapIndexBuildDrop(t *testing.T) {
	t.Parallel()
	mmapRewriteAfterMap(t, "build_then_drop_index")
}

func TestMmapRewrite_AfterMapShardAdmin(t *testing.T) {
	t.Parallel()
	mmapRewriteAfterMap(t, "shard_add_remove_compact")
}

// TestFilterToFileRequest_RenameRefusalIsCoded: when the publishing
// rename is refused — here the output path is a non-empty directory; on
// Windows also a view another reader maps over the output —
// FilterToFileWithRequest fails with ENCODING_IO like every other atomic
// rewrite, the staged file removed, never a raw OS error.
//
// Falsified by restoring the fmt.Errorf wrap in filter_to_file_request.go.
func TestFilterToFileRequest_RenameRefusalIsCoded(t *testing.T) {
	t.Parallel()

	p, dir := mmapNewDataDir(t)
	mmapWriteCohort(t, p, "c.pulse", mmapRewriteRows)
	blocked := filepath.Join(dir, "out", "blocked.pulse")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := p.FilterToFileWithRequest(context.Background(), &pulse.FilterToFileRequest{
		SourcePath: "c.pulse", Expression: "id < 10", OutputDir: "out", OutputName: "blocked.pulse",
	})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.ENCODING_IO {
		t.Fatalf("FilterToFileWithRequest onto a held path = %v, want ENCODING_IO", err)
	}
	if got := mmapDirListing(t, filepath.Join(dir, "out")); !slices.Equal(got, []string{"blocked.pulse"}) {
		t.Errorf("out/ holds %v after the refused rename, want only blocked.pulse", got)
	}
}
