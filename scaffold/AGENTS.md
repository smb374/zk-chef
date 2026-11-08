<!-- zk-chef -->
## Vault

This repo is an LLM-maintained Zettelkasten vault (`.zk-chef/config.toml`).

- Load `zk-chef-ops` before ingesting, updating a raw file, superseding, running `zk-chef lint` or `zk-chef fix`, or committing.
- Load `zettel-writing` before creating or editing a note, hub, source note or log.
- One writer works at a time. Commit once per task, with subject `<verb>: <what>`.
- The pre-commit hook runs on every commit. `--no-verify` belongs to the human.
- The human alone edits `review:` and `## Review`, through `zk-chef review`.
- Report a source's `review:` value whenever a note or an answer rests on a source.
<!-- /zk-chef -->
