package gosdk

import (
	"context"

	"github.com/frankbardon/pulse"
	core "github.com/frankbardon/pulse/internal/mcp"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Core exposes the unexported core-config projection to the external
// gosdk_test package. Test-only: the public surface does not name the
// internal core Config.
func (c Config) Core() core.Config { return c.coreConfig() }

// ReadSkillViaTemplate serves uri through the pulse-skill://{+name}
// template's handler for p's instance — the path an exact resource
// shadows in a live session.
func ReadSkillViaTemplate(p *pulse.Pulse, uri string) (string, error) {
	res, err := skillReaderFor(p)(context.Background(), &mcpsdk.ReadResourceRequest{Params: &mcpsdk.ReadResourceParams{URI: uri}})
	if err != nil {
		return "", err
	}
	return res.Contents[0].Text, nil
}
