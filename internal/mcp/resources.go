//go:build ignore

// TODO(E1-S2/S3/S4): not yet ported to github.com/modelcontextprotocol/go-sdk.
// This file still targets mark3labs/mcp-go and is excluded from the build by
// the constraint above. Handler logic is preserved verbatim; the per-file
// migration stories (tools/strict_decode/import_tools -> E1-S2; resources/
// prompts -> E1-S3; schema_bind -> E1-S4) remove this constraint as they port.

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
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/afero"
)

// CohortURIScheme is the URI scheme for .pulse cohort resources.
const CohortURIScheme = "pulse://"

// SkillURIScheme is the URI scheme for embedded skill resources.
const SkillURIScheme = "pulse-skill://"

// registerResources scans the Pulse filesystem for .pulse files and registers
// the embedded skill set. Resources are static after registration; clients
// that expect new files to appear must reconnect.
func registerResources(s *server.MCPServer, p *pulse.Pulse) {
	registerSkillResources(s)
	registerCohortResources(s, p)
	registerSchemaResource(s)
}

// SchemaResourceURI is the static resource URI exposing the payload JSON
// Schema. It is a reserved name under the cohort scheme (no .pulse suffix,
// so it never collides with a cohort file) — the same contract published
// at the github.io docs URL and emitted by `pulse schema`.
const SchemaResourceURI = CohortURIScheme + "schema"

// registerSchemaResource exposes descriptor.BuildPayloadSchema() as a
// static MCP resource so agents can fetch the request/response contract
// in-session alongside pulse_manifest. A resource (not a tool) keeps the
// tool surface unchanged.
func registerSchemaResource(s *server.MCPServer) {
	res := mcpgo.NewResource(SchemaResourceURI, "payload-schema",
		mcpgo.WithMIMEType("application/json"),
		mcpgo.WithResourceDescription("JSON Schema (draft 2020-12) for every public Pulse request/response payload"),
	)
	s.AddResource(res, func(ctx context.Context, req mcpgo.ReadResourceRequest) ([]mcpgo.ResourceContents, error) {
		return []mcpgo.ResourceContents{
			mcpgo.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(descriptor.BuildPayloadSchema()),
			},
		}, nil
	})
}

func registerSkillResources(s *server.MCPServer) {
	for _, meta := range skills.List() {
		uri := SkillURIScheme + meta.Name
		res := mcpgo.NewResource(uri, meta.Name,
			mcpgo.WithMIMEType("text/markdown"),
			mcpgo.WithResourceDescription(meta.Description),
		)
		s.AddResource(res, makeSkillReader(meta.Name))
	}
}

func registerCohortResources(s *server.MCPServer, p *pulse.Pulse) {
	files := scanPulseFiles(p.Fs())
	for _, name := range files {
		uri := CohortURIScheme + name
		res := mcpgo.NewResource(uri, name,
			mcpgo.WithMIMEType("application/json"),
			mcpgo.WithResourceDescription("Pulse cohort: header and schema as JSON"),
		)
		s.AddResource(res, makeCohortReader(p, name))
	}
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

func makeSkillReader(name string) server.ResourceHandlerFunc {
	return func(ctx context.Context, req mcpgo.ReadResourceRequest) ([]mcpgo.ResourceContents, error) {
		body, ok := skills.Get(name)
		if !ok {
			return nil, fmt.Errorf("skill %q not found", name)
		}
		return []mcpgo.ResourceContents{
			mcpgo.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "text/markdown",
				Text:     body,
			},
		}, nil
	}
}

func makeCohortReader(p *pulse.Pulse, path string) server.ResourceHandlerFunc {
	return func(ctx context.Context, req mcpgo.ReadResourceRequest) ([]mcpgo.ResourceContents, error) {
		result, err := p.Inspect(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", path, err)
		}
		body, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("encode inspect result: %w", err)
		}
		return []mcpgo.ResourceContents{
			mcpgo.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(body),
			},
		}, nil
	}
}
