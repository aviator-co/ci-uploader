# aviator-ci

Uploads CI test results to [Aviator](https://aviator.co):

- **Logs of failed checks**, so the merge queue's `flaky_retry` can tell a
  flaky failure from a real one on CI providers whose logs Aviator can't read
  itself, such as Jenkins.
- **JUnit reports from every build**, passing ones included, so Aviator's
  flaky tests can track each test's history, detect flaky tests and quarantine
  them. Without `--junit`, passing builds make no network calls.

It exits with the test command's exit code, even if the upload fails. The only
exception is `--quarantine`, when every failed test is quarantined.

## Install

Pin a version, so a new release can't change your builds until you choose it:

```bash
VERSION=v0.1.0
BIN=aviator-ci_linux_amd64
BASE=https://github.com/aviator-co/ci-uploader/releases/download/$VERSION
curl -fsSLO "$BASE/$BIN" -O "$BASE/checksums.txt"
grep " $BIN\$" checksums.txt | sha256sum -c -
install -m 755 "$BIN" /usr/local/bin/aviator-ci
```

Builds are published for `linux`, `darwin` and `windows` on `amd64` and
`arm64`.

## Setup

1. Turn on `flaky_retry` in your Aviator config and list the check under
   `retriable_checks`, with the exact name your CI reports to GitHub. For
   flaky tests, turn on `flaky_tests` and pass `--junit`.
2. Set `AVIATOR_API_TOKEN` to an Aviator API token in the CI job.
3. For self-hosted Aviator, set `AVIATOR_API_HOST` to its API URL.

## Usage

Wrap the test command:

```bash
aviator-ci run --check continuous-integration/jenkins/tests -- make test
```

Or, when the test step already writes its output to a file:

```bash
rc=0; make test > out.txt 2>&1 || rc=$?
aviator-ci upload --check continuous-integration/jenkins/tests --log out.txt --exit-code $rc
```

`--exit-code` is required for `upload`. With `0` only JUnit reports are
uploaded. With `--junit`, `--started-at` is required too: the test command's
start time, so reports left by an earlier build aren't mistaken for this one's. The
`|| rc=$?` keeps a shell running with `-e`, as Jenkins and GitHub Actions do,
from stopping before the upload.

| Flag | |
|---|---|
| `--check` | The check's name, matching `retriable_checks`. Required. |
| `--junit` | JUnit reports to upload, as a glob such as `'reports/**/*.xml'`. Uploaded on passing builds too. Repeatable. |
| `--quarantine` | Exit 0 when every failed test is quarantined in Aviator. Needs `--junit`; `run` exits 2 before running the command without it. See below. |
| `--quarantine-exit-codes` | Exit codes that mean "tests failed", which `--quarantine` may turn into 0. Default: `1`. |
| `--variant` | Splits a test's history by environment, such as `linux`. At most 128 characters, no `\|`. |
| `--branch` | The branch under test. Default: from the CI environment, then git. |
| `--pull-request` | The pull request number in a PR build. Default: from `CHANGE_ID`, `GITHUB_REF`, `BUILDKITE_PULL_REQUEST` or CircleCI. |
| `--started-at` | `upload` only: when the test command started, as Unix seconds or RFC 3339. Required with `--junit`. |
| `--repo` | `owner/name`. Default: from `GIT_URL`. |
| `--commit` | Full SHA under test. Default: in a Jenkins PR build, the PR's head (the first parent when Jenkins merged it with its target); else `GIT_COMMIT`, then `git rev-parse HEAD`. Pass it when another CI builds a merge of the PR with its target. |
| `--build-url` | This build's URL. Default: `BUILD_URL`. |

Notes for `run`:

- The last 16 MiB of output is kept and uploaded.
- When the build is aborted, `aviator-ci` passes the signal on and uploads
  nothing. Put a timeout inside it (`aviator-ci run ... -- timeout 30m make
  test`), so a hung test still uploads.

## Quarantine

With `--quarantine`, a failed build exits 0 only when all of these hold:

- the repository's `flaky_tests.quarantine.shadow_mode` is `false`;
- every JUnit report parsed, was written after the command started, and
  uploaded to Aviator;
- the run reported no fewer tests than the check's recent runs, when those
  all reported about the same number;
- the reports show at least one failed test, and every one is quarantined;
- the exit code is one of `--quarantine-exit-codes`.

Otherwise, and whenever Aviator can't be reached, it keeps the real exit code.
In shadow mode it prints which failures would have been quarantined.

These checks can only see the tests that reported. So wrap a single test
runner invocation, whose failing exit code means only "tests failed", and make
`--junit` match only the reports that command writes. A broader glob can pick
up another step's reports from the same workspace:

```bash
aviator-ci run --check ci/tests --junit reports/unit.xml --quarantine -- pytest --junitxml=reports/unit.xml
```

Don't use `--quarantine` around:

- a build tool, such as `make` (which exits 2 for any failed step), Gradle or
  Maven. A compile error in one module exits the same way as a failed test,
  and writes no report;
- a fail-fast run, such as `pytest -x`, `jest --bail`, or Maven's default
  reactor, which skips the tests after the first failure;
- anything else in the same command, such as a lint step or a coverage
  threshold.

The test reporter must also report a test file that fails to load, such as one
with a syntax error, as a failure. jest-junit leaves those out unless
`reportTestSuiteErrors` is on.

Compile in an earlier step, and run each test runner in its own `aviator-ci`
invocation.

## Releasing

Push a `v*` tag. GoReleaser builds the binaries and publishes the GitHub
release. A tag with a suffix, such as `v0.2.0-rc.1`, is published as a
pre-release.

## License

MIT
