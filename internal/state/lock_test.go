package state_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/weirdGuy/agentform/internal/state"
)

func TestLockAcquireReleaseReacquire(t *testing.T) {
	dir := t.TempDir()

	release, err := state.Lock(dir)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, state.LockFilename)); err != nil {
		t.Fatalf("lock file not created: %v", err)
	}

	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, state.LockFilename)); !os.IsNotExist(err) {
		t.Fatal("lock file still exists after release")
	}

	release, err = state.Lock(dir)
	if err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
	defer release()
}

func TestLockContention(t *testing.T) {
	dir := t.TempDir()

	release, err := state.Lock(dir)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer release()

	_, err = state.Lock(dir)
	if err == nil {
		t.Fatal("second Lock succeeded, want contention error")
	}
	// The error must say who holds the lock and how to recover.
	for _, want := range []string{
		state.LockFilename,
		"pid " + strconv.Itoa(os.Getpid()),
		"delete",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("contention error %q missing substring %q", err, want)
		}
	}
}

func TestLockContentionWithUnreadableLockFile(t *testing.T) {
	dir := t.TempDir()
	// A lock file left by something else entirely still blocks, without
	// crashing on the unparsable payload.
	if err := os.WriteFile(filepath.Join(dir, state.LockFilename), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := state.Lock(dir)
	if err == nil {
		t.Fatal("Lock succeeded despite existing lock file")
	}
	if !strings.Contains(err.Error(), state.LockFilename) {
		t.Errorf("error %q does not name the lock file", err)
	}
}
