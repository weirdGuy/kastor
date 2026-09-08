package claude

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/getkastordev/kastor/internal/provider"
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
	seen := make(map[string]struct{})
	var sorted []string
	for key := range desired {
		seen[key] = struct{}{}
		sorted = append(sorted, key)
	}
	for key := range remote {
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
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
			// An absent key and an explicit null are the same state, so a
			// null desired value is not an addition. This is what keeps the
			// create path (remote is the empty object) from reporting every
			// unset optional field as an attribute it will set.
			if want == nil {
				continue
			}
			*out = append(*out, provider.AttrDiff{Path: subpath, Old: nil, New: want})
		case !inDesired:
			if got == nil {
				continue
			}
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
