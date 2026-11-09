# Build notes: results

Checked 2026-11-08 with zk 0.15.7, Python 3.14, PyYAML 6.0.3, git, gpg.

1. **ID in frontmatter.** Pass. `id: {{filename-stem}}` equals the filename.
2. **Obsidian resolution.** Unverified. Needs a manual check by the human.
3. **zk link queries.** Pass. `zk list --link-to ID` and `--linked-by` count links on the Hubs line, and accept a bare ID. Run `zk index` first after files change outside zk.
4. **Hook speed.** Pass, no cache needed. `lint` on 200 sources, each with one signed block, took 0.33 s (about 1.6 ms per block; Ed25519 key, one temporary `GNUPGHOME`). Without blocks it took 0.05 s. At 2000 reviewed sources expect about 3 s. Add a cache keyed by block hash if this grows.
5. **Ingest helper.** Decided: v2 adds an `ingest` command (v1 had none; the skill kept the manual steps).

## Findings that change the design page

- zk's `format-date` uses local time and has no timezone option. The ID needs UTC, so the filename template calls `{{sh 'date -u +%Y%m%d%H%M'}}`.
- Frontmatter titles use `{{json title}}`. A JSON string is valid YAML, so quotes in titles are safe.
- `zk new` takes the target dir relative to the working dir. Use `-W VAULT --notebook-dir VAULT` from scripts.
- Seed hubs have fixed IDs; `.zk-chef/config.toml` names them (`index_id`, `projects_id`, `sources_id`).

## Release checks

- **go install.** Partly checked. `TestGoInstallCarriesEmbeddedFiles` builds the module zip with `golang.org/x/mod/zip` and asserts that `scaffold/` (config, hook, zk config) and `skills/` are in it. This stands in for `go install github.com/smb374/zk-chef@latest`. Unchecked: a real `go install` after the first tag is pushed, because the repo is not pushed yet.
- **Release assets.** `TestBuildScript` builds the four binaries and `checksums.txt` and checks the host binary prints the ldflags version. `TestInstallScript` runs `install.sh` against a local HTTP server. Unchecked: `release.yml` on GitHub, and `install.sh` against a real release in a clean container.
