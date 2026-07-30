package claude

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/weirdGuy/kastor/internal/provider"
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
	modelKeys    map[string]bool
}

// normalizeForDiff returns two independent comparison objects with all
// provider-injected and provider-owned fields handled.
func normalizeForDiff(desired *provider.Resource, remote provider.Object) (provider.Object, provider.Object, error) {
	spec, rules, err := normalizeSpec(desired)
	if err != nil {
		return nil, nil, err
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

	model, modelKeys, err := normalizeSpecModel(desired.Addr, cfg["model"])
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

	tools, mcpServers, err := normalizeSpecTools(desired.Addr, cfg["tools"])
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
		modelKeys:    modelKeys,
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
	model, err := normalizeEchoModel(remote["model"], rules.modelKeys)
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

func normalizeSpecModel(addr string, raw any) (map[string]any, map[string]bool, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("%s: model must be an object, got %T", addr, raw)
	}
	providerName, err := requiredString(obj["provider"], addr+".model.provider")
	if err != nil {
		return nil, nil, err
	}
	if providerName != "anthropic" {
		return nil, nil, fmt.Errorf("%s: model.provider is %q, expected %q for Claude Managed Agents", addr, providerName, "anthropic")
	}
	id, err := requiredString(obj["id"], addr+".model.id")
	if err != nil {
		return nil, nil, err
	}

	model := map[string]any{"id": id, "speed": defaultModelSpeed}
	keys := map[string]bool{"id": true, "speed": true}
	if rawParams, exists := obj["params"]; exists {
		params, ok := rawParams.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("%s: model.params must be an object, got %T", addr, rawParams)
		}
		for key, value := range params {
			model[key] = cloneValue(value)
			keys[key] = true
		}
	}
	if speed, ok := model["speed"]; ok {
		if _, err := requiredString(speed, addr+".model.params.speed"); err != nil {
			return nil, nil, err
		}
	}
	return model, keys, nil
}

// normalizeEchoModel accepts both API request forms. String models are
// lifted to the response object form and omitted speed defaults to standard.
// Response-only defaults such as effort are ignored unless the spec owns the
// corresponding model param.
func normalizeEchoModel(raw any, owned map[string]bool) (map[string]any, error) {
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
		for key := range owned {
			if key == "id" || key == "speed" {
				continue
			}
			if value, exists := model[key]; exists {
				out[key] = cloneValue(value)
			}
		}
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

func normalizeSpecTools(addr string, raw any) ([]any, []any, error) {
	neutral, err := arrayValue(raw, addr+".tools")
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
			group.configs = append(group.configs, enabledTool(name))
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
				// SPEC.md makes source.uri an identity pin, not deployment
				// transport. The API echo's required URL is therefore
				// intentionally projected out by normalizeEchoMCPServers.
				mcpServers = append(mcpServers, map[string]any{
					"type": "url",
					"name": server,
				})
			}
			group.configs = append(group.configs, enabledTool(toolName))
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
	if mcpServers == nil {
		mcpServers = []any{}
	}
	return tools, mcpServers, nil
}

func newToolset(typ, server string) *toolsetBuilder {
	obj := map[string]any{
		"type": typ,
		"default_config": map[string]any{
			"enabled": false,
			"permission_policy": map[string]any{
				"type": "always_allow",
			},
		},
	}
	if server != "" {
		obj["mcp_server_name"] = server
	}
	return &toolsetBuilder{object: obj}
}

func enabledTool(name string) map[string]any {
	return map[string]any{"name": name, "enabled": true}
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

func normalizeEchoTools(tools []any) ([]any, error) {
	out := make([]any, len(tools))
	for i, value := range tools {
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("remote.tools[%d] must be an object, got %T", i, value)
		}
		out[i] = cloneValue(obj)
	}
	return out, nil
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
		out[i] = map[string]any{"type": typ, "name": name}
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
