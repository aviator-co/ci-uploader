# aviator-ci

Uploads the output of a failed CI check to [Aviator](https://aviator.co), so
the merge queue's `flaky_retry` can tell a flaky failure from a real one on CI
providers whose logs Aviator can't read itself, such as Jenkins.

- Uploads only when the test command fails. Passing builds make no network calls.
- Always exits with the test command's exit code, even if the upload fails.
- Uploads only for merge queue builds. Others are skipped after one API call.

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
   `retriable_checks`, with the exact name your CI reports to GitHub.
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

`--exit-code` is required for `upload`. With `0` nothing is uploaded. The
`|| rc=$?` keeps a shell running with `-e`, as Jenkins and GitHub Actions do,
from stopping before the upload.

| Flag | |
|---|---|
| `--check` | The check's name, matching `retriable_checks`. Required. |
| `--junit` | JUnit reports to upload too, as a glob such as `'reports/**/*.xml'`. Repeatable. |
| `--repo` | `owner/name`. Default: from `GIT_URL`. |
| `--commit` | Full SHA under test. Default: `GIT_COMMIT`, then `git rev-parse HEAD`. Pass it when your CI builds a merge of the PR with its target. |
| `--build-url` | This build's URL. Default: `BUILD_URL`. |

Notes for `run`:

- The last 16 MiB of output is kept and uploaded.
- When the build is aborted, `aviator-ci` passes the signal on and uploads
  nothing. Put a timeout inside it (`aviator-ci run ... -- timeout 30m make
  test`), so a hung test still uploads.

## Releasing

Push a `v*` tag. GoReleaser builds the binaries and publishes the GitHub
release. A tag with a suffix, such as `v0.2.0-rc.1`, is published as a
pre-release.

## License

MIT
