package service

// The model regressions (internal/model.ModelRegression — REG_MMM_HILL,
// REG_MMM_BAYES) install themselves on the engine's built-in regression
// registry at internal/regadapt's init. The service imports it so every
// binary that runs a request — the root facade, the stock CLI,
// `pulse mcp`, and every service test — runs them with no Extensions
// and no root package.
import _ "github.com/frankbardon/pulse/internal/regadapt"
