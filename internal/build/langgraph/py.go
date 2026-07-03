package langgraph

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// pyKeywords are the Python 3 keywords; a name that collides gets a trailing
// underscore so generated code stays importable.
var pyKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true, "class": true,
	"continue": true, "def": true, "del": true, "elif": true, "else": true,
	"except": true, "finally": true, "for": true, "from": true, "global": true,
	"if": true, "import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
}

// pyIdent maps an ADL name to a valid Python identifier: characters outside
// [A-Za-z0-9_] become underscores, a leading digit gets an underscore
// prefix, and Python keywords get an underscore suffix.
func pyIdent(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	id := b.String()
	if id == "" {
		id = "_"
	}
	if id[0] >= '0' && id[0] <= '9' {
		id = "_" + id
	}
	if pyKeywords[id] {
		id += "_"
	}
	return id
}

// pyString renders s as a double-quoted single-line Python string literal.
func pyString(s string) string {
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

// pyTriple renders s as a triple-quoted Python string literal, keeping the
// body readable: newlines stay verbatim; only backslashes and quote runs
// that would terminate the literal are escaped.
func pyTriple(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"""`, `\"\"\"`)
	if strings.HasSuffix(s, `"`) {
		s = s[:len(s)-1] + `\"`
	}
	return `"""` + s + `"""`
}

// pyLiteral renders a decoded HCL literal (see parser) as a Python literal.
func pyLiteral(v any) (string, error) {
	switch v := v.(type) {
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), nil
	case string:
		return pyString(v), nil
	}
	return "", fmt.Errorf("unsupported literal %v (%T)", v, v)
}

// pyType maps an ADL type keyword to its Python type hint.
func pyType(t string) string {
	switch t {
	case "string":
		return "str"
	case "number":
		return "float"
	case "bool":
		return "bool"
	}
	return "object"
}

// pyParam is one parameter of a generated function signature.
type pyParam struct {
	name   string // python identifier
	source string // original ADL name, used for dict keys and diagnostics
	hint   string // type hint
	def    string // default value literal; "" means required
	doc    string // Args: line without the leading name; "" omits the entry
}

// signature renders "def name(...) -> ret:", one parameter per line.
func signature(name string, params []pyParam, ret string) string {
	if len(params) == 0 {
		return fmt.Sprintf("def %s() -> %s:", name, ret)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "def %s(\n", name)
	for _, p := range params {
		b.WriteString("    " + p.name + ": " + p.hint)
		if p.def != "" {
			b.WriteString(" = " + p.def)
		}
		b.WriteString(",\n")
	}
	fmt.Fprintf(&b, ") -> %s:", ret)
	return b.String()
}

// docstring renders a function docstring at indent: a summary line plus a
// google-style Args section for the documented parameters.
func docstring(indent, summary string, params []pyParam) string {
	summary = strings.ReplaceAll(summary, `"""`, `\"\"\"`)
	var args []string
	for _, p := range params {
		if p.doc != "" {
			args = append(args, p.name+": "+p.doc)
		}
	}
	if len(args) == 0 {
		return indent + `"""` + summary + `"""` + "\n"
	}
	var b strings.Builder
	b.WriteString(indent + `"""` + summary + "\n\n")
	b.WriteString(indent + "Args:\n")
	for _, a := range args {
		b.WriteString(indent + "    " + a + "\n")
	}
	b.WriteString(indent + `"""` + "\n")
	return b.String()
}

// argsDict renders the {"param": param, ...} literal that forwards a tool's
// parameters, keyed by their ADL names in sorted order. indent is the
// indentation of the line the literal starts on.
func argsDict(params []pyParam, indent string) string {
	if len(params) == 0 {
		return "{}"
	}
	sorted := append([]pyParam(nil), params...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].source < sorted[j].source })

	var b strings.Builder
	b.WriteString("{\n")
	for _, p := range sorted {
		b.WriteString(indent + "    " + pyString(p.source) + ": " + p.name + ",\n")
	}
	b.WriteString(indent + "}")
	return b.String()
}

// sortedCopy returns in re-sorted by name without mutating the module.
func sortedCopy[T any](in []T, name func(T) string) []T {
	out := append([]T(nil), in...)
	sort.Slice(out, func(i, j int) bool { return name(out[i]) < name(out[j]) })
	return out
}
