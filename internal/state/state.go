// Package state owns the adl.state.json file (SPEC.md §5): the record of
// which remote resource each block address maps to and the configuration
// last applied to it, used by adl plan/apply for three-way comparison and
// drift detection.
//
// Determinism guarantee: writing equal logical state always produces
// byte-identical files — struct fields serialize in declared order, maps in
// sorted key order, and configs are stored as canonical JSON produced by
// the provider engine.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Version is the state file format version this adl reads and writes.
// Load rejects any other version rather than misinterpreting it (the same
// stance SPEC.md §9 takes on language versioning).
const Version = 1

// Filename is the state file's name, fixed at the module root.
const Filename = "adl.state.json"

// File is the decoded state file. Serial increases by one on every Write,
// so any two snapshots of the same module's state are ordered.
type File struct {
	Version int                     `json:"version"`
	Serial  uint64                  `json:"serial"`
	Targets map[string]*TargetState `json:"targets"`
}

// TargetState is the managed resource set of one platform target.
type TargetState struct {
	Resources map[string]*Resource `json:"resources"`
}

// Resource records one managed remote resource: the remote ID it maps to,
// the canonical JSON of the configuration last applied to it (full config,
// not a hash — drift reports name the attributes that changed), and its
// managed dependencies (block addresses), kept so a resource removed from
// the spec can still be destroyed in reverse dependency order.
type Resource struct {
	ID           string          `json:"id"`
	Config       json.RawMessage `json:"config"`
	Dependencies []string        `json:"dependencies,omitempty"`
}

// Load reads the state file at the root of dir. A missing file is an empty
// state, not an error — every module starts unmanaged.
func Load(dir string) (*File, error) {
	path := filepath.Join(dir, Filename)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &File{Version: Version, Targets: map[string]*TargetState{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state file: %w", err)
	}

	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: parsing state file: %w", path, err)
	}
	if f.Version != Version {
		return nil, fmt.Errorf("%s: state file version %d is not supported by this adl (supports version %d)", path, f.Version, Version)
	}
	if f.Targets == nil {
		f.Targets = map[string]*TargetState{}
	}
	return &f, nil
}

// Write bumps the serial and atomically replaces the state file at the root
// of dir (temp file + rename, so a crash never leaves a torn file). Targets
// with no resources are dropped from the output — an empty entry carries no
// information and would accumulate forever.
func (f *File) Write(dir string) error {
	f.Version = Version
	f.Serial++

	out := &File{Version: f.Version, Serial: f.Serial, Targets: map[string]*TargetState{}}
	for name, ts := range f.Targets {
		if len(ts.Resources) > 0 {
			out.Targets[name] = ts
		}
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding state file: %w", err)
	}
	data = append(data, '\n')

	path := filepath.Join(dir, Filename)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}
	return nil
}

// Target returns the state of one platform target, creating an empty entry
// if the target is not tracked yet.
func (f *File) Target(name string) *TargetState {
	if ts, ok := f.Targets[name]; ok {
		if ts.Resources == nil {
			ts.Resources = map[string]*Resource{}
		}
		return ts
	}
	ts := &TargetState{Resources: map[string]*Resource{}}
	f.Targets[name] = ts
	return ts
}
