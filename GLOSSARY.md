# zk-chef

A tool kit that keeps an agent-maintained Zettelkasten vault valid, and that can turn the vault into plain Markdown for a static site generator.

## Language

**Vault**:
A separate git repo of Markdown notes, hubs, sources and project logs that `zk-chef init` sets up. The vault is the source of truth.
_Avoid_: Wiki, notebook

**Export**:
A derived copy of the vault, written to another directory as standard Markdown by `zk-chef export DIR`. The generic Export can be read by any static site generator. With a **Generator** selected by `--generator`, the Export is tailored to that generator (today: `hugo`). Either way, an Export keeps a manifest of the pages it wrote (never the Hugo scaffold files), and the vault itself is never changed.
_Avoid_: Build, publish, render

**Generator**:
The static site generator that an Export is tailored to, selected with `--generator`. Only `hugo` is supported. With `hugo`, `DIR` is the site root: the pages go to `DIR/content/`, and the Export creates a write-once scaffold (`hugo.toml` and layouts) around them. With no Generator named, the Export is generic and writes pages only.
_Avoid_: SSG, target, theme

**Backlinks**:
The section that Export appends to a page. It lists the pages that link to that page.
_Avoid_: Inbound links, citers

**Members**:
The section of a hub that lists the pages whose `Hubs:` line names it. It is separate from Backlinks: a parent or a prose mention that links to the hub is a backlink, not a member.
_Avoid_: Children, forward links
