package upload

import (
	"os"
	"path/filepath"
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
