# zk-chef

A single Go binary for an agent-maintained Zettelkasten wiki. [zk](https://github.com/zk-org/zk) makes IDs and answers link queries, git keeps history, and `zk-chef lint` checks the invariants. Two skills tell the agent how to do the bookkeeping and how to write. A human GPG signature is the gate on raw sources.

This repo is the kit. A vault is a separate git repo that you set up with `zk-chef init`.

## Requirements

- git and gpg
- zk (`zk-chef install` offers to download it to `~/.local/bin`)
- Obsidian, optional, to browse the vault

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/smb374/zk-chef/main/install.sh | sh
```

Or run `./install.sh` from a clone. The script downloads the binary for your platform (Linux or macOS, amd64 or arm64), checks its SHA-256 against `checksums.txt`, puts it in `~/.local/bin`, and runs `zk-chef install`.

`zk-chef install` writes the skills to `~/.agents/skills/` and offers to download zk (`-y` accepts). It does not overwrite a skill directory that it did not write. `zk-chef self-update` replaces the binary with the latest release and refreshes the skills.

## Set up a vault

```sh
zk-chef init /path/to/vault
```

`init` is safe to run again. It does these things:

- runs `git init` when needed;
- copies the scaffold (zk config and templates, `.zk-chef/config.toml`, pre-commit hook, seed hubs, and the root `index.md` beside `AGENTS.md`) and never overwrites a file;
- adds a `<!-- zk-chef -->` block to `AGENTS.md`, or creates the file;
- creates `notes/ hubs/ sources/ projects/ raw/ keys/`;
- sets `core.hooksPath` to `.githooks`;
- records `min_version` in the vault config. An older binary then refuses to work on the vault.

It prints `kept your …` for files that differ from the scaffold. Run `zk-chef doctor` to check the setup.

## Set up your signing key (once, human)

Source reviews need a pinned key.

```sh
zk-chef key add YOUR_FINGERPRINT
```

Without a fingerprint, it uses your first secret key. It exports the public key to `keys/FPR.asc` and adds the fingerprint to `pinned_fingerprints` in `.zk-chef/config.toml`. Lint accepts a signature only when the key is in `keys/` and its fingerprint is pinned.

## Use

The agent loads the skills from `AGENTS.md`. It creates notes with `zk new`, edits files and commits. The hook runs `zk-chef lint --staged` on every commit.

Every verb accepts `--json` and `--help`. Verbs that work on a vault pick it in this order: `--root DIR`, the `ZK_CHEF_ROOT` env var, the current directory. There is no search up the directory tree. `zk-chef --help` lists the verbs.

| Command | Who | What it does |
| --- | --- | --- |
| `zk-chef init [DIR] [-y]` | human | Sets up a vault |
| `zk-chef install [-y] [--skills-only]` | human | Installs the skills and, if needed, zk |
| `zk-chef self-update` | human | Replaces the binary with the latest release |
| `zk-chef upgrade [-y]` | human | Shows and applies scaffold changes (zk config, templates, hook, `AGENTS.md` block, new config keys) after a binary update |
| `zk-chef doctor` | human | Checks zk, git, gpg, hooks, pinned key, skills, PATH and `min_version` |
| `zk-chef key add [FPR] \| list \| rm FPR` | human | Manages pinned signing keys |
| `zk-chef ingest PATH\|URL --title T` | agent | Copies a file or URL into `raw/` and creates its `unreviewed` source note |
| `zk-chef lint [--staged]` | agent, hook | Runs every check; errors give exit code 1 |
| `zk-chef fix` | agent | Rewrites stale link aliases to the targets' current titles |
| `zk-chef review [SRC-ID reviewed\|disputed] [--no-commit]` | human only | Shows the raw file, asks you to confirm, signs the verdict and commits. Without arguments it walks the queue |
| `zk-chef status` | agent | Lists new and re-review sources, disputed sources still cited, and lint counts |
| `zk-chef export DIR [--generator hugo]` | agent | Writes the notes, hubs, sources and projects pages to `DIR` as Markdown, with frontmatter cut to a safe set of fields. `--generator hugo` shapes the output for Hugo. With it, `DIR` is a Hugo site root. The pages go to `DIR/content/`. The export also creates a write-once scaffold. Lint errors stop it with exit code 1. It never changes the vault. `DIR` must be outside the vault |

Lint codes and fixes are in `skills/zk-chef-ops/LINT.md`.

## Export a vault as Markdown

`zk-chef export DIR` writes a copy of the vault to `DIR` as standard Markdown. Any static site generator can read it. The vault never changes, and `DIR` must be outside it.

```sh
zk-chef export /tmp/vault-md --root /path/to/vault
```

It runs lint first. Lint errors stop the export with exit code 1: the findings go to stdout, as with `lint`, and nothing is written. Warnings do not stop it. Other failures exit with code 2: no argument, `DIR` is the vault or inside it, an unknown `--generator`, or a corrupt manifest (see below). `--json` prints a report with the written paths and the counts `written` and `removed`.

What it writes:

- the pages in `notes/`, `hubs/`, `sources/` and `projects/` (one level deep), and the root `index.md`, with the same paths;
- `[[ID|Title]]` links as relative Markdown links, such as `[Title](../sources/ID.md)`;
- only these frontmatter fields: `title`, `kind`, `status`, `superseded_by`, and for sources `review`, `url`, `citation`, `authors`, `published`;
- a `Superseded by` line at the top of a superseded note;
- a `## Backlinks` section on a page that other pages link to, and a `## Members` section on a hub that other pages name in their `Hubs:` line. Both are sorted by title. A parent hub is a backlink, not a member.

What it leaves out:

- `raw/`, `keys/`, `AGENTS.md`, dot folders and every other file in the vault;
- every frontmatter field not listed above, such as `id`, `raw` and `sha256`;
- the `## Extract` section of a source. Its `## Review` section stays;
- links in code fences and in `## Extract` and `## Review` sections stay as `[[…]]` text, and they make no backlinks.

`DIR` may be a generator's content folder (for Hugo, use `--generator hugo` and the site root instead). The export writes `.zk-chef-export.json` in `DIR`, which lists the files it wrote. On the next run, the export deletes only the files in that manifest (and the folders they leave empty), then writes the new pages. A file that you add to `DIR` stays. A corrupt manifest stops the run and deletes nothing. The output line shows the counts: `wrote N pages to DIR, removed M`.

### Hugo

`--generator hugo` shapes the output for [Hugo](https://gohugo.io) 0.146 or newer. `DIR` is the Hugo site root:

```sh
zk-chef export site --generator hugo --root /path/to/vault
cd site && hugo
```

The Hugo export differs from the generic export in these ways:

- the pages go to `DIR/content/`, and the root page is `content/_index.md`, Hugo's home page. The manifest lists these pages only;
- `kind`, `url` and `published` are written as `zk_kind`, `source_url` and `source_published`. Hugo reserves `kind` (it accepts only page kinds, so a value such as `hub` fails the build in Hugo 0.123 and newer), and `url` would change the page's URL;
- each page gets a `date` from the first 12 digits of its ID (UTC). A page whose ID is not a date gets no `date`;
- shortcode calls such as `{{< x >}}` in the text are escaped, so Hugo shows them as text;
- the export creates a minimal site in `DIR`: `hugo.toml` and the layouts `layouts/baseof.html`, `home.html`, `list.html` and `single.html`.

The scaffold is write-once. The export creates a scaffold file only when the file does not exist. It never changes an existing file, so you can edit the files. The export skips `hugo.toml` when `DIR` already has a Hugo config. A Hugo config is one of these: `hugo.{toml,yaml,yml,json}`, `config.{toml,yaml,yml,json}` or a `config/` folder. To get a file again, delete it and run the export again. The summary line lists the scaffold files that the run created. `--json` lists them in `scaffolded`.

`hugo.toml` sets the title to the title of the root page, and sets the setting that makes Markdown links resolve to site URLs (`[markup.goldmark.renderHooks.link] useEmbedded = 'always'`). To change the title or the base URL, edit `hugo.toml`.

The generic export writes no config by design. It writes pages only.

## Option: publish a vault as a site with Quartz

A vault is plain Markdown with `[[ID|Title]]` links. [Quartz](https://quartz.jzhao.xyz) v5 builds it into a static site with wikilinks, backlinks, search and a graph. It needs Node 22 or newer. Tested on 2026-11-09 with Quartz branch `v5` at commit `3c7cba4`; there is no v5 release tag newer than `v5.0.0`, so pin a commit.

```sh
git clone --branch v5 https://github.com/jackyzha0/quartz.git site
cd site
git checkout 3c7cba4
npm ci
cp quartz.config.default.yaml quartz.config.yaml
```

In `quartz.config.yaml`, set `baseUrl` and `pageTitle`. Only four folders and `index.md` are copied, so `raw/` never enters `content/`. Then copy the vault in and build:

```sh
VAULT=/path/to/vault
rm -rf content && mkdir content
cp -r "$VAULT"/{notes,hubs,sources,projects} "$VAULT/index.md" content/
npx quartz plugin install --from-config
npx quartz build --serve   # or without --serve; output goes to public/
```

Notes:

- Quartz resolves a bare ID such as `[[202611091044tnn3|Title]]` when exactly one file has that name. Keep note names unique across folders, which the lint already requires.
- Quartz reads dates from git. Copied files give a "not tracked by git" warning and wrong dates; this is harmless.
- A published note that links to a note in an ignored folder gets a broken link.
- Quartz lists backlinks by frontmatter `title`. Every note has one.
- The test build of 23 files took about 3 s.

## Repo layout

```txt
main.go, embed.go                 entry point; embeds scaffold/ and skills/
internal/cli/                     one file per verb, with tests
internal/lint/                    vault loading, checks, signatures, status
internal/diff, internal/version   upgrade diffs; semver and version gate
scaffold/                         files that init copies into a vault
skills/zk-chef-ops/               bookkeeping skill (+ LINT.md)
skills/zettel-writing/            content skill
install.sh                        release installer
.github/                          release workflow and build script
docs/build-notes.md               results of the build-time checks
```

## Develop

```sh
just fmt
just typecheck
just test        # needs zk and gpg for some tests
```

Pushing a `v*` tag runs `.github/workflows/release.yml`. It builds four binaries and `checksums.txt` and creates a GitHub release.

## Limits

- The gate guards against mistakes, not against an agent set on bypassing the hook.
- A review vouches for a raw file, not for the notes that cite it. Lint compares notes with each other, not with reality.
- `## Extract` and the paraphrases in a source note are unsigned.
- Search is keyword-only. Windows is not supported.
- Not yet checked: that Obsidian resolves `[[ID|Title]]` across the four folders (`docs/build-notes.md`, item 2).
