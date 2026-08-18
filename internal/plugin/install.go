package plugin

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/weirdGuy/kastor/internal/schema"
	protocol "github.com/weirdGuy/kastor/protocol/v1"
)

const pluginCacheEnv = "KASTOR_PLUGIN_CACHE_DIR"

// InstallOptions controls dependency resolution. Frozen is intended for CI:
// requirements and the existing lock must agree exactly. Offline additionally
// forbids every network request and uses only verified cached archives.
type InstallOptions struct {
	Upgrade     bool
	Offline     bool
	Frozen      bool
	CacheDir    string
	APIBase     string
	ReleaseBase string
	HTTPClient  *http.Client
}

// InstallResult describes the deterministic selection installed by Init.
type InstallResult struct {
	Lock      *LockFile
	Installed []string
}

type githubRelease struct {
	TagName    string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Init resolves module requirements, verifies or installs their executables,
// and writes .kastor.lock.hcl. It is the only normal command that performs
// dependency network access.
func Init(ctx context.Context, root string, requirements []*schema.PluginRequirement, options InstallOptions) (*InstallResult, error) {
	options = defaultInstallOptions(options)
	existing, err := ReadLock(root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if existing == nil {
		existing = &LockFile{}
	}

	requirements = append([]*schema.PluginRequirement(nil), requirements...)
	sort.Slice(requirements, func(i, j int) bool { return requirements[i].Name < requirements[j].Name })
	seen := map[string]bool{}
	lock := &LockFile{}
	result := &InstallResult{Lock: lock}
	for _, requirement := range requirements {
		if requirement == nil || requirement.Name == "" {
			return nil, fmt.Errorf("plugin requirement has no local name")
		}
		if seen[requirement.Name] {
			return nil, fmt.Errorf("plugin.%s: requirement is declared more than once", requirement.Name)
		}
		seen[requirement.Name] = true

		entry, locked := existing.Plugin(requirement.Name)
		usable := locked && lockMatchesRequirement(entry, requirement)
		if options.Frozen && !usable {
			return nil, fmt.Errorf("plugin.%s: dependency lock does not match the module; run `kastor init` and commit %s", requirement.Name, LockFilename)
		}
		if !usable || options.Upgrade {
			if options.Frozen {
				return nil, fmt.Errorf("plugin.%s: --upgrade cannot change a frozen dependency lock", requirement.Name)
			}
			if options.Offline {
				return nil, fmt.Errorf("plugin.%s: cannot resolve %q in offline mode; restore a matching %s or run `kastor init` with network access", requirement.Name, requirement.Version, LockFilename)
			}
			entry, err = resolveRelease(ctx, requirement, options)
			if err != nil {
				return nil, err
			}
		}
		if err := ensureInstalled(ctx, entry, options); err != nil {
			return nil, fmt.Errorf("plugin.%s: %w", requirement.Name, err)
		}
		lock.Plugins = append(lock.Plugins, entry)
		result.Installed = append(result.Installed, requirement.Name)
	}
	if options.Frozen {
		if len(existing.Plugins) != len(lock.Plugins) {
			return nil, fmt.Errorf("dependency lock contains plugins no longer required by the module; run `kastor init` and commit %s", LockFilename)
		}
		if !bytesEqual(RenderLock(existing), RenderLock(lock)) {
			return nil, fmt.Errorf("dependency lock would change in frozen mode; run `kastor init` and commit %s", LockFilename)
		}
		return result, nil
	}
	if err := WriteLock(root, lock); err != nil {
		return nil, err
	}
	return result, nil
}

func defaultInstallOptions(options InstallOptions) InstallOptions {
	if options.APIBase == "" {
		options.APIBase = "https://api.github.com"
	}
	if options.ReleaseBase == "" {
		options.ReleaseBase = "https://github.com"
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if options.CacheDir == "" {
		options.CacheDir = os.Getenv(pluginCacheEnv)
	}
	if options.CacheDir == "" {
		if directory, err := os.UserCacheDir(); err == nil {
			options.CacheDir = filepath.Join(directory, "kastor", "plugins")
		} else {
			options.CacheDir = filepath.Join(os.TempDir(), "kastor", "plugins")
		}
	}
	return options
}

func lockMatchesRequirement(entry *LockedPlugin, requirement *schema.PluginRequirement) bool {
	return entry != nil && entry.Source == requirement.Source && entry.Constraints == requirement.Version &&
		entry.Protocol == protocol.Version && versionMatches(entry.Version, requirement.Version)
}

func resolveRelease(ctx context.Context, requirement *schema.PluginRequirement, options InstallOptions) (*LockedPlugin, error) {
	owner, repository, err := githubCoordinates(requirement.Source)
	if err != nil {
		return nil, fmt.Errorf("plugin.%s: %w", requirement.Name, err)
	}
	endpoint := strings.TrimRight(options.APIBase, "/") + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + "/releases?per_page=100"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := options.HTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("plugin.%s: list GitHub releases: %w", requirement.Name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plugin.%s: list GitHub releases: %s", requirement.Name, response.Status)
	}
	var releases []githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&releases); err != nil {
		return nil, fmt.Errorf("plugin.%s: decode GitHub releases: %w", requirement.Name, err)
	}
	var selected *githubRelease
	selectedVersion := ""
	for i := range releases {
		release := &releases[i]
		version := canonicalVersion(release.TagName)
		if release.Draft || release.Prerelease || version == "" || !versionMatches(version, requirement.Version) {
			continue
		}
		if selected == nil || semver.Compare(version, selectedVersion) > 0 {
			selected, selectedVersion = release, version
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("plugin.%s: no published release of %s satisfies %q", requirement.Name, requirement.Source, requirement.Version)
	}
	checksumAsset, ok := findAsset(selected.Assets, "checksums.txt")
	if !ok {
		return nil, fmt.Errorf("plugin.%s: release %s has no checksums.txt", requirement.Name, selected.TagName)
	}
	checksumData, err := downloadBytes(ctx, checksumAsset.URL, options)
	if err != nil {
		return nil, fmt.Errorf("plugin.%s: download checksums.txt: %w", requirement.Name, err)
	}
	checksums, err := parseChecksums(checksumData)
	if err != nil {
		return nil, fmt.Errorf("plugin.%s: %w", requirement.Name, err)
	}
	platforms := releasePlatforms(selected.Assets, checksums, path.Base(requirement.Source))
	key := platformKey(runtime.GOOS, runtime.GOARCH)
	if platforms[key] == "" {
		return nil, fmt.Errorf("plugin.%s: release %s has no archive for %s", requirement.Name, selected.TagName, key)
	}
	return &LockedPlugin{
		Name:        requirement.Name,
		Source:      requirement.Source,
		Version:     strings.TrimPrefix(selectedVersion, "v"),
		Constraints: requirement.Version,
		Release:     selected.TagName,
		Protocol:    protocol.Version,
		Platforms:   platforms,
		Checksums:   checksums,
	}, nil
}

func githubCoordinates(source string) (string, string, error) {
	parts := strings.Split(source, "/")
	if len(parts) != 3 || parts[0] != "github.com" || parts[1] == "" || parts[2] == "" || parts[1] == "." || parts[1] == ".." || parts[2] == "." || parts[2] == ".." {
		return "", "", fmt.Errorf("source %q is unsupported; v0 installs require github.com/<owner>/<repository>", source)
	}
	return parts[1], parts[2], nil
}

func findAsset(assets []githubAsset, name string) (githubAsset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return githubAsset{}, false
}

func releasePlatforms(assets []githubAsset, checksums map[string]string, binary string) map[string]string {
	result := map[string]string{}
	for _, asset := range assets {
		lower := strings.ToLower(asset.Name)
		if !strings.HasPrefix(lower, strings.ToLower(binary)+"_") || (!strings.HasSuffix(lower, ".tar.gz") && !strings.HasSuffix(lower, ".zip")) || checksums[asset.Name] == "" {
			continue
		}
		for _, goos := range []string{"linux", "darwin", "windows"} {
			if !tokenInAsset(lower, goos) {
				continue
			}
			for _, goarch := range []string{"amd64", "arm64"} {
				aliases := []string{goarch}
				if goarch == "amd64" {
					aliases = append(aliases, "x86_64")
				} else {
					aliases = append(aliases, "aarch64")
				}
				for _, alias := range aliases {
					if tokenInAsset(lower, alias) {
						result[platformKey(goos, goarch)] = asset.Name
						break
					}
				}
			}
		}
	}
	return result
}

func tokenInAsset(name, token string) bool {
	replacer := strings.NewReplacer("-", "_", ".", "_", " ", "_")
	for _, part := range strings.Split(replacer.Replace(name), "_") {
		if part == token {
			return true
		}
	}
	return false
}

func parseChecksums(data []byte) (map[string]string, error) {
	result := map[string]string{}
	for lineNumber, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("checksums.txt line %d is malformed", lineNumber+1)
		}
		digest := strings.ToLower(fields[0])
		name := strings.TrimPrefix(fields[1], "*")
		if len(digest) != sha256.Size*2 {
			return nil, fmt.Errorf("checksums.txt has invalid SHA-256 for %s", name)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return nil, fmt.Errorf("checksums.txt has invalid SHA-256 for %s", name)
		}
		result[name] = digest
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("checksums.txt is empty")
	}
	return result, nil
}

func ensureInstalled(ctx context.Context, entry *LockedPlugin, options InstallOptions) error {
	asset := entry.Platforms[platformKey(runtime.GOOS, runtime.GOARCH)]
	if asset == "" {
		return fmt.Errorf("locked release %s has no archive for %s", entry.Release, platformKey(runtime.GOOS, runtime.GOARCH))
	}
	want := entry.Checksums[asset]
	if want == "" {
		return fmt.Errorf("locked archive %s has no checksum", asset)
	}
	directory, archive, executable, err := installPaths(options.CacheDir, entry, asset)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create plugin cache: %w", err)
	}
	archiveOK := verifyFileSHA256(archive, want) == nil
	if !archiveOK {
		if options.Offline {
			return fmt.Errorf("verified archive %s is unavailable in the plugin cache while offline", asset)
		}
		owner, repository, err := githubCoordinates(entry.Source)
		if err != nil {
			return err
		}
		downloadURL := strings.TrimRight(options.ReleaseBase, "/") + "/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + "/releases/download/" + url.PathEscape(entry.Release) + "/" + url.PathEscape(asset)
		if err := downloadFile(ctx, downloadURL, archive, want, options); err != nil {
			return fmt.Errorf("download %s: %w", asset, err)
		}
	}
	binaryData, err := executableFromArchive(archive, path.Base(entry.Source), runtime.GOOS)
	if err != nil {
		return err
	}
	if data, err := os.ReadFile(executable); err == nil && sha256Hex(data) == sha256Hex(binaryData) {
		return nil
	}
	if err := atomicExecutable(executable, binaryData); err != nil {
		return err
	}
	return nil
}

// LockedExecutable resolves and verifies the cached executable selected by a
// module lock. It never repairs, downloads, or falls back after a lock exists.
func LockedExecutable(root, localName string, requirement *schema.PluginRequirement) (string, error) {
	lock, err := ReadLock(root)
	if err != nil {
		return "", err
	}
	entry, ok := lock.Plugin(localName)
	if !ok || !lockMatchesRequirement(entry, requirement) {
		return "", fmt.Errorf("plugin.%s: %s is missing or stale; run `kastor init`", localName, LockFilename)
	}
	options := defaultInstallOptions(InstallOptions{})
	asset := entry.Platforms[platformKey(runtime.GOOS, runtime.GOARCH)]
	if asset == "" {
		return "", fmt.Errorf("plugin.%s: locked release %s does not support %s", localName, entry.Release, platformKey(runtime.GOOS, runtime.GOARCH))
	}
	_, archive, executable, err := installPaths(options.CacheDir, entry, asset)
	if err != nil {
		return "", err
	}
	if err := verifyFileSHA256(archive, entry.Checksums[asset]); err != nil {
		return "", fmt.Errorf("plugin.%s: cached archive failed lock verification: %w; run `kastor init`", localName, err)
	}
	wantData, err := executableFromArchive(archive, path.Base(entry.Source), runtime.GOOS)
	if err != nil {
		return "", fmt.Errorf("plugin.%s: verify cached executable: %w", localName, err)
	}
	installedData, err := os.ReadFile(executable)
	if err != nil {
		return "", fmt.Errorf("plugin.%s: locked executable is not installed; run `kastor init`: %w", localName, err)
	}
	if sha256Hex(installedData) != sha256Hex(wantData) {
		return "", fmt.Errorf("plugin.%s: cached executable checksum does not match its verified release archive; refusing to execute it; run `kastor init` to repair the cache", localName)
	}
	return executableFile(executable, "plugin lock")
}

func installPaths(cache string, entry *LockedPlugin, asset string) (directory, archive, executable string, err error) {
	if asset == "" || asset != filepath.Base(asset) || strings.ContainsAny(asset, `/\\`) {
		return "", "", "", fmt.Errorf("locked release has unsafe archive name %q", asset)
	}
	if canonicalVersion(entry.Version) == "" {
		return "", "", "", fmt.Errorf("locked release has invalid version %q", entry.Version)
	}
	owner, repository, err := githubCoordinates(entry.Source)
	if err != nil {
		return "", "", "", err
	}
	directory = filepath.Join(cache, "github.com", owner, repository, entry.Version, platformKey(runtime.GOOS, runtime.GOARCH))
	archive = filepath.Join(directory, asset)
	binary := repository
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	executable = filepath.Join(directory, binary)
	return directory, archive, executable, nil
}

func downloadFile(ctx context.Context, source, destination, checksum string, options InstallOptions) error {
	data, err := downloadBytes(ctx, source, options)
	if err != nil {
		return err
	}
	if got := sha256Hex(data); got != strings.ToLower(checksum) {
		return fmt.Errorf("checksum mismatch: got %s, want %s", got, checksum)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".download-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(temporaryPath, destination)
}

func downloadBytes(ctx context.Context, source string, options InstallOptions) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	if token := os.Getenv("GITHUB_TOKEN"); token != "" && strings.HasPrefix(source, "https://api.github.com/") {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := options.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 256<<20))
	if err != nil {
		return nil, err
	}
	return data, nil
}

func verifyFileSHA256(filename, want string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != strings.ToLower(want) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", filepath.Base(filename), got, want)
	}
	return nil
}

func executableFromArchive(filename, binary, goos string) ([]byte, error) {
	want := binary
	if goos == "windows" {
		want += ".exe"
	}
	if strings.HasSuffix(strings.ToLower(filename), ".zip") {
		archive, err := zip.OpenReader(filename)
		if err != nil {
			return nil, fmt.Errorf("open plugin archive: %w", err)
		}
		defer archive.Close()
		for _, file := range archive.File {
			if path.Base(strings.ReplaceAll(file.Name, "\\", "/")) != want {
				continue
			}
			reader, err := file.Open()
			if err != nil {
				return nil, err
			}
			data, readErr := io.ReadAll(io.LimitReader(reader, 128<<20))
			_ = reader.Close()
			return data, readErr
		}
	} else {
		file, err := os.Open(filename)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			return nil, fmt.Errorf("open plugin archive: %w", err)
		}
		defer gzipReader.Close()
		tarReader := tar.NewReader(gzipReader)
		for {
			header, err := tarReader.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("read plugin archive: %w", err)
			}
			if header.Typeflag != tar.TypeReg || path.Base(header.Name) != want {
				continue
			}
			return io.ReadAll(io.LimitReader(tarReader, 128<<20))
		}
	}
	return nil, fmt.Errorf("plugin archive %s does not contain %s", filepath.Base(filename), want)
}

func atomicExecutable(destination string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".plugin-*")
	if err != nil {
		return fmt.Errorf("install executable: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("install executable: %w", err)
	}
	if err := temporary.Chmod(0o755); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("install executable: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("install executable: %w", err)
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("install executable: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("install executable: %w", err)
	}
	return nil
}

func platformKey(goos, goarch string) string { return goos + "_" + goarch }

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func bytesEqual(left, right []byte) bool { return string(left) == string(right) }
