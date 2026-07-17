package main

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

//go:embed all:scaffold
var scaffoldFS embed.FS

// scaffolds maps a --target value to its embedded scaffold directory: one
// entry per codegen target kastor can pre-configure, mirroring the
// generators map in build.go. eve joins when its generator ships (KAS-32) —
// scaffolding it earlier would emit a module kastor build cannot build.
var scaffolds = map[string]string{
	"langgraph": "scaffold/langgraph",
}

func newInitCmd() *cobra.Command {
	var target string
	var force bool
	cmd := &cobra.Command{
		Use:   "init [--target name] [--force] [dir]",
		Short: "Scaffold a new Kastor module",
		Long:  "init writes a minimal working module — kastor.hcl, one agent, one MCP tool, one prompt, MCP runtime config, and a README — into dir (default: the current directory), creating it if missing. The scaffold passes kastor validate and kastor build with zero edits. A directory already holding visible files is refused unless --force, which overwrites the scaffold's own file names and leaves everything else in place.",
		Args:  usageMaxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runInit(cmd.OutOrStdout(), dir, target, force)
		},
	}
	cmd.Flags().StringVar(&target, "target", "langgraph", "codegen target to pre-configure (available: "+strings.Join(scaffoldNames(), ", ")+")")
	cmd.Flags().BoolVar(&force, "force", false, "scaffold into a non-empty directory, overwriting only the scaffold's own file names")
	return cmd
}

// runInit copies the embedded scaffold for the chosen target into dir. The
// scaffold is static bytes baked into the binary, so the same kastor version
// always produces a byte-identical module.
func runInit(stdout io.Writer, dir, targetName string, force bool) error {
	root, ok := scaffolds[targetName]
	if !ok {
		return usageErrorf("no scaffold for target %q (available: %s)", targetName, strings.Join(scaffoldNames(), ", "))
	}

	if !force {
		if err := refuseNonEmpty(dir); err != nil {
			return err
		}
	}

	files, err := scaffoldFiles(root)
	if err != nil {
		return err
	}

	for _, rel := range files {
		data, err := scaffoldFS.ReadFile(path.Join(root, rel))
		if err != nil {
			return fmt.Errorf("reading embedded scaffold %s: %w", rel, err)
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return withExitCode(2, fmt.Errorf("creating %s: %w", filepath.Dir(dst), err))
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return withExitCode(2, fmt.Errorf("writing %s: %w", dst, err))
		}
		fmt.Fprintf(stdout, "  created %s\n", dst)
	}

	fmt.Fprintf(stdout, "\nScaffolded a new module: %s (target %s).\n\nNext steps:\n", countNoun(len(files), "file"), targetName)
	if dir != "." {
		fmt.Fprintf(stdout, "  cd %s\n", dir)
	}
	fmt.Fprint(stdout, "  kastor validate\n  kastor build\n")
	return nil
}

// refuseNonEmpty errors when dir already holds visible entries. Hidden
// (dot-prefixed) entries — a .git, a .venv — belong to the user and don't
// count, the same ownership rule build.Write applies to output directories,
// so init works in a freshly created git repository. A missing dir is fine:
// the write path creates it.
func refuseNonEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return withExitCode(2, fmt.Errorf("reading %s: %w", dir, err))
	}
	var visible []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			visible = append(visible, e.Name())
		}
	}
	if len(visible) == 0 {
		return nil
	}
	preview := strings.Join(visible[:min(len(visible), 3)], ", ")
	if len(visible) > 3 {
		preview += ", …"
	}
	return usageErrorf("%s is not empty (found %s); init needs an empty or new directory — use --force to scaffold anyway, overwriting only the scaffold's own file names", dir, preview)
}

// scaffoldFiles lists the scaffold's file paths relative to root, sorted so
// creation output is deterministic.
func scaffoldFiles(root string) ([]string, error) {
	var files []string
	err := fs.WalkDir(scaffoldFS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, strings.TrimPrefix(p, root+"/"))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading embedded scaffold: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func scaffoldNames() []string {
	names := make([]string, 0, len(scaffolds))
	for name := range scaffolds {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
