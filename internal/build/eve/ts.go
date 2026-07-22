package eve

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// tsString renders s as a double-quoted single-line TypeScript string literal.
func tsString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tsLiteral renders a decoded HCL literal (see parser) as a TypeScript literal.
func tsLiteral(v any) (string, error) {
	switch v := v.(type) {
	case bool:
		return strconv.FormatBool(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	case string:
		return tsString(v), nil
	}
	return "", fmt.Errorf("unsupported literal %v (%T)", v, v)
}

// tsKey renders an object-literal key: bare when it is a plain identifier,
// quoted otherwise. Reserved words are legal as bare object keys in
// JavaScript, so only the character set matters.
func tsKey(s string) string {
	if isTSIdent(s) {
		return s
	}
	return tsString(s)
}

func isTSIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		alpha := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || r == '$'
		if !alpha && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// envName maps an MCP server name to the environment variable holding its
// endpoint URL: characters outside [A-Za-z0-9] become underscores.
func envName(server string) string {
	var b strings.Builder
	b.WriteString("KASTOR_MCP_")
	for _, r := range strings.ToUpper(server) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	b.WriteString("_URL")
	return b.String()
}

// sortedCopy returns in re-sorted by name without mutating the module.
func sortedCopy[T any](in []T, name func(T) string) []T {
	out := append([]T(nil), in...)
	sort.Slice(out, func(i, j int) bool { return name(out[i]) < name(out[j]) })
	return out
}

// sortedKeys returns the keys of a set in sorted order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
