## Vault

This repo is an LLM-maintained Zettelkasten vault (`.vault/config.toml`).

- Load `vault-ops` before ingesting, updating a raw file, superseding, running lint or fix, or committing.
- Load `vault-writing` before creating or editing a note, hub, source note or log.
- One writer works at a time. Commit once per task, with subject `<verb>: <what>`.
- The pre-commit hook runs on every commit. `--no-verify` belongs to the human.
- The human alone edits `review:` and `## Review`, through `vault.py review`.
- Report a source's `review:` value whenever a note or an answer rests on that source.
