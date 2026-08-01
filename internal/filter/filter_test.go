package filter

import (
	"reflect"
	"sort"
	"testing"
)

func TestIncluded(t *testing.T) {
	tests := []struct {
		name     string
		paths    []string
		include  []string
		exclude  []string
		expected []string
	}{
		{
			name:     "default include matches everything",
			paths:    []string{"a.md", "b/c.md"},
			expected: []string{"a.md", "b/c.md"},
		},
		{
			name:     "explicit include restricts to subtree",
			paths:    []string{"a.md", "agents/coding.md"},
			include:  []string{"agents/**"},
			expected: []string{"agents/coding.md"},
		},
		{
			name:     "exclude removes matches",
			paths:    []string{"a.md", "env.local.json"},
			include:  []string{"**"},
			exclude:  []string{"env.local.json"},
			expected: []string{"a.md"},
		},
		{
			name:     "brace expansion includes multiple extensions",
			paths:    []string{"a.md", "a.txt", "b.toml", "b.md"},
			include:  []string{"*.{md,toml}"},
			expected: []string{"a.md", "b.toml", "b.md"},
		},
		{
			name:     "deep globstar matches at any depth",
			paths:    []string{"a/b/c/d.md", "x/y.md", "z.md"},
			include:  []string{"**/*.md"},
			expected: []string{"a/b/c/d.md", "x/y.md", "z.md"},
		},
		{
			name:     "single-star does not cross slashes",
			paths:    []string{"agents/coding.md", "snippets/coding.md", "coding.md"},
			include:  []string{"*.md"},
			expected: []string{"coding.md"},
		},
		{
			name:     "exclude branch",
			paths:    []string{"agents/a.md", "agents/secret/b.md", "snippets/c.md"},
			include:  []string{"**/*.md"},
			exclude:  []string{"agents/secret/**"},
			expected: []string{"agents/a.md", "snippets/c.md"},
		},
		{
			name:     "character class",
			paths:    []string{"a.md", "b.md", "c.txt"},
			include:  []string{"[ab].md"},
			expected: []string{"a.md", "b.md"},
		},
		{
			name:     "negated character class",
			paths:    []string{"a.md", "b.md", "x.md"},
			include:  []string{"[!ab].md"},
			expected: []string{"x.md"},
		},
		{
			name:     "question mark matches single chars",
			paths:    []string{"a.md", "ab.md", "abc.md"},
			include:  []string{"a?.md"},
			expected: []string{"ab.md"},
		},
		{
			name:     "include empty list of paths returns empty",
			paths:    []string{},
			expected: []string{},
		},
		{
			name:     "all excluded returns empty",
			paths:    []string{"a.md", "b.md"},
			include:  []string{"**"},
			exclude:  []string{"**"},
			expected: []string{},
		},
		{
			name:     "no include match returns empty",
			paths:    []string{"a.md", "b.md"},
			include:  []string{"agents/**"},
			expected: []string{},
		},
		{
			name:     "negation through exclude beats include",
			paths:    []string{"x.md", "x.env", "y.md"},
			include:  []string{"**"},
			exclude:  []string{"*.env"},
			expected: []string{"x.md", "y.md"},
		},
		{
			name:     "dotfiles included by **",
			paths:    []string{".gitignore", ".DS_Store", "a.md"},
			include:  []string{"**"},
			expected: []string{".DS_Store", ".gitignore", "a.md"},
		},
		{
			name:     "two include patterns",
			paths:    []string{"agents/a.md", "snippets/b.md", "other/c.md"},
			include:  []string{"agents/**", "snippets/**"},
			expected: []string{"agents/a.md", "snippets/b.md"},
		},
		{
			name:     "exclude by glob only",
			paths:    []string{"x.tmp", "x.md", "y.tmp", "y.md"},
			include:  []string{"**"},
			exclude:  []string{"*.tmp"},
			expected: []string{"x.md", "y.md"},
		},
		{
			name:     "case sensitivity preserved",
			paths:    []string{"Readme.md", "readme.md", "README.md"},
			include:  []string{"README.md"},
			expected: []string{"README.md"},
		},
		{
			name:     "deep intermediate dirs (doublestar allows zero)",
			paths:    []string{"a/b/c/d.md", "a/c.md", "a.md"},
			include:  []string{"a/**/*.md"},
			expected: []string{"a/b/c/d.md", "a/c.md"},
		},
		{
			name:     "exclude beats include even when same pattern",
			paths:    []string{"a.md", "b.md"},
			include:  []string{"*.md"},
			exclude:  []string{"a.md"},
			expected: []string{"b.md"},
		},
		{
			name:     "include with brace alternation in subdirs",
			paths:    []string{"agents/a.md", "snippets/a.md", "x/a.md"},
			include:  []string{"{agents,snippets}/**"},
			expected: []string{"agents/a.md", "snippets/a.md"},
		},
		{
			name:     "exclude directory and its descendants",
			paths:    []string{"a/b/c.md", "a/d.md"},
			include:  []string{"a/**/*.md"},
			exclude:  []string{"a/b/**"},
			expected: []string{"a/d.md"},
		},
		{
			name:     "include pattern with no matching paths",
			paths:    []string{"a.md", "b.md"},
			include:  []string{"agents/**"},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Included(tt.paths, tt.include, tt.exclude)
			if err != nil {
				t.Fatalf("Included: %v", err)
			}
			if !reflect.DeepEqual(sortStrings(got), sortStrings(tt.expected)) {
				t.Errorf("got %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	if err := Validate([]string{"**", "*.md"}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if err := Validate([]string{"["}); err == nil {
		t.Error("expected error for [")
	}
}

func TestCount(t *testing.T) {
	paths := []string{"a.md", "b/c.md", "x.tmp"}
	include := []string{"**"}
	exclude := []string{"*.tmp"}
	got, err := Count(paths, include, exclude)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Errorf("Count: got %d, want 2", got)
	}
}

func sortStrings(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	sort.Strings(out)
	return out
}
