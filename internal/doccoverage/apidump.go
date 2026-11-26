package doccoverage

import (
	"bufio"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
)

// APIDumpPath is the frozen public API dump the API rows come from.
const APIDumpPath = "internal/apigolden/testdata/public_api.txt"

// apiDecl is one top-level declaration or method in the dump.
type apiDecl struct {
	Pkg    string // dump package path ("pulse", "pulse/types")
	Kind   string // type, func, const, var, method
	Name   string // declared name; for a method the receiver type
	Method string
}

// apiField is one struct field in the dump.
type apiField struct {
	Pkg, Type, Name, Tag string
}

type apiDump struct {
	pkgNames map[string]string // dump path -> package name
	decls    []apiDecl
	fields   []apiField
}

// parseAPIDump reads the line-oriented dump (internal/apigolden's format:
// "<pkg>: <fact>").
func parseAPIDump(r io.Reader) (*apiDump, error) {
	d := &apiDump{pkgNames: map[string]string{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		pkg, fact, ok := strings.Cut(line, ": ")
		if !ok {
			return nil, fmt.Errorf("%s:%d: want \"<pkg>: <fact>\"", APIDumpPath, n)
		}
		word, rest, _ := strings.Cut(fact, " ")
		switch word {
		case "package":
			d.pkgNames[pkg] = rest
		case "func", "const", "var":
			d.decls = append(d.decls, apiDecl{Pkg: pkg, Kind: word, Name: leadingIdent(rest)})
		case "type":
			name := leadingIdent(rest)
			tail := strings.TrimSpace(skipTypeParams(rest[len(name):]))
			member, after, _ := strings.Cut(tail, " ")
			switch member {
			case "underlying", "imethod", "embed":
			case "field":
				fname, ftail, _ := strings.Cut(after, " ")
				var tag string
				if i := strings.LastIndex(ftail, ` "`); i >= 0 && strings.HasSuffix(ftail, `"`) {
					if u, err := strconv.Unquote(ftail[i+1:]); err == nil {
						tag = u
					}
				}
				d.fields = append(d.fields, apiField{Pkg: pkg, Type: name, Name: fname, Tag: tag})
			case "method":
				_, sig, ok := strings.Cut(after, ") ")
				if !ok {
					return nil, fmt.Errorf("%s:%d: method without receiver", APIDumpPath, n)
				}
				d.decls = append(d.decls, apiDecl{Pkg: pkg, Kind: "method", Name: name, Method: leadingIdent(sig)})
			default:
				d.decls = append(d.decls, apiDecl{Pkg: pkg, Kind: "type", Name: name})
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(d.pkgNames) == 0 {
		return nil, fmt.Errorf("%s: no package lines", APIDumpPath)
	}
	return d, nil
}

func leadingIdent(s string) string {
	i := 0
	for i < len(s) && isWord(s[i]) {
		i++
	}
	return s[:i]
}

// skipTypeParams drops a leading "[T any]" type-parameter list.
func skipTypeParams(s string) string {
	if !strings.HasPrefix(s, "[") {
		return s
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return s[i+1:]
			}
		}
	}
	return s
}

// jsonKey is a struct tag's JSON name; "" for an untagged or "-" field.
func jsonKey(tag string) string {
	v := reflect.StructTag(tag).Get("json")
	name, _, _ := strings.Cut(v, ",")
	if name == "-" {
		return ""
	}
	return name
}

// apiRows turns the dump into the API, Options, Request-slot and
// Response-slot rows, deciding each GoDoc cell from the source.
func apiRows(d *apiDump, src *goSource) ([]Row, error) {
	var rows []Row
	for _, a := range d.decls {
		dir, ok := dirOf(a.Pkg)
		if !ok {
			return nil, fmt.Errorf("doccoverage: dump package %q is outside the module", a.Pkg)
		}
		pkgName := d.pkgNames[a.Pkg]
		id := pkgName + "." + a.Name
		var doc bool
		var err error
		switch a.Kind {
		case "type":
			doc, err = src.typeDoc(dir, a.Name)
		case "func":
			doc, err = src.funcDoc(dir, a.Name)
		case "const", "var":
			doc, err = src.valueDoc(dir, a.Name)
		case "method":
			id += "." + a.Method
			doc, err = src.methodDoc(dir, a.Name, a.Method)
		}
		if err != nil {
			return nil, err
		}
		rows = append(rows, Row{Kind: KindAPI, Group: a.Pkg, ID: id, GoDoc: doc})
	}
	for _, f := range d.fields {
		var kind, id string
		switch {
		case f.Pkg == "pulse" && f.Type == "Options":
			kind, id = KindOptions, f.Name
		case f.Pkg == "pulse/types" && f.Type == "Request":
			kind, id = KindRequestSlot, jsonKey(f.Tag)
		case f.Pkg == "pulse/types" && f.Type == "Response":
			kind, id = KindResponseSlot, jsonKey(f.Tag)
		default:
			continue
		}
		if id == "" {
			continue
		}
		dir, _ := dirOf(f.Pkg)
		doc, err := src.fieldDoc(dir, f.Type, f.Name)
		if err != nil {
			return nil, err
		}
		r := Row{Kind: kind, ID: id, GoDoc: doc}
		if kind != KindOptions {
			r.Field = f.Name
		}
		rows = append(rows, r)
	}
	return rows, nil
}
