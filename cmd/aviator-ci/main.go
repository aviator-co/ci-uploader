// Command aviator-ci uploads CI test results to Aviator: a failed check's
// output, so its merge queue can tell flaky failures from real ones, and JUnit
// reports from every run, for flaky test detection.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aviator-co/ci-uploader/internal/api"
	"github.com/aviator-co/ci-uploader/internal/cienv"
	"github.com/aviator-co/ci-uploader/internal/upload"
)

// Version is set at release time.
var Version = "dev"

// The whole upload, so a slow API never holds up the build for long. A passing
// build only sends history, so it waits less.
const (
	uploadTimeout        = 2 * time.Minute
	passingUploadTimeout = 15 * time.Second
)

const usage = `Usage: aviator-ci <command> [flags]

Commands:
  run      Run a test command and upload its JUnit reports, and its output if it fails
  upload   Upload JUnit reports, and a log after a failed test command
  version  Print the version

Set AVIATOR_API_TOKEN to an Aviator API token. For self-hosted Aviator, set
AVIATOR_API_HOST to its URL.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		return runRun(args[1:])
	case "upload":
		return runUpload(args[1:])
	case "version", "--version":
		fmt.Println(Version)
		return 0
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "aviator-ci: unknown command %q\n\n%s", args[0], usage)
	return 2
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// target is what every command needs to know about the check.
type target struct {
	check   string
	junit   stringList
	variant string
	build   cienv.Build
}

func (t *target) register(fs *flag.FlagSet) {
	fs.StringVar(&t.check, "check", "", "the check's name as it reports to GitHub, matching flaky_retry's retriable_checks (required)")
	fs.Var(&t.junit, "junit", "JUnit report files to upload, as a glob such as 'reports/**/*.xml' (repeatable); uploaded on success too, for flaky tests")
	fs.StringVar(&t.variant, "variant", "", "splits a test's history by environment, such as 'linux'; at most 128 characters, no '|' (optional)")
	fs.StringVar(&t.build.Branch, "branch", "", "the branch under test (default: from the CI environment, then git)")
	fs.IntVar(&t.build.PullRequest, "pull-request", 0, "the pull request number in a PR build (default: from the CI environment)")
	fs.StringVar(&t.build.Repo, "repo", "", "owner/name of the GitHub repository (default: from GIT_URL)")
	fs.StringVar(&t.build.Commit, "commit", "", "full SHA of the commit under test (default: the PR head in a Jenkins PR build, else GIT_COMMIT, then git HEAD)")
	fs.StringVar(&t.build.BuildURL, "build-url", "", "URL of this build (default: BUILD_URL)")
}

func runUpload(args []string) int {
	fs := flag.NewFlagSet("upload", flag.ContinueOnError)
	var t target
	t.register(fs)
	logPath := fs.String("log", "", "the failed command's output")
	exitCode := fs.Int("exit-code", -1, "the test command's exit code, which aviator-ci exits with; 0 uploads only the JUnit reports (required)")
	startedAt := fs.String("started-at", "", "when the test command started, as Unix seconds or RFC 3339; reports older than this are left out, and without it the reports count as incomplete")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: aviator-ci upload --check <name> --log <file> --exit-code <code> [flags]\n\n")
		fs.PrintDefaults()
	}
	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err == nil && fs.NArg() > 0 {
		err = fmt.Errorf("unexpected argument %q", fs.Arg(0))
		fmt.Fprintf(os.Stderr, "aviator-ci: %v\n", err)
	}
	// Required: any default would guess the build's result, and in a Jenkins
	// post block a wrong guess changes it.
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == "exit-code" })
	if err == nil && !given {
		fmt.Fprintln(os.Stderr, "aviator-ci: --exit-code is required")
		return 2
	}
	if err != nil {
		// Still exit with the build's result, so a flag this version doesn't
		// know can't fail a passing build.
		if code, ok := exitCodeArg(args); ok {
			warnf("not uploading")
			return code
		}
		return 2
	}
	started, err := parseStartedAt(*startedAt)
	if err != nil {
		warnf("--started-at: %v; treating the reports as incomplete", err)
	}
	if *exitCode == 0 {
		if len(t.junit) > 0 {
			send(t, outcome{started: started})
		}
		return 0
	}
	*exitCode = failureCode(*exitCode)

	var log []byte
	var size int64
	if *logPath != "" {
		if log, size, err = upload.ReadTail(*logPath); err != nil {
			warnf("could not read the log: %v", err)
		}
	}
	send(t, outcome{log: log, logSize: size, exitCode: *exitCode, started: started})
	return *exitCode
}

func parseStartedAt(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if secs, perr := strconv.ParseInt(s, 10, 64); perr == nil {
		t, err = time.Unix(secs, 0), nil
	}
	if err == nil && t.After(time.Now()) {
		// Milliseconds read as seconds land here, and would make every report
		// look stale.
		return time.Time{}, fmt.Errorf("%q is in the future", s)
	}
	return t, err
}

// exitCodeArg finds --exit-code in args that failed to parse.
func exitCodeArg(args []string) (int, bool) {
	for i, arg := range args {
		if arg == "--" {
			break
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !strings.HasPrefix(arg, "-") || name != "exit-code" {
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				return 0, false
			}
			value = args[i+1]
		}
		code, err := strconv.Atoi(value)
		return failureCode(code), err == nil
	}
	return 0, false
}

// failureCode keeps a negative code, which no process exits with, from
// wrapping to something that reads as success.
func failureCode(code int) int {
	if code < 0 {
		return 1
	}
	return code
}

// outcome is what the test command did.
type outcome struct {
	log      []byte
	logSize  int64
	exitCode int
	// When the command started; zero when aviator-ci didn't run it.
	started time.Time
}

// send uploads whatever it can and only ever warns, so a problem here never
// changes the build's result.
func send(t target, r outcome) {
	if t.check == "" {
		warnf("--check is required; not uploading")
		return
	}
	token := os.Getenv("AVIATOR_API_TOKEN")
	if token == "" {
		warnf("AVIATOR_API_TOKEN is not set; not uploading")
		return
	}
	build, err := cienv.Detect(t.build, os.Getenv)
	if err != nil {
		warnf("%v; not uploading", err)
		return
	}
	var junit []string
	missing := false
	seen := map[string]bool{}
	for _, pattern := range t.junit {
		paths, err := upload.Glob(pattern)
		if err != nil {
			warnf("bad --junit pattern %q: %v", pattern, err)
			missing = true
			continue
		}
		if len(paths) == 0 {
			warnf("--junit %q matched no reports", pattern)
			missing = true
		}
		for _, p := range paths {
			if !seen[p] {
				seen[p] = true
				junit = append(junit, p)
			}
		}
	}
	timeout := uploadTimeout
	if r.exitCode == 0 {
		timeout = passingUploadTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client := api.New(os.Getenv("AVIATOR_API_HOST"), token, "aviator-ci/"+Version)
	_, err = upload.Run(ctx, client, upload.Request{
		Repo:         build.Repo,
		Commit:       build.Commit,
		Check:        t.check,
		BuildURL:     build.BuildURL,
		Branch:       build.Branch,
		PullRequest:  build.PullRequest,
		Variant:      t.variant,
		ExitCode:     r.exitCode,
		Log:          r.log,
		LogSize:      r.logSize,
		JUnitPaths:   junit,
		JUnitMissing: missing,
		Since:        r.started,
	}, infof)
	if err != nil {
		warnf("upload failed: %v", err)
	}
}

func infof(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "aviator-ci: "+format+"\n", args...)
}

func warnf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "aviator-ci: warning: "+format+"\n", args...)
}
