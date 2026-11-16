---
name: zettel-writing
description: >
  Write vault content: claim titles, note bodies, link context, link-back, citations, source note bodies, hub maps and project logs. Use before creating or editing a note, hub, source note or log in a vault (a repo with .zk-chef/config.toml).
---

# zettel-writing

`zk-chef-ops` owns the commands, ingest, supersede and commits. This skill owns what you write.

A vault holds **claims**: one per note, titled by the claim, joined by **link context**, grouped by **hubs** whose prose is a **map**. Evidence is cited paper-style.

## Write a note

1. **Search.** Run `zk list --quiet --match "<topic>"`. Done when you know whether a live note already makes your claim; if one does, use "Edit a note".
2. **Title the claim.** Create the file with `zk note --title "<claim>"` (`zk-chef-ops`, "Commands"). The title is one complete, positive sentence: "Raft elects at most one leader per term". Done when the title alone tells a reader what the note asserts. A title that needs "and" joining two predicates or two subjects is two notes; split it before you write the body.
3. **Place it.** List the hubs (`zk list --quiet --tag kind/hub --format "[[{{filename-stem}}|{{title}}]]"`). Fill the first body line as `Hubs: [[stem|Hub]], [[stem|Hub]]`: links joined with `, `, one for every hub whose category the claim fits. Until a cluster justifies a new hub, use the closest existing one. Add a `topic/<value>` tag to `tags` when the claim has a topic (lowercase kebab case, `/` for nesting, such as `topic/ml/rnn`).
4. **Write the body.** State the claim in your own words: what it says, when it holds, what follows. Mark a claim that goes beyond the cited source with "Inference:" at its start, and cite the sources it builds on. For a fast-changing subject (products, tools, versions), state the "as of" date of the evidence in the body. Factor by concept. Material about one source belongs in its source note. A claim true only inside one project belongs under that project's hub.
5. **Add link context.** Put each body link in a sentence that states the relationship: supports, contradicts, refines, is an example of, depends on, follows from. Done when a reader can decide to follow each link without opening it. A link alias in a note sentence is your wording: `zk-chef lint --fix` does not change it, so write it to fit the sentence.
6. **Cite the evidence** (see "Citations").
7. **Link back.** For each link whose relationship is supports, contradicts, refines or is an example of, open the target, add a sentence that links the new note with its link context. Done when every such target names the new note. A passing mention or a References entry needs no link-back; `zk list --quiet --link-to notes/STEM.md` already lists citers.
8. **Close.** Run lint and commit (`zk-chef-ops`, "Commit"). One commit holds the note and its link-backs. Then propose the load-bearing URLs to ingest (`zk-chef-ops`, "Ingest a source"); do not ingest them yourself.

## Edit a note

Edit in place while the claim stays the same: wording, link context, links, evidence, hubs. A reworded title with the same claim is a retitle (`zk-chef-ops`). A title that would assert something different is a new note plus a supersede (`zk-chef-ops`).

## Citations

- **Inline:** `[n]` or `[n, locator]` after the claim it supports: `one leader per term [1, §5.2].` Several: `[1], [3, p. 9]`. A locator is `p. 4`, `§5.2` or `12:30`. For a source with no pages or numbered sections (web docs), use `§` plus the heading text, unquoted: `[1, § Summary]`.
- **References:** one `## References` heading, the last H2, with a numbered list. Each entry is one of:
  - a source or note: `1. [[stem|Ongaro 2014]]`
  - a URL (the default for web sources): `2. A. Author, "Title," Site, Oct. 30, 2026. [Online]. Available: https://example.org/x`. After ingest, the entry ends with `, Archive: [[stem|Title]]`.
  - the human: `3. [user, 2026-11-08] statement`
- **Numbering:** order of first inline use, from 1, without gaps. Renumber on every edit that changes the order. Done when every inline `[n]` has entry n and every entry is cited inline.
- **Review status:** an unreviewed source is citable. When a note or an answer rests on a source, report that source's `review/` tag in the reply. When a claim is load-bearing and its source is unreviewed, say so in the note body, so a reader of the note sees it. A `disputed` source needs a sentence that says why you cite it.
- A note without evidence has no References section.

## Source notes

A source note is a **reference note**, like a wiki `File:` page. `zk-chef ingest` makes the file, its tags (`kind/source`, `review/unreviewed`), its Hubs line and its evidence fields (`url` with `accessed`, `raw` with `sha256`). Title: the work's short title. Body, in order:

1. The Hubs line, as ingest wrote it. Add a hub when the source also belongs to another topic.
2. One line on why the file was ingested.
3. Short paraphrases of what the file says, each with a locator.
4. `## Extract` (optional): the text extracted from the raw file, for search, because zk does not index `raw/`. Demote its headings to H3 or lower (or plain bold) so they do not enter the note outline.
5. `## Review`: the empty heading, which lint requires. The human's signed blocks go under it (leave them alone).

Add the frontmatter fields you know: `authors` (a list) and `published` (`YYYY` or `YYYY-MM-DD`). A source note has no `citation:` field: the work's description is in the title, the paraphrases and the fields above. Cite a source note in `## References` as `[[stem|Title]]`.

Ideas the file prompts go into concept notes that cite the source.

## Hubs

A hub is a **category**, like a wiki `Category:` page. Its title is a noun phrase: "Consensus protocols". It is a file in `notes/` with the tag `kind/hub`, made with `zk hub --title "<title>"`. Its Hubs line names its parent hub, so every hub reaches the Index. The Index is the one exception: it is `index.md` in the vault root, beside `AGENTS.md`, and links as `[[index|Index]]`.

- **Members** join through their own Hubs lines; `zk-chef lint --children HUB-STEM` lists them.
- **Child hubs:** the parent's body links each child hub, in its map. Lint warns (`W_HUB_UNLISTED`) when it does not. A top-level hub is linked from the Index.
- **Projects:** a project is a hub. Make a Projects hub when you need one, and put each project hub under it. The vault has no Sources or Projects hub until you make them.
- **New hubs:** create one when a cluster forms, meaning several notes in one hub link each other on a subtopic.
- **The map** is the hub's prose. It holds no claims; an overview that argues is a note that the map links.
  - New hub: one line stating the category's scope.
  - When members link each other: a short intro, then links to the important members. Do not list every member or order them as a walk. Name tensions. Done when a reader can pick the right entry note from the map alone.
- When a new member is important, update the map in the same commit.

## Logs

A log holds project material: decisions, meetings, plans, status. Create it with `zk log --title "<title>"`; the title is free. The template gives it the tag `kind/log` and a `## Updates` section. The Hubs line names the project hub. Add a `type/<value>` tag when one fits (`type/decision`, `type/meeting`, `type/plan` or `type/status`; lowercase kebab case; only logs have a `type/` tag). Add `topic/<value>` tags as for a note.

Write each update under `## Updates` as a `### YYYY-MM-DD` heading, newest first. Other H2 sections may come before `## Updates`. Edit the log in place as the project moves. A lesson that holds beyond the project becomes a note under its topic hub, and the log links it.

## Scope

The vault keeps what stays true: claims, sources, maps, project logs. A thought that holds no claim yet stays uncommitted, or becomes a `TODO:` line in a live note.
