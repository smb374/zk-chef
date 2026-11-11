# zk-chef

A tool kit that keeps an agent-maintained Zettelkasten vault valid, and that can turn the vault into plain Markdown for a static site generator.

## Language

**Vault**:
A separate git repo of Markdown notes, hubs, sources and project logs that `zk-chef init` sets up. The vault is the source of truth.
_Avoid_: Wiki, notebook

**Export**:
A derived copy of the vault, written to another directory as standard Markdown by `zk-chef export DIR`. The generic Export can be read by any static site generator. With a **Generator** selected by `--generator`, the Export is tailored to that generator (today: `hugo`). Either way, an Export keeps a manifest of the pages it wrote (never the Export scaffold), and the vault itself is never changed.
_Avoid_: Build, publish, render

**Generator**:
The static site generator that an Export is tailored to, selected with `--generator`. Only `hugo` is supported. With `hugo`, `DIR` is the site root: the pages go to `DIR/content/`, and the Export also writes an **Export scaffold** around them. With no Generator named, the Export is generic and writes pages only.
_Avoid_: SSG, target, theme

**Export scaffold**:
The site files that an Export with a Generator writes beside the pages (for `hugo`: `hugo.toml`, layouts and vendored static files). It is not listed in the manifest. Not the vault's own `scaffold/`, which `zk-chef init` writes.
_Avoid_: Theme, template (the `zk new` templates are a different thing)

**Hubs line**:
The first body line of every page except the root page: `Hubs: ` followed by links to the hubs the page belongs to. It decides Members.
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
The section of a hub that lists the pages whose `Hubs:` line names it. It is separate from Backlinks: a parent or a prose mention that links to the hub is a backlink, not a member.
_Avoid_: Children, forward links
