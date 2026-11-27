package upload

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// Glob is filepath.Glob plus "**" for any number of directories, as in
// "reports/**/*.xml". It follows symlinked directories, as filepath.Glob does.
// Unlike filepath.Glob, a directory it can't read is an error, returned with
// whatever did match, since a report may be in it.
func Glob(pattern string) ([]string, error) {
	pattern = filepath.ToSlash(pattern)
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	root, rest := split(pattern)
	g := globber{rest: rest, seen: map[string]bool{}}
	g.walk(filepath.FromSlash(root), nil)
	return g.matches, g.err
}

type globber struct {
	rest    []string
	seen    map[string]bool
	matches []string
	err     error
}

func (g *globber) walk(dir string, names []string) {
	// A symlink loop would otherwise never end.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		if g.seen[real] {
			return
		}
		g.seen[real] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if len(names) == 0 && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)) {
			return
		}
		g.err = errors.Join(g.err, err)
	}
	for _, e := range entries {
		sub := append(slices.Clone(names), e.Name())
		p := filepath.Join(dir, e.Name())
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 {
			if info, err := os.Stat(p); err == nil {
				isDir = info.IsDir()
			}
		}
		switch {
		case isDir && matchPrefix(g.rest, sub):
			g.walk(p, sub)
		case !isDir && matchParts(g.rest, sub):
			g.matches = append(g.matches, p)
		}
	}
}

// split cuts pattern into the directory before the first wildcard, cleaned,
// and the parts after it.
func split(pattern string) (string, []string) {
	parts := strings.Split(pattern, "/")
	k := len(parts) - 1
	for i, part := range parts[:len(parts)-1] {
		if strings.ContainsAny(part, "*?[\\") {
			k = i
			break
		}
	}
	var rest []string
	for _, part := range parts[k:] {
		if part != "" && part != "." {
			rest = append(rest, part)
		}
	}
	if k == 0 {
		return ".", rest
	}
	root := strings.Join(parts[:k], "/")
	if root == "" {
		return "/", rest
	}
	root = path.Clean(root)
	if strings.HasSuffix(root, ":") {
		// A Windows drive's root, not its working directory.
		root += "/"
	}
	return root, rest
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
