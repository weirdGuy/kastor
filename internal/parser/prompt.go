package parser

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/weirdGuy/agentform/internal/schema"
)

// promptDelim marks the start and end of a .prompt file's frontmatter.
const promptDelim = "---"

// promptFrontmatterHCL mirrors the HCL attributes allowed inside frontmatter.
type promptFrontmatterHCL struct {
	Name     string   `hcl:"name"`
	Requires []string `hcl:"requires,optional"`
}

// promptVarPattern matches a {{var}} reference: an identifier with optional
// surrounding whitespace. Anything else between braces is literal body text.
var promptVarPattern = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)

// ParsePromptFile reads and decodes a prompt template (.prompt).
func ParsePromptFile(path string) (*schema.Prompt, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading prompt file: %w", err)
	}
	return ParsePrompt(path, src)
}

// ParsePrompt decodes prompt file source. filename is used in diagnostics.
func ParsePrompt(filename string, src []byte) (*schema.Prompt, error) {
	frontmatter, fmStart, body, err := splitFrontmatter(filename, src)
	if err != nil {
		return nil, err
	}

	// Parse the frontmatter as HCL, offset so diagnostics report positions
	// in the .prompt file rather than in the extracted region.
	file, diags := hclsyntax.ParseConfig(frontmatter, filename, fmStart)
	if diags.HasErrors() {
		return nil, diags
	}
	var raw promptFrontmatterHCL
	if diags := gohcl.DecodeBody(file.Body, nil, &raw); diags.HasErrors() {
		return nil, diags
	}

	prompt := &schema.Prompt{
		Name:     raw.Name,
		Requires: raw.Requires,
		Body:     string(body),
	}

	if strings.TrimSpace(prompt.Body) == "" {
		return nil, fmt.Errorf("%s: prompt body is empty", prompt.Addr())
	}

	used := map[string]bool{}
	for _, m := range promptVarPattern.FindAllStringSubmatch(prompt.Body, -1) {
		v := m[1]
		if !used[v] {
			used[v] = true
			prompt.Vars = append(prompt.Vars, v)
		}
	}

	// requires is an optional contract: when omitted the variables are
	// inferred from the body; when present (including an explicit empty
	// list) it must match the body's variables exactly.
	if raw.Requires != nil {
		declared := make(map[string]bool, len(prompt.Requires))
		for _, v := range prompt.Requires {
			if declared[v] {
				return nil, fmt.Errorf("%s: variable %q declared more than once in requires", prompt.Addr(), v)
			}
			declared[v] = true
		}
		for _, v := range prompt.Vars {
			if !declared[v] {
				return nil, fmt.Errorf("%s: variable %q used in body but not declared in requires", prompt.Addr(), v)
			}
		}
		for _, v := range prompt.Requires {
			if !used[v] {
				return nil, fmt.Errorf("%s: required variable %q is never used in the body", prompt.Addr(), v)
			}
		}
	}

	return prompt, nil
}

// splitFrontmatter separates a .prompt file into its frontmatter region and
// raw body. fmStart is the position of the first frontmatter byte, for HCL
// diagnostics. The body is everything after the closing delimiter line,
// preserved byte for byte.
func splitFrontmatter(filename string, src []byte) (frontmatter []byte, fmStart hcl.Pos, body []byte, err error) {
	first, rest, ok := cutLine(src)
	if !ok || string(trimCR(first)) != promptDelim {
		return nil, hcl.Pos{}, nil, fmt.Errorf("%s: prompt file must begin with a %q frontmatter delimiter", filename, promptDelim)
	}

	fmStart = hcl.Pos{Line: 2, Column: 1, Byte: len(src) - len(rest)}
	for offset := fmStart.Byte; len(rest) > 0; offset = len(src) - len(rest) {
		var raw []byte
		raw, rest, _ = cutLine(rest)
		if string(trimCR(raw)) == promptDelim {
			return src[fmStart.Byte:offset], fmStart, rest, nil
		}
	}
	return nil, hcl.Pos{}, nil, fmt.Errorf("%s: unterminated frontmatter: closing %q not found", filename, promptDelim)
}

// cutLine splits off the first line, excluding its newline. ok is false when
// src is empty.
func cutLine(src []byte) (line, rest []byte, ok bool) {
	if len(src) == 0 {
		return nil, nil, false
	}
	if i := bytes.IndexByte(src, '\n'); i >= 0 {
		return src[:i], src[i+1:], true
	}
	return src, nil, true
}

// trimCR drops a trailing carriage return so CRLF files parse like LF files.
func trimCR(line []byte) []byte {
	return bytes.TrimSuffix(line, []byte{'\r'})
}
