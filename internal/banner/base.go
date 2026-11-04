package banner

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Compose base (library-gaps E6-S1).
//
// ComposedRequest.Base is a Request-shaped partial merged copy-on-write
// into every EXPLICIT slot — never the sweep body, the banner slots or
// a fit request — at the top of ExpandCompose, so serial Compose,
// ComposeParallel and predict all judge the same merged slots, and the
// params inheritance, the sweep and banner expansion, the slot gates,
// the result() catalog and every item for_each expansion downstream see
// the base items as the slot's own.
//
// The merge walks types.Request's fields by reflection, so a list slot
// added to Request (or an item kind gaining for_each) inherits the rules
// with no edit here:
//
//   - `label`: never merged — a base that sets one is refused
//     (base_label).
//   - a map (`params`): key-wise, the slot's key winning.
//   - a list: the base items first, then the slot's own; a base item
//     whose label or name (the item's `label`, `name` or `as` string)
//     equals one of the slot's own in the same list is refused
//     (label_collision).
//   - anything else (a string, a pointer): the slot's own value when
//     set, else the base's.
//
// Every merged slot gets its own deep copy of the base items (one JSON
// round trip per slot, the banner base request's technique), so a
// slot's in-place rewrites — smart defaults, item fan-out — never reach
// a sibling or the caller's request. The slot's own items pass through
// as written.

// MergeBase returns c with c.Base merged into every explicit slot and
// Base cleared; c itself when it carries no base. c is never mutated.
func MergeBase(c *types.ComposedRequest) (*types.ComposedRequest, *errors.CodedError) {
	if c == nil || c.Base == nil {
		return c, nil
	}
	if c.Base.Label != "" {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_COMPOSE_BASE_COLLISION,
			fmt.Sprintf("compose base: the base sets label %q; a base names no slot — label each slot in requests instead", c.Base.Label),
			map[string]any{"reason": "base_label", "label": c.Base.Label})
	}
	raw, err := json.Marshal(c.Base)
	if err != nil {
		return nil, baseCopyFault(err)
	}
	out := *c
	out.Base = nil
	out.Requests = make([]*types.Request, len(c.Requests))
	for i, slot := range c.Requests {
		if slot == nil {
			// Refused downstream as every nil slot is.
			continue
		}
		var fresh types.Request
		if err := json.Unmarshal(raw, &fresh); err != nil {
			return nil, baseCopyFault(err)
		}
		merged, ce := mergeSlot(&fresh, c.Base.Params, slot, i)
		if ce != nil {
			return nil, ce
		}
		out.Requests[i] = merged
	}
	return &out, nil
}

// baseCopyFault is the (unreachable for a decoded request) failure to
// deep-copy the base through JSON.
func baseCopyFault(err error) *errors.CodedError {
	ce := errors.NewCodedError(errors.SERVICE_INTERNAL, "compose base could not be copied: "+err.Error())
	ce.Cause = err
	return ce
}

// mergeSlot merges base (this slot's own deep copy) into slot; params
// are the caller's base params, copied value by value.
func mergeSlot(base *types.Request, params types.Params, slot *types.Request, i int) (*types.Request, *errors.CodedError) {
	merged := *slot
	bv := reflect.ValueOf(base).Elem()
	mv := reflect.ValueOf(&merged).Elem()
	rt := mv.Type()
	for f := 0; f < rt.NumField(); f++ {
		field := rt.Field(f)
		if !field.IsExported() || field.Name == "Label" {
			continue
		}
		own, from := mv.Field(f), bv.Field(f)
		switch own.Kind() {
		case reflect.Map:
			if field.Type != reflect.TypeOf(types.Params(nil)) {
				// Defensive: every map slot of Request today is params.
				if own.Len() == 0 && from.Len() > 0 {
					own.Set(from)
				}
				continue
			}
			if len(params) == 0 {
				continue
			}
			p := make(types.Params, len(params)+len(slot.Params))
			for k, v := range params {
				p[k] = v
			}
			for k, v := range slot.Params {
				p[k] = v
			}
			own.Set(reflect.ValueOf(p))
		case reflect.Slice:
			if from.Len() == 0 {
				continue
			}
			if own.Len() > 0 {
				if ce := listCollision(from, own, jsonKey(field), slot, i); ce != nil {
					return nil, ce
				}
			}
			joined := reflect.MakeSlice(field.Type, 0, from.Len()+own.Len())
			joined = reflect.AppendSlice(joined, from)
			joined = reflect.AppendSlice(joined, own)
			own.Set(joined)
		default:
			if own.IsZero() && !from.IsZero() {
				own.Set(from)
			}
		}
	}
	return &merged, nil
}

// listCollision refuses a base item whose identity (itemID) equals one
// of the slot's own items' in the same list.
func listCollision(base, own reflect.Value, list string, slot *types.Request, i int) *errors.CodedError {
	seen := make(map[string]bool, own.Len())
	for j := 0; j < own.Len(); j++ {
		if id := itemID(own.Index(j)); id != "" {
			seen[id] = true
		}
	}
	for j := 0; j < base.Len(); j++ {
		id := itemID(base.Index(j))
		if id == "" || !seen[id] {
			continue
		}
		label := slot.Label
		if label == "" {
			label = fmt.Sprintf("request_%d", i+1)
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_COMPOSE_BASE_COLLISION,
			fmt.Sprintf("compose base: %s %q is declared by both the base and slot %q; rename the slot's own item or drop it", list, id, label),
			map[string]any{"reason": "label_collision", "request": i, "slot": label, "list": list, "label": id})
	}
	return nil
}

// itemID is a list item's identity: its `label`, `name` or `as` string
// field, the first set in that order; "" for an item carrying none.
func itemID(v reflect.Value) string {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	t := v.Type()
	for _, key := range []string{"label", "name", "as"} {
		for f := 0; f < t.NumField(); f++ {
			sf := t.Field(f)
			if sf.Type.Kind() == reflect.String && jsonKey(sf) == key {
				if s := v.Field(f).String(); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// jsonKey is a struct field's JSON key.
func jsonKey(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" {
		return f.Name
	}
	return name
}
