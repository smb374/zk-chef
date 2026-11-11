package cli

// scaffoldHashes maps an Export scaffold file (slash path under export-scaffold/hugo) to the sha256 hex of each version zk-chef released.
// Append a release's hashes when you tag it (docs/build-notes.md, "Export scaffold hashes").
var scaffoldHashes = map[string][]string{
	"layouts/baseof.html": {"9c2120522283947b0f975b13b6ad10948f3a41b7b603ca1982fa6f5e17e91f06"}, // v0.2.0
	"layouts/home.html":   {"9a9f76fec3467c6937e24edbbdcc0d2c4234ec12765ec7a42ae36e22c4caace6"}, // v0.2.0
	"layouts/list.html":   {"9a9f76fec3467c6937e24edbbdcc0d2c4234ec12765ec7a42ae36e22c4caace6"}, // v0.2.0
	"layouts/single.html": {"bf044cd45e01b0a6fe0d9fab4aa3e1ceffd5dff058f5177de0d9a961386f865e"}, // v0.2.0
}

// hugoTomlTemplates are the hugo.toml.tmpl texts zk-chef released, one per version, oldest first.
var hugoTomlTemplates = []string{
	// v0.2.0
	`baseURL = '/'
title = @TITLE@
refLinksErrorLevel = 'ERROR'
disableKinds = ['taxonomy', 'term', 'RSS', 'sitemap']

[markup.goldmark.renderHooks.link]
  useEmbedded = 'always'

[module.hugoVersion]
  min = '0.146.0'
`,
}
