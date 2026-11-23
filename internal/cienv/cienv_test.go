package cienv

import (
	"os"
	"os/exec"
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

func TestDetectJenkinsMergedPullRequest(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec,noctx // test fixture
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "base")
	run("checkout", "-q", "-b", "pr")
	run("commit", "-q", "--allow-empty", "-m", "pr")
	prHead := run("rev-parse", "HEAD")
	run("checkout", "-q", "main")
	run("commit", "-q", "--allow-empty", "-m", "target moved")
	run("merge", "-q", "--no-ff", "--no-edit", "pr")
	merge := run("rev-parse", "HEAD")

	env := map[string]string{"GIT_URL": "https://github.com/acme/app.git", "GIT_COMMIT": merge, "CHANGE_ID": "12"}
	b, err := Detect(Build{}, func(k string) string { return env[k] })
	if err != nil || b.Commit != prHead {
		t.Errorf("PR build: commit %s (%v), want the PR head %s", b.Commit, err, prHead)
	}

	delete(env, "CHANGE_ID")
	b, err = Detect(Build{}, func(k string) string { return env[k] })
	if err != nil || b.Commit != merge {
		t.Errorf("branch build: commit %s (%v), want GIT_COMMIT %s", b.Commit, err, merge)
	}
}
