package upload

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestGlobDoubleStar(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, p := range []string{"reports/a.xml", "reports/x/b.xml", "reports/x/y/c.xml", "reports/x/c.txt", "other/d.xml"} {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for pattern, want := range map[string][]string{
		"reports/**/*.xml":   {"reports/a.xml", "reports/x/b.xml", "reports/x/y/c.xml"},
		"./reports/**/*.xml": {"reports/a.xml", "reports/x/b.xml", "reports/x/y/c.xml"},
		"**/c.*":             {"reports/x/c.txt", "reports/x/y/c.xml"},
		"reports/*.xml":      {"reports/a.xml"},
		"missing/**/*.xml":   nil,
	} {
		got, err := Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for i := range got {
			got[i] = filepath.ToSlash(got[i])
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("Glob(%q) = %v, want %v", pattern, got, want)
		}
	}
}

func TestGlobReportsUnreadableDirectories(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("needs a directory this user can't read")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	for _, p := range []string{"reports/a.xml", "reports/locked/b.xml"} {
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod("reports/locked", 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod("reports/locked", 0o750) })
	for _, pattern := range []string{"reports/**/*.xml", "reports/*/*.xml"} {
		got, err := Glob(pattern)
		if err == nil {
			t.Errorf("Glob(%q): no error", pattern)
		}
		if pattern == "reports/**/*.xml" && !slices.Equal(got, []string{filepath.FromSlash("reports/a.xml")}) {
			t.Errorf("Glob(%q) = %v", pattern, got)
		}
	}
}
