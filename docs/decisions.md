# Architecture decisions

The decisions that shaped this service, one record each: what forced a choice, what was chosen
and what was turned down, and what follows from it. [Architecture](architecture.md) describes the
result; these records say why it is that way.

Most were recorded after they were made: their date is that of the commit that made them, which
is the first place to look for the details.

## How to use these records

- **Before changing something a record covers**, read it: the constraint that forced the choice
  may still hold.
- **To change a decision**, add a new record that supersedes the old one. Set the old record's
  status to "Superseded by ADR-NNNN", and change nothing else in it: the history is the point.
  A decision that no longer applies, with nothing in its place, is "Deprecated: <why>": `make
  init` marks so the records of the features a service leaves out.
- **To record a new decision**, copy the template below into a new file in [`adr/`](adr/), named
  for its number and title (`adr/0045-the-decision-in-a-few-words.md`), taking the next number.
  Record it in the pull request that makes the change. Each record is a file of its own, so pull
  requests that add records at the same time don't conflict; if two take the same number, the
  check below fails on the second, which renumbers.

`docs/architecture_test.go` checks the records are numbered in order with no gaps or repeats,
each file is named for its number, and each has a status, date, context, decision and
consequences; and that every record cited in these documents exists.

Template:

```text
# ADR-NNNN: <the decision, as a short statement>

- **Status:** Proposed | Accepted | Superseded by ADR-NNNN | Deprecated: <why it no longer applies>
- **Date:** YYYY-MM-DD (commit)

**Context.** What forces a choice: the problem, the constraints, the options considered.

**Decision.** What was chosen, in a sentence or two.

**Consequences.** What follows, good and bad, and what holds it in place.
```

## The records

One file each, in [`adr/`](adr/), in number order: the folder's listing is the index, and the
documentation site's version of this page lists them with their status.
