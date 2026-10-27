package pulse_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// An extension param group declared ExactlyOneOf projects onto the
// manifest beside the built-in operators' exactly_one_of groups.
func TestExtensions_ExactlyOneOfProjects(t *testing.T) {
	t.Parallel()

	ext := pulse.Extensions{
		Windows: []pulse.WindowRegistration{{
			Name:    "WIN_ACME_SMOOTH",
			Factory: stubWindowFactory,
			Params: []pulse.ParamMeta{
				{Name: "span", JSONType: "number", ExactlyOneOf: "smoothing"},
				{Name: "halflife", JSONType: "number", ExactlyOneOf: "smoothing"},
				{Name: "adjust", JSONType: "boolean"},
			},
		}},
	}
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	m := p.Manifest(context.Background())
	if len(m.Extensions.Windows) != 1 {
		t.Fatalf("windows = %+v", m.Extensions.Windows)
	}
	got := map[string]string{}
	for _, pm := range m.Extensions.Windows[0].Params {
		got[pm.Name] = pm.ExactlyOneOf
	}
	if got["span"] != "smoothing" || got["halflife"] != "smoothing" || got["adjust"] != "" {
		t.Errorf("exactly_one_of projection = %v", got)
	}
}

// A malformed group is refused at pulse.New: one member, a Required
// member, or a defaulted member.
func TestExtensions_ExactlyOneOfMalformedRefused(t *testing.T) {
	t.Parallel()

	cases := map[string][]pulse.ParamMeta{
		"one member": {
			{Name: "span", JSONType: "number", ExactlyOneOf: "smoothing"},
			{Name: "halflife", JSONType: "number"},
		},
		"required member": {
			{Name: "span", JSONType: "number", Required: true, ExactlyOneOf: "smoothing"},
			{Name: "halflife", JSONType: "number", ExactlyOneOf: "smoothing"},
		},
		"defaulted member": {
			{Name: "span", JSONType: "number", ExactlyOneOf: "smoothing"},
			{Name: "halflife", JSONType: "number", Default: 2.0, ExactlyOneOf: "smoothing"},
		},
	}
	for name, params := range cases {
		t.Run(name, func(t *testing.T) {
			ext := pulse.Extensions{
				Windows: []pulse.WindowRegistration{{
					Name:    "WIN_ACME_SMOOTH",
					Factory: stubWindowFactory,
					Params:  params,
				}},
			}
			_, err := pulse.New(newTestOptions(ext))
			assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
		})
	}
}
