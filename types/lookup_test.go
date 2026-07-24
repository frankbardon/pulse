package types

import "testing"

func TestLookupRequest_KeyComponents_NilRequest(t *testing.T) {
	var r *LookupRequest
	if got := r.KeyComponents(); got != nil {
		t.Errorf("KeyComponents() on nil request = %v, want nil", got)
	}
}

func TestLookupRequest_KeyComponents_EmptyRequestReturnsNil(t *testing.T) {
	r := &LookupRequest{}
	if got := r.KeyComponents(); got != nil {
		t.Errorf("KeyComponents() on empty request = %v, want nil", got)
	}
}

func TestLookupRequest_KeyComponents_SingleKeyPath(t *testing.T) {
	r := &LookupRequest{Field: "id", Value: "3"}
	got := r.KeyComponents()
	want := []LookupKey{{Field: "id", Value: "3"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("KeyComponents() = %+v, want %+v", got, want)
	}
}

func TestLookupRequest_KeyComponents_KeysTakesPrecedence(t *testing.T) {
	r := &LookupRequest{
		Field: "id", // should be ignored
		Value: "999",
		Keys: []LookupKey{
			{Field: "region", Value: "east"},
			{Field: "period", Value: "2023"},
		},
	}
	got := r.KeyComponents()
	if len(got) != 2 {
		t.Fatalf("KeyComponents() len = %d, want 2 (Keys must win over Field/Value)", len(got))
	}
	if got[0].Field != "region" || got[1].Field != "period" {
		t.Errorf("KeyComponents() = %+v, want [region period] in order", got)
	}
}

func TestLookupRequest_KeyComponents_OrderPreserved(t *testing.T) {
	r := &LookupRequest{
		Keys: []LookupKey{
			{Field: "c", Value: "3"},
			{Field: "a", Value: "1"},
			{Field: "b", Value: "2"},
		},
	}
	got := r.KeyComponents()
	wantOrder := []string{"c", "a", "b"}
	for i, name := range wantOrder {
		if got[i].Field != name {
			t.Errorf("KeyComponents()[%d].Field = %q, want %q (order must be preserved verbatim)", i, got[i].Field, name)
		}
	}
}
