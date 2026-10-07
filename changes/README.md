# Changes

What's changed since the last release, one file per change, for the next
release's section of [CHANGELOG.md](../CHANGELOG.md). Each pull request with a
change worth telling adds a file here, instead of editing the changelog, so
pull requests made at the same time don't conflict.

Name the file for the change (`request-deadline.md`), and write it as the
changelog's own entries, under Keep a Changelog's headings:

```markdown
### Fixed

- A request that ran past the server's write timeout got no response. Every
  request now has a deadline, `REQUEST_TIMEOUT` ([ADR-0041](../docs/adr/0041-every-request-has-a-deadline-inside-the-servers-write-timeout.md)).
```

The headings are `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed` and
`Security`. Mark a change that may break API clients **Breaking**: a pull
request that breaks the API (`make api-compat`) needs one. Links are relative to
this directory, as above, and are rewritten for the changelog's place.

`make release-prepare` gathers the files into the release's section, each
heading once, and removes them; `sh scripts/changelog.sh notes` shows what the
next release holds. `docs/changelog_test.go` checks every file here gathers.
