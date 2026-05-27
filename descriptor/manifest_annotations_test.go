package descriptor

import "testing"

func TestManifest_CommandAnnotationsPopulated(t *testing.T) {
	m := BuildManifest()
	if len(m.Commands) == 0 {
		t.Fatalf("manifest has no commands")
	}
	for _, cmd := range m.Commands {
		// Every command must carry an annotation block. We assert
		// that at least one of the three flags reflects a non-zero
		// signal — synth happens to set all three meaningfully so a
		// blanket all-false would be a regression worth catching.
		if cmd.Annotations == (CommandAnnotations{}) {
			t.Errorf("command %q has empty annotations", cmd.Name)
		}
	}
}

func TestManifest_OperationsPopulated(t *testing.T) {
	m := BuildManifest()
	want := map[string]bool{
		"filter_to_file": false,
		"process_stream": false,
		"synth_stream":   false,
		"watch":          false,
	}
	for _, op := range m.Operations {
		if _, ok := want[op.Name]; ok {
			want[op.Name] = true
		}
		if op.Annotations == (CommandAnnotations{}) {
			t.Errorf("operation %q has empty annotations", op.Name)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing expected operation %q", name)
		}
	}
}

func TestManifest_AnnotationSemantics(t *testing.T) {
	m := BuildManifest()
	byName := func(name string) CommandAnnotations {
		for _, cmd := range m.Commands {
			if cmd.Name == name {
				return cmd.Annotations
			}
		}
		for _, op := range m.Operations {
			if op.Name == name {
				return op.Annotations
			}
		}
		t.Fatalf("command/operation %q not in manifest", name)
		return CommandAnnotations{}
	}

	// Spot-check the annotations the spec called out.
	if a := byName("process"); !a.Deterministic {
		t.Errorf("process should be deterministic")
	}
	if a := byName("synth"); a.Deterministic {
		t.Errorf("synth is non-deterministic without a seed override")
	}
	if a := byName("filter_to_file"); !a.Deterministic || !a.Expensive {
		t.Errorf("filter_to_file must be deterministic + expensive, got %+v", a)
	}
	if a := byName("watch"); !a.Streamable {
		t.Errorf("watch should be marked streamable")
	}
}
