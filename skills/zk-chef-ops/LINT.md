# Lint codes

`zk-chef lint` prints `path:line: CODE message`.

Flags: `--staged` lints the git index. `--fix` first rewrites stale aliases (see `W_STALE_ALIAS`), then lints. `--children HUB` prints the pages whose Hubs line names `HUB` and does not lint (`--json`: a list; an unknown or non-hub `HUB` exits with code 2).

## Errors (block the commit)

| Code | Cause | Fix |
| --- | --- | --- |
| `E_BROKEN_LINK` | A `[[ID\|Title]]` names an ID that does not exist | Correct the ID or remove the link |
| `E_FRONTMATTER` | A required field or tag is missing or invalid. `title` is a quoted string, `date` is UTC (`2026-01-02T03:04:05Z`), `tags` is a list. A page has exactly one `kind:` tag (`note`, `hub`, `source` or `log`). The file stem is 6 lowercase letters and digits (`index` for the root `index.md` only), and `notes/` has no subfolder. A source needs evidence: `url` with `accessed` (`YYYY-MM-DD`), or `raw` with `sha256`, or both. `raw` is `raw/<stem>` or `raw/<stem>.<ext>`. `authors` and `published` (`YYYY` or `YYYY-MM-DD`) are optional, and there is no `citation:` field. A source also needs exactly one `review:` tag; only a source has a `review:` tag. A page with the `status:superseded` tag needs `superseded_by` with an existing stem, and the body line `Superseded by [[stem|Title]]` right after the Hubs line, naming that stem. A hub or log cannot be superseded | Set the field, tag or line the message names |
| `E_REVIEW_HEADING` | A source note has no `## Review` heading | Add an empty `## Review` heading at the end |
| `E_DUPLICATE_ID` | Two files share a stem (for example `notes/ab12cd.md` and `notes/sub/ab12cd.md`) | Rename one file, or remove it |
| `E_HUBS_LINE` | The first body line is not `Hubs: [[ID\|Hub]], [[ID\|Hub]]` (links joined with `, `; the v0.2 ` · ` is wrong), or it links a note that is not another hub | Write the line with real hubs |
| `E_UNREACHABLE` | The note does not reach the Index hub through Hubs lines | Link a hub that sits under Index |
| `E_CITATION` | An `[n]` without entry n, an entry never cited, numbering with gaps or out of first-use order, or `## References` not the last H2 | Renumber and fix the entries (`zettel-writing`, "Citations") |
| `E_RAW_ORPHAN` | A file in `raw/` that no source note names in `raw:` | Ingest it, or remove it |
| `E_SHA256` | The source names a raw file that is missing, or its hash differs from `sha256:` | Update `sha256:` after a deliberate raw update; restore the file otherwise |
| `E_SIGNATURE` | A signed block does not verify, its signer is not pinned, or its text is not a `vault-review v2` message (a `vault-review v1` block is no longer valid) | Leave the block as it is and tell the human |
| `E_REVIEW_MISMATCH` | The `review:` tag is `reviewed` or `disputed` but the latest block differs from the note. A `vault-review v2` message has `verdict` and `date`, and `path` and `sha256` for a source with `raw`, or `url` and `accessed` for a source with no `raw`. A source with both signs the raw form. The block's verdict, path and sha256 (or url and accessed) must equal the note's, and the sha256 must equal the raw file | After a raw update, or a change of `url` or `accessed`, set the tag `review:unreviewed`; otherwise tell the human |

## Warnings (never block)

| Code | Cause | Fix |
| --- | --- | --- |
| `W_STALE_ALIAS` | A link's alias differs from the target's title, on the Hubs line or in `## References` of a note, or on any line of a hub, source, log or the Index. An alias in note prose is not checked | `zk-chef lint --fix` |
| `W_UNKNOWN_KEY` | A frontmatter key that the page's kind does not list. Every kind lists `title`, `date`, `tags`, `aliases`; note and source add `superseded_by`; source adds `url`, `accessed`, `authors`, `published`, `raw`, `sha256` | Fix the key name, or remove it |
| `W_UNKNOWN_TAG` | A tag outside `kind:`, `review:` (sources), `status:superseded`, `topic:` (kebab segments joined by `/`, such as `topic:ml/rnn`) and `type:` (one kebab segment, logs only) | Fix or remove the tag |
| `W_HUB_UNLISTED` | A hub names a parent hub (the Index counts) whose body, past its Hubs line, has no link to it | Link the hub from the parent's body |
| `W_LOG_HEADING` | A log has no `## Updates`, an `###` heading under it is not `YYYY-MM-DD`, or the dates are not newest first | Fix the headings and their order |
| `W_SUPERSEDED_LINK` | A live note links a superseded note | Point the link at `superseded_by` |
| `W_MARKER` | `TODO:` or `CONTRADICTION:` is present | Resolve it, or keep it as a deliberate marker |
| `W_WORD_COUNT` | A claim note is longer than `word_count_warn` | Split it by claim |

## Not checked

Lint cannot read meaning. These rules depend on you: `Inference:` markers on claims that go beyond the source, and the "as of" date for fast-changing subjects.
