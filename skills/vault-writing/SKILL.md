---
name: vault-writing
description: Write vault content: claim titles, note bodies, link context, link-back, citations, source note bodies, hub maps and project logs. Use before creating or editing a note, hub, source note or log in a vault (a repo with .vault/config.toml).
---

# vault-writing

`vault-ops` owns the commands, ingest, supersede and commits. This skill owns what you write.

A vault holds **claims**: one per note, titled by the claim, joined by **link context**, grouped by **hubs** whose prose is a **map**. Evidence is cited paper-style.

## Write a note

1. **Search.** Run `zk list --match "<topic>"`. Done when you know whether a live note already makes your claim; if one does, use "Edit a note".
2. **Title the claim.** Create the file with `zk new notes --title "<claim>" --print-path --no-input`. The title is one complete, positive sentence: "Raft elects at most one leader per term". Done when the title alone tells a reader what the note asserts. A title that needs "and" is two notes.
3. **Place it.** List the hubs (`zk list hubs --format "[[{{metadata.id}}|{{title}}]]"`). Write the first body line as `Hubs: [[ID|Hub]] · [[ID|Hub]]` with every hub whose category the claim fits. Until a cluster justifies a new hub, use the closest existing one.
4. **Write the body.** State the claim in your own words: what it says, when it holds, what follows. Factor by concept. Material about one source belongs in its source note. A claim true only inside one project belongs under that project's hub.
5. **Add link context.** Put each body link in a sentence that states the relationship: supports, contradicts, refines, is an example of, depends on, follows from. Done when a reader can decide to follow each link without opening it.
6. **Cite the evidence** (see "Citations").
7. **Link back.** For each link whose relationship is supports, contradicts, refines or is an example of, open the target, add a sentence that links the new note with its link context. Done when every such target names the new note. A passing mention or a References entry needs no link-back; `zk list --link-to ID` already lists citers.
8. **Close.** Run lint and commit (`vault-ops`, "Commit"). One commit holds the note and its link-backs.

## Edit a note

Edit in place while the claim stays the same: wording, link context, links, evidence, hubs. A reworded title with the same claim is a retitle (`vault-ops`). A title that would assert something different is a new note plus a supersede (`vault-ops`).

## Citations

- **Inline:** `[n]` or `[n, locator]` after the claim it supports: `one leader per term [1, §5.2].` Several: `[1], [3, p. 9]`. A locator is `p. 4`, `§5.2` or `12:30`.
- **References:** one `## References` heading, the last H2, with a numbered list. Each entry is one of:
  - a source or note: `1. [[ID|Ongaro 2014]]`
  - a URL: `2. A. Author, "Title," Site, Oct. 30, 2026. [Online]. Available: https://example.org/x`
  - the human: `3. [user, 2026-11-08] statement`
- **Numbering:** order of first inline use, from 1, without gaps. Renumber on every edit that changes the order. Done when every inline `[n]` has entry n and every entry is cited inline.
- **Review status:** an unreviewed source is citable. When a note or an answer rests on a source, report that source's `review:` value. A `disputed` source needs a sentence that says why you cite it.
- A note without evidence has no References section.

## Source notes

A source note is a **reference note**, like a wiki `File:` page. Title: the work's short title. Body, in order:

1. `Hubs: [[ID|Sources]]` plus topic hubs.
2. One line on why the file was ingested.
3. Short paraphrases of what the file says, each with a locator.
4. `## Extract`: the text extracted from the raw file, for search.
5. `## Review`: the human's signed blocks (leave it alone).

Ideas the file prompts go into concept notes that cite the source.

## Hubs

A hub is a **category**, like a wiki `Category:` page. Its title is a noun phrase: "Consensus protocols". Its file lives in `hubs/` and its Hubs line names its parent hub, so every hub reaches Index.

- **Members** join through their own Hubs lines; `zk list --link-to HUB-ID` lists them.
- **Projects:** each project gets a hub under the Projects hub.
- **New hubs:** create one when a cluster forms, meaning several notes in one hub link each other on a subtopic.
- **The map** is the hub's prose. It holds no claims; an overview that argues is a note that the map links.
  - New hub: one line stating the category's scope.
  - Once members relate to each other or a gap appears: an ordered walk. Entry note first, then each member in reading order with a sentence relating it to the one before. Name tensions. Write each missing note as a `GAP:` line. Done when a reader can pick the right entry note from the map alone.
- When a new member changes the reading order or fills a `GAP:`, update the map in the same commit.

## Logs

A log holds project material: decisions, meetings, plans, status. Create it with `zk new projects --title "<title>" --print-path --no-input`; the title is free. Set `type:` to `decision`, `meeting`, `plan` or `status` when one fits. The Hubs line names the project hub. Edit the log in place as the project moves. A lesson that holds beyond the project becomes a note in `notes/` under its topic hub, and the log links it.

## Scope

The vault keeps what stays true: claims, sources, maps, project logs. A thought that holds no claim yet stays uncommitted, or becomes a `GAP:` or `TODO:` line in a live note.