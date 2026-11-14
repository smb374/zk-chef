# The site's Style is kept in its own state file

`export -g hugo` keeps the selected Style (`plain` or `tufte`) in `.zk-chef-site.json` in the site root, so an export without `--style` does not switch a site to another look. The manifest (`.zk-chef-export.json`) stays a list of pages only, as ADR 0001 requires. A site exported by v0.2.2 has no state file; then the export uses `tufte` when `static/vendored/tufte/` exists, because only the Tufte Style writes that folder, and `plain` otherwise.

## Considered Options

- **No state, an omitted `--style` means `plain`.** Rejected: a Tufte site exported again without the flag changes its look without a warning.
- **Detect the Style from the layout hashes.** Rejected: it fails as soon as the user edits a layout.
- **Store the Style in the manifest.** Rejected: ADR 0001 keeps the manifest to pages.
