package cli

// scaffoldHashes maps an Export scaffold file (slash path under export-scaffold/hugo) to the sha256 hex of each version zk-chef released.
// Append a release's hashes when you tag it (docs/build-notes.md, "Export scaffold hashes").
var scaffoldHashes = map[string][]string{
	"layouts/baseof.html": {"9c2120522283947b0f975b13b6ad10948f3a41b7b603ca1982fa6f5e17e91f06", "118a720bc7e1f560241b05fccbe8d8f9d4aea7fca306b21956e2098dc012068f", "f8525a5039fe46ad1104cda286ae8fa0fdeffec1fac9d92ba89cef684e12d240", "e30d67d1601c2131cc831fa50c3035651de7c6d82ab52cb798cb64a48186595c"}, // v0.2.0, #32, #33, v0.2.2
	"layouts/home.html":   {"9a9f76fec3467c6937e24edbbdcc0d2c4234ec12765ec7a42ae36e22c4caace6", "d6cf8e385a2c437329cd37b1eb3289b26a7ea9e8b59e5338c9073e7d1d1781cf"},                                                                                                                                         // v0.2.0, v0.2.2
	"layouts/list.html":   {"9a9f76fec3467c6937e24edbbdcc0d2c4234ec12765ec7a42ae36e22c4caace6", "d6cf8e385a2c437329cd37b1eb3289b26a7ea9e8b59e5338c9073e7d1d1781cf"},                                                                                                                                         // v0.2.0, v0.2.2
	"layouts/single.html": {"bf044cd45e01b0a6fe0d9fab4aa3e1ceffd5dff058f5177de0d9a961386f865e", "b8ea7e8c615785d72acb51e7ab277d3a6264e8268c787da991de6f59e723822a", "e8f4c044f2151c8e50336677bfa25d9a9d34a5897baed6eceda9533062ecc63c"},                                                                     // v0.2.0, #33, v0.2.2
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
	// v0.2.2
	`baseURL = '/'
title = @TITLE@
refLinksErrorLevel = 'ERROR'
disableKinds = ['taxonomy', 'term', 'RSS', 'sitemap']

[markup.goldmark.renderHooks.link]
  useEmbedded = 'always'

[module.hugoVersion]
  min = '0.169.0'
`,
}
