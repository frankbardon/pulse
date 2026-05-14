package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/frankbardon/pulse"
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
