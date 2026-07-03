package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/spf13/cobra"

	"github.com/weirdGuy/agentform/internal/diff"
	"github.com/weirdGuy/agentform/internal/module"
)

func newFmtCmd() *cobra.Command {
	var check, showDiff bool
	cmd := &cobra.Command{
		Use:   "fmt [dir]",
		Short: "Canonically format ADL files",
		Long:  "fmt rewrites the module's HCL files (.agent, .tool, adl.hcl, .adl) to canonical style and prints the names of changed files. Prompt files are left untouched — their body is preserved byte for byte. With --check nothing is written and a non-zero exit reports files that would change; --diff prints unified diffs.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runFmt(cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, check, showDiff)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "write nothing; exit non-zero if any file would change")
	cmd.Flags().BoolVar(&showDiff, "diff", false, "print unified diffs of formatting changes")
	return cmd
}

// runFmt formats every formattable file in the module rooted at dir. Files
// that fail to tokenize as HCL are reported and skipped — formatting broken
// input could mangle it further. All files are always processed; failures
// and pending changes only decide the exit status at the end.
func runFmt(stdout, stderr io.Writer, dir string, check, showDiff bool) error {
	files, err := module.Files(dir)
	if err != nil {
		return err
	}

	var changed, failed int
	for _, rel := range files {
		if !formattable(rel) {
			continue
		}
		path := filepath.Join(dir, rel)

		src, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(stderr, err)
			failed++
			continue
		}
		if _, diags := hclsyntax.ParseConfig(src, path, hcl.InitialPos); diags.HasErrors() {
			for _, d := range diags {
				fmt.Fprintln(stderr, formatHCLDiagnostic(d, dir))
			}
			failed++
			continue
		}

		formatted := hclwrite.Format(src)
		if bytes.Equal(src, formatted) {
			continue
		}
		changed++

		fmt.Fprintln(stdout, path)
		if showDiff {
			fmt.Fprint(stdout, diff.Unified(rel, src, formatted))
		}
		if check {
			continue
		}
		// The permission argument only applies on create; an existing
		// file keeps its mode.
		if err := os.WriteFile(path, formatted, 0o644); err != nil {
			fmt.Fprintln(stderr, err)
			failed++
		}
	}

	if failed > 0 {
		return fmt.Errorf("fmt failed: %s", countNoun(failed, "error"))
	}
	if check && changed > 0 {
		return fmt.Errorf("%s would be reformatted", countNoun(changed, "file"))
	}
	return nil
}

// formattable reports whether fmt owns the file's formatting. Prompt files
// are excluded: their body must stay byte-identical and the frontmatter is
// not a standalone HCL document.
func formattable(path string) bool {
	switch filepath.Ext(path) {
	case ".agent", ".tool", ".adl":
		return true
	}
	return filepath.Base(path) == "adl.hcl"
}
