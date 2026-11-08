# Build notes: results

Checked 2026-11-08 with zk 0.15.7, Python 3.14, PyYAML 6.0.3, git, gpg.

1. **ID in frontmatter.** Pass. `id: {{filename-stem}}` equals the filename.
2. **Obsidian resolution.** Unverified. Needs a manual check by the human.
3. **zk link queries.** Pass. `zk list --link-to ID` and `--linked-by` count links on the Hubs line, and accept a bare ID. Run `zk index` first after files change outside zk.
4. **Hook speed.** Pass, no cache needed. `lint` on 200 sources, each with one signed block, took 0.33 s (about 1.6 ms per block; Ed25519 key, one temporary `GNUPGHOME`). Without blocks it took 0.05 s. At 2000 reviewed sources expect about 3 s. Add a cache keyed by block hash if this grows.
5. **Ingest helper.** Decided: no `ingest` command. The skill keeps the manual steps.

## Findings that change the design page

- zk's `format-date` uses local time and has no timezone option. The ID needs UTC, so the filename template calls `{{sh 'date -u +%Y%m%d%H%M'}}`.
- Frontmatter titles use `{{json title}}`. A JSON string is valid YAML, so quotes in titles are safe.
- `zk new` takes the target dir relative to the working dir. Use `-W VAULT --notebook-dir VAULT` from scripts.
- Seed hubs have fixed IDs; `.vault/config.toml` names them (`index_id`, `projects_id`, `sources_id`).
