package cienv

import (
	"strings"
	"testing"
)

func TestRepoFromURL(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/acme/app.git":         "acme/app",
		"https://github.com/acme/app":             "acme/app",
		"https://user@ghe.acme.com/acme/app.git/": "acme/app",
		"git@github.com:acme/app.git":             "acme/app",
		"ssh://git@github.com/acme/app.git":       "acme/app",
		"https://github.com/acme":                 "",
		"":                                        "",
	} {
		if got := RepoFromURL(url); got != want {
			t.Errorf("RepoFromURL(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestDetect(t *testing.T) {
	sha := strings.Repeat("ab", 20)
	env := map[string]string{
		"GIT_URL":    "https://github.com/acme/app.git",
		"GIT_COMMIT": strings.ToUpper(sha),
		"BUILD_URL":  "https://jenkins/job/app/42/",
	}
	b, err := Detect(Build{}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if b != (Build{Repo: "acme/app", Commit: sha, BuildURL: "https://jenkins/job/app/42/"}) {
		t.Errorf("Detect = %+v", b)
	}

	b, err = Detect(Build{Repo: "acme/other", Commit: sha}, func(k string) string { return env[k] })
	if err != nil || b.Repo != "acme/other" {
		t.Errorf("flags not preferred: %+v, %v", b, err)
	}

	if _, err := Detect(Build{Commit: sha}, func(string) string { return "" }); err == nil {
		t.Error("no repo should fail")
	}
	if _, err := Detect(Build{Repo: "acme/app", Commit: "abc123"}, func(string) string { return "" }); err == nil {
		t.Error("short sha should fail")
	}
}
