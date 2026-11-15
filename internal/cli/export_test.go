package cli

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// readTree returns every regular file under dir (slash path -> content).
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExportLintErrorStopsExport(t *testing.T) {
	out := t.TempDir()
	files := with_(t, map[string]string{"notes/a.md": "no frontmatter"})
	got := runIn(t, "dev", files, "export", out)
	if got.code != 1 || !strings.Contains(got.stdout, "notes/a.md:1: E_FRONTMATTER missing frontmatter\n") {
		t.Errorf("got code %d stdout %q stderr %q, want 1 and the finding on stdout", got.code, got.stdout, got.stderr)
	}
	if tree := readTree(t, out); len(tree) != 0 {
		t.Errorf("OUT has files %v, want none", tree)
	}
}

// exportVault is the base vault plus a rich source, a project log and files that must not be exported.
func exportVault(t *testing.T) map[string]string {
	t.Helper()
	return with_(t, map[string]string{
		"notes/src001.md": doc("Source one", "source",
			"Hubs: [[hubsrc|Sources]]\n\nWhy.\n\n## Review",
			kv{"raw", "raw/src001-a.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"},
			kv{"url", "https://example.com/x"}, kv{"citation", "Doe 2026"}, kv{"accessed", "2026-01-03"}, kv{"authors", "[Doe, Roe]"}, kv{"published", "2026-01-02"}),
		"notes/log001.md":      doc("Project one", "log", hubLine+"\n\nDecision."),
		"AGENTS.md":            "agent rules",
		"keys/k.asc":           "key",
		".githooks/pre-commit": "hook",
		".zk/config.toml":      "zk",
	})
}

func TestExportCleanVaultLayout(t *testing.T) {
	out := t.TempDir()
	files := exportVault(t)
	got := runIn(t, "dev", files, "export", out)
	if got.code != 0 {
		t.Fatalf("got code %d stdout %q stderr %q, want 0", got.code, got.stdout, got.stderr)
	}
	tree := withoutManifest(readTree(t, out))
	want := map[string]string{
		"notes/not001.md": "---\ntitle: Leaders are unique\ndate: 2026-01-02T03:04:05Z\nkind: note\n---\nHubs: [Index](../index.md)\n\nOne leader per term [1, p. 4].\n\n## References\n\n1. [Source one](src001.md)\n",
		"notes/src001.md": "---\ntitle: Source one\ndate: 2026-01-02T03:04:05Z\nkind: source\nreview: unreviewed\nurl: https://example.com/x\ncitation: Doe 2026\naccessed: 2026-01-03\nauthors: [Doe, Roe]\npublished: 2026-01-02\n---\nHubs: [Sources](hubsrc.md)\n\nWhy.\n\n## Review\n\n## Backlinks\n\n- [Leaders are unique](not001.md)\n",
		"notes/log001.md": "---\ntitle: Project one\ndate: 2026-01-02T03:04:05Z\nkind: log\n---\nHubs: [Index](../index.md)\n\nDecision.\n",
	}
	for p, w := range want {
		if tree[p] != w {
			t.Errorf("%s = %q, want %q", p, tree[p], w)
		}
	}
	for _, p := range []string{"index.md", "notes/hubsrc.md"} {
		page, ok := tree[p]
		if !ok || !strings.HasPrefix(page, "---\ntitle: ") || strings.Contains(page, "\nid: ") {
			t.Errorf("%s = %q (present %v), want a page with title first and no id", p, page, ok)
		}
	}
	for p := range tree {
		if strings.HasPrefix(p, "raw/") || strings.HasPrefix(p, "keys/") || strings.HasPrefix(p, ".") || p == "AGENTS.md" {
			t.Errorf("OUT holds %s, which must not be exported", p)
		}
	}
}
func TestExportRefusesBadOut(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	before := readTree(t, ".")
	for name, args := range map[string][]string{
		"missing DIR":     {"export"},
		"DIR is vault":    {"export", "."},
		"DIR in vault":    {"export", "site"},
		"DIR in folder":   {"export", "notes/site"},
		"DIR with --root": {"export", "--root", ".", "./"},
	} {
		got := run(env, args...)
		if got.code == 0 || got.stdout != "" || got.stderr == "" {
			t.Errorf("%s: got code %d stdout %q stderr %q, want nonzero, no stdout, an error", name, got.code, got.stdout, got.stderr)
		}
	}
	if after := readTree(t, "."); len(after) != len(before) {
		t.Errorf("vault has %d files after refused exports, want %d", len(after), len(before))
	}
}

// TestExportRefusesOutThatHoldsVaultFolders: a vault named like an export folder, with its parent as OUT, would get the pages written into the vault.
func TestExportRefusesOutThatHoldsVaultFolders(t *testing.T) {
	env := setupVault(t, "dev", baseVault(t))
	tmp, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	vault := filepath.Join(parent, "notes")
	if err := os.Rename(tmp, vault); err != nil {
		t.Fatal(err)
	}
	before := readTree(t, parent)
	got := run(env, "export", "--root", vault, parent)
	if got.code != 2 || got.stdout != "" || got.stderr == "" || !maps.Equal(before, readTree(t, parent)) {
		t.Errorf("got code %d stdout %q stderr %q, want 2, no stdout, an error and the vault unchanged", got.code, got.stdout, got.stderr)
	}
}

func TestExportLeavesVaultUnchanged(t *testing.T) {
	out := t.TempDir()
	env := setupVault(t, "dev", exportVault(t))
	before := readTree(t, ".")
	if got := run(env, "export", out); got.code != 0 {
		t.Fatalf("got code %d stderr %q, want 0", got.code, got.stderr)
	}
	after := readTree(t, ".")
	if len(after) != len(before) {
		t.Errorf("vault has %d files after export, want %d", len(after), len(before))
	}
	for p, b := range before {
		if after[p] != b {
			t.Errorf("%s changed by export", p)
		}
	}
}
func TestExportJSONReport(t *testing.T) {
	out := t.TempDir()
	got := runIn(t, "dev", exportVault(t), "export", "--json", out)
	var r struct {
		OK      bool     `json:"ok"`
		Written []string `json:"written"`
		Counts  struct {
			Written int `json:"written"`
		} `json:"counts"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &r); err != nil {
		t.Fatalf("stdout %q stderr %q is not a report: %v", got.stdout, got.stderr, err)
	}
	var onDisk []string
	for p := range withoutManifest(readTree(t, out)) {
		onDisk = append(onDisk, p)
	}
	slices.Sort(onDisk)
	if got.code != 0 || !r.OK || !slices.Equal(r.Written, onDisk) || r.Counts.Written != len(onDisk) || len(onDisk) == 0 {
		t.Errorf("got code %d report %+v, want 0, ok, written = %v and a matching count", got.code, r, onDisk)
	}
}

func TestExportHelpListsVerb(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"export", "--help"}} {
		got := runIn(t, "dev", nil, args...)
		if got.code != 0 || !strings.Contains(got.stdout, "export") {
			t.Errorf("%v: got code %d stdout %q, want 0 and export listed", args, got.code, got.stdout)
		}
	}
}

// exportOK exports files to a fresh OUT and returns the exported tree.
func exportOK(t *testing.T, files map[string]string) map[string]string {
	t.Helper()
	out := t.TempDir()
	got := runIn(t, "dev", files, "export", out)
	if got.code != 0 {
		t.Fatalf("got code %d stdout %q stderr %q, want 0", got.code, got.stdout, got.stderr)
	}
	return readTree(t, out)
}

func TestExportLinksFromRootPage(t *testing.T) {
	tree := exportOK(t, with_(t, map[string]string{
		"index.md": doc("Index", "hub", "- [[hubsrc|Sources]]\n- [[not001]]\n- [[src001|Reading]]"),
	}))
	want := "---\ntitle: Index\ndate: 2026-01-02T03:04:05Z\nkind: hub\n---\n- [Sources](notes/hubsrc.md)\n- [Leaders are unique](notes/not001.md)\n- [Reading](notes/src001.md)\n"
	if !strings.HasPrefix(tree["index.md"], want) {
		t.Errorf("index.md = %q, want it to start with %q", tree["index.md"], want)
	}
}

func TestExportLinksFromFolderPage(t *testing.T) {
	tree := exportOK(t, with_(t, map[string]string{
		"notes/not001.md": doc("Leaders are unique", "note", hubLine+"\n\nSee [[not002]], [[not002|Two]], [[src001]] and [[src001|Reading]]. Claim [1, p. 4].\n\n## References\n\n1. [[src001|Source one]]"),
		"notes/not002.md": doc("Note two", "note", hubLine+"\n\nBack to [[not001]]."),
	}))
	want := "---\ntitle: Leaders are unique\ndate: 2026-01-02T03:04:05Z\nkind: note\n---\nHubs: [Index](../index.md)\n\nSee [Note two](not002.md), [Two](not002.md), [Source one](src001.md) and [Reading](src001.md). Claim [1, p. 4].\n\n## References\n\n1. [Source one](src001.md)\n"
	if !strings.HasPrefix(tree["notes/not001.md"], want) {
		t.Errorf("notes/not001.md = %q, want it to start with %q", tree["notes/not001.md"], want)
	}
}

func TestExportLinksAcrossFolders(t *testing.T) {
	tree := exportOK(t, with_(t, map[string]string{
		"notes/hubsrc.md": doc("Sources", "hub", "Hubs: [[index|Index]]\n\n- [[not001]]\n- [[src001|Reading]]\n- [[hubprj]]"),
	}))
	want := "---\ntitle: Sources\ndate: 2026-01-02T03:04:05Z\nkind: hub\n---\nHubs: [Index](../index.md)\n\n- [Leaders are unique](not001.md)\n- [Reading](src001.md)\n- [Projects](hubprj.md)\n"
	if !strings.HasPrefix(tree["notes/hubsrc.md"], want) {
		t.Errorf("notes/hubsrc.md = %q, want it to start with %q", tree["notes/hubsrc.md"], want)
	}
}

func TestExportLinksEscapeBrackets(t *testing.T) {
	tree := exportOK(t, with_(t, map[string]string{
		"notes/not002.md": doc("Array [a] of x", "note", hubLine+"\n\nBack to [[not001]]."),
		"notes/not001.md": doc("Leaders are unique", "note", hubLine+"\n\nSee [[not002]] and [[src001|Source [one]]."),
	}))
	want := "---\ntitle: Leaders are unique\ndate: 2026-01-02T03:04:05Z\nkind: note\n---\nHubs: [Index](../index.md)\n\nSee [Array \\[a\\] of x](not002.md) and [Source \\[one](src001.md).\n"
	if !strings.HasPrefix(tree["notes/not001.md"], want) {
		t.Errorf("notes/not001.md = %q, want it to start with %q", tree["notes/not001.md"], want)
	}
}

func TestExportLinksLeaveExcludedPlaces(t *testing.T) {
	tree := exportOK(t, with_(t, map[string]string{
		"notes/not001.md": doc("Leaders are unique", "note",
			hubLine+"\n\nLive [[src001|Reading]]. Claim [1, p. 4].\n\n```\nfenced [[src001]]\n```\n\n## Extract\n\nquoted [[src001]]\n\n## References\n\n1. [[src001|Source one]]"),
		"notes/src001.md": doc("Source one", "source",
			"Hubs: [[hubsrc|Sources]]\n\nWhy.\n\n## Review\n\nreviewed [[not001]]",
			kv{"raw", "raw/src001-a.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"}),
	}))
	var left []string
	for p, page := range tree {
		for l := range strings.SplitSeq(page, "\n") {
			if strings.Contains(l, "[[") {
				left = append(left, p+": "+l)
			}
		}
	}
	slices.Sort(left)
	want := []string{"notes/not001.md: fenced [[src001]]", "notes/not001.md: quoted [[src001]]", "notes/src001.md: reviewed [[not001]]"}
	if !slices.Equal(left, want) {
		t.Errorf("lines with [[ = %q, want %q", left, want)
	}
	if !strings.Contains(tree["notes/not001.md"], "Live [Reading](src001.md).") {
		t.Errorf("notes/not001.md = %q, want the live link converted", tree["notes/not001.md"])
	}
}

// backlinkVault adds notes that link not001, not002 and the Sources hub in several ways.
func backlinkVault(t *testing.T) map[string]string {
	t.Helper()
	return with_(t, map[string]string{
		"notes/not002.md": doc("Zed note", "note", hubLine+"\n\nBack to [[not001]]."),
		"notes/not003.md": doc("Alpha note", "note", hubLine+"\n\nBack to [[not001|Leaders]] and [[not001]] again."),
	})
}

func TestExportBacklinksListsLinkingPagesSortedByTitle(t *testing.T) {
	tree := exportOK(t, backlinkVault(t))
	want := "---\ntitle: Leaders are unique\ndate: 2026-01-02T03:04:05Z\nkind: note\n---\nHubs: [Index](../index.md)\n\nOne leader per term [1, p. 4].\n\n## References\n\n1. [Source one](src001.md)\n\n## Backlinks\n\n- [Alpha note](not003.md)\n- [Zed note](not002.md)\n"
	if !strings.HasPrefix(tree["notes/not001.md"], want) {
		t.Errorf("notes/not001.md = %q, want it to start with %q", tree["notes/not001.md"], want)
	}
	wantS1 := "\n## Backlinks\n\n- [Leaders are unique](not001.md)\n"
	if !strings.HasSuffix(tree["notes/src001.md"], wantS1) {
		t.Errorf("notes/src001.md = %q, want it to end with %q", tree["notes/src001.md"], wantS1)
	}
}

func TestExportBacklinksOmitSectionWithoutBacklinks(t *testing.T) {
	tree := exportOK(t, backlinkVault(t))
	for _, p := range []string{"notes/not002.md", "notes/not003.md", "notes/log001.md"} {
		if page, ok := tree[p]; ok && strings.Contains(page, "## Backlinks") {
			t.Errorf("%s = %q, want no Backlinks section", p, page)
		}
	}
}

func TestExportBacklinksSkipSelfFenceExtractAndReview(t *testing.T) {
	tree := exportOK(t, with_(t, map[string]string{
		"notes/not001.md": doc("Leaders are unique", "note",
			hubLine+"\n\nSelf [[not001]]. One leader per term [1, p. 4].\n\n## References\n\n1. [[src001|Source one]]"),
		"notes/not002.md": doc("Zed note", "note",
			hubLine+"\n\nLive [[src001]].\n\n```\nfenced [[not001]]\n```\n\n## Extract\n\nquoted [[not001]]"),
		"notes/src001.md": doc("Source one", "source",
			"Hubs: [[hubsrc|Sources]]\n\nWhy.\n\n## Review\n\nreviewed [[not001]]",
			kv{"raw", "raw/src001-a.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"}),
	}))
	if page := tree["notes/not001.md"]; strings.Contains(page, "## Backlinks") {
		t.Errorf("notes/not001.md = %q, want no Backlinks section", page)
	}
}

func TestExportBacklinksEscapeBracketsInTitles(t *testing.T) {
	tree := exportOK(t, with_(t, map[string]string{
		"notes/not002.md": doc("Array [a] of x", "note", hubLine+"\n\nBack to [[not001]]."),
	}))
	if want := "\n## Backlinks\n\n- [Array \\[a\\] of x](not002.md)\n"; !strings.HasSuffix(tree["notes/not001.md"], want) {
		t.Errorf("notes/not001.md = %q, want it to end with %q", tree["notes/not001.md"], want)
	}
}

func TestExportBacklinksSameOutputOnTwoRuns(t *testing.T) {
	files := backlinkVault(t)
	first, second := exportOK(t, files), exportOK(t, files)
	if !maps.Equal(first, second) {
		t.Errorf("two runs differ: %v vs %v", first, second)
	}
}

// memberVault adds a Raft hub with two members, a prose mention and a member that also links the hub in its body.
func memberVault(t *testing.T) map[string]string {
	t.Helper()
	return with_(t, map[string]string{
		"notes/hubraf.md": doc("Raft", "hub", "Hubs: [[index|Index]]\n\n- [[not002]]\n- [[not003]]"),
		"notes/not002.md": doc("Zed note", "note", "Hubs: [[hubraf|Raft]]\n\nSee [[not003]] and [[hubraf]]."),
		"notes/not003.md": doc("Alpha note", "note", "Hubs: [[hubraf|Raft]]\n\nSee [[not002]]."),
		"notes/not004.md": doc("Mentions", "note", hubLine+"\n\nTalks about [[hubraf|Raft]] and [[not002]]."),
	})
}

func TestExportMembersListsHubsLineNamersSortedByTitle(t *testing.T) {
	tree := exportOK(t, memberVault(t))
	want := "\n## Members\n\n- [Alpha note](not003.md)\n- [Zed note](not002.md)\n\n## Backlinks\n"
	if page := tree["notes/hubraf.md"]; !strings.Contains(page, want) {
		t.Errorf("notes/hubraf.md = %q, want it to contain %q", page, want)
	}
}

func TestExportMembersExcludeProseMentionAndAreNotBacklinks(t *testing.T) {
	tree := exportOK(t, memberVault(t))
	want := "---\ntitle: Raft\ndate: 2026-01-02T03:04:05Z\nkind: hub\n---\nHubs: [Index](../index.md)\n\n- [Zed note](not002.md)\n- [Alpha note](not003.md)\n\n## Members\n\n- [Alpha note](not003.md)\n- [Zed note](not002.md)\n\n## Backlinks\n\n- [Mentions](not004.md)\n"
	if got := tree["notes/hubraf.md"]; got != want {
		t.Errorf("notes/hubraf.md = %q, want %q", got, want)
	}
}

func TestExportMembersParentHubIsBacklinkNotMember(t *testing.T) {
	tree := exportOK(t, exportVault(t))
	want := "---\ntitle: Sources\ndate: 2026-01-02T03:04:05Z\nkind: hub\n---\nHubs: [Index](../index.md)\n\nOne source note per raw file.\n\n## Members\n\n- [Source one](src001.md)\n\n## Backlinks\n\n- [Index](../index.md)\n"
	if got := tree["notes/hubsrc.md"]; got != want {
		t.Errorf("notes/hubsrc.md = %q, want %q", got, want)
	}
}

func TestExportMembersOnlyForHubsWithMembers(t *testing.T) {
	tree := exportOK(t, exportVault(t))
	if page := tree["notes/hubprj.md"]; strings.Contains(page, "## Members") {
		t.Errorf("notes/hubprj.md = %q, want no Members section for a hub without members", page)
	}
	for p, page := range tree {
		if !strings.Contains(page, "\nkind: hub\n") && strings.Contains(page, "## Members") {
			t.Errorf("%s = %q, want no Members section on a non-hub", p, page)
		}
	}
}

// extractSource is a source whose body holds paraphrases, an Extract section and a Review block.
func extractSource(body string) map[string]string {
	return map[string]string{"notes/src001.md": doc("Source one", "source", body,
		kv{"raw", "raw/src001-a.bin"}, kv{"sha256", rawSHA}, kv{"review", "unreviewed"})}
}

func TestExportExtractStripsSectionKeepsReviewAndParaphrases(t *testing.T) {
	tree := exportOK(t, with_(t, extractSource(
		"Hubs: [[hubsrc|Sources]]\n\nWhy. Paraphrase [1, p. 2].\n\n## Extract\n\nquoted text\n\n### Part\n\nmore quoted\n\n## Review\n\nreviewed text")))
	want := "---\ntitle: Source one\ndate: 2026-01-02T03:04:05Z\nkind: source\nreview: unreviewed\n---\nHubs: [Sources](hubsrc.md)\n\nWhy. Paraphrase [1, p. 2].\n\n## Review\n\nreviewed text\n\n## Backlinks\n\n- [Leaders are unique](not001.md)\n"
	if got := tree["notes/src001.md"]; got != want {
		t.Errorf("notes/src001.md = %q, want %q", got, want)
	}
}

func TestExportExtractStripsSectionAtEndOfFile(t *testing.T) {
	tree := exportOK(t, with_(t, extractSource(
		"Hubs: [[hubsrc|Sources]]\n\nWhy.\n\n## Review\n\nreviewed text\n\n## Extract\n\nquoted text")))
	if got := tree["notes/src001.md"]; strings.Contains(got, "Extract") || strings.Contains(got, "quoted") || !strings.Contains(got, "## Review\n\nreviewed text\n") {
		t.Errorf("notes/src001.md = %q, want Review kept and no Extract", got)
	}
}

func TestExportExtractWithoutSectionIsUnchanged(t *testing.T) {
	tree := exportOK(t, with_(t, extractSource(
		"Hubs: [[hubsrc|Sources]]\n\nWhy.\n\n## Review\n\nreviewed text")))
	want := "---\ntitle: Source one\ndate: 2026-01-02T03:04:05Z\nkind: source\nreview: unreviewed\n---\nHubs: [Sources](hubsrc.md)\n\nWhy.\n\n## Review\n\nreviewed text\n\n## Backlinks\n\n- [Leaders are unique](not001.md)\n"
	if got := tree["notes/src001.md"]; got != want {
		t.Errorf("notes/src001.md = %q, want %q", got, want)
	}
}

func TestExportExtractFencedHeadingIsNotASection(t *testing.T) {
	tree := exportOK(t, with_(t, extractSource(
		"Hubs: [[hubsrc|Sources]]\n\nWhy.\n\n```\n## Extract\nfenced\n```\n\n## Review")))
	if got := tree["notes/src001.md"]; !strings.Contains(got, "```\n## Extract\nfenced\n```\n\n## Review") {
		t.Errorf("notes/src001.md = %q, want the fenced block kept", got)
	}
}

func TestExportSupersededNoticeLinksReplacement(t *testing.T) {
	tree := exportOK(t, with_(t, map[string]string{
		"notes/not001.md": doc("Leaders are unique", "note", hubLine+"\n\nOld claim.",
			kv{"status", "superseded"}, kv{"superseded_by", "not002"}),
		"notes/not002.md": doc("Array [a] of x", "note", hubLine+"\n\nNew claim."),
	}))
	want := "---\ntitle: Leaders are unique\ndate: 2026-01-02T03:04:05Z\nkind: note\nstatus: superseded\nsuperseded_by: not002\n---\nSuperseded by [Array \\[a\\] of x](not002.md).\n\nHubs: [Index](../index.md)\n\nOld claim.\n"
	if got := tree["notes/not001.md"]; got != want {
		t.Errorf("notes/not001.md = %q, want %q", got, want)
	}
	if _, ok := tree["notes/not002.md"]; !ok {
		t.Errorf("tree %v has no notes/not002.md, the replacement", slices.Collect(maps.Keys(tree)))
	}
}

func TestExportSupersededLiveNoteHasNoNotice(t *testing.T) {
	tree := exportOK(t, with_(t, map[string]string{
		"notes/not001.md": doc("Leaders are unique", "note", hubLine+"\n\nOld claim.",
			kv{"status", "superseded"}, kv{"superseded_by", "not002"}),
		"notes/not002.md": doc("New note", "note", hubLine+"\n\nNew claim."),
	}))
	for p, page := range tree {
		if p != "notes/not001.md" && strings.Contains(page, "Superseded by") {
			t.Errorf("%s = %q, want no notice on a live page", p, page)
		}
	}
}

// exportManifestName is the file in OUT that lists what the last run wrote.
const exportManifestName = ".zk-chef-export.json"

// withoutManifest drops the manifest from a tree.
func withoutManifest(tree map[string]string) map[string]string {
	tree = maps.Clone(tree)
	delete(tree, exportManifestName)
	return tree
}

func TestExportManifestRemovedNoteDisappears(t *testing.T) {
	out := t.TempDir()
	env := setupVault(t, "dev", with_(t, map[string]string{
		"notes/not002.md": doc("Note two", "note", hubLine+"\n\nSecond."),
	}))
	if got := run(env, "export", out); got.code != 0 {
		t.Fatalf("first run: code %d stderr %q, want 0", got.code, got.stderr)
	}
	if _, ok := readTree(t, out)["notes/not002.md"]; !ok {
		t.Fatalf("first run did not write notes/not002.md")
	}
	if err := os.Remove("notes/not002.md"); err != nil {
		t.Fatal(err)
	}
	got := run(env, "export", "--json", out)
	var r struct {
		Counts struct{ Written, Removed int } `json:"counts"`
	}
	if err := json.Unmarshal([]byte(got.stdout), &r); err != nil {
		t.Fatalf("stdout %q stderr %q is not a report: %v", got.stdout, got.stderr, err)
	}
	if _, ok := readTree(t, out)["notes/not002.md"]; ok || got.code != 0 || r.Counts.Removed != 1 {
		t.Errorf("code %d counts %+v tree %v, want 0, removed 1 and no notes/not002.md", got.code, r.Counts, slices.Collect(maps.Keys(readTree(t, out))))
	}
}

func TestExportManifestForeignFileSurvivesTwoRuns(t *testing.T) {
	out := t.TempDir()
	foreign := map[string]string{"hugo.toml": "title = 'x'", "static/logo.svg": "<svg/>", "notes/mine.md": "mine"}
	for p, c := range foreign {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(out, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := setupVault(t, "dev", baseVault(t))
	for i := range 2 {
		if got := run(env, "export", out); got.code != 0 {
			t.Fatalf("run %d: code %d stderr %q, want 0", i+1, got.code, got.stderr)
		}
	}
	tree := readTree(t, out)
	for p, c := range foreign {
		if tree[p] != c {
			t.Errorf("%s = %q, want %q kept", p, tree[p], c)
		}
	}
}

func TestExportManifestSecondRunIsIdentical(t *testing.T) {
	out := t.TempDir()
	env := setupVault(t, "dev", backlinkVault(t))
	if got := run(env, "export", out); got.code != 0 {
		t.Fatalf("first run: code %d stderr %q, want 0", got.code, got.stderr)
	}
	first := readTree(t, out)
	if _, ok := first[exportManifestName]; !ok {
		t.Fatalf("tree %v has no %s", slices.Collect(maps.Keys(first)), exportManifestName)
	}
	if got := run(env, "export", out); got.code != 0 {
		t.Fatalf("second run: code %d stderr %q, want 0", got.code, got.stderr)
	}
	if second := readTree(t, out); !maps.Equal(first, second) {
		t.Errorf("runs differ: %v vs %v", first, second)
	}
}

func TestExportManifestEntryOutsideOutIsIgnored(t *testing.T) {
	parent := t.TempDir()
	out := filepath.Join(parent, "out")
	victim := filepath.Join(parent, "victim.md")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{victim, filepath.Join(out, "inside.md")} {
		if err := os.WriteFile(p, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"files":["../victim.md","` + filepath.ToSlash(victim) + `","gone/../../victim.md"]}`
	if err := os.WriteFile(filepath.Join(out, exportManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := runIn(t, "dev", baseVault(t), "export", out); got.code != 0 {
		t.Fatalf("code %d stderr %q, want 0", got.code, got.stderr)
	}
	if b, err := os.ReadFile(victim); err != nil || string(b) != "keep" {
		t.Errorf("victim.md = %q, %v, want it kept", b, err)
	}
}

func TestExportManifestCorruptStopsWithoutDeleting(t *testing.T) {
	out := t.TempDir()
	env := setupVault(t, "dev", baseVault(t))
	if got := run(env, "export", out); got.code != 0 {
		t.Fatalf("first run: code %d stderr %q, want 0", got.code, got.stderr)
	}
	if err := os.WriteFile(filepath.Join(out, exportManifestName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := readTree(t, out)
	got := run(env, "export", out)
	if got.code != 2 || got.stderr == "" || !maps.Equal(before, readTree(t, out)) {
		t.Errorf("code %d stderr %q, want 2 (as for any failed export), an error and OUT unchanged", got.code, got.stderr)
	}
}

// exportHugoOK exports files with --generator hugo, and the flags in extra, to a fresh OUT and returns the exported tree.
func exportHugoOK(t *testing.T, files map[string]string, extra ...string) map[string]string {
	t.Helper()
	out := t.TempDir()
	got := runIn(t, "dev", files, append([]string{"export", "--generator", "hugo"}, append(extra, out)...)...)
	if got.code != 0 {
		t.Fatalf("got code %d stdout %q stderr %q, want 0", got.code, got.stdout, got.stderr)
	}
	return readTree(t, out)
}

func TestExportHugoRootPageIsUnderscoreIndex(t *testing.T) {
	tree := exportHugoOK(t, baseVault(t))
	for _, p := range []string{"index.md", "_index.md", "content/index.md"} {
		if _, ok := tree[p]; ok {
			t.Errorf("OUT has %s, want only content/_index.md", p)
		}
	}
	if page, ok := tree["content/_index.md"]; !ok || !strings.HasPrefix(page, "---\ntitle: ") {
		t.Errorf("content/_index.md = %q (present %v), want the root page", page, ok)
	}
	var m struct{ Files []string }
	if err := json.Unmarshal([]byte(tree[exportManifestName]), &m); err != nil || !slices.Contains(m.Files, "content/_index.md") || slices.Contains(m.Files, "index.md") {
		t.Errorf("manifest %q (%v), want it to list content/_index.md and not index.md", tree[exportManifestName], err)
	}
	for p := range tree {
		if strings.HasPrefix(p, "notes/") || strings.HasPrefix(p, "notes/") || strings.HasPrefix(p, "notes/") || strings.HasPrefix(p, "notes/") {
			t.Errorf("OUT has %s, want pages only under content/", p)
		}
	}
	for _, p := range m.Files {
		if !strings.HasPrefix(p, "content/") {
			t.Errorf("manifest lists %s, want only content/ pages", p)
		}
	}
}

func TestExportHugoUnknownGeneratorWritesNothing(t *testing.T) {
	out := t.TempDir()
	bad := with_(t, map[string]string{"notes/a.md": "no frontmatter"}) // before setupVault changes the directory
	env := setupVault(t, "dev", baseVault(t))
	if got := run(env, "export", out); got.code != 0 {
		t.Fatalf("first run: code %d stderr %q, want 0", got.code, got.stderr)
	}
	before := readTree(t, out)
	got := run(env, "export", "--generator", "nonsense", out)
	if got.code != 2 || got.stderr == "" || !maps.Equal(before, readTree(t, out)) {
		t.Errorf("code %d stderr %q, want 2, an error and OUT unchanged", got.code, got.stderr)
	}
	// The check comes before lint: a vault with a lint error still gets the usage error.
	out2 := t.TempDir()
	if got := runIn(t, "dev", bad, "export", "--generator", "nonsense", out2); got.code != 2 || got.stdout != "" || got.stderr == "" {
		t.Errorf("lint-error vault: code %d stdout %q stderr %q, want 2, no stdout and an error", got.code, got.stdout, got.stderr)
	}
}

func TestExportHugoRenamesReservedSourceKeys(t *testing.T) {
	tree := exportHugoOK(t, exportVault(t))
	got := tree["content/notes/src001.md"]
	want := "---\ntitle: Source one\ndate: 2026-01-02T03:04:05Z\nzk_kind: source\nreview: unreviewed\nsource_url: https://example.com/x\ncitation: Doe 2026\naccessed: 2026-01-03\nauthors: [Doe, Roe]\nsource_published: 2026-01-02\n"
	if !strings.HasPrefix(got, want) {
		t.Errorf("content/notes/src001.md = %q, want it to start with %q", got, want)
	}
	for _, k := range []string{"\nurl:", "\npublished:", "\nid:", "\nraw:", "\nsha256:"} {
		if strings.Contains(got, k) {
			t.Errorf("content/notes/src001.md = %q, want no %q key", got, k)
		}
	}
}

// frontmatterKeys returns the top-level keys of a page's frontmatter, in file order.
func frontmatterKeys(t *testing.T, page string) []string {
	t.Helper()
	fm, _, ok := strings.Cut(strings.TrimPrefix(page, "---\n"), "\n---\n")
	if !ok {
		t.Fatalf("page %q has no frontmatter", page)
	}
	var keys []string
	for _, line := range strings.Split(fm, "\n") {
		if k, _, ok := strings.Cut(line, ":"); ok && !strings.HasPrefix(line, " ") {
			keys = append(keys, k)
		}
	}
	return keys
}

func TestExportHugoRenamesKindToZkKind(t *testing.T) {
	tree := exportHugoOK(t, exportVault(t))
	for _, p := range []string{"content/_index.md", "content/notes/hubsrc.md", "content/notes/not001.md", "content/notes/src001.md"} {
		keys := frontmatterKeys(t, tree[p])
		if !slices.Contains(keys, "zk_kind") || slices.Contains(keys, "kind") {
			t.Errorf("%s frontmatter keys = %v, want zk_kind and no kind", p, keys)
		}
	}
}

func TestExportGenericKeepsKind(t *testing.T) {
	tree := exportOK(t, exportVault(t))
	for _, p := range []string{"index.md", "notes/hubsrc.md", "notes/not001.md", "notes/src001.md"} {
		keys := frontmatterKeys(t, tree[p])
		if !slices.Contains(keys, "kind") || slices.Contains(keys, "zk_kind") {
			t.Errorf("%s frontmatter keys = %v, want kind and no zk_kind", p, keys)
		}
	}
}

// dated is a note whose date field differs from the date every other fixture page has.
func dated(t *testing.T) map[string]string {
	t.Helper()
	return with_(t, map[string]string{
		"notes/notdat.md": "---\ntitle: Dated note\ndate: 2027-05-06T07:08:09Z\ntags: [kind:note]\n---\n" + hubLine + "\n\nBody.\n",
	})
}

func TestExportDateComesFromTheField(t *testing.T) {
	files := dated(t) // before the first run changes the directory
	generic := exportOK(t, files)
	hugo := exportHugoOK(t, files)
	for p, page := range map[string]string{"notes/notdat.md": generic["notes/notdat.md"], "content/notes/notdat.md": hugo["content/notes/notdat.md"]} {
		if want := "title: Dated note\ndate: 2027-05-06T07:08:09Z\n"; !strings.Contains(page, want) {
			t.Errorf("%s = %q, want it to contain %q", p, page, want)
		}
	}
	for _, p := range []string{"index.md", "notes/not001.md", "notes/src001.md"} {
		if !strings.Contains(generic[p], "\ndate: 2026-01-02T03:04:05Z\n") {
			t.Errorf("%s = %q, want the date of its field", p, generic[p])
		}
	}
}

func TestExportTagsKeepTopicAndTypeOnly(t *testing.T) {
	files := with_(t, map[string]string{
		"notes/notaaa.md": "---\ntitle: Tagged\ndate: 2026-01-02T03:04:05Z\ntags: [kind:log, topic:ml/rnn, type:decision, topic:x, custom]\naliases: [Other name]\n---\n" + hubLine + "\n\nBody.\n",
	})
	for name, page := range map[string]string{"generic": exportOK(t, files)["notes/notaaa.md"], "hugo": exportHugoOK(t, files)["content/notes/notaaa.md"]} {
		kind := "kind"
		if name == "hugo" {
			kind = "zk_kind"
		}
		want := "---\ntitle: Tagged\ndate: 2026-01-02T03:04:05Z\n" + kind + ": log\ntags: ['topic:ml/rnn', 'type:decision', 'topic:x']\n---\n"
		if !strings.HasPrefix(page, want) {
			t.Errorf("%s page = %q, want it to start with %q", name, page, want)
		}
		if strings.Contains(page, "aliases") || strings.Contains(page, "Other name") {
			t.Errorf("%s page = %q, want no aliases", name, page)
		}
	}
}

// pageBody returns the text of a page after its frontmatter.
func pageBody(t *testing.T, page string) string {
	t.Helper()
	_, body, ok := strings.Cut(strings.TrimPrefix(page, "---\n"), "\n---\n")
	if !ok {
		t.Fatalf("page %q has no frontmatter", page)
	}
	return body
}

// hugoPages returns the pages of a hugo export tree: the files under content/.
func hugoPages(tree map[string]string) map[string]string {
	pages := map[string]string{}
	for p, page := range tree {
		if strings.HasPrefix(p, "content/") {
			pages[p] = page
		}
	}
	return pages
}

func TestExportHugoBodiesMatchGeneric(t *testing.T) {
	files := memberVault(t)
	files["notes/not005.md"] = doc("Old note", "note", hubLine+"\n\nOld claim.", kv{"status", "superseded"}, kv{"superseded_by", "not002"})
	generic, hugo := withoutManifest(exportOK(t, files)), exportHugoOK(t, files)
	if n := len(hugoPages(hugo)); len(generic) != n {
		t.Fatalf("generic has %d pages, hugo %d", len(generic), n)
	}
	for p, page := range generic {
		if p == "index.md" {
			p = "_index.md"
		}
		p = "content/" + p
		if got, want := pageBody(t, hugo[p]), pageBody(t, page); got != want {
			t.Errorf("%s body = %q, want the generic body %q", p, got, want)
		}
	}
}

func TestExportDropsSourceOnlyKeysFromNonSources(t *testing.T) {
	tree := exportOK(t, with_(t, map[string]string{
		"notes/not001.md": doc("Leaders are unique", "note", hubLine+"\n\nBody.",
			kv{"url", "https://example.com/x"}, kv{"citation", "Doe 2026"}, kv{"accessed", "2026-01-02"}, kv{"authors", "[Doe]"}, kv{"published", "2026-01-02"}),
	}))
	want := "---\ntitle: Leaders are unique\ndate: 2026-01-02T03:04:05Z\nkind: note\n---\n"
	if got := tree["notes/not001.md"]; !strings.HasPrefix(got, want) {
		t.Errorf("notes/not001.md = %q, want it to start with %q", got, want)
	}
}

func TestExportHugoSwitchLeavesNoStaleRootPage(t *testing.T) {
	out := t.TempDir()
	env := setupVault(t, "dev", baseVault(t))
	for i, c := range []struct {
		args        []string
		have, stale []string
	}{
		{[]string{"export", out}, []string{"index.md", "notes/not001.md"}, []string{"content/_index.md", "content/notes/not001.md"}},
		{[]string{"export", "--generator", "hugo", out}, []string{"content/_index.md", "content/notes/not001.md"}, []string{"index.md", "notes/not001.md"}},
		{[]string{"export", out}, []string{"index.md", "notes/not001.md"}, []string{"content/_index.md", "content/notes/not001.md"}},
	} {
		if got := run(env, c.args...); got.code != 0 {
			t.Fatalf("run %d: code %d stderr %q, want 0", i+1, got.code, got.stderr)
		}
		tree := readTree(t, out)
		for _, p := range c.have {
			if _, ok := tree[p]; !ok {
				t.Errorf("run %d: OUT has no %s", i+1, p)
			}
		}
		for _, p := range c.stale {
			if _, ok := tree[p]; ok {
				t.Errorf("run %d: OUT still has stale %s", i+1, p)
			}
		}
	}
}

// shortcodeVault has a note whose prose, code fence and multi-line shortcode hold Hugo shortcodes, and a note with one in its title that links it.
func shortcodeVault(t *testing.T) map[string]string {
	t.Helper()
	return with_(t, map[string]string{
		"notes/not001.md": doc("Leaders are unique", "note",
			hubLine+"\n\nProse {{< figure src=\"x\" >}} and {{% note %}} end.\n\n```\n{{% note %}}\n```\n\n{{< highlight go\nlinenos=true >}}\ncode\n{{< /highlight >}}"),
		"notes/not002.md": doc("Zed {{% b %}} note", "note", hubLine+"\n\nBack to [[not001]]."),
	})
}

func TestExportHugoEscapesShortcodes(t *testing.T) {
	tree := exportHugoOK(t, shortcodeVault(t))
	not001 := pageBody(t, tree["content/notes/not001.md"])
	for _, want := range []string{
		"Prose {{</* figure src=\"x\" */>}} and {{%/* note */%}} end.",
		"```\n{{%/* note */%}}\n```",
		"{{</* highlight go\nlinenos=true */>}}\ncode\n{{</* /highlight */>}}",
		"- [Zed {{%/* b */%}} note](not002.md)\n",
	} {
		if !strings.Contains(not001, want) {
			t.Errorf("content/notes/not001.md body = %q, want it to contain %q", not001, want)
		}
	}
	if strings.Contains(not001, "{{< ") || strings.Contains(not001, "{{% ") {
		t.Errorf("content/notes/not001.md body = %q, want no live shortcode", not001)
	}
	if want := "---\ntitle: Zed {{% b %}} note\ndate: 2026-01-02T03:04:05Z\nzk_kind: note\n"; !strings.HasPrefix(tree["content/notes/not002.md"], want) {
		t.Errorf("content/notes/not002.md = %q, want the frontmatter title unchanged: %q", tree["content/notes/not002.md"], want)
	}
}

func TestExportGenericKeepsShortcodes(t *testing.T) {
	tree := exportOK(t, shortcodeVault(t))
	not001 := pageBody(t, tree["notes/not001.md"])
	for _, want := range []string{"{{< figure src=\"x\" >}} and {{% note %}} end.", "```\n{{% note %}}\n```", "{{< highlight go\nlinenos=true >}}", "- [Zed {{% b %}} note](not002.md)\n"} {
		if !strings.Contains(not001, want) {
			t.Errorf("notes/not001.md body = %q, want it to contain %q", not001, want)
		}
	}
}

func TestExportShortGenerator(t *testing.T) {
	short, long := t.TempDir(), t.TempDir()
	env := setupVault(t, "dev", baseVault(t))
	for _, c := range []struct{ out, flag string }{{short, "-g"}, {long, "--generator"}} {
		if got := run(env, "export", c.flag, "hugo", c.out); got.code != 0 {
			t.Fatalf("export %s hugo: code %d stderr %q, want 0", c.flag, got.code, got.stderr)
		}
	}
	got, want := readTree(t, short), readTree(t, long)
	if _, ok := got["content/_index.md"]; !ok || !maps.Equal(got, want) {
		t.Errorf("-g hugo wrote %v, want the same tree as --generator hugo (%v), with content/_index.md", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
}
