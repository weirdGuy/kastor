package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// LockFilename is the local lock file guarding the state file. It is
// hidden so the module walker never treats it as module input. Remote
// state backends (and their locking) are deferred per SPEC.md §7.
const LockFilename = ".adl.state.lock"

// lockInfo is the lock file payload — purely informational, for the
// contention error message.
type lockInfo struct {
	PID     int    `json:"pid"`
	Created string `json:"created"`
}

// Lock takes the state lock for the module rooted at dir by creating the
// lock file exclusively. It returns a release function that removes the
// file. If another process holds the lock, the error says who and how to
// recover; a crashed process leaves a stale lock that must be deleted by
// hand in v0.
func Lock(dir string) (release func() error, err error) {
	path := filepath.Join(dir, LockFilename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil, contentionError(path)
	}
	if err != nil {
		return nil, fmt.Errorf("locking state: %w", err)
	}

	info := lockInfo{PID: os.Getpid(), Created: time.Now().UTC().Format(time.RFC3339)}
	data, _ := json.Marshal(info)
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("locking state: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("locking state: %w", err)
	}

	return func() error {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("releasing state lock: %w", err)
		}
		return nil
	}, nil
}

// contentionError describes an already-held lock, including the holder
// recorded in the lock file when it is readable.
func contentionError(path string) error {
	holder := ""
	if data, err := os.ReadFile(path); err == nil {
		var info lockInfo
		if json.Unmarshal(data, &info) == nil && info.PID != 0 {
			holder = fmt.Sprintf(" (held by pid %d since %s)", info.PID, info.Created)
		}
	}
	return fmt.Errorf("state is locked by another adl process%s: if that process is no longer running, delete %s", holder, path)
}
