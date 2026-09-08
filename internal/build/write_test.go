package build_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/getkastordev/kastor/internal/build"
)

func write(t *testing.T, dir string, files ...build.File) *build.Report {
	t.Helper()
	report, err := build.Write(dir, files)
	if err != nil {
		t.Fatalf("Write: unexpected error: %v", err)
	}
	return report
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(data)
}

func TestWriteCreatesOutputDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gen", "dev")

	write(t, dir,
		build.File{Path: "main.py", Data: []byte("main\n")},
		build.File{Path: "pkg/util.py", Data: []byte("util\n")},
	)

	if got := readFile(t, filepath.Join(dir, "main.py")); got != "main\n" {
		t.Errorf("main.py = %q, want %q", got, "main\n")
	}
	if got := readFile(t, filepath.Join(dir, "pkg", "util.py")); got != "util\n" {
		t.Errorf("pkg/util.py = %q, want %q", got, "util\n")
	}
	if _, err := os.Stat(filepath.Join(dir, build.Marker)); err != nil {
		t.Errorf("marker file: %v", err)
	}
}

func TestWriteIntoEmptyExistingDir(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, build.File{Path: "main.py", Data: []byte("main\n")})
	if got := readFile(t, filepath.Join(dir, "main.py")); got != "main\n" {
		t.Errorf("main.py = %q, want %q", got, "main\n")
	}
}

func TestWriteRefusesForeignDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "precious.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := build.Write(dir, []build.File{{Path: "main.py", Data: []byte("main\n")}})
	if err == nil {
		t.Fatal("Write: expected error for non-empty unmarked directory, got nil")
	}
	for _, want := range []string{dir, build.Marker} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Write error = %q\nwant substring %q", err, want)
		}
	}
	if got := readFile(t, filepath.Join(dir, "precious.txt")); got != "mine" {
		t.Errorf("precious.txt = %q, want untouched", got)
	}
}

func TestWriteRemovesStaleFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir,
		build.File{Path: "main.py", Data: []byte("v1\n")},
		build.File{Path: "pkg/util.py", Data: []byte("util\n")},
	)

	write(t, dir, build.File{Path: "main.py", Data: []byte("v2\n")})

	if got := readFile(t, filepath.Join(dir, "main.py")); got != "v2\n" {
		t.Errorf("main.py = %q, want %q", got, "v2\n")
	}
	if _, err := os.Stat(filepath.Join(dir, "pkg", "util.py")); !os.IsNotExist(err) {
		t.Errorf("pkg/util.py should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pkg")); !os.IsNotExist(err) {
		t.Errorf("emptied pkg/ directory should be removed, stat err = %v", err)
	}
}

func TestWriteLeavesHiddenEntriesAlone(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, build.File{Path: "main.py", Data: []byte("main\n")})

	if err := os.WriteFile(filepath.Join(dir, ".user"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	write(t, dir, build.File{Path: "main.py", Data: []byte("main\n")})

	if got := readFile(t, filepath.Join(dir, ".user")); got != "keep" {
		t.Errorf(".user = %q, want untouched", got)
	}
	if got := readFile(t, filepath.Join(dir, ".git", "config")); got != "keep" {
		t.Errorf(".git/config = %q, want untouched", got)
	}
}

func TestWriteSkipsUnchangedFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, build.File{Path: "main.py", Data: []byte("main\n")})

	past := time.Now().Add(-time.Hour)
	target := filepath.Join(dir, "main.py")
	if err := os.Chtimes(target, past, past); err != nil {
		t.Fatal(err)
	}

	write(t, dir, build.File{Path: "main.py", Data: []byte("main\n")})

	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(past) {
		t.Errorf("unchanged main.py was rewritten: mtime = %v, want %v", info.ModTime(), past)
	}
}

// Preserve-mode fixtures: the stub the generator emits for a tool with source
// kind "runtime", the stub after a spec change (a param was added), and what
// the file looks like once the user has implemented it.
const (
	stubPath     = "tools/get_weather.py"
	stubSidecar  = "tools/get_weather.py.kastor-new"
	renamedPath  = "tools/forecast.py"
	stubV1       = "# stub\ndef get_weather(city):\n    raise NotImplementedError\n"
	stubV2       = "# stub\ndef get_weather(city, units):\n    raise NotImplementedError\n"
	userImpl     = "# stub\ndef get_weather(city):\n    return lookup(city)\n"
	renamedStub  = "# stub\ndef forecast(city):\n    raise NotImplementedError\n"
	plainFile    = "main.py"
	plainContent = "generated\n"
)

func preserved(path, data string) build.File {
	return build.File{Path: path, Data: []byte(data), Preserve: true}
}

func plain(data string) build.File {
	return build.File{Path: plainFile, Data: []byte(data)}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("Stat %s: %v", path, err)
	}
	return err == nil
}

// TestWritePreservesStubs covers the whole preserve-mode contract: kastor owns
// a generated stub until the user edits it, and never writes the file again
// after that — but a spec change that lands under an edited file still has to
// reach the user, through the sidecar and the report.
//
// Every case runs the same shape: build v1, optionally let the user edit the
// stub, then build again with whatever the spec generates next. The second
// build's report and the resulting files are the assertions; a third identical
// build then has to be silent, since the report is a one-time message.
func TestWritePreservesStubs(t *testing.T) {
	tests := []struct {
		name string
		// edit is what the user leaves in the stub file after the first build;
		// empty means they never touched it.
		edit string
		// second is what the spec generates on the second build.
		second []build.File
		// want is the stub file's content afterwards; empty means it must be gone.
		want           string
		wantSidecar    string
		wantSuperseded []build.Change
		wantOrphaned   []string
		// wantRenamed is the content expected at renamedPath, if the case
		// generates one.
		wantRenamed string
	}{
		{
			name:   "untouched stub, unchanged spec: left as generated",
			second: []build.File{preserved(stubPath, stubV1), plain(plainContent)},
			want:   stubV1,
		},
		{
			name:   "untouched stub, changed spec: regenerated in place",
			second: []build.File{preserved(stubPath, stubV2), plain(plainContent)},
			want:   stubV2,
		},
		{
			name:   "implemented stub, unchanged spec: kept, nothing reported",
			edit:   userImpl,
			second: []build.File{preserved(stubPath, stubV1), plain(plainContent)},
			want:   userImpl,
		},
		{
			name:           "implemented stub, changed spec: kept, new stub to the sidecar",
			edit:           userImpl,
			second:         []build.File{preserved(stubPath, stubV2), plain(plainContent)},
			want:           userImpl,
			wantSidecar:    stubV2,
			wantSuperseded: []build.Change{{Path: stubPath, Sidecar: stubSidecar}},
		},
		{
			name:         "implemented stub, tool removed: kept, not deleted",
			edit:         userImpl,
			second:       []build.File{plain(plainContent)},
			want:         userImpl,
			wantOrphaned: []string{stubPath},
		},
		{
			name:   "untouched stub, tool removed: deleted like any generated file",
			second: []build.File{plain(plainContent)},
			want:   "",
		},
		{
			name:         "implemented stub, tool renamed: implementation kept, new stub written",
			edit:         userImpl,
			second:       []build.File{preserved(renamedPath, renamedStub), plain(plainContent)},
			want:         userImpl,
			wantOrphaned: []string{stubPath},
			wantRenamed:  renamedStub,
		},
		{
			name:        "implemented stub, unrelated file changed: file rewritten, stub kept",
			edit:        userImpl,
			second:      []build.File{preserved(stubPath, stubV1), plain("regenerated\n")},
			want:        userImpl,
			wantSidecar: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, preserved(stubPath, stubV1), plain(plainContent))
			if got := readFile(t, filepath.Join(dir, stubPath)); got != stubV1 {
				t.Fatalf("first build wrote %q, want the stub %q", got, stubV1)
			}
			if tt.edit != "" {
				if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(stubPath)), []byte(tt.edit), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			report := write(t, dir, tt.second...)

			if diff := cmp.Diff(tt.wantSuperseded, report.Superseded); diff != "" {
				t.Errorf("Report.Superseded mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantOrphaned, report.Orphaned); diff != "" {
				t.Errorf("Report.Orphaned mismatch (-want +got):\n%s", diff)
			}

			stubFile := filepath.Join(dir, filepath.FromSlash(stubPath))
			if tt.want == "" {
				if exists(t, stubFile) {
					t.Errorf("%s = %q, want removed", stubPath, readFile(t, stubFile))
				}
			} else if got := readFile(t, stubFile); got != tt.want {
				t.Errorf("%s = %q, want %q", stubPath, got, tt.want)
			}

			sidecarFile := filepath.Join(dir, filepath.FromSlash(stubSidecar))
			if tt.wantSidecar == "" {
				if exists(t, sidecarFile) {
					t.Errorf("%s = %q, want no sidecar", stubSidecar, readFile(t, sidecarFile))
				}
			} else if got := readFile(t, sidecarFile); got != tt.wantSidecar {
				t.Errorf("%s = %q, want %q", stubSidecar, got, tt.wantSidecar)
			}

			if tt.wantRenamed != "" {
				if got := readFile(t, filepath.Join(dir, filepath.FromSlash(renamedPath))); got != tt.wantRenamed {
					t.Errorf("%s = %q, want %q", renamedPath, got, tt.wantRenamed)
				}
			}

			// The report is a one-time message: nothing about the spec or the
			// user's files changed, so a repeat build must say nothing and
			// touch nothing. The sidecar in particular is a visible file no
			// build generates, so it has to survive the stale sweep — it is
			// the durable half of the message.
			again := write(t, dir, tt.second...)
			if len(again.Superseded) > 0 || len(again.Orphaned) > 0 {
				t.Errorf("repeat build reported again: %+v", again)
			}
			if tt.want != "" {
				if got := readFile(t, stubFile); got != tt.want {
					t.Errorf("after repeat build %s = %q, want %q", stubPath, got, tt.want)
				}
			}
			if tt.wantSidecar == "" {
				if exists(t, sidecarFile) {
					t.Errorf("after repeat build %s exists, want no sidecar", stubSidecar)
				}
			} else if got := readFile(t, sidecarFile); got != tt.wantSidecar {
				t.Errorf("after repeat build %s = %q, want %q", stubSidecar, got, tt.wantSidecar)
			}
		})
	}
}

// TestWriteReportsOrphanAfterSupersede keeps the two reports independent. Both
// concern a file Write has stopped writing, so it is tempting to treat "the
// user already knows this file is theirs" as one fact — but "the interface
// changed" and "this file is no longer generated at all" are different news,
// and hearing the first must not silence the second.
func TestWriteReportsOrphanAfterSupersede(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, preserved(stubPath, stubV1), plain(plainContent))
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(stubPath)), []byte(userImpl), 0o644); err != nil {
		t.Fatal(err)
	}

	report := write(t, dir, preserved(stubPath, stubV2), plain(plainContent))
	if len(report.Superseded) != 1 || len(report.Orphaned) != 0 {
		t.Fatalf("spec change report = %+v, want one superseded file and no orphan", report)
	}

	// Now the tool leaves the spec entirely.
	report = write(t, dir, plain(plainContent))
	if diff := cmp.Diff([]string{stubPath}, report.Orphaned); diff != "" {
		t.Errorf("Report.Orphaned mismatch (-want +got):\n%s", diff)
	}
	if got := readFile(t, filepath.Join(dir, filepath.FromSlash(stubPath))); got != userImpl {
		t.Errorf("%s = %q, want the implementation kept", stubPath, got)
	}

	if report = write(t, dir, plain(plainContent)); len(report.Orphaned) > 0 {
		t.Errorf("orphan reported twice: %+v", report.Orphaned)
	}
}

// TestWriteReclaimsRevertedStub covers the way out of preserve mode: a user who
// throws their implementation away and restores the generated stub hands the
// file back, and the sidecar that prompted them goes with it.
func TestWriteReclaimsRevertedStub(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, preserved(stubPath, stubV1))

	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(stubPath)), []byte(userImpl), 0o644); err != nil {
		t.Fatal(err)
	}
	if report := write(t, dir, preserved(stubPath, stubV2)); len(report.Superseded) != 1 {
		t.Fatalf("Report.Superseded = %+v, want the stub change", report.Superseded)
	}

	// The user reconciles by taking the new stub as-is.
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(stubPath)), []byte(stubV2), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, dir, preserved(stubPath, stubV2))
	if exists(t, filepath.Join(dir, filepath.FromSlash(stubSidecar))) {
		t.Error("sidecar survived the user reverting to the generated stub")
	}

	// Ownership is back with kastor, so the next spec change regenerates.
	report := write(t, dir, preserved(stubPath, stubV1))
	if len(report.Superseded) > 0 {
		t.Errorf("Report.Superseded = %+v, want empty for a reclaimed stub", report.Superseded)
	}
	if got := readFile(t, filepath.Join(dir, filepath.FromSlash(stubPath))); got != stubV1 {
		t.Errorf("%s = %q, want the regenerated stub %q", stubPath, got, stubV1)
	}
}

// TestWriteSkipsUnchangedStub keeps the mtime guarantee for preserve-mode files
// too: an untouched stub the spec still generates is not rewritten.
func TestWriteSkipsUnchangedStub(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, preserved(stubPath, stubV1))

	target := filepath.Join(dir, filepath.FromSlash(stubPath))
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(target, past, past); err != nil {
		t.Fatal(err)
	}

	write(t, dir, preserved(stubPath, stubV1))

	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(past) {
		t.Errorf("unchanged stub was rewritten: mtime = %v, want %v", info.ModTime(), past)
	}
}

// TestWriteTreatsUnrecordedStubAsUserOwned covers output directories built
// before the marker recorded stubs: with no record, Write cannot tell its own
// stub from an implementation, and must assume the file is the user's.
func TestWriteTreatsUnrecordedStubAsUserOwned(t *testing.T) {
	dir := t.TempDir()
	legacy := "Generated by kastor build. Do not edit files in this directory.\n"
	if err := os.WriteFile(filepath.Join(dir, build.Marker), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(stubPath)), []byte(userImpl), 0o644); err != nil {
		t.Fatal(err)
	}

	report := write(t, dir, preserved(stubPath, stubV2))

	if got := readFile(t, filepath.Join(dir, filepath.FromSlash(stubPath))); got != userImpl {
		t.Errorf("%s = %q, want the pre-existing file kept", stubPath, got)
	}
	want := []build.Change{{Path: stubPath, Sidecar: stubSidecar}}
	if diff := cmp.Diff(want, report.Superseded); diff != "" {
		t.Errorf("Report.Superseded mismatch (-want +got):\n%s", diff)
	}
	if got := readFile(t, filepath.Join(dir, filepath.FromSlash(stubSidecar))); got != stubV2 {
		t.Errorf("%s = %q, want %q", stubSidecar, got, stubV2)
	}
}

// TestWriteRefusesNewerMarker is the one marker problem Write will not paper
// over: records it cannot interpret must not be silently discarded, since that
// would reassign ownership of every file in the directory.
func TestWriteRefusesNewerMarker(t *testing.T) {
	dir := t.TempDir()
	marker := "# Generated by kastor build.\nkastorbuild 99\n"
	if err := os.WriteFile(filepath.Join(dir, build.Marker), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := build.Write(dir, []build.File{preserved(stubPath, stubV1)})
	if err == nil {
		t.Fatal("Write: expected error for a marker from a newer kastor, got nil")
	}
	for _, want := range []string{"99", "newer kastor"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Write error = %q\nwant substring %q", err, want)
		}
	}
}

func TestWriteRejectsBadPath(t *testing.T) {
	dir := t.TempDir()
	_, err := build.Write(dir, []build.File{{Path: "../escape.py", Data: []byte("x")}})
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Errorf("Write error = %v, want path-escape error", err)
	}
}
