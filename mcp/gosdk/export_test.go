package gosdk

import core "github.com/frankbardon/pulse/internal/mcp"

// Core exposes the unexported core-config projection to the external
// gosdk_test package. Test-only: the public surface does not name the
// internal core Config.
func (c Config) Core() core.Config { return c.coreConfig() }
