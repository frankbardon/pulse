package integration_test

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/vectors"
)

// jsonTagName captures the key of a `json:"key..."` struct tag.
var jsonTagName = regexp.MustCompile("json:\"([a-z0-9_]+)")

// fixupIndexSegment is an abstract (`*`) or concrete slice index.
var fixupIndexSegment = regexp.MustCompile(`^(\*|[0-9]+)$`)

// nestedParamKeys are wire keys inside an operator's open `params`
// object that no struct tag or manifest param name spells.
var nestedParamKeys = map[string]string{
	vectors.ReferenceRecord: "params.reference.record — a comparison attribute's stored-record reference",
}

// TestFixupPathsAreWireKeys: every Fixup.Path segment in the errors
// metadata table is an index (`*` or digits) or a wire key — a JSON tag
// some payload / manifest / MCP struct declares, a manifest-declared
// operator param name, or a nestedParamKeys entry. A Go field name
// (`Crosstab`, `SplitBy`) is never a segment: a path addresses the
// JSON a caller writes, as every `details.path` does (#797). A fixup
// whose fix lives on a Go-only surface (a pulse.Options field, a CLI
// flag, an extension registration func) carries no Path; its Hint names
// the field.
func TestFixupPathsAreWireKeys(t *testing.T) {
	t.Parallel()

	keys := wireKeyUniverse(t)
	checked := 0
	for _, c := range perrors.AllCodes() {
		md, _ := perrors.MetadataFor(c)
		for i, fx := range md.Fixups {
			for _, seg := range fx.Path {
				checked++
				if fixupIndexSegment.MatchString(seg) || keys[seg] {
					continue
				}
				t.Errorf("%s fixup %d path %q: segment %q is not a wire key or an index", c, i, fx.Path, seg)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no Fixup.Path segment found; the gate checks nothing")
	}
}

// wireKeyUniverse is every JSON tag key a non-test Go file of the
// module declares, every param name the manifest declares and the
// nestedParamKeys.
func wireKeyUniverse(t *testing.T) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	for _, path := range findGoFiles(t, ".") {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range jsonTagName.FindAllSubmatch(src, -1) {
			keys[string(m[1])] = true
		}
	}
	collectParamNames(reflect.ValueOf(descx.BuildManifestForInstance(nil)), keys)
	for k := range nestedParamKeys {
		keys[k] = true
	}
	return keys
}

// paramType is the manifest's declared-param shape.
var paramType = reflect.TypeOf(descriptor.Param{})

// collectParamNames adds the Name of every descriptor.Param reachable
// from v.
func collectParamNames(v reflect.Value, out map[string]bool) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			collectParamNames(v.Elem(), out)
		}
	case reflect.Struct:
		if v.Type() == paramType {
			out[v.FieldByName("Name").String()] = true
		}
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				collectParamNames(v.Field(i), out)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			collectParamNames(v.Index(i), out)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			collectParamNames(v.MapIndex(k), out)
		}
	}
}
