// Package paramread is the ONE way built-in code reads an operator's
// `params` object: every runtime decode of a slot's Params blob (or a
// lookup into an overlay's params map) goes through Decode /
// DecodeOptional / Get / Read, naming the operator it reads for.
//
// Production behaviour is exactly encoding/json's: Decode is
// json.Unmarshal. The package exists so the keys an operator READS are
// observable: while a Recording is active, every call records
// (category, operator, key) — a struct target contributes every
// top-level json key it can decode (all of them, whether the blob set
// them or not), a map lookup the key it looked up. The declared-vs-read
// gate (TestParamsDeclaredMatchRead, root package) runs the example
// library under a Recording and holds every recorded key to the
// operator's manifest Param list, the allowlist strict params decode
// enforces (internal/descriptor ParamsRefusal) — so a manifest list
// cannot fall behind what the runtime reads and cause a false refusal.
// TestParamsReadThroughParamread bans a direct json decode of a slot's
// `.Params` anywhere else.
//
// Leaf package: stdlib only.
package paramread

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Category names an operator family, spelled as the manifest's
// Operator.Category (and "test" / "matrix" / "overlay" for the
// non-Operator catalogs).
type Category string

// The operator families whose slots carry a params object.
const (
	Aggregator Category = "aggregator"
	Attribute  Category = "attribute"
	Filterer   Category = "filterer"
	Grouper    Category = "grouper"
	Window     Category = "window"
	Feature    Category = "feature"
	Test       Category = "test"
	PostTest   Category = "post_test"
	Matrix     Category = "matrix"
	Overlay    Category = "overlay"
)

// Key identifies one operator.
type Key struct {
	Category Category
	Op       string
}

// Recording collects the keys read while it is active.
type Recording struct {
	mu   sync.Mutex
	read map[Key]map[string]bool
}

var active atomic.Pointer[Recording]

// Start begins recording, replacing any active Recording. Test-only:
// production never starts one, so a read costs one atomic load.
func Start() *Recording {
	r := &Recording{read: map[Key]map[string]bool{}}
	active.Store(r)
	return r
}

// Stop ends recording (when r is still the active one) and returns the
// keys read per operator.
func (r *Recording) Stop() map[Key][]string {
	active.CompareAndSwap(r, nil)
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[Key][]string, len(r.read))
	for k, set := range r.read {
		keys := make([]string, 0, len(set))
		for name := range set {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		out[k] = keys
	}
	return out
}

func (r *Recording) add(cat Category, op string, keys []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := Key{Category: cat, Op: op}
	set := r.read[k]
	if set == nil {
		set = map[string]bool{}
		r.read[k] = set
	}
	for _, name := range keys {
		set[name] = true
	}
}

// Read records that the code reading op's params reads keys (a lookup
// into a decoded params map or a raw-object scan). It is the
// declaration Get makes for one key.
func Read(cat Category, op string, keys ...string) {
	if r := active.Load(); r != nil {
		r.add(cat, op, keys)
	}
}

// Declare records every top-level key the decode target v (a pointer
// to a struct) accepts, without decoding — for a site that decodes the
// blob elsewhere or only conditionally.
func Declare(cat Category, op string, v any) {
	if r := active.Load(); r != nil {
		r.add(cat, op, structKeys(reflect.TypeOf(v)))
	}
}

// Decode is json.Unmarshal(raw, v) for op's params, recording v's keys.
func Decode(cat Category, op string, raw []byte, v any) error {
	Declare(cat, op, v)
	return json.Unmarshal(raw, v)
}

// DecodeOptional is Decode skipped when raw is empty (an absent params
// object): v keeps its defaults. The keys are recorded either way.
func DecodeOptional(cat Category, op string, raw []byte, v any) error {
	Declare(cat, op, v)
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// Get looks key up in a decoded params map, recording the read.
func Get[V any](cat Category, op string, m map[string]V, key string) (V, bool) {
	Read(cat, op, key)
	v, ok := m[key]
	return v, ok
}

// structKeys is encoding/json's decodable key set of t's struct (a
// pointer chain dereferenced); nil for a non-struct target (a map or
// an interface records its reads through Get / Read instead).
func structKeys(t reflect.Type) []string {
	if t == nil {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	collect(t, &out, map[reflect.Type]bool{})
	return out
}

func collect(t reflect.Type, out *[]string, seen map[reflect.Type]bool) {
	if seen[t] {
		return
	}
	seen[t] = true
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				collect(ft, out, seen)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		*out = append(*out, name)
	}
}

// DecodeStrict is Decode refusing an unknown key (encoding/json's
// DisallowUnknownFields), for a reader that already judged its own
// keys before the manifest allowlist did.
func DecodeStrict(cat Category, op string, raw []byte, v any) error {
	Declare(cat, op, v)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// Probe is json.Unmarshal(raw, v) WITHOUT recording: a lenient look at
// a key whose operator decodes (and records) it elsewhere — a planner
// peeking at params.fit, a default rule reading params.metric. A probe
// never makes a key valid, so it is not a read the manifest must
// declare.
func Probe(raw []byte, v any) error {
	return json.Unmarshal(raw, v)
}
