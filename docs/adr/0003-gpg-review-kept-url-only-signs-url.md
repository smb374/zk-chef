# GPG review stays, and a source with no raw file signs its URL

The zk-only design that v0.3.0 borrows from has no signatures: the human edits the `review:` tag by hand. zk-chef keeps `zk-chef review` and its GPG-signed blocks, because a signed verdict that lint checks against the tag is what stops an agent from marking a source reviewed. Since v0.3.0 a source may have no raw file, the signed message (`vault-review v2`) covers the raw path and sha256 when the raw file exists, and else the `url` and `accessed` date.

## Consequences

- A review of a URL-only source vouches for what the human read at that URL on that date, not for bytes that the vault holds. The page can change later without a lint error.
- `keys/`, `key`, pinned fingerprints and the gpg dependency stay.
