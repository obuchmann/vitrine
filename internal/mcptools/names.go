package mcptools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/obuchmann/vitrine/internal/store"
)

// resolveName completes the name of a prototype (docs/adr/0001-runtime-modes.md):
// <repo>/<name> stays as it is, a bare <name> gets the repo that contains
// file (else the repo of dir, the agent's working directory), and no name at
// all is taken from file: prototypes/login/index.html is "login",
// prototypes/login.html too.
func resolveName(dir, file, name string) (string, error) {
	if strings.Contains(name, "/") {
		if !store.ValidName(name) {
			return "", store.ErrInvalidName
		}
		return name, nil
	}
	if name == "" {
		base := filepath.Base(file)
		name = strings.TrimSuffix(base, filepath.Ext(base))
		if strings.EqualFold(base, "index.html") || strings.EqualFold(base, "index.htm") {
			name = filepath.Base(filepath.Dir(file))
		}
		name = slug(name)
	}
	root := gitRoot(filepath.Dir(file))
	if root == "" {
		root = gitRoot(dir)
	}
	if root == "" {
		return "", fmt.Errorf("neither %s nor %s is in a git repository: pass name as <repo>/<name>", file, dir)
	}
	full := slug(filepath.Base(root)) + "/" + name
	if !store.ValidName(full) {
		return "", fmt.Errorf("%w (got %q): pass name explicitly", store.ErrInvalidName, full)
	}
	return full, nil
}

// gitRoot returns the closest directory at or above dir that holds .git, a
// directory in a clone and a file in a worktree, or "" if there is none.
func gitRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

var notSlug = regexp.MustCompile(`[^a-z0-9._-]+`)

// slug turns a directory or file name into a name segment, e.g. "My App" into "my-app".
func slug(s string) string {
	s = notSlug.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-._")
}
