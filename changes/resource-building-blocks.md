### Changed

- The 412 a stale `If-Match` gets now says "it has changed since that version: …", not "the item
  has changed …": the message every resource will share. Its code, `precondition_failed`, is the
  same.
