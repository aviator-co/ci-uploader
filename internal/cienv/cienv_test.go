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
		"GIT_BRANCH": "origin/main",
	}
	b, err := Detect(Build{}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if b != (Build{Repo: "acme/app", Commit: sha, BuildURL: "https://jenkins/job/app/42/", Branch: "main"}) {
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
	run := func(committer string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:noctx // test fixture
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL="+committer)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("t@t", "init", "-q", "-b", "main")
	run("t@t", "commit", "-q", "--allow-empty", "-m", "base")
	run("t@t", "checkout", "-q", "-b", "pr")
	run("t@t", "commit", "-q", "--allow-empty", "-m", "pr")
	run("t@t", "checkout", "-q", "main")
	run("t@t", "commit", "-q", "--allow-empty", "-m", "target moved")
	run("t@t", "checkout", "-q", "pr")
	// A PR head that is itself a merge, as queue drafts can be.
	run("t@t", "merge", "-q", "--no-ff", "--no-edit", "main")
	prHead := run("t@t", "rev-parse", "HEAD")
	run("t@t", "checkout", "-q", "main")
	run("t@t", "commit", "-q", "--allow-empty", "-m", "target moved again")
	run("t@t", "checkout", "-q", "--detach", "pr")
	// Jenkins checks out the PR head and merges the target into it.
	run(jenkinsMergeCommitter, "merge", "-q", "--no-ff", "--no-edit", "main")
	merge := run("t@t", "rev-parse", "HEAD")

	env := map[string]string{"GIT_URL": "https://github.com/acme/app.git", "GIT_COMMIT": merge, "CHANGE_ID": "12"}
	b, err := Detect(Build{}, func(k string) string { return env[k] })
	if err != nil || b.Commit != prHead {
		t.Errorf("Jenkins merge: commit %s (%v), want the PR head %s", b.Commit, err, prHead)
	}

	run("t@t", "checkout", "-q", "--detach", prHead)
	env["GIT_COMMIT"] = prHead
	b, err = Detect(Build{}, func(k string) string { return env[k] })
	if err != nil || b.Commit != prHead {
		t.Errorf("PR head that is a merge: commit %s (%v), want %s", b.Commit, err, prHead)
	}

	delete(env, "CHANGE_ID")
	env["GIT_COMMIT"] = merge
	b, err = Detect(Build{}, func(k string) string { return env[k] })
	if err != nil || b.Commit != merge {
		t.Errorf("branch build: commit %s (%v), want GIT_COMMIT %s", b.Commit, err, merge)
	}
}

func TestDetectBranchPrefersThePullRequestBranch(t *testing.T) {
	env := map[string]string{"CHANGE_BRANCH": "feature", "BRANCH_NAME": "PR-12"}
	if got := detectBranch(func(k string) string { return env[k] }); got != "feature" {
		t.Errorf("detectBranch = %q", got)
	}
}

func TestDetectPullRequest(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want int
	}{
		{map[string]string{"CHANGE_ID": "12"}, 12},
		{map[string]string{"GITHUB_REF": "refs/pull/34/merge"}, 34},
		{map[string]string{"GITHUB_REF": "refs/heads/main"}, 0},
		{map[string]string{"BUILDKITE_PULL_REQUEST": "false"}, 0},
		{map[string]string{"BUILDKITE_PULL_REQUEST": "56"}, 56},
		{map[string]string{"CIRCLE_PULL_REQUEST": "https://github.com/acme/app/pull/78"}, 78},
		{map[string]string{}, 0},
	} {
		if got := detectPullRequest(func(k string) string { return tc.env[k] }); got != tc.want {
			t.Errorf("detectPullRequest(%v) = %d, want %d", tc.env, got, tc.want)
		}
	}
}
