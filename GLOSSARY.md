# zk-chef

A tool kit that keeps an agent-maintained Zettelkasten vault valid, and that can turn the vault into plain Markdown for a static site generator.

## Language

**Vault**:
A separate git repo of Markdown notes, hubs, sources and project logs that `zk-chef init` sets up. The vault is the source of truth.
_Avoid_: Wiki, notebook

**Export**:
A derived copy of the vault, written to another directory as standard Markdown that any static site generator can read. The vault itself is never changed.
_Avoid_: Build, publish, render

**Backlinks**:
The section that Export appends to a page. It lists the pages that link to that page.
_Avoid_: Inbound links, citers

**Members**:
The Backlinks section of a hub, shown under the heading "Members". It has no separate source: a hub's members are the pages that link to it.
_Avoid_: Children, forward links
