package pulse_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// typeOnlyWidthCalls are the call names that size a field from its TYPE
// alone. FieldType.ByteSize is matched as any zero-argument `.ByteSize()`
// selector call (FieldType is the module's only ByteSize method — the
// gate re-checks that below). CellWidth and onWireWidth are the retired
// type-only stride tables: every stride site now calls the ONE
// field-aware width, encoding.Schema.FieldWidth, so any call to either
// name is a reintroduction. fixedWidthBytes is the decoders' scalar
// decode width, legitimate only inside the per-type decode arms.
var typeOnlyWidthCalls = map[string]bool{
	"CellWidth":       true,
	"onWireWidth":     true,
	"fixedWidthBytes": true,
}

// typeOnlyWidthHome is the file that DEFINES FieldType.ByteSize; it is
// the only file not scanned.
const typeOnlyWidthHome = "encoding/field_type.go"

// typeOnlyWidthAllowed is the explicit allow-list: file (slash path
// relative to the module root) -> enclosing function -> why a type-only
// width is correct there. Every entry is a VALUE-encoding or per-type
// dispatch site that sizes a scalar or a set rung, never a stride,
// offset or skip over a row or group entry. A stride/offset/skip site
// never belongs here: it calls encoding.Schema.FieldWidth. A stale entry
// (no matching call) fails the gate, so the list cannot rot.
var typeOnlyWidthAllowed = map[string]map[string]string{
	"encoding/schema.go": {
		"FieldWidth": "the field-aware width itself: a scalar field's width is its type's",
	},
	"encoding/set_mask.go": {
		"wideSetWidth": "wide set rung's bitmask width for the SetMask value API",
	},
	// The decoders' generic scalar arm: bytes decodeFixed consumes for a
	// scalar type, and its unknown-type check. TestOnWireWidthMatchesByteSize
	// (internal/encoding) pins fixedWidthBytes == Schema.FieldWidth for
	// every type it answers, so the arm's cursor and the stride agree.
	"internal/encoding/reader_fast.go": {
		"decodeStrideIndexed": "scalar decode arm (decodeFixed width)",
		"decodeGroupBytes":    "scalar decode arm (decodeFixed width)",
	},
	"internal/encoding/reader_runskip.go": {
		"writeFieldBytes": "scalar decode arm: unknown-type check before decodeFixed",
	},
	"internal/encoding/shard_rewrite.go": {
		"applyRemap": "wide set rung's bitmask width for the SetMask remap",
	},
	"internal/encoding/widen.go": {
		"readSetMaskSlot":  "set rung's bitmask width (set-only codec)",
		"writeSetMaskSlot": "set rung's bitmask width (set-only codec)",
	},
	"internal/io/cell_encode.go": {
		"wideSetCell": "wide set rung's bitmask width for PutSetMask",
	},
	"internal/processing/index_key.go": {
		"numericOnWireBytes":    "scalar index-key value encoding (u8..u64, categorical id)",
		"ResolveLookupKeyBytes": "categorical dictionary id value encoding",
	},
	"internal/processing/record.go": {
		"maskWordsAt":   "wide set rung's uint64 word count",
		"putMaskAt":     "wide set rung's uint64 word count",
		"copyTypedWide": "wide set rung's uint64 word count",
	},
	"internal/processing/record_layout.go": {
		"wideKindOf": "wide set rung's uint64 word count",
	},
}

// typeOnlyWidthSkipDirs are directory base names never descended into.
var typeOnlyWidthSkipDirs = map[string]bool{
	"vendor":       true,
	"testdata":     true,
	"node_modules": true,
}

// TestNoTypeOnlyWidthInStridePaths walks every non-test Go file in the
// module (cmd/ and internal/embeddersmoke/ included) and fails
// on any type-only width call — `.ByteSize()`, `CellWidth(`,
// `onWireWidth(`, `fixedWidthBytes(` — outside typeOnlyWidthAllowed.
// Every stride, offset and skip is a sum of encoding.Schema.FieldWidth:
// a field's width is a property of the field within its schema, and a
// site that sizes it from the type byte alone silently misaligns every
// field after it the day the two differ.
func TestNoTypeOnlyWidthInStridePaths(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	scanned := 0
	hit := map[string]map[string]bool{}
	byteSizeOwners := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(path)
		if d.IsDir() {
			base := d.Name()
			if rel != "." && (strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") || typeOnlyWidthSkipDirs[base]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		f, err := parser.ParseFile(fset, rel, src, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("%s: parse: %v", rel, err)
			return nil
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if ok && fd.Name.Name == "ByteSize" && fd.Recv != nil {
				byteSizeOwners[rel] = true
			}
		}
		if rel == typeOnlyWidthHome {
			return nil
		}
		for _, v := range typeOnlyWidthViolations(fset, f) {
			allowed := typeOnlyWidthAllowed[rel][v.fn]
			if allowed == "" {
				t.Errorf("%s: %s calls %s — a type-only width; size the field with encoding.Schema.FieldWidth (or, for a value-encoding / per-type dispatch site that is not a stride, add an explained typeOnlyWidthAllowed entry)", v.pos, v.fn, v.call)
				continue
			}
			if hit[rel] == nil {
				hit[rel] = map[string]bool{}
			}
			hit[rel][v.fn] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d production files; the walk is not reaching the module", scanned)
	}
	if len(byteSizeOwners) != 1 || !byteSizeOwners[typeOnlyWidthHome] {
		t.Errorf("ByteSize methods declared in %v; the gate matches every `.ByteSize()` call as FieldType's and needs it to be the only one (in %s)", keysOf(byteSizeOwners), typeOnlyWidthHome)
	}
	for file, fns := range typeOnlyWidthAllowed {
		for fn := range fns {
			if !hit[file][fn] {
				t.Errorf("stale typeOnlyWidthAllowed entry %s:%s — no type-only width call there any more; delete it", file, fn)
			}
		}
	}
}

type typeOnlyWidthViolation struct {
	pos  token.Position
	fn   string
	call string
}

// typeOnlyWidthViolations returns every type-only width call in f with
// its enclosing top-level function ("" at file scope).
func typeOnlyWidthViolations(fset *token.FileSet, f *ast.File) []typeOnlyWidthViolation {
	var out []typeOnlyWidthViolation
	for _, decl := range f.Decls {
		fn := ""
		if fd, ok := decl.(*ast.FuncDecl); ok {
			fn = fd.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := ""
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				if fun.Sel.Name == "ByteSize" && len(call.Args) == 0 {
					name = ".ByteSize()"
				} else if typeOnlyWidthCalls[fun.Sel.Name] {
					name = fun.Sel.Name + "("
				}
			case *ast.Ident:
				if typeOnlyWidthCalls[fun.Name] {
					name = fun.Name + "("
				}
			}
			if name != "" {
				out = append(out, typeOnlyWidthViolation{pos: fset.Position(call.Pos()), fn: fn, call: name})
			}
			return true
		})
	}
	return out
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestNoTypeOnlyWidthInStridePaths_Detects proves the matcher bites: a
// planted stride sum over each banned spelling is reported, and the
// field-aware spelling is not.
func TestNoTypeOnlyWidthInStridePaths_Detects(t *testing.T) {
	t.Parallel()

	src := `package x
func stride(s *Schema) (n int) {
	for i := range s.Fields {
		n += s.Fields[i].Type.ByteSize()
		n += CellWidth(s.Fields[i].Type)
		n += encx.onWireWidth(s.Fields[i].Type)
		n += fixedWidthBytes(s.Fields[i].Type)
		n += s.FieldWidth(i)
	}
	return n
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "planted.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range typeOnlyWidthViolations(fset, f) {
		got = append(got, fmt.Sprintf("%s@%s", v.call, v.fn))
	}
	want := []string{".ByteSize()@stride", "CellWidth(@stride", "onWireWidth(@stride", "fixedWidthBytes(@stride"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("violations = %v, want %v", got, want)
	}
}
