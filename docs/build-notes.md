# Build notes: results

Checked 2026-11-08 with zk 0.15.7, git, gpg. Items 1 to 5 were checked during the v1 (Python) build and carried into the Go binary.

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


## Export check with Hugo

Checked once, by hand, with Hugo v0.169.0-1da10fc44701c566b1669cdb2d12638f155138bd+extended+withdeploy linux/amd64. The notes below describe that manual check, which used the old layout (pages written to `site/content`, hugo config and layouts written by hand).

Layout change: `export --generator hugo DIR` now makes `DIR` the site root. It writes the pages to `DIR/content/`. It also writes a write-once scaffold: `hugo.toml` and four layouts. A Go test covers the build: `TestExportHugoSiteBuilds` in `internal/cli/export_hugo_test.go`. The test runs `hugo` in a fresh export. It skips when `hugo` is not on PATH. CI installs the latest Hugo (`hugo-version: "latest"` in `.github/workflows/ci.yml`) before `go test`. This note does not record a new manual run.

- **Vault.** A small throwaway vault, not a real one. It came from `zk-chef init -y` plus three hand-written pages: a source (with `url`, `citation`, an `## Extract`, a `## Review` and a raw file), a note, and a superseded note. `zk-chef lint` gave 0 errors and 2 warnings (`W_SUPERSEDED_LINK`, `W_STALE_ALIAS`; the second came from a deliberate bracket alias). No real vault was available.
- **Export.** `zk-chef export site/content --generator hugo --root vault` printed `wrote 6 pages to site/content, removed 0`.
- **Hugo config.** `hugo.toml` had `baseURL`, `title`, `refLinksErrorLevel = 'ERROR'` and `[markup.goldmark.renderHooks.link]` with `useEmbedded = 'always'`. The site had minimal layouts of its own: `layouts/single.html`, `list.html` and `home.html` (Hugo 0.169 uses these flat names). Hugo needs a theme or layouts; the export does not write them.
- **Build.** Pass: 18 pages, no errors. With `refLinksErrorLevel = 'ERROR'`, Hugo reported no broken link.
- **Link resolution.** Every relative `.md` link became a site URL, for example `../sources/<id>.md` became `/sources/<id>/`. Links to the root page resolved to `/`.
- **Backlinks.** The source page listed the notes that cite it. The superseded note listed its replacement under Backlinks.
- **Members.** The Sources hub listed the source note. The home page (`_index.md`) listed every page whose `Hubs:` line names `index`. The link from the index to the Sources hub showed under the Sources hub's Backlinks, not under Members.
- **Home page.** `_index.md` became the home page (`public/index.html`) with its Members. Hugo gave the home page the date of the newest page; the export writes no date for it.
- **`source_url` and `date`.** `source_url` was available as `.Params.source_url` and was rendered. `url` was not set, so page URLs did not change. `date` from the ID rendered, for example `2026-11-09T10:44Z`.
- **Shortcodes.** `{{< figure src="x" >}}` in source text showed as literal text, not as a figure.
- **Extract.** The Extract text was absent from the source page. The Review heading stayed.
- **Observation (not an export bug).** In `[[ID|Raft [paper]]]` the link alias ends at the first `]]`. Lint warns about it, and the exported text matches that parse.

## Export scaffold hashes

`zk-chef export -g hugo` replaces a scaffold file only when its bytes equal a version zk-chef shipped (ADR 0001). The list is in `internal/cli/scaffold_hashes.go`. Before you tag a release that changes a scaffold file, append the hashes of the files as they are in the release:

1. For each changed layout, run `sha256sum layouts/<file>` in `export-scaffold/hugo`. Append the hash to that file's list in `scaffoldHashes`, with the version in a comment. A file the release adds gets a new entry.
2. If `hugo.toml.tmpl` changed, append its full text to `hugoTomlTemplates`.
3. Do not list `static/vendor/` files. zk-chef overwrites them.

`TestScaffoldHashesCoverEmbeddedFiles` fails when the current version of an embedded file is not in the list.

