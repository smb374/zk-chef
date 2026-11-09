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
- copies the scaffold (zk config and templates, `.zk-chef/config.toml`, pre-commit hook, seed hubs) and never overwrites a file;
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

Lint codes and fixes are in `skills/zk-chef-ops/LINT.md`.

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
