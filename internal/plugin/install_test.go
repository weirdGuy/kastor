package plugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/weirdGuy/kastor/internal/schema"
	protocol "github.com/weirdGuy/kastor/protocol/v1"
)

func TestInitLocksInstallsAndWorksOffline(t *testing.T) {
	binary := []byte("#!/bin/sh\nexit 0\n")
	archive := testTarGz(t, "kastor-test", binary)
	archiveName := fmt.Sprintf("kastor-test_0.1.2_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archiveChecksum := sha256Hex(archive)
	checksums := []byte(archiveChecksum + "  " + archiveName + "\n")

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasPrefix(request.URL.Path, "/repos/acme/kastor-test/releases"):
			_ = json.NewEncoder(response).Encode([]githubRelease{{
				TagName: "v0.1.2",
				Assets: []githubAsset{
					{Name: "checksums.txt", URL: server.URL + "/checksums.txt"},
					{Name: archiveName, URL: server.URL + "/unused-direct-asset-url"},
				},
			}})
		case request.URL.Path == "/checksums.txt":
			_, _ = response.Write(checksums)
		case request.URL.Path == "/acme/kastor-test/releases/download/v0.1.2/"+archiveName:
			_, _ = response.Write(archive)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	cache := t.TempDir()
	requirement := &schema.PluginRequirement{Name: "test", Source: "github.com/acme/kastor-test", Version: "~> 0.1"}
	options := InstallOptions{CacheDir: cache, APIBase: server.URL, ReleaseBase: server.URL, HTTPClient: server.Client()}
	result, err := Init(context.Background(), root, []*schema.PluginRequirement{requirement}, options)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := result.Lock.Plugin("test")
	if !ok || entry.Version != "0.1.2" || entry.Checksums[archiveName] != archiveChecksum || entry.Protocol != protocol.Version {
		t.Fatalf("lock entry = %#v", entry)
	}
	firstLock, err := os.ReadFile(filepath.Join(root, LockFilename))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Init(context.Background(), root, []*schema.PluginRequirement{requirement}, InstallOptions{CacheDir: cache, Offline: true}); err != nil {
		t.Fatalf("offline init: %v", err)
	}
	secondLock, err := os.ReadFile(filepath.Join(root, LockFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstLock, secondLock) {
		t.Fatal("offline init changed the lock file")
	}

	t.Setenv(pluginCacheEnv, cache)
	executable, err := LockedExecutable(root, "test", requirement)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(executable); err != nil || !bytes.Equal(got, binary) {
		t.Fatalf("installed executable = %q, %v", got, err)
	}
	if err := os.WriteFile(executable, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LockedExecutable(root, "test", requirement); err == nil || !strings.Contains(err.Error(), "refusing to execute") {
		t.Fatalf("tampered executable error = %v", err)
	}
}

func TestInitFrozenRejectsRequirementDrift(t *testing.T) {
	root := t.TempDir()
	lock := &LockFile{Plugins: []*LockedPlugin{{
		Name: "test", Source: "github.com/acme/kastor-test", Version: "0.1.2", Constraints: "~> 0.1",
		Release: "v0.1.2", Protocol: protocol.Version, Platforms: map[string]string{}, Checksums: map[string]string{},
	}}}
	if err := WriteLock(root, lock); err != nil {
		t.Fatal(err)
	}
	requirement := &schema.PluginRequirement{Name: "test", Source: "github.com/acme/kastor-test", Version: "~> 0.2"}
	_, err := Init(context.Background(), root, []*schema.PluginRequirement{requirement}, InstallOptions{Frozen: true, Offline: true, CacheDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v", err)
	}
}

func TestLockRenderingIsDeterministicAndRoundTrips(t *testing.T) {
	lock := &LockFile{Plugins: []*LockedPlugin{
		{Name: "z", Source: "github.com/acme/z", Version: "1.2.3", Constraints: "= 1.2.3", Release: "v1.2.3", Protocol: 1, Platforms: map[string]string{"linux_amd64": "z.tgz"}, Checksums: map[string]string{"z.tgz": strings.Repeat("a", 64)}},
		{Name: "a", Source: "github.com/acme/a", Version: "0.1.0", Constraints: "~> 0.1", Release: "v0.1.0", Protocol: 1, Platforms: map[string]string{"darwin_arm64": "a.tgz", "linux_amd64": "b.tgz"}, Checksums: map[string]string{"b.tgz": strings.Repeat("b", 64), "a.tgz": strings.Repeat("c", 64)}},
	}}
	first := RenderLock(lock)
	second := RenderLock(lock)
	if !bytes.Equal(first, second) || bytes.Index(first, []byte(`plugin "a"`)) > bytes.Index(first, []byte(`plugin "z"`)) {
		t.Fatalf("non-canonical lock:\n%s", first)
	}
	root := t.TempDir()
	if err := WriteLock(root, lock); err != nil {
		t.Fatal(err)
	}
	parsed, err := ReadLock(root)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(RenderLock(parsed), first) {
		t.Fatalf("round trip changed lock:\n%s", RenderLock(parsed))
	}
}

func TestInstallPathsRejectsLockfileTraversal(t *testing.T) {
	entry := &LockedPlugin{Source: "github.com/acme/kastor-test", Version: "0.1.0"}
	for _, asset := range []string{"../outside.tar.gz", "nested/archive.tar.gz", `nested\archive.zip`} {
		if _, _, _, err := installPaths(t.TempDir(), entry, asset); err == nil {
			t.Errorf("installPaths(%q) succeeded", asset)
		}
	}
}

func testTarGz(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
