package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// zoneShard writes a small cohort — a datetime and a categorical `tz`
// of valid zones — declaring zone as its default ("" none).
func zoneShard(t *testing.T, fsys afero.Fs, path, zone string, zones ...string) []byte {
	t.Helper()
	d := encoding.NewDictionary()
	for _, z := range zones {
		if _, err := d.Add(z); err != nil {
			t.Fatal(err)
		}
	}
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "at", Type: encoding.FieldTypeDateTime},
		{Name: "tz", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
	}, ZoneField: zone}
	encx.LayoutFields(s)
	var b bytes.Buffer
	if err := encx.WritePreamble(&b, s); err != nil {
		t.Fatal(err)
	}
	for i := range zones {
		if err := encoding.WriteFieldValue(&b, encoding.FieldTypeDateTime, uint64(1_700_000_000+3600*i)); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteFieldValue(&b, encoding.FieldTypeCategoricalU8, uint64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := afero.WriteFile(fsys, path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// archiveShardSchemas reads the canonical schema and every shard
// payload's own schema out of the archive at path.
func archiveShardSchemas(t *testing.T, fsys afero.Fs, path string) (*encoding.Schema, map[string]*encoding.Schema, map[string][]byte) {
	t.Helper()
	a, err := openArchiveFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	doc, err := readSchemaDocEntry(a.arch)
	if err != nil {
		t.Fatal(err)
	}
	schemas, payloads := map[string]*encoding.Schema{}, map[string][]byte{}
	for _, e := range a.arch.Entries() {
		if e.Name == encx.ReservedSchemaName {
			continue
		}
		b, err := readEntryBytes(a.arch, e.Name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := readSinglePulseSchema(b)
		if err != nil {
			t.Fatal(err)
		}
		schemas[e.Name], payloads[e.Name] = s, b
	}
	return doc.Schema, schemas, payloads
}

// TestSetCohortZoneField_Archive: an archive set rewrites the canonical
// schema AND every shard payload (shards must agree), warns
// PULSE_SHARD_ZONE_REWRITTEN, verifies clean, and a clear returns every
// shard payload to its original bytes.
func TestSetCohortZoneField_Archive(t *testing.T) {
	t.Parallel()

	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	ctx := context.Background()
	zoneShard(t, fsys, "a.pulse", "", "UTC", "Europe/Berlin")
	zoneShard(t, fsys, "b.pulse", "", "America/New_York")
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	// The STORED payloads (create remaps b onto the dictionary union).
	_, _, stored := archiveShardSchemas(t, fsys, "arch.pulse")
	origA, origB := stored["a.pulse"], stored["b.pulse"]
	res, err := svc.SetCohortZoneField(ctx, "arch.pulse", "tz")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if !res.Changed || res.Layout != CohortLayoutShardArchive || res.ShardsRewritten != 2 ||
		len(res.Warnings) != 1 || res.Warnings[0].Code != string(errors.PULSE_SHARD_ZONE_REWRITTEN) ||
		res.Warnings[0].Details["shards_rewritten"] != 2 {
		t.Fatalf("set result %+v", res)
	}
	canon, shards, _ := archiveShardSchemas(t, fsys, "arch.pulse")
	if canon.ZoneField != "tz" {
		t.Fatalf("canonical zone %q", canon.ZoneField)
	}
	for name, s := range shards {
		if s.ZoneField != "tz" {
			t.Errorf("shard %s zone %q, want tz", name, s.ZoneField)
		}
	}
	v, err := svc.VerifyShardArchive(ctx, "arch.pulse")
	if err != nil || len(v.Errors) != 0 {
		t.Fatalf("verify after set: %v %+v", err, v)
	}
	again, err := svc.SetCohortZoneField(ctx, "arch.pulse", "tz")
	if err != nil || again.Changed || len(again.Warnings) != 0 {
		t.Fatalf("re-set: %+v %v, want an unchanged no-op", again, err)
	}
	if _, err := svc.SetCohortZoneField(ctx, "arch.pulse", ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	canon, _, payloads := archiveShardSchemas(t, fsys, "arch.pulse")
	if canon.ZoneField != "" || !bytes.Equal(payloads["a.pulse"], origA) || !bytes.Equal(payloads["b.pulse"], origB) {
		t.Fatalf("set then clear: canonical %q, shard payloads restored a=%v b=%v",
			canon.ZoneField, bytes.Equal(payloads["a.pulse"], origA), bytes.Equal(payloads["b.pulse"], origB))
	}
}

// TestSetCohortZoneField_ArchiveAtomic: a failed publish leaves the
// archive byte-identical.
func TestSetCohortZoneField_ArchiveAtomic(t *testing.T) {
	t.Parallel()

	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	ctx := context.Background()
	zoneShard(t, fsys, "a.pulse", "", "UTC")
	if _, err := New(cfg).CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	before, _ := afero.ReadFile(fsys, "arch.pulse")
	failing, err := fs.New(fs.WithFs(&failingRenameFs{Fs: fsys}))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(failing)
	if _, err := svc.SetCohortZoneField(ctx, "arch.pulse", "tz"); err == nil {
		t.Fatal("set over a failing rename succeeded")
	}
	after, _ := afero.ReadFile(fsys, "arch.pulse")
	if !bytes.Equal(before, after) {
		t.Fatal("a failed archive zone rewrite changed the archive")
	}
}

// TestSetCohortZoneField_Refusals: an anchored shard and a bad field
// keep SERVICE_VALIDATION with details.option "zone_field".
func TestSetCohortZoneField_Refusals(t *testing.T) {
	t.Parallel()

	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	ctx := context.Background()
	zoneShard(t, fsys, "a.pulse", "", "UTC")
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ path, field string }{
		"anchored shard":          {"arch.pulse#a.pulse", "tz"},
		"archive non-categorical": {"arch.pulse", "at"},
		"file missing field":      {"a.pulse", "nope"},
	} {
		_, err := svc.SetCohortZoneField(ctx, tc.path, tc.field)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["option"] != "zone_field" {
			t.Errorf("%s: %v, want SERVICE_VALIDATION option zone_field", name, err)
		}
	}
	if _, err := svc.SetCohortZoneField(ctx, "missing.pulse", "tz"); !errors.HasCode(err, errors.SERVICE_RESOURCE) {
		t.Errorf("missing cohort: %v, want SERVICE_RESOURCE", err)
	}
}

// TestShardZoneDisagreement_Refused: create and add refuse shards whose
// default zone fields disagree (PULSE_SHARD_SCHEMA_MISMATCH, dimension
// zone_field), and the append pre-check (the CohortBuilder's) reaches
// the same verdict as the add itself.
func TestShardZoneDisagreement_Refused(t *testing.T) {
	t.Parallel()

	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	ctx := context.Background()
	zoneShard(t, fsys, "declared.pulse", "tz", "UTC")
	zoneShard(t, fsys, "plain.pulse", "", "UTC")
	zoneShard(t, fsys, "declared2.pulse", "tz", "Europe/Berlin")
	isZoneMismatch := func(err error) bool {
		var ce *errors.CodedError
		return stderrors.As(err, &ce) && ce.Code == errors.PULSE_SHARD_SCHEMA_MISMATCH && ce.Details["dimension"] == "zone_field"
	}
	if _, err := svc.CreateShardArchive(ctx, "mixed.pulse", []string{"declared.pulse", "plain.pulse"}); !isZoneMismatch(err) {
		t.Fatalf("create over disagreeing shards: %v", err)
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"declared.pulse", "declared2.pulse"}); err != nil {
		t.Fatalf("create over agreeing shards: %v", err)
	}
	canon, shards, _ := archiveShardSchemas(t, fsys, "arch.pulse")
	if canon.ZoneField != "tz" || shards["declared2.pulse"].ZoneField != "tz" {
		t.Fatalf("created archive dropped the default: canonical %q", canon.ZoneField)
	}
	_, addErr := svc.AddShard(ctx, "arch.pulse", "plain.pulse")
	if !isZoneMismatch(addErr) {
		t.Fatalf("add of a disagreeing shard: %v", addErr)
	}
	plain, err := readSinglePulseSchema(zoneShard(t, fsys, "plain.pulse", "", "UTC"))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.PrecheckShardCohesion(ctx, "arch.pulse", plain); !isZoneMismatch(err) {
		t.Fatalf("precheck: %v, want the add's own verdict", err)
	}
}
