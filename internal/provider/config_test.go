package provider_test

import (
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/weirdGuy/agentform/internal/module"
	"github.com/weirdGuy/agentform/internal/provider"
	"github.com/weirdGuy/agentform/internal/schema"
)

func loadModule(t *testing.T, dir string) *module.Module {
	t.Helper()
	mod, err := module.Load(filepath.Join("testdata", dir))
	if err != nil {
		t.Fatalf("Load(%s): unexpected error: %v", dir, err)
	}
	return mod
}

func agentByAddr(t *testing.T, mod *module.Module, addr string) *schema.Agent {
	t.Helper()
	sym, ok := mod.Lookup(addr)
	if !ok {
		t.Fatalf("module has no %s", addr)
	}
	return sym.Block.(*schema.Agent)
}

func TestDesiredConfig(t *testing.T) {
	mod := loadModule(t, "weather")

	tests := []struct {
		addr string
		want provider.Object
	}{
		{
			// Full closure: description, model params, instructions from the
			// prompt body, tools, mixed inputs, outputs. All numbers are
			// float64 — the JSON value model.
			addr: "agent.weather",
			want: provider.Object{
				"description": "Answers weather questions",
				"model": map[string]any{
					"provider": "openai",
					"id":       "gpt-4o-mini",
					"params":   map[string]any{"temperature": 0.2, "max_tokens": float64(4096)},
				},
				"instructions": "You are a weather assistant for {{location}}.\n",
				"tools": []any{
					map[string]any{
						"name":        "web_search",
						"description": "Search the web",
						"params": []any{
							map[string]any{"name": "query", "type": "string", "description": "The query to search for"},
							map[string]any{"name": "max_results", "type": "number", "default": float64(10)},
						},
						"returns": map[string]any{"type": "string"},
						"source":  map[string]any{"kind": "mcp", "uri": "mcp://search-server/web_search"},
					},
				},
				"inputs": []any{
					map[string]any{"name": "location", "type": "string", "description": "The location to get the weather for"},
					map[string]any{"name": "date", "type": "string", "optional": true},
					// forecast_context's default is a cross-agent output
					// reference: ordering-only in v0, so it must not appear
					// in the config (a ref change must not cause an update).
					map[string]any{"name": "forecast_context", "type": "string"},
				},
				"outputs": []any{
					map[string]any{"name": "report", "type": "string"},
				},
			},
		},
		{
			// Minimal agent: no description, no prompt, no tools — the keys
			// are omitted, not present with empty values.
			addr: "agent.geocoder",
			want: provider.Object{
				"model": map[string]any{
					"provider": "openai",
					"id":       "gpt-4o-mini",
					"params":   map[string]any{"temperature": 0.2, "max_tokens": float64(4096)},
				},
				"inputs":  []any{map[string]any{"name": "place", "type": "string"}},
				"outputs": []any{map[string]any{"name": "coordinates", "type": "string"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			got, err := provider.DesiredConfig(mod, agentByAddr(t, mod, tt.addr))
			if err != nil {
				t.Fatalf("DesiredConfig: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("DesiredConfig(%s) (-want +got):\n%s", tt.addr, diff)
			}
		})
	}
}

func TestMarshalConfigIsDeterministic(t *testing.T) {
	mod := loadModule(t, "weather")
	a := agentByAddr(t, mod, "agent.weather")

	var first []byte
	for i := 0; i < 5; i++ {
		cfg, err := provider.DesiredConfig(mod, a)
		if err != nil {
			t.Fatalf("DesiredConfig #%d: %v", i+1, err)
		}
		raw, err := provider.MarshalConfig(cfg)
		if err != nil {
			t.Fatalf("MarshalConfig #%d: %v", i+1, err)
		}
		if i == 0 {
			first = raw
			continue
		}
		if string(raw) != string(first) {
			t.Fatalf("MarshalConfig is not deterministic (run 1 vs run %d):\n%s\nvs:\n%s", i+1, first, raw)
		}
	}
}
