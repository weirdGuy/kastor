package plugin

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"

	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
)

// LockFilename is the reviewable dependency lock written at a module root.
const LockFilename = ".kastor.lock.hcl"

// LockFile records every executable plugin selected for a module.
type LockFile struct {
	Plugins []*LockedPlugin
}

// LockedPlugin pins one module-local plugin name to a release and its
// publisher-provided archive checksums. Platforms maps GOOS_GOARCH to the
// corresponding release asset name.
type LockedPlugin struct {
	Name        string
	Source      string
	Version     string
	Constraints string
	Release     string
	Protocol    int
	Platforms   map[string]string
	Checksums   map[string]string
}

type lockFileHCL struct {
	Plugins []lockedPluginHCL `hcl:"plugin,block"`
}

type lockedPluginHCL struct {
	Name        string            `hcl:"name,label"`
	Source      string            `hcl:"source"`
	Version     string            `hcl:"version"`
	Constraints string            `hcl:"constraints"`
	Release     string            `hcl:"release"`
	Protocol    int               `hcl:"protocol"`
	Platforms   map[string]string `hcl:"platforms"`
	Checksums   map[string]string `hcl:"checksums"`
}

// ReadLock parses a module lock file. A missing file is returned as an
// os.ErrNotExist-compatible error so callers can distinguish first init.
func ReadLock(root string) (*LockFile, error) {
	path := filepath.Join(root, LockFilename)
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCLFile(path)
	if diags.HasErrors() {
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("reading plugin lock: %s", diags.Error())
	}
	var raw lockFileHCL
	if diags := gohcl.DecodeBody(file.Body, nil, &raw); diags.HasErrors() {
		return nil, fmt.Errorf("reading plugin lock: %s", diags.Error())
	}
	lock := &LockFile{Plugins: make([]*LockedPlugin, 0, len(raw.Plugins))}
	seen := map[string]bool{}
	for _, item := range raw.Plugins {
		if item.Name == "" || item.Source == "" || item.Version == "" || item.Release == "" || item.Protocol <= 0 {
			return nil, fmt.Errorf("reading plugin lock: plugin %q has incomplete identity metadata", item.Name)
		}
		if seen[item.Name] {
			return nil, fmt.Errorf("reading plugin lock: plugin %q is locked more than once", item.Name)
		}
		seen[item.Name] = true
		lock.Plugins = append(lock.Plugins, &LockedPlugin{
			Name:        item.Name,
			Source:      item.Source,
			Version:     item.Version,
			Constraints: item.Constraints,
			Release:     item.Release,
			Protocol:    item.Protocol,
			Platforms:   cloneStrings(item.Platforms),
			Checksums:   cloneStrings(item.Checksums),
		})
	}
	sort.Slice(lock.Plugins, func(i, j int) bool { return lock.Plugins[i].Name < lock.Plugins[j].Name })
	return lock, nil
}

// Plugin returns the entry for a module-local plugin name.
func (l *LockFile) Plugin(name string) (*LockedPlugin, bool) {
	if l == nil {
		return nil, false
	}
	for _, item := range l.Plugins {
		if item.Name == name {
			return item, true
		}
	}
	return nil, false
}

// WriteLock atomically writes canonical HCL so identical selections always
// produce a byte-identical, review-friendly lock file.
func WriteLock(root string, lock *LockFile) error {
	data := RenderLock(lock)
	path := filepath.Join(root, LockFilename)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("creating module directory: %w", err)
	}
	temporary, err := os.CreateTemp(root, ".kastor.lock-*")
	if err != nil {
		return fmt.Errorf("writing plugin lock: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("writing plugin lock: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("writing plugin lock: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("writing plugin lock: %w", err)
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("writing plugin lock: %w", err)
		}
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("writing plugin lock: %w", err)
	}
	return nil
}

// RenderLock returns the canonical representation used by WriteLock.
func RenderLock(lock *LockFile) []byte {
	var out bytes.Buffer
	out.WriteString("# This file is maintained by `kastor init`. Commit it to version control.\n")
	out.WriteString("# It pins executable plugins and publisher-provided release checksums.\n")
	plugins := append([]*LockedPlugin(nil), lock.Plugins...)
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name < plugins[j].Name })
	for _, item := range plugins {
		fmt.Fprintf(&out, "\nplugin %s {\n", strconv.Quote(item.Name))
		fmt.Fprintf(&out, "  source      = %s\n", strconv.Quote(item.Source))
		fmt.Fprintf(&out, "  version     = %s\n", strconv.Quote(item.Version))
		fmt.Fprintf(&out, "  constraints = %s\n", strconv.Quote(item.Constraints))
		fmt.Fprintf(&out, "  release     = %s\n", strconv.Quote(item.Release))
		fmt.Fprintf(&out, "  protocol    = %d\n", item.Protocol)
		renderStringMap(&out, "platforms", item.Platforms)
		renderStringMap(&out, "checksums", item.Checksums)
		out.WriteString("}\n")
	}
	return out.Bytes()
}

func renderStringMap(out *bytes.Buffer, name string, values map[string]string) {
	fmt.Fprintf(out, "  %s = {\n", name)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(out, "    %s = %s\n", strconv.Quote(key), strconv.Quote(values[key]))
	}
	out.WriteString("  }\n")
}

func cloneStrings(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
