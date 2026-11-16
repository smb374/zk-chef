# zk-chef

A tool kit that keeps an agent-maintained Zettelkasten vault valid, and that can turn the vault into plain Markdown for a static site generator.

## Language

**Vault**:
A separate git repo that `zk-chef init` sets up. Its pages are Markdown files in one flat folder, `notes/`, plus the root page `index.md`. The vault is the source of truth.
_Avoid_: Wiki, notebook

**Page**:
A Markdown file of the vault: the root page, or a file in `notes/`. Its **Kind** says what it is. Its ID is its file name without `.md`: 6 lowercase letters and digits, or `index` for the root page.
_Avoid_: Document, file

**Kind**:
What a Page is: `note` (one claim), `hub`, `source` or `log`. A Tag on the Page, `kind/note` and so on. Each Page has exactly one.
_Avoid_: Class, variety

**Tag**:
A string in the `tags` list of a Page, written `namespace/value`: `kind/note`, `review/reviewed`, `status/superseded`, `topic/ml/rnn`, `type/decision`. The Kind, the review state and the supersede state are Tags. In `topic/ml/rnn` the namespace is `topic` and the value is `ml/rnn`.
_Avoid_: Label, category

**Source**:
A Page of Kind `source` that records a work that notes cite. It has evidence: a URL with the date it was accessed, a raw copy kept in `raw/`, or both. Its review state is a Tag, `review/unreviewed`, `review/reviewed` or `review/disputed`.
_Avoid_: Reference, citation

**Log**:
A Page of Kind `log` that holds project material. Its body has an `## Updates` section made of Log entries.
_Avoid_: Journal, diary

**Log entry**:
A `### YYYY-MM-DD` heading under `## Updates` in a Log, with the text below it. The newest entry comes first.
_Avoid_: Update, post

**Export**:
A derived copy of the vault, written to another directory as standard Markdown by `zk-chef export DIR`. The generic Export can be read by any static site generator. With a **Generator** selected by `--generator`, the Export is tailored to that generator (today: `hugo`). Either way, an Export keeps a manifest of the pages it wrote (never the Export scaffold), and the vault itself is never changed.
_Avoid_: Build, publish, render

**Generator**:
The static site generator that an Export is tailored to, selected with `--generator`. Only `hugo` is supported. With `hugo`, `DIR` is the site root: the pages go to `DIR/content/`, and the Export also writes an **Export scaffold** around them. With no Generator named, the Export is generic and writes pages only.
_Avoid_: SSG, target, theme

**Export scaffold**:
The site files that an Export with a Generator writes beside the pages (for `hugo`: `hugo.toml`, layouts and vendored static files). It is not listed in the manifest. Not the vault's own `scaffold/`, which `zk-chef init` writes.
_Avoid_: Theme, template (the `zk new` templates are a different thing)

**Style**:
A named look of the Export scaffold: `plain` or `tufte`, selected with `--style`. A site keeps its Style between Exports until another one is selected. A Hugo theme (a `themes/` folder or module) is a different thing.
_Avoid_: Theme, skin

**Hubs line**:
The first body line of every page except the root page: `Hubs: ` followed by links to the hubs the page belongs to, joined with `, `. It decides Members.
_Avoid_: Parents line, breadcrumb

**Alias**:
The text after `|` in a link, `[[ID|Alias]]`. It is stale when it differs from the target's current title.
_Avoid_: Label, link text, display text

**Prose**:
The body of a note between its Hubs line and its `## References` section (or the end of the page when there is none). The writer owns the Aliases in it.
_Avoid_: Body text, content

**Backlinks**:
The section that Export appends to a page. It lists the pages that link to that page.
_Avoid_: Inbound links, citers

**Members**:
The section of a hub that lists the pages whose `Hubs:` line names it. It is separate from Backlinks: a parent or a prose mention that links to the hub is a backlink, not a member. `zk-chef lint --children HUB` lists them.
_Avoid_: Forward links
