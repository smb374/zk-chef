# zk-vault-skill

Skills and scripts for an agent-maintained Zettelkasten wiki. [zk](https://github.com/zk-org/zk) makes IDs and answers link queries, git keeps history, and `scripts/vault.py` checks the invariants. Two skills tell the agent how to do the bookkeeping and how to write. A human GPG signature is the gate on raw sources.

This repo is the kit. A vault is a separate git repo where you install it.

## Requirements

- Python 3.11 or newer, with PyYAML (`pip install pyyaml`)
- git and gpg
- zk (`init` offers to download it to `~/.local/bin`)
- Obsidian, optional, to browse the vault

## Install

```sh
python3 scripts/vault.py init /path/to/vault      # add --yes to accept the zk download
```

`init` is safe to run again. It does these things:

- runs `git init` when needed;
- copies the scaffold (zk config and templates, `.vault/config.toml`, pre-commit hook, seed hubs, `AGENTS.md`), the skills (to `.agents/skills/`) and `scripts/vault.py`, and never overwrites a file;
- creates `notes/ hubs/ sources/ projects/ raw/ keys/`;
- sets `core.hooksPath` to `.githooks`;
- checks that zk, gpg and PyYAML exist.

Run `init` once per clone. It keeps your edited files and prints `kept your …` when they differ from the kit's copy. If the vault already has an `AGENTS.md`, add the lines from `scaffold/AGENTS.md` by hand.

## Set up your signing key (once, human)

Source reviews need a pinned key.

```sh
gpg --armor --export YOUR_FINGERPRINT > keys/me.asc
```

Then add the fingerprint to `pinned_fingerprints` in `.vault/config.toml`. Lint accepts a signature only when the key is in `keys/` and its fingerprint is pinned.

## Use

The agent loads the skills from `AGENTS.md`. It creates notes with `zk new`, edits files and commits. The hook runs `vault.py lint --staged` on every commit.

| Command | Who | What it does |
| --- | --- | --- |
| `vault.py init [DIR] [-y]` | human | Sets up a vault |
| `vault.py lint [--staged] [--root DIR]` | agent, hook | Runs every check; errors give exit code 1 |
| `vault.py fix [--root DIR]` | agent | Rewrites stale link aliases to the targets' current titles |
| `vault.py review SRC-ID reviewed\|disputed` | human only | Shows the raw file, asks you to confirm, signs the verdict and commits |

Lint codes and fixes are in `skills/vault-ops/LINT.md`.

## Repo layout

```txt
scripts/vault.py                  init, lint, fix, review
scaffold/                         files that init copies into a vault
skills/vault-ops/                 bookkeeping skill (+ LINT.md)
skills/vault-writing/             content skill
tests/test_vault.py               self-check
docs/build-notes.md               results of the build-time checks
```

## Develop

```sh
python3 tests/test_vault.py       # needs zk and gpg; prints "ok <name>" per test
```

## Limits

- The gate guards against mistakes, not against an agent set on bypassing the hook.
- A review vouches for a raw file, not for the notes that cite it. Lint compares notes with each other, not with reality.
- `## Extract` and the paraphrases in a source note are unsigned.
- Search is keyword-only. Windows is not supported.
- Not yet checked: that Obsidian resolves `[[ID|Title]]` across the four folders (`docs/build-notes.md`, item 2).
