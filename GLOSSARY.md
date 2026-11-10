# zk-chef

A tool kit that keeps an agent-maintained Zettelkasten vault valid, and that can turn the vault into plain Markdown for a static site generator.

## Language

**Vault**:
A separate git repo of Markdown notes, hubs, sources and project logs that `zk-chef init` sets up. The vault is the source of truth.
_Avoid_: Wiki, notebook

**Export**:
A derived copy of the vault, written to another directory as standard Markdown. By default any static site generator can read it. With a **Generator** named, it is tailored to that generator. The vault itself is never changed.
_Avoid_: Build, publish, render

**Generator**:
The static site generator (for example Hugo) that an Export is tailored to. With no Generator named, the Export is generic.
_Avoid_: SSG, target, theme

**Backlinks**:
The section that Export appends to a page. It lists the pages that link to that page.
_Avoid_: Inbound links, citers

**Members**:
The Backlinks section of a hub, shown under the heading "Members". It has no separate source: a hub's members are the pages that link to it.
_Avoid_: Children, forward links
