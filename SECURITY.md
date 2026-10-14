# Security

## Reporting a vulnerability

Please report vulnerabilities privately, through GitHub: on the repository's **Security** tab,
choose **Report a vulnerability**. Don't open a public issue or pull request for one.

Say what's affected, how to reproduce it, and what an attacker could do with it. You'll get an
answer within a few working days, and credit in the release that fixes it if you'd like it.

## Supported versions

Fixes go into the latest release only: upgrade to it to get them. Releases are listed in the
[changelog](CHANGELOG.md).

## What the service does about security

[Architecture](docs/architecture.md#requirements) lists the security requirements (SEC-1 onwards),
each with the test that verifies it, and the [standards](docs/standards.md#security) the rules every
change follows. In short: API keys stored as hashes, every route but the probes authenticated and
rate limited, untrusted input bounded and fuzzed, static analysis and known-vulnerability checks
on every change, no third-party dependencies in the service, and a distroless image running as a
non-root user.
