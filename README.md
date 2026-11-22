# aviator-ci

Uploads the output of a failed CI check to [Aviator](https://aviator.co), so
the merge queue's `flaky_retry` can tell a flaky failure from a real one on CI
providers whose logs Aviator can't read itself, such as Jenkins.

- Uploads only when the wrapped command fails. Passing builds make no network calls.
- Always exits with the wrapped command's exit code, even if the upload fails.

Usage docs are coming with the first release.

## License

MIT
