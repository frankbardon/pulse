package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/skills"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// CohortURIScheme is the URI scheme for .pulse cohort resources.
const CohortURIScheme = "pulse://"

// SkillURIScheme is the URI scheme for embedded skill resources.
const SkillURIScheme = "pulse-skill://"

// SchemaResourceURI is the static resource URI exposing the payload JSON
// Schema. It is a reserved name under the cohort scheme (no .pulse suffix,
// so it never collides with a cohort file) — the same contract published
// at the github.io docs URL and emitted by `pulse schema`.
const SchemaResourceURI = CohortURIScheme + "schema"

// CohortURITemplate and SkillURITemplate are the RFC 6570 templates registered
// for the two URI schemes. Reserved expansion ({+var}) is used so the variable
// captures slashes — sub-directory cohorts (sub/d.pulse) and any future
// path-shaped skill names resolve through the one template. go-sdk's
// AddResourceTemplate panics on an invalid template; both literals below are
// valid Level-2 RFC 6570 templates with non-empty (absolute) schemes.
const (
	CohortURITemplate = CohortURIScheme + "{+path}"
	SkillURITemplate  = SkillURIScheme + "{+name}"
)

// registerResources mounts the resource surface on the go-sdk server: the
// static payload-schema resource, the embedded skill set, and the discovered
// cohort files. Each known skill / cohort is registered as an exact-match
// static resource (so resources/list enumerates concrete URIs); the two URI
// schemes are additionally backed by RFC 6570 resource templates so a read of
// any pulse:// or pulse-skill:// URI resolves even when it was not enumerated
// at registration time. Static resources are matched before templates, so the
// reserved pulse://schema resource always wins over the cohort template.
func registerResources(s *mcpsdk.Server, p *pulse.Pulse) {
	registerSchemaResource(s)
	registerSkillResources(s)
	registerCohortResources(s, p)
}

// registerSchemaResource exposes descriptor.BuildPayloadSchema() as a static
// MCP resource so agents can fetch the request/response contract in-session
// alongside pulse_manifest. A resource (not a tool) keeps the tool surface
// unchanged.
func registerSchemaResource(s *mcpsdk.Server) {
	s.AddResource(&mcpsdk.Resource{
		URI:         SchemaResourceURI,
		Name:        "payload-schema",
		MIMEType:    "application/json",
		Description: "JSON Schema (draft 2020-12) for every public Pulse request/response payload",
	}, func(_ context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		return textResource(req.Params.URI, "application/json", string(descriptor.BuildPayloadSchema())), nil
	})
}

// registerSkillResources registers each embedded skill as an exact-match static
// resource plus a scheme-level template. Both paths funnel into skillReader,
// which derives the skill name from the requested URI.
func registerSkillResources(s *mcpsdk.Server) {
	for _, meta := range skills.List() {
		s.AddResource(&mcpsdk.Resource{
			URI:         SkillURIScheme + meta.Name,
			Name:        meta.Name,
			MIMEType:    "text/markdown",
			Description: meta.Description,
		}, skillReader)
	}
	s.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: SkillURITemplate,
		Name:        "pulse-skill",
		MIMEType:    "text/markdown",
		Description: "Embedded Pulse skill markdown, addressed by name (pulse-skill://<name>)",
	}, skillReader)
}

// registerCohortResources registers each discovered .pulse file as an
// exact-match static resource plus a scheme-level template. Both paths funnel
// into the cohortReader closure, which derives the cohort path from the
// requested URI and serves its inspect JSON.
func registerCohortResources(s *mcpsdk.Server, p *pulse.Pulse) {
	reader := cohortReader(p)
	for _, name := range scanPulseFiles(p.Fs()) {
		s.AddResource(&mcpsdk.Resource{
			URI:         CohortURIScheme + name,
			Name:        name,
			MIMEType:    "application/json",
			Description: "Pulse cohort: header and schema as JSON",
		}, reader)
	}
	s.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: CohortURITemplate,
		Name:        "pulse-cohort",
		MIMEType:    "application/json",
		Description: "Pulse cohort header and schema as JSON, addressed by path (pulse://<path>.pulse)",
	}, reader)
}

func scanPulseFiles(fsys afero.Fs) []string {
	var out []string
	_ = afero.Walk(fsys, "", func(path string, info os.FileInfo, err error) error {
		if err != nil || path == "" {
			return nil
		}
		if info != nil && info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".pulse") {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// skillReader serves an embedded skill body. It derives the skill name by
// stripping the scheme from the requested URI, so it backs both the per-skill
// static resources and the pulse-skill:// template.
func skillReader(_ context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
	name := strings.TrimPrefix(req.Params.URI, SkillURIScheme)
	body, ok := skills.Get(name)
	if !ok {
		return nil, fmt.Errorf("skill %q not found", name)
	}
	return textResource(req.Params.URI, "text/markdown", body), nil
}

// cohortReader returns a handler that serves a cohort's inspect JSON. It
// derives the cohort path by stripping the scheme from the requested URI, so it
// backs both the per-file static resources and the pulse:// template.
func cohortReader(p *pulse.Pulse) mcpsdk.ResourceHandler {
	return func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		path := strings.TrimPrefix(req.Params.URI, CohortURIScheme)
		result, err := p.Inspect(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", path, err)
		}
		body, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("encode inspect result: %w", err)
		}
		return textResource(req.Params.URI, "application/json", string(body)), nil
	}
}

// textResource builds a single-text-content ReadResourceResult.
func textResource(uri, mimeType, text string) *mcpsdk.ReadResourceResult {
	return &mcpsdk.ReadResourceResult{
		Contents: []*mcpsdk.ResourceContents{{
			URI:      uri,
			MIMEType: mimeType,
			Text:     text,
		}},
	}
}
