// Package mmmpulse is Pulse's media-mix modelling pack: the REG_MMM_HILL
// regression, registered through Pulse's public extension contract.
//
// Register it by merging Extensions into the instance's options:
//
//	p, err := pulse.New(pulse.Options{Extensions: mmmpulse.Extensions()})
//
// Then name it on a regression slot like any built-in:
//
//	{"type": "REG_MMM_HILL", "name": "mmm", "target": "sales",
//	 "predictors": ["tv_ad", "search_ad", "price"],
//	 "params": {"channels": ["tv_ad", "search_ad"]}}
//
// The channels are already-adstocked spend columns (WIN_DISTRIBUTED_LAG
// upstream, its decay a sweep axis); the model does not adstock. Every
// other predictor is a linear control. See Registration for the model,
// its params and its coefficient table.
//
// The pack lives in its own Go module, versioned in lockstep with Pulse
// (tag contrib/mmmpulse/vX.Y.Z = Pulse vX.Y.Z), and imports only Pulse's
// public packages; Pulse itself never imports it.
package mmmpulse

import (
	"embed"
	"io/fs"

	"github.com/frankbardon/pulse"
)

//go:embed skills/*.md
var skillFiles embed.FS

// Extensions is the pack's registration set: REG_MMM_HILL and its two
// skills (the operator's and a media-mix workflow guide), served through
// Extensions.Skills. Use it as pulse.Options.Extensions; to combine it
// with the embedder's own, append its Regressions to the existing set and
// keep one Skills filesystem that holds both skill sets. Each call returns
// a fresh value.
func Extensions() pulse.Extensions {
	skills, err := fs.Sub(skillFiles, "skills")
	if err != nil {
		panic(err) // the embedded directory is fixed at build time
	}
	return pulse.Extensions{
		Regressions: []pulse.RegressionRegistration{Registration()},
		Skills:      skills,
	}
}
