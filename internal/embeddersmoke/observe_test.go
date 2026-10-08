package embeddersmoke

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/observe"
	"github.com/spf13/afero"
)

// TestObserveHooksFlow wires pulse.Options.Hooks from outside the module
// and sees one start and one end for an operation.
func TestObserveHooksFlow(t *testing.T) {
	var starts, ends int
	var code string
	p, err := pulse.New(pulse.Options{
		FS: afero.NewMemMapFs(),
		Hooks: &observe.Hooks{
			OnOperationStart: func(ctx context.Context, info observe.OperationInfo) context.Context {
				if info.Kind == observe.OpManifest {
					starts++
				}
				return ctx
			},
			OnOperationEnd: func(_ context.Context, info observe.OperationInfo, res observe.OperationResult) {
				if info.Kind == observe.OpManifest {
					ends++
					code = res.Code
				}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Manifest(context.Background()) == nil {
		t.Fatal("nil manifest")
	}
	if starts != 1 || ends != 1 || code != observe.CodeOK {
		t.Fatalf("starts=%d ends=%d code=%q, want 1/1/ok", starts, ends, code)
	}
}
