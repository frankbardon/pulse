package pulse_test

// The MCP bridge for TestProfileInvisibilityParity. The parity harness
// lives in package pulse (it reads the instance snapshot), which cannot
// import mcp/gosdk — gosdk imports pulse. This external test file is
// compiled into the same test binary, so it installs the go-sdk client
// side the harness drives: an in-memory server per instance, mounted
// through gosdk.Register exactly as a host mounts it.

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/mcp/gosdk"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func init() {
	pulse.NewMCPParitySession = newMCPParitySession
}

// newMCPParitySession mounts p on a fresh go-sdk server (bind-on-inspect
// on, cohort enumeration left to the instance's feature set) and
// connects an in-memory client. The session closes at test cleanup.
func newMCPParitySession(t *testing.T, p *pulse.Pulse) *pulse.MCPParitySession {
	t.Helper()
	ctx := context.Background()
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "parity-host", Version: "9.9.9"}, nil)
	if err := gosdk.Register(srv, p, gosdk.Config{Version: "9.9.9", BindOnInspect: true}); err != nil {
		t.Fatalf("gosdk.Register: %v", err)
	}
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "parity-client", Version: "1.0.0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = ss.Close()
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Close()
	})

	marshal := func(v any) []byte {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return raw
	}
	return &pulse.MCPParitySession{
		CallTool: func(name string, args any) []byte {
			res, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
			if err != nil {
				return []byte("protocol error: " + err.Error())
			}
			if res.IsError {
				return append([]byte("error: "), marshal(res)...)
			}
			return append([]byte("ok: "), marshal(res)...)
		},
		ListTools: func() ([]byte, []string) {
			out, err := cs.ListTools(ctx, nil)
			if err != nil {
				t.Fatalf("ListTools: %v", err)
			}
			var names []string
			for _, tool := range out.Tools {
				names = append(names, tool.Name)
			}
			slices.Sort(names)
			return marshal(out), names
		},
		ListPrompts: func() ([]byte, []string) {
			// A server with no prompt registered does not advertise the
			// capability at all — exactly a server that never had one.
			if cs.InitializeResult().Capabilities.Prompts == nil {
				return nil, nil
			}
			out, err := cs.ListPrompts(ctx, nil)
			if err != nil {
				t.Fatalf("ListPrompts: %v", err)
			}
			var names []string
			for _, pr := range out.Prompts {
				names = append(names, pr.Name)
			}
			slices.Sort(names)
			return marshal(out), names
		},
		GetPrompt: func(name string) []byte {
			res, err := cs.GetPrompt(ctx, &mcpsdk.GetPromptParams{Name: name, Arguments: map[string]string{"question": "q"}})
			if err != nil {
				return []byte("protocol error: " + err.Error())
			}
			return marshal(res)
		},
		ListResources: func() ([]byte, []string) {
			out, err := cs.ListResources(ctx, nil)
			if err != nil {
				t.Fatalf("ListResources: %v", err)
			}
			var uris []string
			for _, r := range out.Resources {
				uris = append(uris, r.URI)
			}
			return marshal(out), uris
		},
		ListResourceTemplates: func() []byte {
			out, err := cs.ListResourceTemplates(ctx, nil)
			if err != nil {
				t.Fatalf("ListResourceTemplates: %v", err)
			}
			return marshal(out)
		},
		ReadResource: func(uri string) []byte {
			res, err := cs.ReadResource(ctx, &mcpsdk.ReadResourceParams{URI: uri})
			if err != nil {
				return []byte("protocol error: " + err.Error())
			}
			return marshal(res)
		},
	}
}
