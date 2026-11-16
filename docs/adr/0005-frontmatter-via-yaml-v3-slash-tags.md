# Frontmatter is edited only through yaml.v3, and tags are namespace/value

Every zk-chef write to frontmatter (`review`, `ingest`, `lint --fix`) parses it into a `yaml.v3` node tree, changes the tree, and encodes it again with a 2-space indent. That output is the canonical form of the frontmatter. `lint` reports `W_FRONTMATTER_FORMAT` for a page that differs from it, and `lint --fix` rewrites the page. The earlier writers edited lines with regular expressions and missed valid layouts: `review` signed a verdict and left the tag unchanged when the tags were a column-0 block list. Tags are written `namespace/value` (`kind/note`, `review/reviewed`, `topic/ml/rnn`), because `yaml.v3` puts quotes around every item that contains `:` in a flow list, so `[kind:note]` comes back as `['kind:note']`. `/` needs no quotes. zk 0.15.7 matches slash tags with `--tag`, and Obsidian uses `/` for nested tags.

## Considered Options

- **Keep regular-expression line edits.** Rejected: each YAML layout needs its own rule, and a missed layout fails without an error.
- **Use yaml.v3 to find the item, then replace only its bytes.** Rejected: it keeps every byte but needs character-to-byte column conversion and the source length of quoted items. Re-encoding is less code and gives one canonical form.
- **Keep `:` tags and accept the quotes.** Rejected: the first write quotes every tag of the page, and the quoted form is noisier for writers and agents.

## Consequences

- The canonical form keeps comments, key order, quote styles and the flow or block style of each list. The encoder removes extra spaces, gives column-0 block lists a 2-space indent and joins a flow list that is split over lines.
- A writer skips a page whose frontmatter is not valid YAML; lint reports it as `E_FRONTMATTER`.
- `W_FRONTMATTER_FORMAT` is a warning, so the pre-commit hook does not refuse the commit. It ignores how a double-quoted scalar is spelled (`\u0026` or `&`, `\U0001F680` or the emoji), because zk's `{{json title}}` and yaml.v3 spell some characters differently.
- A tag in a known namespace (`kind`, `review`, `status`, `topic`, `type`) with a bad value is `E_FRONTMATTER` only. `W_UNKNOWN_TAG` is for an unknown namespace or a tag without `/`. Old `:` tags get no special case.
- Export keeps `topic/` and `type/` tags as written.
