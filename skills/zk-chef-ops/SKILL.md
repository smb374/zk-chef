---
name: zk-chef-ops
description: Vault bookkeeping with zk, git and zk-chef. Use before ingesting a raw file, updating a raw file, superseding a note or source, running lint or fix, reading a lint code, or committing in a vault (a repo with .zk-chef/config.toml).
---

# zk-chef-ops

This skill owns the commands, the history and the checks. `zettel-writing` owns what you write inside a note.

Run every command from the vault root. A note's ID is its filename stem (`YYYYmmddHHMM` in UTC + 4 random characters); the seed hubs keep their fixed IDs.

## Commands

- **Create:** `zk new <dir> --title "<title>" --print-path --no-input`, with `<dir>` one of `notes`, `hubs`, `sources`, `projects`. It prints the new file's path. The folder's template fills the frontmatter and the Hubs line.
- **Query:** run `zk index --quiet` first, then `zk list --quiet --format "{{path}}  {{title}}"` with one of:
  - `--match "<query>"`: full-text search, including `## Extract`.
  - `--link-to ID`: notes that link ID (its citers, or a hub's members).
  - `--linked-by ID`: notes that ID links.
  - `--orphan`: notes nothing links.
  - a folder, e.g. `hubs`, with `--format "[[{{metadata.id}}|{{title}}]]"`: ready-made links.
- **Check:** `zk-chef lint`. Findings read `path:line: CODE message`. Errors block the commit; warnings never do. Read `LINT.md` in this folder for any code you do not know.
- **Refresh aliases:** `zk-chef fix` rewrites every stale link alias to the target's current title.

## Ingest a raw file

1. Create the source note with `zk new sources --title "<short title of the work>" --print-path --no-input`. Its ID names the raw file.
2. Copy the file to `raw/<ID>-<original name>`.
3. Set the frontmatter: `raw:` to that path, `sha256:` to the output of `sha256sum`, `review: unreviewed`. Add `authors`, `published`, `url` or `citation` (IEEE style) when you know them.
4. Write the body as `zettel-writing` describes under "Source notes".
5. Run lint, then commit with `ingest: <title>`. Done when lint reports 0 errors.

An unreviewed source is citable at once.

## Update a raw file

**Same file name.**

1. Overwrite `raw/<ID>-<name>`.
2. Set `sha256:` to the new hash and `review: unreviewed`. Leave `## Review` as it is; older blocks stay valid.
3. List the citers with `zk list --link-to SRC-ID`. Open each one, check every claim and locator that cites the source against the new file, and edit what changed.
4. Commit the file, the note and the citer edits together with `edit: <what>`. Done when every citer is checked.
5. Tell the human the source needs a new review.

**Different file name.** Ingest the file as a new source, then supersede the old source note.

## Supersede a note or source

Supersede when the claim itself changed. Wording, link and citation fixes are edits in place.

1. Write the replacement NEW (`zettel-writing`, "Write a note"; for a source, "Ingest a raw file").
2. On OLD, add `status: superseded` and `superseded_by: NEW-ID` to the frontmatter. Leave OLD's content as it is.
3. For each live note in `zk list --link-to OLD`, point its link at NEW. Done when lint shows no `W_SUPERSEDED_LINK`.
4. Run lint and commit with `supersede: <what>`.

## Retitle

A retitle keeps the claim and changes the wording. Edit `title:`, run `zk-chef fix`, and commit with `edit: retitle <ID>`. A title that would assert something different is a supersede.

## Commit

- Commit once per coherent task: a new note with its link-backs, an ingest, a raw update with its citer fixes.
- Run `git add` with the task's paths. Subject: `<verb>: <what>`, with the verb one of `ingest`, `note`, `edit`, `supersede`, `hub`, `log`, `fix`, `review`.
- The pre-commit hook runs `zk-chef lint --staged`. When it blocks, remove the cause (`LINT.md`) and commit again.

## Review belongs to the human

`zk-chef review SRC-ID reviewed|disputed` signs the verdict. When a task needs a review, name that command in your reply and stop there.
