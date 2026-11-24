// Package cienv reads the repository, commit and build URL from the CI
// environment.
package cienv

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type Build struct {
	Repo     string
	Commit   string
	BuildURL string
	// The branch under test, or "" when the CI doesn't say and HEAD is detached.
	Branch string
}

// branchVars name the branch, most specific first: a Jenkins PR build has the
// PR's branch in CHANGE_BRANCH and "PR-123" in BRANCH_NAME.
var branchVars = []string{
	"CHANGE_BRANCH",
	"BRANCH_NAME",
	"GIT_BRANCH",
	"GITHUB_HEAD_REF",
	"GITHUB_REF_NAME",
	"BUILDKITE_BRANCH",
	"CIRCLE_BRANCH",
}

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Detect fills in whatever b leaves empty from the environment, then checks
// that Repo and Commit are usable.
func Detect(b Build, getenv func(string) string) (Build, error) {
	if b.Repo == "" {
		for _, key := range []string{"GIT_URL", "GIT_URL_1"} {
			if url := getenv(key); url != "" {
				b.Repo = RepoFromURL(url)
				break
			}
		}
	}
	if b.Commit == "" && getenv("CHANGE_ID") != "" {
		// Jenkins builds a pull request merged with its target by default, and
		// GIT_COMMIT is then that local merge. Jenkins has no variable for the
		// PR head.
		b.Commit = pullRequestHead()
	}
	if b.Commit == "" {
		b.Commit = getenv("GIT_COMMIT")
	}
	if b.Commit == "" {
		b.Commit = gitHead()
	}
	if b.BuildURL == "" {
		b.BuildURL = getenv("BUILD_URL")
	}
	if b.Branch == "" {
		b.Branch = detectBranch(getenv)
	}

	b.Commit = strings.ToLower(strings.TrimSpace(b.Commit))
	if b.Repo == "" {
		return b, fmt.Errorf("could not tell the repository from GIT_URL; pass --repo owner/name")
	}
	if strings.Count(b.Repo, "/") != 1 || strings.ContainsAny(b.Repo, " \t") {
		return b, fmt.Errorf("repository %q is not owner/name", b.Repo)
	}
	if !shaRE.MatchString(b.Commit) {
		return b, fmt.Errorf("could not tell the commit from GIT_COMMIT or git; pass --commit <full sha>")
	}
	return b, nil
}

// RepoFromURL returns owner/name from a git remote URL, or "" if it has no
// owner and name.
func RepoFromURL(url string) string {
	url = strings.TrimSuffix(strings.TrimSpace(url), "/")
	url = strings.TrimSuffix(url, ".git")
	if i := strings.Index(url, "://"); i >= 0 {
		url = url[i+3:]
	} else if i := strings.Index(url, ":"); i >= 0 {
		// scp-like: git@github.com:owner/name
		url = url[:i] + "/" + url[i+1:]
	}
	parts := strings.Split(url, "/")
	if len(parts) < 3 {
		return ""
	}
	owner, name := parts[len(parts)-2], parts[len(parts)-1]
	if owner == "" || name == "" {
		return ""
	}
	return owner + "/" + name
}

func detectBranch(getenv func(string) string) string {
	for _, key := range branchVars {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			// Jenkins' GIT_BRANCH carries the remote.
			return strings.TrimPrefix(v, "origin/")
		}
	}
	if branch := git("rev-parse", "--abbrev-ref", "HEAD"); branch != "HEAD" {
		return branch
	}
	return ""
}

func gitHead() string {
	return git("rev-parse", "HEAD")
}

// jenkinsMergeCommitter is who Jenkins' merge-with-target checkout commits
// as. Checking it keeps a PR head that is itself a merge from being mistaken
// for Jenkins' merge.
const jenkinsMergeCommitter = "nobody@nowhere"

// pullRequestHead is HEAD, or its first parent when HEAD is Jenkins' merge of
// the target into the PR head.
func pullRequestHead() string {
	fields := strings.Fields(git("log", "-1", "--format=%H %ce %P", "HEAD"))
	if len(fields) == 0 {
		return ""
	}
	if len(fields) == 4 && fields[1] == jenkinsMergeCommitter {
		return fields[2]
	}
	return fields[0]
}

func git(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
