package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/weirdGuy/kastor/internal/build"
	"github.com/weirdGuy/kastor/internal/build/eve"
	"github.com/weirdGuy/kastor/internal/build/langgraph"
	"github.com/weirdGuy/kastor/internal/graph"
	"github.com/weirdGuy/kastor/internal/module"
	"github.com/weirdGuy/kastor/internal/schema"
)

// generators maps a codegen target's name to its framework generator: the
// target label doubles as the framework selector (SPEC.md §3.5 has no
// separate framework attribute). A codegen target whose name has no entry
// here is a codegen error at build time, not a validation error — the block
// itself is valid spec.
var generators = map[string]build.Generator{
	"eve":       eve.Generator{},
	"langgraph": langgraph.Generator{},
}

func newBuildCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "build [--target name] [dir]",
		Short: "Generate framework code from the module's codegen targets",
		Long:  "build runs the full validate pipeline, then generates code for the module's codegen targets and syncs it into each target's declared output directory. With --target only the named target is built; otherwise all codegen targets build in lexicographic name order. Platform targets are never built — they belong to kastor plan / kastor apply.",
		Args:  usageMaxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runBuild(cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, target)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "build only the named codegen target (default: all codegen targets)")
	return cmd
}

// usageMaxArgs is cobra.MaximumNArgs with the usage exit code attached.
func usageMaxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.MaximumNArgs(n)(cmd, args); err != nil {
			return withExitCode(2, err)
		}
		return nil
	}
}

// runBuild validates the module exactly like kastor validate, then builds the
// selected codegen targets in order, stopping at the first failure. Output
// already synced for earlier targets stays in place — every target's sync is
// independently complete or untouched.
func runBuild(stdout, stderr io.Writer, dir, targetName string) error {
	mod, g, err := compileModule(stderr, dir)
	if err != nil {
		return err
	}

	targets, err := selectTargets(mod, targetName)
	if err != nil {
		return err
	}

	for _, tgt := range targets {
		if err := buildTarget(stdout, mod, g, tgt); err != nil {
			return err
		}
	}
	return nil
}

// selectTargets picks the codegen targets to build: the named one, or all of
// them in lexicographic name order when no name is given. Selection failures
// are usage errors (exit 2): the invocation asked the module for something
// it does not declare.
func selectTargets(mod *module.Module, name string) ([]*schema.Target, error) {
	if name != "" {
		for _, tgt := range mod.Targets {
			if tgt.Name != name {
				continue
			}
			if tgt.Type != "codegen" {
				return nil, usageErrorf("target.%s is a %s target; kastor build only builds codegen targets — use kastor plan / kastor apply for it", name, tgt.Type)
			}
			return []*schema.Target{tgt}, nil
		}
		return nil, usageErrorf("target.%s: not declared in the module (codegen targets: %s)", name, joinOrNone(codegenNames(mod)))
	}

	var targets []*schema.Target
	for _, tgt := range mod.Targets {
		if tgt.Type == "codegen" {
			targets = append(targets, tgt)
		}
	}
	if len(targets) == 0 {
		return nil, usageErrorf("module declares no codegen targets; kastor build needs at least one target with type = \"codegen\"")
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
	return targets, nil
}

// buildTarget generates one codegen target and syncs the files into its
// output directory. Generation failures keep the default exit code 1
// (codegen errors); sync failures are IO errors, exit 2.
func buildTarget(stdout io.Writer, mod *module.Module, g *graph.Graph, tgt *schema.Target) error {
	gen, ok := generators[tgt.Name]
	if !ok {
		return fmt.Errorf("%s: no code generator named %q (available: %s)", tgt.Addr(), tgt.Name, strings.Join(generatorNames(), ", "))
	}

	files, err := build.Run(gen, &build.Job{Module: mod, Graph: g, Target: tgt})
	if err != nil {
		return err
	}

	outDir, err := build.OutputDir(mod, tgt)
	if err != nil {
		return err
	}
	if err := build.Write(outDir, files); err != nil {
		return withExitCode(2, fmt.Errorf("%s: %w", tgt.Addr(), err))
	}

	fmt.Fprintf(stdout, "Built target %s: %s → %s\n", tgt.Name, countNoun(len(files), "file"), displayPath(outDir))
	return nil
}

func codegenNames(mod *module.Module) []string {
	var names []string
	for _, tgt := range mod.Targets {
		if tgt.Type == "codegen" {
			names = append(names, tgt.Name)
		}
	}
	sort.Strings(names)
	return names
}

func generatorNames() []string {
	names := make([]string, 0, len(generators))
	for name := range generators {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// displayPath renders the resolved output directory relative to the working
// directory when it lies beneath it, matching how users write output paths.
func displayPath(dir string) string {
	wd, err := os.Getwd()
	if err != nil {
		return dir
	}
	rel, err := filepath.Rel(wd, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return dir
	}
	return rel
}
