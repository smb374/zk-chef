# Lint codes

`zk-chef lint` prints `path:line: CODE message`.

## Errors (block the commit)

| Code | Cause | Fix |
| --- | --- | --- |
| `E_BROKEN_LINK` | A `[[ID\|Title]]` names an ID that does not exist | Correct the ID or remove the link |
| `E_FRONTMATTER` | A required field is missing or invalid; `id`, `title`, `kind` need quoted strings; a source needs `raw` (`raw/<ID>-<name>`), `sha256`, `review`, and `citation` when it has `url`; `status` is `superseded` with an existing `superseded_by`; hubs and logs have no `status` | Set the field the message names |
| `E_REVIEW_HEADING` | A source note has no `## Review` heading | Add an empty `## Review` heading at the end |
| `E_WRONG_FOLDER` | `kind` does not match the folder (`notes`=note, `hubs`=hub, `sources`=source, `projects`=log) | Fix `kind` or move the file |
| `E_DUPLICATE_ID` | Two files share an ID, or `id` differs from the filename | Make `id` equal the filename stem |
| `E_HUBS_LINE` | The first body line is not `Hubs: [[ID\|Hub]] · [[ID\|Hub]]`, or it links a note that is not another hub | Write the line with real hubs; `zk list hubs --format "[[{{metadata.id}}\|{{title}}]]"` gives links |
| `E_UNREACHABLE` | The note does not reach the Index hub through Hubs lines | Link a hub that sits under Index |
| `E_CITATION` | An `[n]` without entry n, an entry never cited, numbering with gaps or out of first-use order, or `## References` not the last H2 | Renumber and fix the entries (`zettel-writing`, "Citations") |
| `E_RAW_ORPHAN` | A file in `raw/` that no source note names | Ingest it, or remove it |
| `E_SHA256` | The raw file is missing, or its hash differs from `sha256:` | Update `sha256:` after a deliberate raw update; restore the file otherwise |
| `E_SIGNATURE` | A signed block does not verify, or its signer is not pinned | Leave the block as it is and tell the human |
| `E_REVIEW_MISMATCH` | `review` is `reviewed` or `disputed` but the latest block's path, sha256 or verdict differs from the note or the file | After a raw update set `review: unreviewed`; otherwise tell the human |

## Warnings (never block)

| Code | Cause | Fix |
| --- | --- | --- |
| `W_STALE_ALIAS` | A link's alias differs from the target's title | `zk-chef fix` |
| `W_DEAD_END` | A claim note links nothing besides its Hubs line | Add link context to related notes, or accept it |
| `W_SUPERSEDED_LINK` | A live note links a superseded note | Point the link at `superseded_by` |
| `W_MARKER` | `TODO:`, `GAP:` or `CONTRADICTION:` is present | Resolve it, or keep it as a deliberate marker |
| `W_WORD_COUNT` | A claim note is longer than `word_count_warn` | Split it by claim |
| `W_HUB_MAP` | Members of a hub link each other, but its map has no ordered walk (`1. ...`) and no `GAP:` line | Write the walk, or the `GAP:` lines (`zettel-writing`, "Hubs") |

## Not checked

Lint cannot read meaning. These rules depend on you: `Inference:` markers on claims that go beyond the source, the "as of" date for fast-changing subjects, and the quality of a hub walk.
