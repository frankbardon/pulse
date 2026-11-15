package extend

import (
	"reflect"
	"testing"
)

// TestPostFitInput_ReachesNoRecord: nothing a post-fit operator is
// handed reaches a record — no method of PostFitInput or PostFitFit
// takes or returns a Record or Rows (or any value carrying one).
func TestPostFitInput_ReachesNoRecord(t *testing.T) {
	t.Parallel()

	banned := map[reflect.Type]bool{
		reflect.TypeFor[Record](): true,
		reflect.TypeFor[Rows]():   true,
	}
	var reaches func(rt reflect.Type, seen map[reflect.Type]bool) bool
	reaches = func(rt reflect.Type, seen map[reflect.Type]bool) bool {
		if banned[rt] {
			return true
		}
		if seen[rt] {
			return false
		}
		seen[rt] = true
		switch rt.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map, reflect.Chan:
			if rt.Kind() == reflect.Map && reaches(rt.Key(), seen) {
				return true
			}
			return reaches(rt.Elem(), seen)
		case reflect.Struct:
			for i := 0; i < rt.NumField(); i++ {
				if reaches(rt.Field(i).Type, seen) {
					return true
				}
			}
		case reflect.Interface, reflect.Func:
			if rt.Kind() == reflect.Func {
				for i := 0; i < rt.NumIn(); i++ {
					if reaches(rt.In(i), seen) {
						return true
					}
				}
				for i := 0; i < rt.NumOut(); i++ {
					if reaches(rt.Out(i), seen) {
						return true
					}
				}
				return false
			}
			for i := 0; i < rt.NumMethod(); i++ {
				if reaches(rt.Method(i).Type, seen) {
					return true
				}
			}
		}
		return false
	}
	for _, rt := range []reflect.Type{reflect.TypeFor[PostFitFunc](), reflect.TypeFor[PostFitInput](), reflect.TypeFor[PostFitFit]()} {
		if reaches(rt, map[reflect.Type]bool{}) {
			t.Errorf("%v reaches a record", rt)
		}
	}
	// The walk itself finds a record where one is reachable.
	if !reaches(reflect.TypeFor[func(Rows) error](), map[reflect.Type]bool{}) {
		t.Fatal("the walk misses a Rows argument")
	}
}
