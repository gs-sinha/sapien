package folder

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gs-sinha/sapien/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"":      "",
		"/":     "",
		"  /  ": "",
		"a":     "a",
		"a/b":   "a/b",
		"/a/b":  "a/b",
		"a/b/":  "a/b",
		"/a/b/": "a/b",
		" a/b ": "a/b",
	}
	for in, want := range cases {
		assert.Equal(t, want, Normalize(in), "Normalize(%q)", in)
	}
}

func TestValidate_Table(t *testing.T) {
	cases := []struct {
		name    string
		folder  string
		wantErr bool
	}{
		{"root", "", false},
		{"simple", "a", false},
		{"nested", "a/b/c", false},
		{"max depth ok", "a/b/c/d/e/f/g/h", false}, // 8 segments
		{"too deep", "a/b/c/d/e/f/g/h/i", true},    // 9 segments
		{"empty segment", "a//b", true},
		{"dot segment", "a/./b", true},
		{"dotdot segment", "a/../b", true},
		{"leading dot", "a/.git/b", true},
		{"backslash", `a\b`, true},
		{"absolute", "/a/b", true},
		{"too long", func() string {
			s := ""
			for i := 0; i < 210; i++ {
				s += "x"
			}
			return s
		}(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.folder)
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, errs.Invalid, errs.CodeOf(err))
				assert.NotEmpty(t, errs.As(err).Hint, "expected a hint on %q", tc.folder)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestOf(t *testing.T) {
	cases := map[string]string{
		"x.flow.yaml":      "",
		"a/x.flow.yaml":    "a",
		"a/b/x.flow.yaml":  "a/b",
		"/a/b/x.flow.yaml": "a/b",
	}
	for in, want := range cases {
		assert.Equal(t, want, Of(in), "Of(%q)", in)
	}
}

func TestFromAbs(t *testing.T) {
	root := filepath.FromSlash("/ws/flows")
	cases := []struct {
		abs  string
		want string
	}{
		{filepath.Join(root, "x.flow.yaml"), ""},
		{filepath.Join(root, "a", "x.flow.yaml"), "a"},
		{filepath.Join(root, "a", "b", "x.flow.yaml"), "a/b"},
		{filepath.FromSlash("/elsewhere/x.flow.yaml"), ""},
		{root, ""},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, FromAbs(root, tc.abs), "FromAbs(%q)", tc.abs)
	}
}

func TestHasPrefix(t *testing.T) {
	assert.True(t, HasPrefix("a", ""))
	assert.True(t, HasPrefix("a", "a"))
	assert.True(t, HasPrefix("a/b", "a"))
	assert.True(t, HasPrefix("a/b/c", "a/b"))
	assert.False(t, HasPrefix("ab", "a"))
	assert.False(t, HasPrefix("a", "a/b"))
	assert.False(t, HasPrefix("b", "a"))
}

func TestJoin(t *testing.T) {
	assert.Equal(t, "x.md", Join("", "x.md"))
	assert.Equal(t, "a/x.md", Join("a", "x.md"))
	assert.Equal(t, "a/b/x.md", Join("a/b", "x.md"))
}

func TestCleanEmptyDirs(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(leaf, 0o755))

	CleanEmptyDirs(leaf, root)

	_, err := os.Stat(filepath.Join(root, "a"))
	assert.True(t, os.IsNotExist(err), "expected %s/a to be removed", root)
	_, err = os.Stat(root)
	assert.NoError(t, err, "root itself must survive")
}

func TestCleanEmptyDirs_StopsAtNonEmpty(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "a", "b")
	require.NoError(t, os.MkdirAll(leaf, 0o755))
	// a/sibling.txt keeps "a" non-empty once "b" is removed.
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "sibling.txt"), []byte("x"), 0o644))

	CleanEmptyDirs(leaf, root)

	_, err := os.Stat(filepath.Join(root, "a"))
	assert.NoError(t, err, "a should survive since it still has a file in it")
	_, err = os.Stat(leaf)
	assert.True(t, os.IsNotExist(err), "b should have been removed")
}

func TestMoveFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "sub", "dst.txt")
	require.NoError(t, os.WriteFile(src, []byte("hello"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))

	require.NoError(t, MoveFile(src, dst))

	_, err := os.Stat(src)
	assert.True(t, os.IsNotExist(err))
	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}
