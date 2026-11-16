---
name: zk-chef-ops
description: >
  Vault bookkeeping with zk, git and zk-chef. Use before ingesting a source, updating a raw file, superseding a note or source, running lint, reading a lint code, exporting, or committing in a vault (a repo with .zk-chef/config.toml).
---

# zk-chef-ops

This skill owns the commands, the history and the checks. `zettel-writing` owns what you write inside a note.

Run every command from the vault root, or give `zk-chef` the vault with `--root DIR` (short form `-r DIR`) or the `ZK_CHEF_ROOT` env var. `--root` applies to `zk-chef` only; run `zk` and `git` from the vault root. `zk-chef <verb> --help` shows the flags of a verb. Every page is a file in the flat folder `notes/`, except the root page `index.md`. A page's ID is its file stem: 6 random lowercase letters and digits (`index` for the root page). Links are `[[stem|Title]]`. Kind and state are tags in the frontmatter: `kind/note|hub|source|log`, `review/unreviewed|reviewed|disputed` (sources only), `status/superseded`, `topic/<kebab>`, `type/<kebab>` (logs only).

## Commands

- **Create:** `zk note --title "<title>"`, `zk hub --title "<title>"` or `zk log --title "<title>"`. Each prints the path of the new file in `notes/`. The template fills the frontmatter and the `Hubs:` line (empty for a note and a log, the Index for a hub): fill it before you commit. A source note comes from `zk-chef ingest`, not from `zk source`.
  - Run these commands one call at a time. Parallel calls lock the database, and a failed call leaves a stray file: delete it.
  - **Clash rule.** A stem is random. When a new stem equals an existing one, zk prints the path of the existing page and writes nothing new. Before each create call, run `zk list --quiet --format "{{path}}" notes`. When the printed path (absolute) ends with one of the paths in that list, the stem clashed: do not write in that page, and run the command again.
- **Query:** run `zk index --quiet` first, then `zk list --quiet --format "{{path}}  {{title}}"` with one of:
  - `--match "<query>"`: full-text search of the pages, including `## Extract`.
  - `--tag kind/source`, `--tag review/unreviewed`, `--tag status/superseded`, `--tag topic/ml/rnn`: pages with that tag. Repeat `--tag` to narrow.
  - `--link-to notes/STEM.md` (the Index: `index.md`): pages that link the target, such as its citers.
  - `--linked-by notes/STEM.md`: pages that the target links.
  - `--orphan`: pages nothing links.
  - `--tag kind/hub` with `--format "[[{{filename-stem}}|{{title}}]]"`: ready-made links to every hub.
- **Members of a hub:** `zk-chef lint --children HUB-STEM` lists the pages whose Hubs line names the hub (not pages that only mention it). `--json` prints a list. An unknown stem, or a stem that is not a hub, exits with code 2.
- **Check:** `zk-chef lint`. Findings read `path:line: CODE message`. Errors block the commit; warnings never do. Read `LINT.md` in this folder for any code you do not know.
- **Refresh aliases:** `zk-chef lint --fix` first rewrites stale link aliases to the target's current title, then lints. It changes the Hubs line and `## References` of a note, and every scanned line of a hub, source, log or the Index. It leaves an alias in note prose as it is.

## Ingest a source

Cite a web URL directly in `## References` (`zettel-writing`, "Citations"). Ingest a URL only after the human approves it. Ingest a local file when the human hands it over. Ingest the primary source, not a summary of it. An LLM-written or agent-written brief is not evidence: cite or ingest the primary URLs it cites. Such a brief may be a source only when the human asks for it.

**Propose.** After a task that cites URLs, list the load-bearing ones, numbered, each with a reason in one line. A URL is load-bearing when a claim fails without it. Skip a URL that only gives context. Done when each listed URL has a number and a reason. Ingest nothing until the human answers with numbers.

1. Pick the hubs the source joins (`zk list --quiet --tag kind/hub --format "[[{{filename-stem}}|{{title}}]]"`). When no hub fits, add one first ("Add a hub"). `ingest` needs at least one hub and the vault has no default Sources hub.
2. For each approved source, run `zk-chef ingest -t "<short title of the work>" --hub HUB-STEM PATH|URL`. Repeat `--hub` for each hub. A stem that is not a hub exits with code 2 and writes nothing.
   - **PATH:** copies the file to `raw/<stem>.<ext>` and sets `raw:` and `sha256:`.
   - **URL:** also sets `url:` and `accessed:` (today, UTC), and downloads the bytes to `raw/<stem>.<ext>` (HTML stays HTML). The extension comes from the last part of the URL path. When that has none, it comes from the Content-Type (`text/html` gives `.html`). When that has none too, the file has no extension.
   - **URL with `--no-raw`:** sets `url:` and `accessed:` and downloads nothing. Use it when the human wants a source cited without a copy of its bytes. `--no-raw` with a PATH exits with code 2. A review of such a source vouches for what the URL showed on `accessed`, not for bytes in the vault.
   - It sets the tags `kind/source` and `review/unreviewed`, writes the hubs on the Hubs line and prints the note path (`--json` adds `raw`, `sha256`, `url` and `accessed`). When zk returns an existing page, it tries again, up to 3 times in all, then exits with code 1 and leaves that page as it was.
   - It does not write the body, `authors`, `published`, `## Extract` or `## Review`.
3. Write the body as `zettel-writing` describes under "Source notes". Add the empty `## Review` heading.
4. In each note that cites the URL directly, keep its References entry and append `, Archive: [[stem|Title]]`. Find the notes by searching the vault for the URL as literal text. Done when every direct entry for the URL ends with the Archive link.
5. Run lint, then commit with `ingest: <title>`. Done when lint reports 0 errors.

An unreviewed source is citable at once.

## Update a source

A source with a raw file:

1. Overwrite `raw/<stem>.<ext>` (keep the name).
2. Set `sha256:` to the new hash and the tag `review/unreviewed`. Leave `## Review` as it is; older blocks stay valid.
3. List the citers with `zk list --quiet --link-to notes/SRC-STEM.md`. Open each one, check every claim and locator that cites the source against the new file, and edit what changed.
4. Commit the file, the note and the citer edits together with `edit: <what>`. Done when every citer is checked.
5. Tell the human the source needs a new review.

A URL-only source: read the URL again, set `accessed:` to today and the tag `review/unreviewed`, and continue with step 3.

**A different file name** (or a different URL). Ingest the file as a new source, then supersede the old source note.

## Supersede a note or source

Supersede when the claim itself changed. Wording, link and citation fixes are edits in place. A hub or a log cannot be superseded.

1. Write the replacement NEW (`zettel-writing`, "Write a note"; for a source, "Ingest a source").
2. On OLD, add the tag `status/superseded` to `tags`, add `superseded_by: NEW-STEM` to the frontmatter, and write the line `Superseded by [[NEW-STEM|NEW title]]` after the Hubs line, with a blank line between. Leave OLD's content as it is.
3. For each live note in `zk list --quiet --link-to notes/OLD-STEM.md`, point its link at NEW. Done when lint shows no `W_SUPERSEDED_LINK`.
4. Run lint and commit with `supersede: <what>`.

## Add a hub

1. Create it with `zk hub --title "<title>"`. The template sets its Hubs line to the Index. For a hub under another hub, replace that link with the parent's.
2. Link the new hub in the parent's body (for a top-level hub, the Index in `index.md`). Done when `zk list --quiet --link-to notes/PARENT-STEM.md` lists the new hub (for the Index: `--link-to index.md`), and lint shows no `W_HUB_UNLISTED`.
3. Commit with `hub: <title>`, apart from any ingest.

## Retitle

A retitle keeps the claim and changes the wording. A title that would assert something different is a supersede.

1. Edit `title:`.
2. Run `zk-chef lint --fix`. It refreshes link aliases on Hubs lines and in References, in every note.
3. List the linking notes with `zk list --quiet --link-to notes/STEM.md`. Reread each link to the retitled note inside its sentences: `--fix` leaves an alias in note prose as the writer's wording. Edit a sentence that no longer reads right. Done when every linking sentence fits the new title.
4. Commit with `edit: retitle <STEM>`.

## Export

Run `zk-chef export [-g hugo [-s plain|tufte]] DIR` when the human wants a copy of the vault for a static site generator. The vault does not change.

- Exit 1: lint found errors. Nothing is written. Fix the errors (`LINT.md`) and run it again.
- Exit 2: a usage problem, for example a missing `DIR`, an unknown generator, an unknown style, `-s` without `-g hugo`, or a `DIR` inside the vault. `DIR` must be outside the vault.
- `-g hugo` makes `DIR` a Hugo site root: the pages go to `DIR/content/`, with a scaffold (`hugo.toml` and layouts) beside them.
- `-s` (`--style`) picks the look of the Hugo site: `plain` or `tufte`. Without `-s`, the export uses the style in `DIR/.zk-chef-site.json`; for a site with no such file it uses `tufte` when `DIR/static/vendored/tufte/` exists, else `plain`.
- The export updates the scaffold by files: it creates a missing file, replaces a file that equals a version zk-chef shipped, and keeps any other file as your edit (text mode prints `kept your <path>` and a diff; `--json` lists `scaffolded`, `updated` and `kept`). Files under `static/vendored/` belong to zk-chef and are overwritten when they differ. `hugo.toml` is not created when another Hugo config exists. To restore a file, delete it and export again.

## Commit

- Commit once per coherent task: a new note with its link-backs, an ingest, a raw update with its citer fixes.
- Keep a `hub:` commit (new hub, its Index or parent-body edit) apart from the `ingest:` commit.
- Run `git add` with the task's paths. Subject: `<verb>: <what>`, with the verb one of `ingest`, `note`, `edit`, `supersede`, `hub`, `log`, `review`.
- The pre-commit hook runs `zk-chef lint --staged`. When it blocks, remove the cause (`LINT.md`) and commit again.

## Review belongs to the human

`zk-chef review SRC-STEM reviewed|disputed` signs the verdict. When a task needs a review, name that command in your reply and stop there. Do not hand-edit `## Review`, and do not set `review/reviewed` or `review/disputed`. You set `review/unreviewed` only when the source changed.

In your reply, also list open items the vault shows: `zk-chef doctor` failures (for example `pinned-key`: the human runs `zk-chef key add`), unreviewed sources that notes rest on (`zk list --quiet --tag review/unreviewed`), and untracked tool folders (`.obsidian/`, `.zk/notebook.db`) that belong in `.gitignore`.
