package claude

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/getkastordev/kastor/internal/provider"
)

// All assumptions about managed-agents-2026-04-01 response and request
// shapes live in this file. When BetaHeader changes, this is the only
// production file that should need a schema audit.

// BetaHeader is the Managed Agents API revision implemented by this file.
const BetaHeader = "managed-agents-2026-04-01"

const (
	managedMarkerKey  = "kastor_managed"
	defaultModelSpeed = "standard"

	agentToolsetType = "agent_toolset_20260401"
	mcpToolsetType   = "mcp_toolset"

	alwaysAllowPolicy = "always_allow"
	alwaysAskPolicy   = "always_ask"
)

var managedAgentTools = map[string]bool{
	"bash":       true,
	"edit":       true,
	"glob":       true,
	"grep":       true,
	"read":       true,
	"web_fetch":  true,
	"web_search": true,
	"write":      true,
}

// normalizationRules carry the spec-owned portions of open API objects.
// Remote metadata and model keys outside these sets are platform- or
// operator-owned and do not participate in comparison.
type normalizationRules struct {
	metadataKeys map[string]bool
}

// normalizeForDiff returns two independent comparison objects with all
// provider-injected and provider-owned fields handled.
//
// A nil remote is the create path: the spec is still normalized in full —
// that is what rejects a config Managed Agents cannot express — and compared
// against an empty object, so every attribute reads as an addition. There is
// no marker to assert, because Create is what stamps it.
func normalizeForDiff(desired *provider.Resource, remote provider.Object) (provider.Object, provider.Object, error) {
	spec, rules, err := normalizeDesired(desired)
	if err != nil {
		return nil, nil, err
	}
	if remote == nil {
		delete(spec["metadata"].(map[string]any), managedMarkerKey)
		return spec, provider.Object{}, nil
	}
	echo, err := normalizeAPIEcho(remote, rules)
	if err != nil {
		return nil, nil, err
	}
	if err := assertManagedMarker(desired.Addr, spec, echo); err != nil {
		return nil, nil, err
	}

	// The marker is an ownership assertion, not user configuration. Remove it
	// after validation so it can never appear as an AttrDiff.
	delete(spec["metadata"].(map[string]any), managedMarkerKey)
	delete(echo["metadata"].(map[string]any), managedMarkerKey)
	return spec, echo, nil
}

// normalizeCreateParams converts the comparison shape into the SDK request
// shape. The SDK parameter override keeps serialization typed through the SDK
// while preserving explicit nulls and empty arrays from the normalized object.
func normalizeCreateParams(desired *provider.Resource) (anthropic.BetaAgentNewParams, error) {
	request, err := normalizeAPIRequest(desired)
	if err != nil {
		return anthropic.BetaAgentNewParams{}, err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return anthropic.BetaAgentNewParams{}, fmt.Errorf("%s: encode Claude Managed Agents create request: %w", desired.Addr, err)
	}

	var params anthropic.BetaAgentNewParams
	param.SetJSON(data, &params)
	return params, nil
}

// normalizeUpdateParams emits a full replacement for every Kastor-owned
// field, plus the version fetched immediately before the update. Metadata is
// the exception to full replacement in the API: null values are retained as
// key-level deletion tombstones.
func normalizeUpdateParams(desired *provider.Resource, version int64) (anthropic.BetaAgentUpdateParams, error) {
	if version < 1 {
		return anthropic.BetaAgentUpdateParams{}, fmt.Errorf("%s: remote version must be at least 1, got %d", desired.Addr, version)
	}
	request, err := normalizeAPIRequest(desired)
	if err != nil {
		return anthropic.BetaAgentUpdateParams{}, err
	}
	request["version"] = version

	if rawMetadata, exists := desired.Config["metadata"]; exists && rawMetadata != nil {
		metadata := rawMetadata.(map[string]any)
		requestMetadata := request["metadata"].(map[string]any)
		for key, value := range metadata {
			if value == nil {
				requestMetadata[key] = nil
			}
		}
	}

	data, err := json.Marshal(request)
	if err != nil {
		return anthropic.BetaAgentUpdateParams{}, fmt.Errorf("%s: encode Claude Managed Agents update request: %w", desired.Addr, err)
	}
	var params anthropic.BetaAgentUpdateParams
	param.SetJSON(data, &params)
	return params, nil
}

// normalizeAPIRequest removes comparison-only defaults from the normalized
// spec. The MCP toolset's default permission is the only one: it governs the
// tools the agent does not declare, so authoring it would be Kastor asserting
// a policy over tools it was never given. Per-tool permissions are the
// opposite — Kastor states those explicitly on every declared tool, because
// leaving them unset is what the platform reads as a denial (see enabledTool).
func normalizeAPIRequest(desired *provider.Resource) (provider.Object, error) {
	spec, _, err := normalizeDesired(desired)
	if err != nil {
		return nil, err
	}
	request := cloneValue(spec).(provider.Object)
	for _, value := range request["tools"].([]any) {
		toolset := value.(map[string]any)
		if toolset["type"] == mcpToolsetType {
			defaultConfig := toolset["default_config"].(map[string]any)
			delete(defaultConfig, "permission_policy")
		}
	}
	return request, nil
}

// normalizeAPIResponse decodes the SDK's unmodified response JSON into the
// provider-neutral value model. Using RawJSON is required because archived_at
// is nullable on the wire but represented as time.Time by the SDK.
func normalizeAPIResponse(agent *anthropic.BetaManagedAgentsAgent) (provider.Object, error) {
	if agent == nil {
		return nil, fmt.Errorf("claude: API returned a nil agent")
	}
	raw := agent.RawJSON()
	if raw == "" {
		return nil, fmt.Errorf("claude: API returned an agent without response JSON")
	}
	var object provider.Object
	if err := json.Unmarshal([]byte(raw), &object); err != nil {
		return nil, fmt.Errorf("claude: decode Managed Agents response: %w", err)
	}
	return object, nil
}

func normalizedAPIID(remote provider.Object) (string, error) {
	return requiredString(remote["id"], "remote.id")
}

func normalizedAPIVersion(remote provider.Object) (int64, error) {
	raw, ok := remote["version"].(float64)
	if !ok || raw < 1 || raw != float64(int64(raw)) {
		return 0, fmt.Errorf("remote.version must be a positive integer, got %v", remote["version"])
	}
	return int64(raw), nil
}

func normalizedAPIArchived(remote provider.Object) (bool, error) {
	value, exists := remote["archived_at"]
	if !exists {
		return false, fmt.Errorf("remote.archived_at is missing")
	}
	return value != nil, nil
}

// NormalizeStateConfig returns the resolved comparison form stored as the
// provider's last-applied config. It preserves the MCP URL and server defaults
// used for this apply instead of resolving them again during a later plan.
func (*Provider) NormalizeStateConfig(desired *provider.Resource) (provider.Object, error) {
	spec, _, err := normalizeDesired(desired)
	return spec, err
}

// normalizeDesired accepts both the neutral module form and the canonical form
// written to state by NormalizeStateConfig.
func normalizeDesired(desired *provider.Resource) (provider.Object, normalizationRules, error) {
	if desired == nil {
		return nil, normalizationRules{}, fmt.Errorf("claude: desired resource is nil")
	}
	if _, normalized := desired.Config["name"]; !normalized {
		return normalizeSpec(desired)
	}

	metadata, ok := desired.Config["metadata"].(map[string]any)
	if !ok {
		return nil, normalizationRules{}, fmt.Errorf("%s: normalized state metadata must be an object, got %T", desired.Addr, desired.Config["metadata"])
	}
	keys := make(map[string]bool, len(metadata))
	for key := range metadata {
		keys[key] = true
	}
	rules := normalizationRules{metadataKeys: keys}
	spec, err := normalizeAPIEcho(desired.Config, rules)
	if err != nil {
		return nil, normalizationRules{}, fmt.Errorf("%s: normalize stored config: %w", desired.Addr, err)
	}
	return spec, rules, nil
}

// normalizeSpec maps the provider-neutral agent closure to the API shape
// Kastor owns. Inputs and outputs are intentionally absent: Managed Agents
// has no corresponding agent-resource fields.
func normalizeSpec(desired *provider.Resource) (provider.Object, normalizationRules, error) {
	if desired == nil {
		return nil, normalizationRules{}, fmt.Errorf("claude: desired resource is nil")
	}
	name, err := agentName(desired.Addr)
	if err != nil {
		return nil, normalizationRules{}, err
	}
	cfg := desired.Config
	if cfg == nil {
		return nil, normalizationRules{}, fmt.Errorf("%s: desired config is nil", desired.Addr)
	}

	model, err := normalizeSpecModel(desired.Addr, cfg["model"])
	if err != nil {
		return nil, normalizationRules{}, err
	}

	description, err := optionalString(cfg, "description", desired.Addr+".description")
	if err != nil {
		return nil, normalizationRules{}, err
	}
	system, err := optionalString(cfg, "instructions", desired.Addr+".instructions")
	if err != nil {
		return nil, normalizationRules{}, err
	}

	tools, mcpServers, err := normalizeSpecTools(desired.Addr, cfg["tools"], cfg["mcp_servers"], cfg["requires_approval"])
	if err != nil {
		return nil, normalizationRules{}, err
	}
	skills, err := optionalArray(cfg, "skills", desired.Addr+".skills")
	if err != nil {
		return nil, normalizationRules{}, err
	}

	metadata, metadataKeys, err := normalizeSpecMetadata(desired.Addr, cfg["metadata"])
	if err != nil {
		return nil, normalizationRules{}, err
	}

	spec := provider.Object{
		"name":        name,
		"model":       model,
		"system":      system,
		"description": description,
		"tools":       tools,
		"mcp_servers": mcpServers,
		"skills":      skills,
		"metadata":    metadata,
	}
	return spec, normalizationRules{
		metadataKeys: metadataKeys,
	}, nil
}

// normalizeAPIEcho projects a recorded/read API response onto the fields
// Kastor owns. The response envelope (id and type), version, and timestamps
// are deliberately excluded. Model defaults and foreign metadata are also
// projected using the ownership rules derived from the spec.
func normalizeAPIEcho(remote provider.Object, rules normalizationRules) (provider.Object, error) {
	if remote == nil {
		return nil, fmt.Errorf("claude: remote object is nil")
	}

	name, err := requiredString(remote["name"], "remote.name")
	if err != nil {
		return nil, err
	}
	model, err := normalizeEchoModel(remote["model"])
	if err != nil {
		return nil, err
	}
	description, err := optionalString(remote, "description", "remote.description")
	if err != nil {
		return nil, err
	}
	system, err := optionalString(remote, "system", "remote.system")
	if err != nil {
		return nil, err
	}
	tools, err := optionalArray(remote, "tools", "remote.tools")
	if err != nil {
		return nil, err
	}
	tools, err = normalizeEchoTools(tools)
	if err != nil {
		return nil, err
	}
	mcpServers, err := optionalArray(remote, "mcp_servers", "remote.mcp_servers")
	if err != nil {
		return nil, err
	}
	mcpServers, err = normalizeEchoMCPServers(mcpServers)
	if err != nil {
		return nil, err
	}
	skills, err := optionalArray(remote, "skills", "remote.skills")
	if err != nil {
		return nil, err
	}
	metadata, err := normalizeEchoMetadata(remote["metadata"], rules.metadataKeys)
	if err != nil {
		return nil, err
	}

	return provider.Object{
		"name":        name,
		"model":       model,
		"system":      system,
		"description": description,
		"tools":       tools,
		"mcp_servers": mcpServers,
		"skills":      skills,
		"metadata":    metadata,
	}, nil
}

func normalizeSpecModel(addr string, raw any) (map[string]any, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: model must be an object, got %T", addr, raw)
	}
	providerName, err := requiredString(obj["provider"], addr+".model.provider")
	if err != nil {
		return nil, err
	}
	if providerName != "anthropic" {
		return nil, fmt.Errorf("%s: model.provider is %q, expected %q for Claude Managed Agents", addr, providerName, "anthropic")
	}
	id, err := requiredString(obj["id"], addr+".model.id")
	if err != nil {
		return nil, err
	}

	model := map[string]any{"id": id, "speed": defaultModelSpeed}
	if rawParams, exists := obj["params"]; exists {
		params, ok := rawParams.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: model.params must be an object, got %T", addr, rawParams)
		}
		for key, value := range params {
			if key != "speed" {
				return nil, fmt.Errorf("%s: model.params.%s is unsupported by Claude Managed Agents, expected only \"speed\"", addr, key)
			}
			speed, err := requiredString(value, addr+".model.params.speed")
			if err != nil {
				return nil, err
			}
			model["speed"] = speed
		}
	}
	return model, nil
}

// normalizeEchoModel accepts both API request forms. String models are
// lifted to the response object form and omitted speed defaults to standard.
// anthropic-sdk-go v1.61.0 also exposes an SDK-only Effort field that is not
// documented by Agent Setup. Kastor intentionally compares only id and speed;
// unknown response fields are not part of the compared object.
func normalizeEchoModel(raw any) (map[string]any, error) {
	switch model := raw.(type) {
	case string:
		if model == "" {
			return nil, fmt.Errorf("remote.model must not be empty")
		}
		return map[string]any{"id": model, "speed": defaultModelSpeed}, nil
	case map[string]any:
		id, err := requiredString(model["id"], "remote.model.id")
		if err != nil {
			return nil, err
		}
		out := map[string]any{"id": id, "speed": defaultModelSpeed}
		if speed, exists := model["speed"]; exists {
			value, err := requiredString(speed, "remote.model.speed")
			if err != nil {
				return nil, err
			}
			out["speed"] = value
		}
		return out, nil
	default:
		return nil, fmt.Errorf("remote.model must be a string or object, got %T", raw)
	}
}

func normalizeSpecMetadata(addr string, raw any) (map[string]any, map[string]bool, error) {
	metadata := map[string]any{}
	keys := map[string]bool{}
	if raw != nil {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("%s: metadata must be an object, got %T", addr, raw)
		}
		for key, value := range obj {
			keys[key] = true
			// A null metadata value is a deletion tombstone. Keep the key in
			// the ownership set but omit it from the desired object so an
			// already-deleted remote key is in sync.
			if value != nil {
				metadata[key] = cloneValue(value)
			}
		}
	}
	metadata[managedMarkerKey] = addr
	keys[managedMarkerKey] = true
	return metadata, keys, nil
}

func normalizeEchoMetadata(raw any, owned map[string]bool) (map[string]any, error) {
	out := map[string]any{}
	if raw == nil {
		return out, nil
	}
	metadata, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("remote.metadata must be an object, got %T", raw)
	}
	for key := range owned {
		if value, exists := metadata[key]; exists {
			out[key] = cloneValue(value)
		}
	}
	return out, nil
}

type toolsetBuilder struct {
	object  map[string]any
	configs []any
}

// normalizeSpecTools groups the closure's tools into Managed Agents toolsets
// and renders the URL connections its MCP tools need. Server connection config
// comes from the closure's mcp_servers (from the module's mcp_server blocks,
// SPEC.md §3.6) — never from the environment, which would make desired state
// depend on the operator's shell and write a shell-derived value into state.
func normalizeSpecTools(addr string, raw, rawServers, rawApprovals any) ([]any, []any, error) {
	neutral, err := arrayValue(raw, addr+".tools")
	if err != nil {
		return nil, nil, err
	}
	requiresApproval, err := specApprovalSet(addr, rawApprovals)
	if err != nil {
		return nil, nil, err
	}
	declared, err := specMCPServers(addr, rawServers)
	if err != nil {
		return nil, nil, err
	}

	var tools []any
	var mcpServers []any
	groups := map[string]*toolsetBuilder{}
	for i, value := range neutral {
		tool, ok := value.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("%s: tools[%d] must be an object, got %T", addr, i, value)
		}
		name, err := requiredString(tool["name"], fmt.Sprintf("%s.tools[%d].name", addr, i))
		if err != nil {
			return nil, nil, err
		}
		blockAddr := "tool." + name
		gated := requiresApproval[blockAddr]
		source, ok := tool["source"].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("%s: source must be an object", blockAddr)
		}
		kind, err := requiredString(source["kind"], blockAddr+".source.kind")
		if err != nil {
			return nil, nil, err
		}

		switch kind {
		case "builtin":
			if !managedAgentTools[name] {
				return nil, nil, fmt.Errorf("%s: builtin tool %q is not in the %s toolset", blockAddr, name, agentToolsetType)
			}
			group := groups["builtin"]
			if group == nil {
				group = newToolset(agentToolsetType, "")
				groups["builtin"] = group
				tools = append(tools, group.object)
			}
			group.configs = append(group.configs, enabledTool(name, gated))
		case "mcp":
			uri, err := requiredString(source["uri"], blockAddr+".source.uri")
			if err != nil {
				return nil, nil, err
			}
			server, toolName, err := parseMCPIdentity(blockAddr, uri)
			if err != nil {
				return nil, nil, err
			}
			key := "mcp:" + server
			group := groups[key]
			if group == nil {
				group = newToolset(mcpToolsetType, server)
				groups[key] = group
				tools = append(tools, group.object)
				declaration, ok := declared[server]
				if !ok {
					return nil, nil, fmt.Errorf("%s: MCP server %q is not declared in the closure; add an mcp_server block for it", blockAddr, server)
				}
				if declaration.transport != "http" {
					return nil, nil, fmt.Errorf("%s: mcp_server.%s uses transport %q; Claude Managed Agents dials a URL and cannot spawn a local process", blockAddr, server, declaration.transport)
				}
				if declaration.url == "" {
					return nil, nil, fmt.Errorf("%s: mcp_server.%s declares no url", blockAddr, server)
				}
				mcpServers = append(mcpServers, map[string]any{
					"type": "url",
					"name": server,
					"url":  declaration.url,
				})
			}
			group.configs = append(group.configs, enabledTool(toolName, gated))
		case "http", "script", "runtime":
			return nil, nil, unsupportedToolKind(blockAddr, kind)
		default:
			return nil, nil, fmt.Errorf("%s: source kind is %q, expected \"mcp\" or \"builtin\" for Claude Managed Agents", blockAddr, kind)
		}
	}

	for _, group := range groups {
		group.object["configs"] = group.configs
	}
	if tools == nil {
		tools = []any{}
	}
	tools = normalizeSpecToolsetDefaults(tools)
	if mcpServers == nil {
		mcpServers = []any{}
	}
	return tools, mcpServers, nil
}

func specApprovalSet(addr string, raw any) (map[string]bool, error) {
	values, err := arrayValue(raw, addr+".requires_approval")
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(values))
	for i, value := range values {
		ref, err := requiredString(value, fmt.Sprintf("%s.requires_approval[%d]", addr, i))
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(ref, "tool.") || strings.TrimPrefix(ref, "tool.") == "" {
			return nil, fmt.Errorf("%s.requires_approval[%d] is %q, expected tool.<name>", addr, i, ref)
		}
		set[ref] = true
	}
	return set, nil
}

func newToolset(typ, server string) *toolsetBuilder {
	defaultConfig := map[string]any{"enabled": false}
	if typ == agentToolsetType {
		defaultConfig["permission_policy"] = map[string]any{
			"type": alwaysAllowPolicy,
		}
	}
	obj := map[string]any{
		"type":           typ,
		"default_config": defaultConfig,
	}
	if server != "" {
		obj["mcp_server_name"] = server
	}
	return &toolsetBuilder{object: obj}
}

// normalizeSpecToolsetDefaults applies the API's resolved response defaults
// to the comparison copy. Only the MCP toolset default needs this: the request
// omits it (see normalizeAPIRequest), and the platform resolves the omission
// to always_ask for the undeclared tools that default governs. The tools the
// agent does declare carry their own policy and never fall through to it.
func normalizeSpecToolsetDefaults(tools []any) []any {
	normalized := cloneValue(tools).([]any)
	for _, value := range normalized {
		toolset := value.(map[string]any)
		if toolset["type"] != mcpToolsetType {
			continue
		}
		defaultConfig := toolset["default_config"].(map[string]any)
		defaultConfig["permission_policy"] = map[string]any{
			"type": alwaysAskPolicy,
		}
	}
	return normalized
}

// enabledTool renders one declared tool. Declaring a tool in the spec is the
// grant: an agent that lists tool.y is permitted to call it, so the permission
// is stated on the request instead of inherited. Leaving it unset is what
// deploys agents that cannot call any of the tools they declare — the platform
// reads an absent permission as a denial.
func enabledTool(name string, requiresApproval bool) map[string]any {
	policy := alwaysAllowPolicy
	if requiresApproval {
		policy = alwaysAskPolicy
	}
	return map[string]any{
		"name":              name,
		"enabled":           true,
		"permission_policy": map[string]any{"type": policy},
	}
}

func unsupportedToolKind(addr, kind string) error {
	return fmt.Errorf("%s: source kind %q cannot be mapped to Claude Managed Agents; custom tools are client-executed and kastor is not a runtime; use an MCP-server wrapper with source kind \"mcp\"", addr, kind)
}

func parseMCPIdentity(addr, uri string) (string, string, error) {
	const prefix = "mcp://"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", fmt.Errorf("%s: MCP source uri is %q, expected mcp://<server>/<tool>", addr, uri)
	}
	identity := strings.TrimPrefix(uri, prefix)
	server, tool, ok := strings.Cut(identity, "/")
	if !ok || server == "" || tool == "" || strings.Contains(tool, "/") {
		return "", "", fmt.Errorf("%s: MCP source uri is %q, expected mcp://<server>/<tool>", addr, uri)
	}
	return server, tool, nil
}

// specMCPServer is one declared server from the neutral closure, reduced to
// the fields the Claude connection needs. AuthRef is carried for kastor doctor
// (SPEC.md §5.3) and is deliberately never sent to the platform: kastor sends
// the server's name and URL only, and the credential stays on the platform.
type specMCPServer struct {
	transport string
	url       string
	authRef   string
}

// specMCPServers indexes the closure's mcp_servers by name.
func specMCPServers(addr string, raw any) (map[string]specMCPServer, error) {
	servers := map[string]specMCPServer{}
	if raw == nil {
		return servers, nil
	}
	list, err := arrayValue(raw, addr+".mcp_servers")
	if err != nil {
		return nil, err
	}
	for i, value := range list {
		path := fmt.Sprintf("%s.mcp_servers[%d]", addr, i)
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be an object, got %T", path, value)
		}
		name, err := requiredString(obj["name"], path+".name")
		if err != nil {
			return nil, err
		}
		transport, err := plainString(obj, "transport", path+".transport")
		if err != nil {
			return nil, err
		}
		if transport == "" {
			transport = "http"
		}
		url, err := plainString(obj, "url", path+".url")
		if err != nil {
			return nil, err
		}
		authRef, err := plainString(obj, "auth_ref", path+".auth_ref")
		if err != nil {
			return nil, err
		}
		servers[name] = specMCPServer{transport: transport, url: url, authRef: authRef}
	}
	return servers, nil
}

func normalizeEchoTools(tools []any) ([]any, error) {
	out := make([]any, len(tools))
	for i, value := range tools {
		toolset, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("remote.tools[%d] must be an object, got %T", i, value)
		}
		path := fmt.Sprintf("remote.tools[%d]", i)
		typ, err := requiredString(toolset["type"], path+".type")
		if err != nil {
			return nil, err
		}
		if typ != agentToolsetType && typ != mcpToolsetType {
			return nil, fmt.Errorf("%s.type is %q, expected %q or %q", path, typ, agentToolsetType, mcpToolsetType)
		}

		defaultConfig, err := normalizeEchoDefaultToolConfig(toolset["default_config"], path+".default_config")
		if err != nil {
			return nil, err
		}
		configs, err := arrayValue(toolset["configs"], path+".configs")
		if err != nil {
			return nil, err
		}
		normalizedConfigs := make([]any, len(configs))
		for j, rawConfig := range configs {
			config, ok := rawConfig.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s.configs[%d] must be an object, got %T", path, j, rawConfig)
			}
			configPath := fmt.Sprintf("%s.configs[%d]", path, j)
			name, err := requiredString(config["name"], configPath+".name")
			if err != nil {
				return nil, err
			}
			enabled, err := requiredBool(config["enabled"], configPath+".enabled")
			if err != nil {
				return nil, err
			}
			policy, err := normalizeEchoToolPermission(config["permission_policy"], configPath+".permission_policy")
			if err != nil {
				return nil, err
			}
			normalizedConfigs[j] = map[string]any{
				"name":              name,
				"enabled":           enabled,
				"permission_policy": policy,
			}
		}

		normalized := map[string]any{
			"type":           typ,
			"default_config": defaultConfig,
			"configs":        normalizedConfigs,
		}
		if typ == mcpToolsetType {
			server, err := requiredString(toolset["mcp_server_name"], path+".mcp_server_name")
			if err != nil {
				return nil, err
			}
			normalized["mcp_server_name"] = server
		}
		out[i] = normalized
	}
	return out, nil
}

// normalizeEchoToolPermission projects one declared tool's permission. The
// API always returns the attribute; the absent case is a config written to
// state before KAS-57, when Kastor authored no permission at all. Those read
// as the grant the spec now states, so the first plan after the upgrade
// reports the remote denial as the drift it is, rather than blaming the
// state file for a value it never recorded.
func normalizeEchoToolPermission(raw any, path string) (map[string]any, error) {
	if raw == nil {
		return map[string]any{"type": alwaysAllowPolicy}, nil
	}
	policy, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object, got %T", path, raw)
	}
	typ, err := requiredString(policy["type"], path+".type")
	if err != nil {
		return nil, err
	}
	return map[string]any{"type": typ}, nil
}

func normalizeEchoDefaultToolConfig(raw any, path string) (map[string]any, error) {
	config, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object, got %T", path, raw)
	}
	enabled, err := requiredBool(config["enabled"], path+".enabled")
	if err != nil {
		return nil, err
	}
	policy, ok := config["permission_policy"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s.permission_policy must be an object, got %T", path, config["permission_policy"])
	}
	typ, err := requiredString(policy["type"], path+".permission_policy.type")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"enabled":           enabled,
		"permission_policy": map[string]any{"type": typ},
	}, nil
}

func normalizeEchoMCPServers(servers []any) ([]any, error) {
	out := make([]any, len(servers))
	for i, value := range servers {
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("remote.mcp_servers[%d] must be an object, got %T", i, value)
		}
		typ, err := requiredString(obj["type"], fmt.Sprintf("remote.mcp_servers[%d].type", i))
		if err != nil {
			return nil, err
		}
		name, err := requiredString(obj["name"], fmt.Sprintf("remote.mcp_servers[%d].name", i))
		if err != nil {
			return nil, err
		}
		url, err := requiredString(obj["url"], fmt.Sprintf("remote.mcp_servers[%d].url", i))
		if err != nil {
			return nil, err
		}
		out[i] = map[string]any{"type": typ, "name": name, "url": url}
	}
	return out, nil
}

func assertManagedMarker(addr string, spec, remote provider.Object) error {
	want := spec["metadata"].(map[string]any)[managedMarkerKey]
	got, exists := remote["metadata"].(map[string]any)[managedMarkerKey]
	if !exists || !reflect.DeepEqual(got, want) {
		return fmt.Errorf("%s: remote metadata.%s is %v, expected %q; refusing to compare an object not marked as managed by kastor", addr, managedMarkerKey, got, want)
	}
	return nil
}

func agentName(addr string) (string, error) {
	const prefix = "agent."
	if !strings.HasPrefix(addr, prefix) || strings.TrimPrefix(addr, prefix) == "" {
		return "", fmt.Errorf("claude: resource address is %q, expected agent.<name>", addr)
	}
	return strings.TrimPrefix(addr, prefix), nil
}

// plainString reads an optional string out of a neutral config object,
// yielding "" when the key is absent. optionalString is its API-echo
// counterpart: there null and absent must stay distinguishable, here they
// cannot be — a module never writes a null into the closure.
func plainString(obj map[string]any, key, path string) (string, error) {
	value, exists := obj[key]
	if !exists || value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string, got %T", path, value)
	}
	return text, nil
}

func optionalString(obj map[string]any, key, path string) (any, error) {
	value, exists := obj[key]
	if !exists || value == nil {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("%s must be a string or null, got %T", path, value)
	}
	return text, nil
}

func requiredString(value any, path string) (string, error) {
	text, ok := value.(string)
	if !ok || text == "" {
		return "", fmt.Errorf("%s must be a non-empty string, got %v", path, value)
	}
	return text, nil
}

func requiredBool(value any, path string) (bool, error) {
	boolean, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean, got %T", path, value)
	}
	return boolean, nil
}

func optionalArray(obj map[string]any, key, path string) ([]any, error) {
	value, exists := obj[key]
	if !exists || value == nil {
		return []any{}, nil
	}
	return arrayValue(value, path)
}

func arrayValue(value any, path string) ([]any, error) {
	if value == nil {
		return []any{}, nil
	}
	array, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array, got %T", path, value)
	}
	return cloneValue(array).([]any), nil
}

func cloneValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, child := range value {
			out[key] = cloneValue(child)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, child := range value {
			out[i] = cloneValue(child)
		}
		return out
	default:
		return value
	}
}

// replaceArray reports the fields whose API update semantics replace the
// complete array. The comparator uses this to render one AttrDiff per
// changed element rather than recursing into an element's fields.
func replaceArray(path string) bool {
	switch path {
	case "tools", "mcp_servers", "skills":
		return true
	default:
		return false
	}
}
