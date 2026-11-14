# Kind and state are tags, and the file stem is the ID

From v0.3.0 a note's kind (`kind:note`), review state (`review:…`) and supersede state (`status:superseded`) are tags, and its ID is only its 6-character lowercase file stem in one flat `notes/` folder. zk can filter by tags (`zk list --tag "kind:source, review:unreviewed"`) but not by custom frontmatter keys, so tags make these states queryable with zk alone. Folders by kind and an `id:` field only repeated what the stem and the tag already say. The stem is lowercase because zk resolves links without case and macOS and Windows file systems fold case.

## Considered Options

- **Keep v0.2 folders and fields.** Rejected: zk cannot query a field, and `E_WRONG_FOLDER` checked a fact that the kind already holds.
- **Date-plus-random IDs (16 characters).** Rejected: `date:` now holds the time, so the date part of the ID adds length and no information.

## Consequences

- v0.2 vaults are not readable by v0.3. There is no migration; the gate refuses them.
- Fields still carry data (`superseded_by`, `url`, `raw`, `sha256`); only states and the kind are tags.
