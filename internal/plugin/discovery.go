package plugin

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/weirdGuy/kastor/internal/schema"
	protocol "github.com/weirdGuy/kastor/protocol/v1"
)

const (
	pluginDirEnv    = "KASTOR_PLUGIN_DIR"
	pluginEnvPrefix = "KASTOR_PLUGIN_"
)

var nonEnv = regexp.MustCompile(`[^A-Za-z0-9]+`)

// Open discovers and starts one declared plugin, then verifies that its
// handshake identity and version satisfy the module requirement.
func Open(ctx context.Context, localName string, requirement *schema.PluginRequirement) (*protocol.Client, error) {
	if requirement == nil {
		return nil, fmt.Errorf("plugin.%s: requirement is nil", localName)
	}
	executable, err := Discover(localName, requirement.Source)
	if err != nil {
		return nil, err
	}
	client, err := protocol.Start(ctx, executable)
	if err != nil {
		return nil, fmt.Errorf("plugin.%s: %w", localName, err)
	}
	metadata := client.Metadata()
	if metadata.Source != requirement.Source {
		_ = client.Close()
		return nil, fmt.Errorf("plugin.%s: executable %s identifies as %q, expected %q", localName, executable, metadata.Source, requirement.Source)
	}
	if !versionMatches(metadata.Version, requirement.Version) {
		_ = client.Close()
		return nil, fmt.Errorf("plugin.%s: installed version %s does not satisfy %q", localName, metadata.Version, requirement.Version)
	}
	return client, nil
}

// Discover applies the explicit development override first, then a shared
// plugin directory, then PATH. The executable convention is the final source
// path segment, e.g. github.com/getkastordev/kastor-eve → kastor-eve.
func Discover(localName, source string) (string, error) {
	envName := pluginEnvPrefix + strings.Trim(nonEnv.ReplaceAllString(strings.ToUpper(localName), "_"), "_")
	if override := os.Getenv(envName); override != "" {
		return executableFile(override, envName)
	}
	binary := path.Base(source)
	if binary == "." || binary == "/" || binary == "" {
		return "", fmt.Errorf("plugin.%s: source %q has no executable name", localName, source)
	}
	if directory := os.Getenv(pluginDirEnv); directory != "" {
		candidate := filepath.Join(directory, binary)
		if resolved, err := executableFile(candidate, pluginDirEnv); err == nil {
			return resolved, nil
		}
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return "", fmt.Errorf("plugin.%s: executable %q not found; install it, add it to PATH, set %s, or set %s", localName, binary, pluginDirEnv, envName)
	}
	return resolved, nil
}

func executableFile(candidate, source string) (string, error) {
	info, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("%s points to %q: %w", source, candidate, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%s points to %q, which is not executable", source, candidate)
	}
	return filepath.Clean(candidate), nil
}

func versionMatches(version, constraint string) bool {
	actual := canonicalVersion(version)
	if actual == "" {
		return false
	}
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return true
	}
	if strings.HasPrefix(constraint, "~>") {
		base := strings.TrimSpace(strings.TrimPrefix(constraint, "~>"))
		minimum := canonicalVersion(base)
		if minimum == "" || semver.Compare(actual, minimum) < 0 {
			return false
		}
		parts := strings.Split(strings.TrimPrefix(minimum, "v"), ".")
		major, _ := strconv.Atoi(parts[0])
		minor, _ := strconv.Atoi(parts[1])
		var maximum string
		if len(strings.Split(base, ".")) <= 2 {
			maximum = fmt.Sprintf("v%d.%d.0", major+1, 0)
		} else {
			maximum = fmt.Sprintf("v%d.%d.0", major, minor+1)
		}
		return semver.Compare(actual, maximum) < 0
	}
	wanted := canonicalVersion(strings.TrimPrefix(constraint, "="))
	return wanted != "" && semver.Compare(actual, wanted) == 0
}

func canonicalVersion(version string) string {
	version = strings.TrimSpace(version)
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return semver.Canonical(version)
}
