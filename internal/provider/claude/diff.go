package claude

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/weirdGuy/kastor/internal/provider"
)

func diffObjects(desired, remote provider.Object) []provider.AttrDiff {
	var diffs []provider.AttrDiff
	diffValue("", desired, remote, &diffs)
	return diffs
}

func diffValue(path string, desired, remote any, out *[]provider.AttrDiff) {
	switch want := desired.(type) {
	case map[string]any:
		got, ok := remote.(map[string]any)
		if !ok {
			appendLeaf(path, desired, remote, out)
			return
		}
		diffMap(path, want, got, out)
	case []any:
		got, ok := remote.([]any)
		if !ok {
			appendLeaf(path, desired, remote, out)
			return
		}
		if replaceArray(path) {
			diffReplaceArray(path, want, got, out)
			return
		}
		if !reflect.DeepEqual(want, got) {
			appendLeaf(path, desired, remote, out)
		}
	default:
		appendLeaf(path, desired, remote, out)
	}
}

func diffMap(path string, desired, remote map[string]any, out *[]provider.AttrDiff) {
	keys := make(map[string]bool, len(desired)+len(remote))
	for key := range desired {
		keys[key] = true
	}
	for key := range remote {
		keys[key] = true
	}
	sorted := make([]string, 0, len(keys))
	for key := range keys {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)

	for _, key := range sorted {
		want, inDesired := desired[key]
		got, inRemote := remote[key]
		subpath := key
		if path != "" {
			subpath = path + "." + key
		}
		switch {
		case !inRemote:
			*out = append(*out, provider.AttrDiff{Path: subpath, Old: nil, New: want})
		case !inDesired:
			*out = append(*out, provider.AttrDiff{Path: subpath, Old: got, New: nil})
		default:
			diffValue(subpath, want, got, out)
		}
	}
}

// diffReplaceArray compares a replace-whole API array order-sensitively but
// emits one entry per changed index so plans remain readable.
func diffReplaceArray(path string, desired, remote []any, out *[]provider.AttrDiff) {
	length := len(desired)
	if len(remote) > length {
		length = len(remote)
	}
	for i := 0; i < length; i++ {
		subpath := fmt.Sprintf("%s[%d]", path, i)
		switch {
		case i >= len(remote):
			*out = append(*out, provider.AttrDiff{Path: subpath, Old: nil, New: desired[i]})
		case i >= len(desired):
			*out = append(*out, provider.AttrDiff{Path: subpath, Old: remote[i], New: nil})
		case !reflect.DeepEqual(desired[i], remote[i]):
			*out = append(*out, provider.AttrDiff{Path: subpath, Old: remote[i], New: desired[i]})
		}
	}
}

func appendLeaf(path string, desired, remote any, out *[]provider.AttrDiff) {
	if !reflect.DeepEqual(desired, remote) {
		*out = append(*out, provider.AttrDiff{Path: path, Old: remote, New: desired})
	}
}
