// Package filter applies include/exclude glob patterns to a set of paths.
package filter

import (
	"github.com/bmatcuk/doublestar/v4"
)

// DefaultInclude is the wildcard used when the config omits an include set.
var DefaultInclude = []string{"**"}

// isMatch returns true if path matches any pattern in patterns.
// Patterns are interpreted as POSIX paths (forward slashes). Use
// doublestar.PathMatch with explicit forward-slash semantics.
func isMatch(path string, patterns []string) (bool, error) {
	for _, p := range patterns {
		ok, err := doublestar.PathMatch(p, path)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// Included returns the subset of paths that match at least one include
// pattern and do not match any exclude pattern. Relative paths must use
// forward slashes.
func Included(paths, include, exclude []string) ([]string, error) {
	if len(include) == 0 {
		include = DefaultInclude
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		inc, err := isMatch(p, include)
		if err != nil {
			return nil, err
		}
		if !inc {
			continue
		}
		exc, err := isMatch(p, exclude)
		if err != nil {
			return nil, err
		}
		if exc {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// Count returns the number of items in paths that match at least one
// include pattern and no exclude pattern.
func Count(paths, include, exclude []string) (int, error) {
	matched, err := Included(paths, include, exclude)
	if err != nil {
		return 0, err
	}
	return len(matched), nil
}

// Validate returns an error if any pattern is syntactically invalid.
func Validate(patterns []string) error {
	for _, p := range patterns {
		if !doublestar.ValidatePattern(p) {
			return &PatternError{Pattern: p}
		}
	}
	return nil
}

// PatternError is returned for syntactically invalid patterns.
type PatternError struct {
	Pattern string
}

func (e *PatternError) Error() string {
	return "invalid glob pattern: " + e.Pattern
}
