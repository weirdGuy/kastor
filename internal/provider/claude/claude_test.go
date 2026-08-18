package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/weirdGuy/kastor/internal/provider"
	"github.com/weirdGuy/kastor/internal/schema"
)

const testAPIKey = "test-anthropic-key"

func TestFactoryResolvesAPIKeyEnvironment(t *testing.T) {
	t.Run("ambient default", func(t *testing.T) {
		t.Setenv(defaultAPIKeyEnv, testAPIKey)
		got, err := Factory(&schema.Target{Name: "claude_agents", Type: "platform"})
		if err != nil || got == nil {
			t.Fatalf("Factory() = %v, %v; want provider, nil", got, err)
		}
	})

	t.Run("target config", func(t *testing.T) {
		const env = "KASTOR_TEST_ANTHROPIC_KEY"
		t.Setenv(env, testAPIKey)
		got, err := Factory(&schema.Target{
			Name:   "claude_agents",
			Type:   "platform",
			Config: map[string]any{"api_key_env": env},
		})
		if err != nil || got == nil {
			t.Fatalf("Factory() = %v, %v; want provider, nil", got, err)
		}
	})

	t.Run("missing", func(t *testing.T) {
		const env = "KASTOR_TEST_MISSING_ANTHROPIC_KEY"
		t.Setenv(env, "")
		_, err := Factory(&schema.Target{
			Name:   "claude_agents",
			Type:   "platform",
			Config: map[string]any{"api_key_env": env},
		})
		if err == nil || !strings.Contains(err.Error(), env) {
			t.Fatalf("Factory() error = %v; want missing-key error naming %s", err, env)
		}
	})

	t.Run("unknown config", func(t *testing.T) {
		_, err := Factory(&schema.Target{
			Name:   "claude_agents",
			Type:   "platform",
			Config: map[string]any{"region": "us-east-1"},
		})
		if err == nil || !strings.Contains(err.Error(), `unsupported config attribute "region"`) {
			t.Fatalf("Factory() error = %v; want unsupported-config error", err)
		}
	})

	t.Run("config type", func(t *testing.T) {
		_, err := Factory(&schema.Target{
			Name:   "claude_agents",
			Type:   "platform",
			Config: map[string]any{"api_key_env": int64(1)},
		})
		if err == nil || !strings.Contains(err.Error(), "config.api_key_env must be a string") {
			t.Fatalf("Factory() error = %v; want config type error", err)
		}
	})
}

func TestCreateSendsNormalizedSDKRequest(t *testing.T) {
	response := fixtureBytes(t, "full_api_response.json")
	var requests atomic.Int32
	p := fakeHTTPProvider(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assertCommonRequest(t, r)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/agents" || r.URL.Query().Get("beta") != "true" {
			t.Errorf("create request = %s %s, want POST /v1/agents?beta=true", r.Method, r.URL.RequestURI())
		}
		body := decodeRequestObject(t, r)
		if got := body["name"]; got != "weather" {
			t.Errorf("request name = %v, want weather", got)
		}
		metadata := body["metadata"].(map[string]any)
		if got := metadata[managedMarkerKey]; got != "agent.weather" {
			t.Errorf("managed marker = %v, want agent.weather", got)
		}
		servers := body["mcp_servers"].([]any)
		if got := servers[0].(map[string]any)["url"]; got != fullMCPURL {
			t.Errorf("MCP URL = %v, want %s", got, fullMCPURL)
		}
		for _, raw := range body["tools"].([]any) {
			toolset := raw.(map[string]any)
			for _, rawConfig := range toolset["configs"].([]any) {
				config := rawConfig.(map[string]any)
				want := alwaysAskPolicy
				if config["name"] == "read" {
					want = alwaysAllowPolicy
				}
				policy, ok := config["permission_policy"].(map[string]any)
				if !ok || policy["type"] != want {
					t.Errorf("create request policy for %v = %#v, want %q", config["name"], config["permission_policy"], want)
				}
			}
			if toolset["type"] != mcpToolsetType {
				continue
			}
			defaultConfig := toolset["default_config"].(map[string]any)
			if _, exists := defaultConfig["permission_policy"]; exists {
				t.Errorf("MCP create request authors a permission policy: %#v", defaultConfig)
			}
		}
		writeJSON(w, http.StatusOK, response)
	})

	id, err := p.Create(context.Background(), fullResource(t))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != "agent_01HqR2k7vXbZ9mNpL3wYcT8f" {
		t.Errorf("Create id = %q, want fixture id", id)
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

func TestReadMissingAndArchivedAreDriftData(t *testing.T) {
	active := fixtureBytes(t, "full_api_response.json")
	archived := mutateFixture(t, active, func(object provider.Object) {
		object["archived_at"] = "2026-07-30T15:00:00Z"
	})

	tests := []struct {
		name      string
		status    int
		response  []byte
		wantFound bool
	}{
		{name: "active", status: http.StatusOK, response: active, wantFound: true},
		{name: "missing", status: http.StatusNotFound, response: apiErrorBody("not_found_error"), wantFound: false},
		{name: "archived", status: http.StatusOK, response: archived, wantFound: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			p := fakeHTTPProvider(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assertCommonRequest(t, r)
				if r.Method != http.MethodGet || r.URL.Path != "/v1/agents/agent_test" {
					t.Errorf("read request = %s %s", r.Method, r.URL.RequestURI())
				}
				writeJSON(w, tt.status, tt.response)
			})
			remote, found, err := p.Read(context.Background(), "agent_test")
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if found != tt.wantFound {
				t.Errorf("found = %v, want %v", found, tt.wantFound)
			}
			if found && remote["id"] == nil {
				t.Errorf("active Read omitted response object: %#v", remote)
			}
			if !found && remote != nil {
				t.Errorf("missing/archived Read remote = %#v, want nil", remote)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
	}
}

func TestUpdateReadsVersionBeforeWriting(t *testing.T) {
	response := fixtureBytes(t, "full_api_response.json")
	desired := fullResource(t)
	desired.Config["metadata"].(map[string]any)["obsolete"] = nil

	var requests atomic.Int32
	p := fakeHTTPProvider(t, func(w http.ResponseWriter, r *http.Request) {
		call := requests.Add(1)
		assertCommonRequest(t, r)
		switch call {
		case 1:
			if r.Method != http.MethodGet || r.URL.Path != "/v1/agents/agent_test" {
				t.Errorf("update pre-read = %s %s", r.Method, r.URL.RequestURI())
			}
			writeJSON(w, http.StatusOK, response)
		case 2:
			if r.Method != http.MethodPost || r.URL.Path != "/v1/agents/agent_test" {
				t.Errorf("update request = %s %s", r.Method, r.URL.RequestURI())
			}
			body := decodeRequestObject(t, r)
			if got := body["version"]; got != float64(7) {
				t.Errorf("update version = %v, want 7 from pre-read", got)
			}
			metadata := body["metadata"].(map[string]any)
			if got, exists := metadata["obsolete"]; !exists || got != nil {
				t.Errorf("metadata deletion tombstone = %v, exists=%v; want explicit null", got, exists)
			}
			writeJSON(w, http.StatusOK, response)
		default:
			t.Errorf("unexpected request %d: %s %s", call, r.Method, r.URL.RequestURI())
			writeJSON(w, http.StatusInternalServerError, apiErrorBody("api_error"))
		}
	})

	if err := p.Update(context.Background(), "agent_test", desired); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("requests = %d, want read then update", got)
	}
}

func TestUpdateConflictIsNotRetried(t *testing.T) {
	response := fixtureBytes(t, "minimal_api_response.json")
	var requests atomic.Int32
	p := fakeHTTPProvider(t, func(w http.ResponseWriter, r *http.Request) {
		call := requests.Add(1)
		if call == 1 {
			writeJSON(w, http.StatusOK, response)
			return
		}
		writeJSON(w, http.StatusConflict, apiErrorBody("conflict_error"))
	})

	err := p.Update(context.Background(), "agent_test", &provider.Resource{
		Addr:   "agent.minimal",
		Config: loadObject(t, "minimal_spec.json"),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Update error = %v, want ErrConflict", err)
	}
	if !strings.Contains(err.Error(), "concurrent out-of-band edit") || !strings.Contains(err.Error(), "re-run kastor plan") {
		t.Errorf("conflict error lacks recovery guidance: %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Errorf("requests = %d, want one read and one update with no conflict retry", got)
	}
}

func TestUpdateRejectsMissingOrArchivedAgentWithoutWriting(t *testing.T) {
	active := fixtureBytes(t, "minimal_api_response.json")
	archived := mutateFixture(t, active, func(object provider.Object) {
		object["archived_at"] = "2026-07-30T15:00:00Z"
	})
	tests := []struct {
		name     string
		status   int
		response []byte
	}{
		{name: "missing", status: http.StatusNotFound, response: apiErrorBody("not_found_error")},
		{name: "archived", status: http.StatusOK, response: archived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			p := fakeHTTPProvider(t, func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				writeJSON(w, tt.status, tt.response)
			})
			err := p.Update(context.Background(), "agent_test", &provider.Resource{
				Addr:   "agent.minimal",
				Config: loadObject(t, "minimal_spec.json"),
			})
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Update error = %v, want ErrNotFound", err)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("requests = %d, want read only", got)
			}
		})
	}
}

func TestDeleteArchivesOnlyActiveAgents(t *testing.T) {
	active := fixtureBytes(t, "minimal_api_response.json")
	archived := mutateFixture(t, active, func(object provider.Object) {
		object["archived_at"] = "2026-07-30T15:00:00Z"
	})
	tests := []struct {
		name         string
		readStatus   int
		readResponse []byte
		archiveCode  int
		wantRequests int32
	}{
		{
			name:         "active",
			readStatus:   http.StatusOK,
			readResponse: active,
			archiveCode:  http.StatusOK,
			wantRequests: 2,
		},
		{
			name:         "missing",
			readStatus:   http.StatusNotFound,
			readResponse: apiErrorBody("not_found_error"),
			wantRequests: 1,
		},
		{
			name:         "already archived",
			readStatus:   http.StatusOK,
			readResponse: archived,
			wantRequests: 1,
		},
		{
			name:         "deleted after read",
			readStatus:   http.StatusOK,
			readResponse: active,
			archiveCode:  http.StatusNotFound,
			wantRequests: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			p := fakeHTTPProvider(t, func(w http.ResponseWriter, r *http.Request) {
				call := requests.Add(1)
				if call == 1 {
					if r.Method != http.MethodGet {
						t.Errorf("first Delete request method = %s, want GET", r.Method)
					}
					writeJSON(w, tt.readStatus, tt.readResponse)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/v1/agents/agent_test/archive" {
					t.Errorf("archive request = %s %s", r.Method, r.URL.RequestURI())
				}
				if tt.archiveCode == http.StatusNotFound {
					writeJSON(w, tt.archiveCode, apiErrorBody("not_found_error"))
					return
				}
				writeJSON(w, tt.archiveCode, active)
			})

			if err := p.Delete(context.Background(), "agent_test"); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if got := requests.Load(); got != tt.wantRequests {
				t.Errorf("requests = %d, want %d", got, tt.wantRequests)
			}
		})
	}
}

func TestErrorTaxonomyAndRetries(t *testing.T) {
	success := fixtureBytes(t, "minimal_api_response.json")
	tests := []struct {
		name         string
		statuses     []int
		wantKind     error
		wantRequests int32
		wantErr      bool
	}{
		{name: "unauthorized", statuses: []int{http.StatusUnauthorized}, wantKind: ErrAuthentication, wantRequests: 1, wantErr: true},
		{name: "forbidden", statuses: []int{http.StatusForbidden}, wantKind: ErrAuthentication, wantRequests: 1, wantErr: true},
		{name: "bad request", statuses: []int{http.StatusBadRequest}, wantRequests: 1, wantErr: true},
		{name: "rate limit then success", statuses: []int{http.StatusTooManyRequests, http.StatusOK}, wantRequests: 2},
		{name: "server failures then success", statuses: []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusOK}, wantRequests: 3},
		{name: "transient exhausted", statuses: []int{http.StatusInternalServerError}, wantKind: ErrTransient, wantRequests: 3, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			p := fakeHTTPProvider(t, func(w http.ResponseWriter, _ *http.Request) {
				call := int(requests.Add(1))
				status := tt.statuses[min(call-1, len(tt.statuses)-1)]
				if status == http.StatusOK {
					writeJSON(w, status, success)
					return
				}
				writeJSON(w, status, apiErrorBody("api_error"))
			})

			_, err := p.Create(context.Background(), &provider.Resource{
				Addr:   "agent.minimal",
				Config: loadObject(t, "minimal_spec.json"),
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Create error = %v, wantErr=%v", err, tt.wantErr)
			}
			if tt.wantKind != nil && !errors.Is(err, tt.wantKind) {
				t.Errorf("Create error = %v, want kind %v", err, tt.wantKind)
			}
			if tt.wantKind == ErrAuthentication && !strings.Contains(err.Error(), defaultAPIKeyEnv) {
				t.Errorf("authentication error does not name %s: %v", defaultAPIKeyEnv, err)
			}
			if got := requests.Load(); got != tt.wantRequests {
				t.Errorf("requests = %d, want %d", got, tt.wantRequests)
			}
		})
	}
}

func TestRequestPacersMatchDocumentedLimits(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		rpm      int
		wantWait time.Duration
	}{
		{name: "create", rpm: 300, wantWait: 200 * time.Millisecond},
		{name: "read", rpm: 600, wantWait: 100 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pacer := newRequestPacer(tt.rpm)
			pacer.now = func() time.Time { return now }
			var waits []time.Duration
			sleep := func(_ context.Context, delay time.Duration) error {
				waits = append(waits, delay)
				return nil
			}
			if err := pacer.Wait(context.Background(), sleep); err != nil {
				t.Fatalf("first Wait: %v", err)
			}
			if err := pacer.Wait(context.Background(), sleep); err != nil {
				t.Fatalf("second Wait: %v", err)
			}
			if len(waits) != 1 || waits[0] != tt.wantWait {
				t.Errorf("waits = %v, want [%s]", waits, tt.wantWait)
			}
		})
	}
}

func fakeHTTPProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p := New(
		option.WithBaseURL(server.URL),
		option.WithAPIKey(testAPIKey),
	)
	p.createPacer.interval = 0
	p.readPacer.interval = 0
	p.sleep = func(context.Context, time.Duration) error { return nil }
	return p
}

func assertCommonRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("X-Api-Key"); got != testAPIKey {
		t.Errorf("X-Api-Key = %q, want test key", got)
	}
	if got := r.Header.Get("anthropic-beta"); got != BetaHeader {
		t.Errorf("anthropic-beta = %q, want %q", got, BetaHeader)
	}
}

func decodeRequestObject(t *testing.T, r *http.Request) provider.Object {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("read request body: %v", err)
		return nil
	}
	var object provider.Object
	if err := json.Unmarshal(data, &object); err != nil {
		t.Errorf("decode request body %q: %v", data, err)
		return nil
	}
	return object
}

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	object := loadObject(t, name)
	data, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("encode fixture %s: %v", name, err)
	}
	return data
}

func mutateFixture(t *testing.T, data []byte, mutate func(provider.Object)) []byte {
	t.Helper()
	var object provider.Object
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	mutate(object)
	updated, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("encode mutated fixture: %v", err)
	}
	return updated
}

func apiErrorBody(errorType string) []byte {
	return []byte(fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":"test failure"}}`, errorType))
}

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
