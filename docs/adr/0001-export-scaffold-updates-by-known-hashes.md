# Export scaffold files are updated by matching released hashes

`zk-chef export -g hugo` replaces an Export scaffold file only when its bytes equal a version zk-chef shipped; the binary embeds the hash of every released version of each file (`hugo.toml` is rendered from each past template with the current root title). A file that matches no shipped version counts as edited by the user and is kept. Files under `static/vendor/` are owned by zk-chef and are overwritten whenever they differ.

## Considered Options

- **Hashes in the manifest.** Rejected: sites exported by v0.2.0 have no recorded hashes, so they would never update. The manifest also stays a list of pages only.
- **Never update (write-once).** Rejected: existing sites keep the old Hugo floor and layouts for ever.
- **Always overwrite.** Rejected: destroys user edits to layouts and `hugo.toml`.

## Consequences

- Every release that changes a scaffold file must append the old hash to the list; a release test checks that each embedded file's current hash is in it.
- A user who changes the root title in `index.md` after exporting, or edits `hugo.toml` by hand, is treated as edited and keeps their file.
- Edits to `static/vendor/` files are lost on the next export; customisations belong in a separate stylesheet.
