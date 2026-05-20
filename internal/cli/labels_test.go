package cli

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestParseLabelBindings_Replace(t *testing.T) {
	got, err := parseLabelBindings([]string{"country=country_names"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Field != "country" || got[0].Table != "country_names" || got[0].Mode != types.LabelModeReplace {
		t.Fatalf("unexpected binding: %+v", got[0])
	}
}

func TestParseLabelBindings_Augment(t *testing.T) {
	got, err := parseLabelBindings([]string{"country=country_names:augment"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Mode != types.LabelModeAugment {
		t.Fatalf("expected augment mode, got %q", got[0].Mode)
	}
}

func TestParseLabelBindings_InvalidMode(t *testing.T) {
	_, err := parseLabelBindings([]string{"country=country_names:highlight"})
	if err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("expected unknown mode error, got %v", err)
	}
}

func TestParseLabelBindings_Multiple(t *testing.T) {
	got, err := parseLabelBindings([]string{
		"country=country_names",
		"region=region_names:augment",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 bindings, got %d", len(got))
	}
	if got[1].Field != "region" || got[1].Mode != types.LabelModeAugment {
		t.Fatalf("unexpected second binding: %+v", got[1])
	}
}

func TestParseLabelBindings_Duplicate(t *testing.T) {
	_, err := parseLabelBindings([]string{
		"country=names",
		"country=other:augment",
	})
	if err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestParseLabelBindings_BadFormat(t *testing.T) {
	_, err := parseLabelBindings([]string{"justname"})
	if err == nil {
		t.Fatal("expected error for missing '='")
	}
}

func TestParseLabelBindings_Empty(t *testing.T) {
	_, err := parseLabelBindings([]string{""})
	if err == nil {
		t.Fatal("expected error for empty binding")
	}
}
