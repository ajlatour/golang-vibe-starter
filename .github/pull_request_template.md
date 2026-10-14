## Summary

<!-- What changed, and why. Link the issue or decision it follows from. -->

## Test plan

<!-- What you ran, and what you broke on purpose to prove a new check catches it. -->

## Checklist

- [ ] One concern, with tests for what it adds or changes
- [ ] `make check` and `make lint` pass (and `make docs-check`, for docs)
- [ ] What describes the code changed with it: the spec, the docs, the log contract
- [ ] A file in `changes/` if users of a release should know; `**Breaking**` if it may break clients
- [ ] A record in `docs/adr/` if it makes an architectural choice
