---
name: zk-chef-ops
description: >
  Vault bookkeeping with zk, git and zk-chef. Use before ingesting a raw file, updating a raw file, superseding a note or source, running lint or fix, reading a lint code, or committing in a vault (a repo with .zk-chef/config.toml).
---

# zk-chef-ops

This skill owns the commands, the history and the checks. `zettel-writing` owns what you write inside a note.

Run every command from the vault root, or give `zk-chef` the vault with `--root DIR` (or the `ZK_CHEF_ROOT` env var). `--root` applies to `zk-chef` only; run `zk` and `git` from the vault root. When you must run `zk new` from elsewhere, pass `--notebook-dir DIR` and an absolute folder path. `zk-chef <verb> --help` shows the flags of a verb. A note's ID is its filename stem (`YYYYmmddHHMM` in UTC + 4 random characters); the seed hubs keep their fixed IDs.

## Commands

- **Create:** `zk new <dir> --title "<title>" --print-path --no-input`, with `<dir>` one of `notes`, `hubs`, `sources`, `projects`. It prints the new file's path. The folder's template fills the frontmatter and leaves the `Hubs:` line empty: fill it before you commit. Run `zk new` one call at a time. Parallel calls lock the database, and a failed call leaves a stray note file: delete it.
- **Query:** run `zk index --quiet` first, then `zk list --quiet --format "{{path}}  {{title}}"` with one of:
  - `--match "<query>"`: full-text search, including `## Extract`.
  - `--link-to ID`: notes that link ID (its citers, or a hub's members).
  - `--linked-by ID`: notes that ID links.
  - `--orphan`: notes nothing links.
  - a folder, e.g. `hubs`, with `--format "[[{{metadata.id}}|{{title}}]]"`: ready-made links.
- **Check:** `zk-chef lint`. Findings read `path:line: CODE message`. Errors block the commit; warnings never do. Read `LINT.md` in this folder for any code you do not know.
- **Refresh aliases:** `zk-chef fix` rewrites stale link aliases to the target's current title: on the Hubs line and in `## References` of a note, and on every scanned line of a hub, source, log or the Index. It leaves an alias in note prose as it is.

## Ingest a raw file

Cite a web URL directly in `## References` (`zettel-writing`, "Citations"). Ingest a URL only after the human approves it. Ingest a local file when the human hands it over. Ingest the primary source, not a summary of it. An LLM-written or agent-written brief is not evidence: cite or ingest the primary URLs it cites. Such a brief may be a source only when the human asks for it.

**Propose.** After a task that cites URLs, list the load-bearing ones, numbered, each with a reason in one line. A URL is load-bearing when a claim fails without it. Skip a URL that only gives context. Done when each listed URL has a number and a reason. Ingest nothing until the human answers with numbers.

1. For each approved URL, run `zk-chef ingest -t "<short title of the work>" PATH|URL`. Add `--hub ID` for each topic hub the source joins (repeatable). The source joins the Sources hub first, then the hubs you give. It creates the source note, copies the file to `raw/<ID>-<original name>`, and sets `raw:`, `sha256:` and `review: unreviewed`; for a URL it also sets `url:` and downloads the raw bytes (HTML stays HTML). It prints the note path (`--json` adds `raw` and `sha256`). It does not fill `## Extract`, `citation:`, `authors`, `published` or the body: add them by hand, as `zettel-writing` describes under "Source notes" (a URL source needs `citation:` in IEEE form).
2. Write the body as `zettel-writing` describes under "Source notes".
3. In each note that cites the URL directly, keep its References entry and append `, Archive: [[ID|Title]]`. Find the notes by searching the vault for the URL as literal text. Done when every direct entry for the URL ends with the Archive link.
4. Run lint, then commit with `ingest: <title>`. Done when lint reports 0 errors.

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

## Add a hub

1. Create it with `zk new hubs`. Its `Hubs:` line names the parent hub.
2. Add a link to it in the parent hub's map (for a top-level hub, the Index in `index.md`). Done when `zk list --link-to HUB-ID` lists the parent.
3. Commit with `hub: <title>`, apart from any ingest.

## Retitle

A retitle keeps the claim and changes the wording. A title that would assert something different is a supersede.

1. Edit `title:`.
2. Run `zk-chef fix`. It refreshes link aliases on Hubs lines and in References, in every note.
3. List the linking notes with `zk list --link-to ID`. Reread each link to the retitled note inside its sentences: `fix` leaves an alias in note prose as the writer's wording. Edit a sentence that no longer reads right. Done when every linking sentence fits the new title.
4. Commit with `edit: retitle <ID>`.

## Export

Run `zk-chef export [-g hugo] DIR` when the human wants a copy of the vault for a static site generator. The vault does not change.

- Exit 1: lint found errors. Nothing is written. Fix the errors (`LINT.md`) and run it again.
- Exit 2: a usage problem, for example a missing `DIR`, an unknown generator, or a `DIR` inside the vault. `DIR` must be outside the vault.
- `-g hugo` makes `DIR` a Hugo site root: the pages go to `DIR/content/`, with a scaffold (`hugo.toml` and layouts) beside them.
- The scaffold is write-once: an export creates a scaffold file only when it does not exist, and never overwrites one. Edit the scaffold in `DIR` as you like. A later release amends this.

## Commit

- Commit once per coherent task: a new note with its link-backs, an ingest, a raw update with its citer fixes.
- Keep a `hub:` commit (new hub, its Index or parent-map edit) apart from the `ingest:` commit.
- Run `git add` with the task's paths. Subject: `<verb>: <what>`, with the verb one of `ingest`, `note`, `edit`, `supersede`, `hub`, `log`, `fix`, `review`.
- The pre-commit hook runs `zk-chef lint --staged`. When it blocks, remove the cause (`LINT.md`) and commit again.

## Review belongs to the human

`zk-chef review SRC-ID reviewed|disputed` signs the verdict. When a task needs a review, name that command in your reply and stop there. Do not hand-edit `## Review`.

In your reply, also list open items the vault shows: `zk-chef doctor` failures (for example `pinned-key`: the human runs `zk-chef key add`), unreviewed sources that notes rest on, and untracked tool folders (`.obsidian/`, `.zk/notebook.db`) that belong in `.gitignore`.
