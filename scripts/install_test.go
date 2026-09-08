package scripts_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("installer requires a POSIX shell")
	}
	for _, tt := range []struct {
		name, os, machine, platform, checksum, wantError string
	}{
		{name: "linux_amd64", os: "Linux", machine: "x86_64", platform: "linux_amd64"},
		{name: "darwin_arm64", os: "Darwin", machine: "arm64", platform: "darwin_arm64"},
		{name: "corrupt_checksum", os: "Linux", machine: "x86_64", platform: "linux_amd64", checksum: "corrupt", wantError: "checksum verification failed"},
		{name: "missing_checksum", os: "Linux", machine: "x86_64", platform: "linux_amd64", checksum: "missing", wantError: "no entry for"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			fixture := filepath.Join(root, "release")
			tmp := filepath.Join(root, "tmp")
			install := filepath.Join(root, "install dir")
			for _, dir := range []string{bin, fixture, tmp} {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, content string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, []byte(content), mode); err != nil {
					t.Fatal(err)
				}
			}
			// Only these real utilities are available; curl can never fall back to the network.
			tools := []string{"sh", "tar", "gzip", "tr", "grep", "head", "cut", "mktemp", "rm", "mkdir", "cp", "chmod"}
			checksumTool := "sha256sum"
			if _, err := exec.LookPath(checksumTool); err != nil {
				checksumTool = "shasum"
			}
			tools = append(tools, checksumTool)
			for _, tool := range tools {
				path, err := exec.LookPath(tool)
				if err != nil {
					t.Fatalf("required installer test utility %s: %v", tool, err)
				}
				if err := os.Symlink(path, filepath.Join(bin, tool)); err != nil {
					t.Fatal(err)
				}
			}
			const payload = "#!/bin/sh\nprintf 'kastor test binary\\n'\n"
			write(filepath.Join(fixture, "kastor"), payload, 0o755)
			archive := "kastor_1.2.3_" + tt.platform + ".tar.gz"
			cmd := exec.Command(filepath.Join(bin, "tar"), "-czf", filepath.Join(fixture, archive), "-C", fixture, "kastor")
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("create archive: %v\n%s", err, output)
			}
			data, err := os.ReadFile(filepath.Join(fixture, archive))
			if err != nil {
				t.Fatal(err)
			}
			checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(data), archive)
			switch tt.checksum {
			case "corrupt":
				checksum = strings.Repeat("0", 64) + "  " + archive + "\n"
			case "missing":
				checksum = fmt.Sprintf("%x  other.tar.gz\n", sha256.Sum256(data))
			}
			write(filepath.Join(fixture, "checksums.txt"), checksum, 0o644)
			write(filepath.Join(bin, "uname"), `#!/bin/sh
set -eu
case "$*" in
    -s) printf '%s\n' "$TEST_OS" ;;
    -m) printf '%s\n' "$TEST_MACHINE" ;;
    *) exit 1 ;;
esac
`, 0o755)
			write(filepath.Join(bin, "curl"), `#!/bin/sh
set -eu
[ "$1" = "-fsSL" ] || exit 1
shift
out=
if [ "$1" = "-o" ]; then
    out=$2
    shift 2
fi
[ "$#" = 1 ] || exit 1
printf '%s\n' "$1" >> "$REQUEST_LOG"
case "$1" in
    https://api.github.com/repos/getkastordev/kastor/releases/latest)
        [ -z "$out" ] || exit 1
        printf '{"tag_name":"v1.2.3"}\n'
        ;;
    "https://github.com/getkastordev/kastor/releases/download/v1.2.3/$TEST_ARCHIVE")
        cp "$FIXTURE/$TEST_ARCHIVE" "$out"
        ;;
    https://github.com/getkastordev/kastor/releases/download/v1.2.3/checksums.txt)
        cp "$FIXTURE/checksums.txt" "$out"
        ;;
    *) printf 'unexpected URL: %s\n' "$1" >&2; exit 1 ;;
esac
`, 0o755)
			log := filepath.Join(root, "requests")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd = exec.CommandContext(ctx, filepath.Join(bin, "sh"), "install.sh")
			cmd.Env = []string{
				"PATH=" + bin, "HOME=" + root, "TMPDIR=" + tmp,
				"KASTOR_INSTALL_DIR=" + install, "FIXTURE=" + fixture,
				"REQUEST_LOG=" + log, "TEST_ARCHIVE=" + archive,
				"TEST_OS=" + tt.os, "TEST_MACHINE=" + tt.machine,
			}
			output, runErr := cmd.CombinedOutput()
			requests, err := os.ReadFile(log)
			if err != nil {
				t.Fatalf("read requests: %v\n%s", err, output)
			}
			wantRequests := "https://api.github.com/repos/getkastordev/kastor/releases/latest\n" +
				"https://github.com/getkastordev/kastor/releases/download/v1.2.3/" + archive + "\n" +
				"https://github.com/getkastordev/kastor/releases/download/v1.2.3/checksums.txt\n"
			if string(requests) != wantRequests {
				t.Errorf("requests = %q, want %q", requests, wantRequests)
			}
			if tt.wantError != "" {
				if runErr == nil || !strings.Contains(string(output), tt.wantError) {
					t.Fatalf("installer error = %v, want %q\n%s", runErr, tt.wantError, output)
				}
				if _, err := os.Stat(install); !os.IsNotExist(err) {
					t.Fatalf("failed verification must not create install directory: %v", err)
				}
				return
			}
			if runErr != nil {
				t.Fatalf("installer: %v\n%s", runErr, output)
			}
			executable := filepath.Join(install, "kastor")
			installed, err := os.ReadFile(executable)
			if err != nil || string(installed) != payload {
				t.Fatalf("installed binary = %q, %v; want %q", installed, err, payload)
			}
			info, err := os.Stat(executable)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o755 {
				t.Errorf("installed mode = %o, want 755", info.Mode().Perm())
			}
			if output, err := exec.CommandContext(ctx, executable).CombinedOutput(); err != nil || string(output) != "kastor test binary\n" {
				t.Fatalf("execute installed binary: %v, output %q", err, output)
			}
		})
	}
}
