package upload

import (
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
)

// Glob is filepath.Glob plus "**" for any number of directories, as in
// "reports/**/*.xml". Unlike filepath.Glob, a directory it can't read is an
// error, returned with whatever did match, since a report may be in it.
func Glob(pattern string) ([]string, error) {
	pattern = filepath.ToSlash(pattern)
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	root := filepath.FromSlash(staticPrefix(pattern))
	parts := strings.Split(pattern, "/")
	var matches []string
	var walkErr error
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			walkErr = errors.Join(walkErr, err)
			return nil
		}
		names := strings.Split(filepath.ToSlash(p), "/")
		if d.IsDir() {
			if p != root && !matchPrefix(parts, names) {
				return fs.SkipDir
			}
			return nil
		}
		if matchParts(parts, names) {
			matches = append(matches, p)
		}
		return nil
	})
	return matches, errors.Join(err, walkErr)
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
	if len(dirs) == 1 && strings.HasSuffix(dirs[0], ":") {
		// A Windows drive's root, not its working directory.
		return dirs[0] + "/"
	}
	if dirs[0] == "" {
		return "/" + strings.Join(dirs[1:], "/")
	}
	return strings.Join(dirs, "/")
}

// matchPrefix is whether a file under the directory names could match.
func matchPrefix(pattern, names []string) bool {
	for len(pattern) > 0 && pattern[0] == "." {
		pattern = pattern[1:]
	}
	switch {
	case len(names) == 0:
		return len(pattern) > 0
	case len(pattern) <= 1:
		// What's left of the pattern names the file, not a directory.
		return len(pattern) == 1 && pattern[0] == "**"
	case pattern[0] == "**":
		return true
	}
	if ok, _ := path.Match(pattern[0], names[0]); !ok {
		return false
	}
	return matchPrefix(pattern[1:], names[1:])
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
