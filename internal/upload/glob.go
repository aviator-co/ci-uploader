package upload

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Glob is filepath.Glob plus "**" for any number of directories, as in
// "reports/**/*.xml".
func Glob(pattern string) ([]string, error) {
	pattern = filepath.ToSlash(pattern)
	if !strings.Contains(pattern, "**") {
		return filepath.Glob(filepath.FromSlash(pattern))
	}
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	root := staticPrefix(pattern)
	parts := strings.Split(pattern, "/")
	var matches []string
	err := filepath.WalkDir(filepath.FromSlash(root), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == filepath.FromSlash(root) {
				return fs.SkipAll
			}
			return nil
		}
		if !d.IsDir() && matchParts(parts, strings.Split(filepath.ToSlash(p), "/")) {
			matches = append(matches, p)
		}
		return nil
	})
	return matches, err
}

// staticPrefix is the directory before the first wildcard.
func staticPrefix(pattern string) string {
	parts := strings.Split(pattern, "/")
	var dirs []string
	for _, part := range parts[:len(parts)-1] {
		if strings.ContainsAny(part, "*?[\\") {
			break
		}
		dirs = append(dirs, part)
	}
	if len(dirs) == 0 {
		return "."
	}
	if dirs[0] == "" {
		return "/" + strings.Join(dirs[1:], "/")
	}
	return strings.Join(dirs, "/")
}

func matchParts(pattern, name []string) bool {
	// WalkDir from "." yields names without the "./" a pattern may start with.
	for len(pattern) > 0 && pattern[0] == "." {
		pattern = pattern[1:]
	}
	if len(pattern) == 0 {
		return len(name) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(name); i++ {
			if matchParts(pattern[1:], name[i:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	if ok, _ := path.Match(pattern[0], name[0]); !ok {
		return false
	}
	return matchParts(pattern[1:], name[1:])
}
