package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/getkastordev/kastor/internal/module"
	pluginruntime "github.com/getkastordev/kastor/internal/plugin"
	"github.com/getkastordev/kastor/internal/schema"
	protocol "github.com/getkastordev/kastor/protocol/v1"
)

const defaultScaffoldSource = "github.com/getkastordev/kastor-langgraph"

var installPlugins = pluginruntime.Init

// newInitCmd owns dependency initialization. It deliberately does not create
// project source: execution commands remain offline and `kastor new` owns
// plugin-provided starter modules.
func newInitCmd() *cobra.Command {
	var options pluginruntime.InstallOptions
	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Install and lock the module's executable plugins",
		Long:  "init resolves required_plugins from GitHub releases, verifies publisher checksums, installs the current platform binaries into Kastor's cache, and writes .kastor.lock.hcl. Commit the lock file so people, agents, and CI execute the same plugin releases.",
		Args:  usageMaxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runPluginInit(cmd.Context(), cmd.OutOrStdout(), dir, options)
		},
	}
	cmd.Flags().BoolVar(&options.Upgrade, "upgrade", false, "select the newest releases allowed by the module constraints")
	cmd.Flags().BoolVar(&options.Offline, "offline", false, "use only the lock file and verified local cache; make no network requests")
	cmd.Flags().BoolVar(&options.Frozen, "frozen", false, "fail if dependency selections would change (recommended in CI)")
	cmd.Flags().StringVar(&options.CacheDir, "plugin-cache", "", "plugin cache directory (default: KASTOR_PLUGIN_CACHE_DIR or the user cache)")
	return cmd
}

func runPluginInit(ctx context.Context, stdout io.Writer, dir string, options pluginruntime.InstallOptions) error {
	mod, err := module.Load(dir)
	if err != nil {
		return fmt.Errorf("cannot initialize plugins: %w", err)
	}
	result, err := installPlugins(ctx, dir, mod.Plugins, options)
	if err != nil {
		return err
	}
	for _, name := range result.Installed {
		entry, _ := result.Lock.Plugin(name)
		fmt.Fprintf(stdout, "Installed plugin.%s %s (%s)\n", name, entry.Version, entry.Source)
	}
	if len(result.Installed) == 0 {
		fmt.Fprintln(stdout, "No plugins are required by this module.")
	}
	if options.Frozen {
		fmt.Fprintf(stdout, "Verified frozen dependencies from %s.\n", pluginruntime.LockFilename)
	} else {
		fmt.Fprintf(stdout, "Wrote %s. Commit this file to version control.\n", pluginruntime.LockFilename)
	}
	return nil
}

func newNewCmd() *cobra.Command {
	var source string
	var constraint string
	var force bool
	var options pluginruntime.InstallOptions
	cmd := &cobra.Command{
		Use:   "new [dir]",
		Short: "Create a module from a plugin-owned scaffold",
		Long:  "new installs the selected plugin, locks its exact release, and asks that plugin for its starter module. This keeps framework-specific templates with the framework plugin instead of baking them into the Kastor binary.",
		Args:  usageMaxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runNew(cmd.Context(), cmd.OutOrStdout(), dir, source, constraint, force, options)
		},
	}
	cmd.Flags().StringVar(&source, "from", defaultScaffoldSource, "plugin source that owns the scaffold")
	cmd.Flags().StringVar(&constraint, "version", "~> 0.1", "allowed plugin release versions")
	cmd.Flags().BoolVar(&force, "force", false, "write into a non-empty directory, overwriting only scaffold-owned files")
	cmd.Flags().BoolVar(&options.Offline, "offline", false, "use a matching lock and verified cache without network access")
	cmd.Flags().StringVar(&options.CacheDir, "plugin-cache", "", "plugin cache directory")
	return cmd
}

func runNew(ctx context.Context, stdout io.Writer, dir, source, constraint string, force bool, options pluginruntime.InstallOptions) error {
	if !force {
		if err := refuseNonEmpty(dir); err != nil {
			return err
		}
	}
	localName, err := scaffoldPluginName(source)
	if err != nil {
		return usageErrorf("%v", err)
	}
	requirement := &schema.PluginRequirement{Name: localName, Source: source, Version: constraint}
	result, err := installPlugins(ctx, dir, []*schema.PluginRequirement{requirement}, options)
	if err != nil {
		return err
	}
	client, err := openPlugin(ctx, dir, localName, requirement)
	if err != nil {
		return err
	}
	metadata := client.Metadata()
	if !metadata.Capabilities.Scaffold {
		_ = client.Close()
		return fmt.Errorf("plugin.%s: %s %s does not provide a scaffold", localName, source, metadata.Version)
	}
	response, scaffoldErr := client.Scaffold(ctx, &protocol.ScaffoldRequest{
		Name: filepath.Base(filepath.Clean(dir)), LocalName: localName,
		Source: source, VersionConstraint: constraint,
	})
	closeErr := client.Close()
	if scaffoldErr != nil {
		return fmt.Errorf("plugin.%s: scaffold failed: %w", localName, scaffoldErr)
	}
	if closeErr != nil {
		return fmt.Errorf("plugin.%s: close: %w", localName, closeErr)
	}
	if response == nil || len(response.Files) == 0 {
		return fmt.Errorf("plugin.%s: scaffold returned no files", localName)
	}
	files := append([]protocol.File(nil), response.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	seen := map[string]bool{}
	for _, file := range files {
		if seen[file.Path] {
			return fmt.Errorf("plugin.%s: scaffold returned path %q more than once", localName, file.Path)
		}
		seen[file.Path] = true
		destination, err := safeScaffoldPath(dir, file.Path)
		if err != nil {
			return fmt.Errorf("plugin.%s: %w", localName, err)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return withExitCode(2, fmt.Errorf("creating %s: %w", filepath.Dir(destination), err))
		}
		if err := os.WriteFile(destination, file.Data, 0o644); err != nil {
			return withExitCode(2, fmt.Errorf("writing %s: %w", destination, err))
		}
		fmt.Fprintf(stdout, "  created %s\n", destination)
	}
	entry, _ := result.Lock.Plugin(localName)
	fmt.Fprintf(stdout, "\nCreated a new module: %s from %s %s.\n\nNext steps:\n", countNoun(len(files), "file"), source, entry.Version)
	if dir != "." {
		fmt.Fprintf(stdout, "  cd %s\n", dir)
	}
	fmt.Fprint(stdout, "  kastor validate\n  kastor build\n")
	return nil
}

func scaffoldPluginName(source string) (string, error) {
	base := filepath.Base(strings.TrimSpace(source))
	if base == "." || base == "" || strings.ContainsAny(base, "\\:") {
		return "", fmt.Errorf("plugin source %q has no usable repository name", source)
	}
	name := strings.TrimPrefix(base, "kastor-")
	if name == "" {
		return "", fmt.Errorf("plugin source %q has no usable local name", source)
	}
	return name, nil
}

func safeScaffoldPath(root, relative string) (string, error) {
	if relative == "" || relative == "." || relative == pluginruntime.LockFilename || strings.Contains(relative, "\\") || filepath.IsAbs(relative) || filepath.Clean(relative) != filepath.FromSlash(relative) || strings.HasPrefix(relative, "../") || relative == ".." {
		return "", fmt.Errorf("scaffold returned unsafe path %q", relative)
	}
	return filepath.Join(root, filepath.FromSlash(relative)), nil
}

// refuseNonEmpty errors when dir already holds visible entries. Hidden files
// such as .git and .kastor.lock.hcl do not block project creation.
func refuseNonEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return withExitCode(2, fmt.Errorf("reading %s: %w", dir, err))
	}
	var visible []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".") {
			visible = append(visible, entry.Name())
		}
	}
	if len(visible) == 0 {
		return nil
	}
	preview := strings.Join(visible[:min(len(visible), 3)], ", ")
	if len(visible) > 3 {
		preview += ", …"
	}
	return usageErrorf("%s is not empty (found %s); new needs an empty or new directory — use --force to continue", dir, preview)
}
