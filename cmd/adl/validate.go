package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/spf13/cobra"

	"github.com/weirdGuy/agentform/internal/graph"
	"github.com/weirdGuy/agentform/internal/module"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [dir]",
		Short: "Parse, type-check, and resolve references in a module",
		Long:  "validate runs the full compile pipeline without producing output: parse every ADL file under the module directory, resolve cross-file references, check prompt variable satisfiability, and build the dependency graph.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runValidate(cmd.OutOrStdout(), cmd.ErrOrStderr(), dir)
		},
	}
}

// runValidate runs the pipeline stages in order: module.Load (parse, symbol
// table, reference resolution, prompt variable checks — all diagnostics
// aggregated) then graph.Build (cycle detection). A failed stage stops the
// run because the next stage needs its output; diagnostics within a stage
// are always reported in full.
func runValidate(stdout, stderr io.Writer, dir string) error {
	mod, err := module.Load(dir)
	if err == nil {
		_, err = graph.Build(mod)
	}
	if err != nil {
		lines := flattenDiagnostics(err, dir)
		for _, line := range lines {
			fmt.Fprintln(stderr, line)
		}
		return fmt.Errorf("validation failed: %s", countNoun(len(lines), "error"))
	}

	fmt.Fprintf(stdout, "Success! Module is valid: %s.\n", moduleSummary(mod))
	return nil
}

// flattenDiagnostics expands an aggregated pipeline error (errors.Join trees
// with hcl.Diagnostics leaves) into one line per diagnostic. HCL diagnostics
// render as file:line,col with the path made relative to the module root so
// they match the resolver's plain errors.
func flattenDiagnostics(err error, root string) []string {
	var lines []string
	var walk func(error)
	walk = func(e error) {
		if diags, ok := e.(hcl.Diagnostics); ok {
			for _, d := range diags {
				lines = append(lines, formatHCLDiagnostic(d, root))
			}
			return
		}
		if multi, ok := e.(interface{ Unwrap() []error }); ok {
			for _, sub := range multi.Unwrap() {
				walk(sub)
			}
			return
		}
		lines = append(lines, e.Error())
	}
	walk(err)
	return lines
}

func formatHCLDiagnostic(d *hcl.Diagnostic, root string) string {
	msg := d.Summary
	if d.Detail != "" {
		msg += "; " + d.Detail
	}
	if d.Subject == nil {
		return msg
	}
	file := d.Subject.Filename
	if rel, err := filepath.Rel(root, file); err == nil && !strings.HasPrefix(rel, "..") {
		file = rel
	}
	return fmt.Sprintf("%s:%d,%d: %s", file, d.Subject.Start.Line, d.Subject.Start.Column, msg)
}

func moduleSummary(mod *module.Module) string {
	return strings.Join([]string{
		countNoun(len(mod.Agents), "agent"),
		countNoun(len(mod.Tools), "tool"),
		countNoun(len(mod.Prompts), "prompt"),
		countNoun(len(mod.Models), "model"),
		countNoun(len(mod.Targets), "target"),
	}, ", ")
}

func countNoun(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
