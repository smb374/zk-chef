#!/usr/bin/env python3
"""Vault tooling: init, lint, fix, review. See docs/ and the vault-ops skill."""

import argparse
import hashlib
import io
import json
import mimetypes
import os
import platform
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import tomllib
import urllib.request
from collections import namedtuple
from datetime import UTC, datetime
from pathlib import Path

KIT = Path(__file__).resolve().parent.parent
ZK_API = "https://api.github.com/repos/zk-org/zk/releases/latest"
ZK_DEST = Path.home() / ".local" / "bin"
FOLDERS = ("notes", "hubs", "sources", "projects", "raw", "keys")


# ---------------------------------------------------------------- zk install


def pick_asset(assets, system, machine):
    """Return the release asset for this OS and CPU, or None."""
    os_name = {"Linux": "linux", "Darwin": "macos"}.get(system)
    arch = {
        ("linux", "x86_64"): "amd64",
        ("linux", "aarch64"): "arm64",
        ("linux", "arm64"): "arm64",
        ("linux", "i686"): "i386",
        ("macos", "arm64"): "arm64",
        ("macos", "x86_64"): "x86_64",
    }.get((os_name, machine))
    if not arch:
        return None
    suffix = f"-{os_name}-{arch}.tar.gz"
    return next((a for a in assets if a["name"].endswith(suffix)), None)


def install_zk(dest=ZK_DEST, urlopen=urllib.request.urlopen):
    """Download the latest zk release, check its sha256, install to dest/zk."""
    with urlopen(ZK_API) as r:
        release = json.load(r)
    asset = pick_asset(release["assets"], platform.system(), platform.machine())
    if not asset:
        raise RuntimeError(f"no zk build for {platform.system()} {platform.machine()}")
    want = (asset.get("digest") or "").removeprefix("sha256:")
    if not want:
        raise RuntimeError("release lists no sha256 digest; refusing to install")
    with urlopen(asset["browser_download_url"]) as r:
        data = r.read()
    if hashlib.sha256(data).hexdigest() != want:
        raise RuntimeError("zk download does not match its sha256 digest")
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as tf:
        member = next(
            (m for m in tf.getmembers() if m.isfile() and Path(m.name).name == "zk"),
            None,
        )
        if not member:
            raise RuntimeError("zk binary not found in archive")
        binary = tf.extractfile(member).read()
    dest.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(dir=dest)
    with os.fdopen(fd, "wb") as f:
        f.write(binary)
    os.chmod(tmp, 0o755)
    os.replace(tmp, dest / "zk")
    return dest / "zk"


# ---------------------------------------------------------------------- init


def copy_missing(src, dst):
    """Copy files from src to dst, never overwriting. Return (created, differing)."""
    created, differing = [], []
    for p in sorted(src.rglob("*")):
        if not p.is_file() or "__pycache__" in p.parts:
            continue
        rel = p.relative_to(src)
        target = dst / rel
        if not target.exists():
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(p, target)
            created.append(rel)
        elif target.read_bytes() != p.read_bytes():
            differing.append(rel)
    return created, differing


def ask(question, yes, interactive):
    if yes:
        return True
    if not interactive:
        return False
    return input(f"{question} [y/N] ").strip().lower() in ("y", "yes")


def init(
    vault,
    *,
    yes=False,
    which=shutil.which,
    installer=install_zk,
    interactive=None,
    out=print,
):
    """Set up a vault in `vault`. Safe to run again. Return a list of problems."""
    interactive = sys.stdin.isatty() if interactive is None else interactive
    vault = Path(vault).resolve()
    scaffold = KIT / "scaffold"
    if not scaffold.is_dir():
        raise SystemExit(
            f"scaffold not found at {scaffold}; run init from the kit repo"
        )
    vault.mkdir(parents=True, exist_ok=True)
    problems = []

    if not (vault / ".git").exists():
        subprocess.run(["git", "init", "-q", str(vault)], check=True)
        out("git init")
    created, differing = copy_missing(scaffold, vault)
    for src, dst in (
        (KIT / "skills", vault / ".agents" / "skills"),
        (KIT / "scripts", vault / "scripts"),
    ):
        if src.is_dir():
            c, d = copy_missing(src, dst)
            created += [dst.relative_to(vault) / x for x in c]
            differing += [dst.relative_to(vault) / x for x in d]
    for f in FOLDERS:
        (vault / f).mkdir(exist_ok=True)
    hook = vault / ".githooks" / "pre-commit"
    if hook.exists():
        hook.chmod(hook.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    subprocess.run(
        ["git", "-C", str(vault), "config", "core.hooksPath", ".githooks"], check=True
    )
    for rel in created:
        out(f"created {rel}")
    for rel in differing:
        out(f"kept your {rel} (differs from the kit's copy)")

    if not which("zk"):
        if ask(f"zk is missing. Download it to {ZK_DEST}?", yes, interactive):
            try:
                out(f"installed {installer()}")
                if str(ZK_DEST) not in os.environ.get("PATH", "").split(os.pathsep):
                    out(f"add {ZK_DEST} to PATH")
            except (OSError, ValueError, RuntimeError, tarfile.TarError) as e:
                problems.append(f"zk install failed: {e}")
        else:
            problems.append(
                "zk missing; install it from https://github.com/zk-org/zk or rerun with --yes"
            )
    if not which("gpg"):
        problems.append("gpg missing; install GnuPG")
    try:
        import yaml  # noqa: F401
    except ImportError:
        problems.append("PyYAML missing; run: pip install pyyaml")
    for p in problems:
        out(f"PROBLEM: {p}")
    return problems


# ---------------------------------------------------------------------- lint

FOLDER_KIND = {"notes": "note", "hubs": "hub", "sources": "source", "projects": "log"}
REVIEWS = ("unreviewed", "reviewed", "disputed")
LOG_TYPES = ("decision", "meeting", "plan", "status")
LINK = re.compile(r"\[\[([^\]|]+)(?:\|([^\]]*))?\]\]")
CITE = re.compile(r"(?<!\[)\[(\d+)(?:,[^\]\n]*)?\](?![\]\(])")
MARKER = re.compile(r"\b(?:TODO|GAP|CONTRADICTION):")
SKIP_SECTIONS = (
    "Extract",
    "Review",
)  # unsigned or signed text from outside; not scanned

Finding = namedtuple("Finding", "code path line msg")


class Doc:
    """One markdown file: frontmatter plus body lines as (lineno, text, scan, section)."""

    def __init__(self, path, text):
        import yaml

        self.path, self.fm, self.err, self.lines = path, None, None, []
        m = re.match(r"---\n(.*?)\n---(?:\n|$)", text, re.DOTALL)
        if not m:
            self.err = "missing frontmatter"
            return
        try:
            self.fm = yaml.safe_load(m.group(1))
        except yaml.YAMLError as e:
            self.err = f"invalid YAML: {str(e).splitlines()[0]}"
        if self.err is None and not isinstance(self.fm, dict):
            self.err = "frontmatter is not a mapping"
        start = text[: m.end()].count("\n") + 1
        fenced, section = False, None
        for i, line in enumerate(text[m.end() :].split("\n")):
            if line.startswith(("```", "~~~")):
                fenced = not fenced
                self.lines.append((start + i, line, False, section))
                continue
            if not fenced and line.startswith("## "):
                section = line[3:].strip()
            self.lines.append(
                (start + i, line, not fenced and section not in SKIP_SECTIONS, section)
            )

    @property
    def id(self):
        return self.fm.get("id") if self.fm else None

    @property
    def kind(self):
        return self.fm.get("kind") if self.fm else None

    def scan(self):
        return [(n, t, s) for n, t, ok, s in self.lines if ok]


def check_frontmatter(d, add):
    fm, folder_kind = d.fm, FOLDER_KIND[d.path.split("/")[0]]
    for key in ("id", "title", "kind"):
        if not isinstance(fm.get(key), str) or not fm[key].strip():
            add(
                "E_FRONTMATTER",
                d.path,
                1,
                f"{key} must be a non-empty string (quote it in YAML)",
            )
    if d.kind not in FOLDER_KIND.values():
        add(
            "E_FRONTMATTER",
            d.path,
            1,
            f"kind {d.kind!r} is not note, hub, source or log",
        )
    elif d.kind != folder_kind:
        add(
            "E_WRONG_FOLDER",
            d.path,
            1,
            f"kind {d.kind} does not belong in {d.path.split('/')[0]}/",
        )
    if "status" in fm:
        if d.kind in ("hub", "log"):
            add("E_FRONTMATTER", d.path, 1, f"{d.kind} must not have status")
        elif fm["status"] != "superseded" or not isinstance(
            fm.get("superseded_by"), str
        ):
            add(
                "E_FRONTMATTER",
                d.path,
                1,
                "status must be 'superseded' with superseded_by: ID",
            )
    if d.kind == "source":
        for key in ("raw", "sha256"):
            if not isinstance(fm.get(key), str) or not fm[key]:
                add("E_FRONTMATTER", d.path, 1, f"source needs {key} (a string)")
        if fm.get("review") not in REVIEWS:
            add(
                "E_FRONTMATTER",
                d.path,
                1,
                f"review must be one of {', '.join(REVIEWS)}",
            )
    if d.kind == "log" and "type" in fm and fm["type"] not in LOG_TYPES:
        add("E_FRONTMATTER", d.path, 1, f"type must be one of {', '.join(LOG_TYPES)}")


def check_citations(d, add):
    """Claim notes only: hubs make no claims, sources paraphrase, logs are exempt."""
    h2s = [(n, t[3:].strip()) for n, t, ok, _ in d.lines if ok and t.startswith("## ")]
    refs = [n for n, name in h2s if name == "References"]
    if refs and (len(refs) > 1 or h2s[-1][1] != "References"):
        add(
            "E_CITATION",
            d.path,
            refs[0],
            "References must be one heading and the last H2",
        )
    cites = [
        (n, int(m[1]))
        for n, t, s in d.scan()
        if s != "References"
        for m in CITE.finditer(t)
    ]
    entries = [
        (n, int(m[1]))
        for n, t, s in d.scan()
        if s == "References"
        if (m := re.match(r"(\d+)\. ", t))
    ]
    if cites and not refs:
        add(
            "E_CITATION",
            d.path,
            cites[0][0],
            f"citation [{cites[0][1]}] but no ## References",
        )
    nums = [k for _, k in entries]
    if nums != list(range(1, len(nums) + 1)):
        add(
            "E_CITATION",
            d.path,
            entries[0][0],
            "References numbering must run 1, 2, 3... without gaps",
        )
    first = list(dict.fromkeys(k for _, k in cites))
    if first != list(range(1, len(first) + 1)):
        add(
            "E_CITATION",
            d.path,
            cites[0][0],
            "citations must first appear in order 1, 2, 3...",
        )
    for n, k in cites:
        if k not in nums:
            add("E_CITATION", d.path, n, f"[{k}] has no References entry")
    for n, k in entries:
        if k not in {c for _, c in cites}:
            add("E_CITATION", d.path, n, f"References entry {k} is never cited")


FENCE = re.compile(r"^(?:```|~~~)")
SIGNED_BEGIN = "-----BEGIN PGP SIGNED MESSAGE-----"
MESSAGE_KEYS = ("path", "sha256", "verdict", "date")


def norm_fpr(s):
    return s.replace(" ", "").upper()


class Verifier:
    """Verify clearsigned blocks with a temporary GNUPGHOME holding only keys/."""

    def __init__(self, files, cfg):
        self.files, self.home = files, None
        self.pinned = {norm_fpr(f) for f in cfg.get("pinned_fingerprints", [])}

    def _env(self):
        if self.home is None:
            self.home = tempfile.mkdtemp(prefix="vault-gpg-")
            for path, data in self.files.items():
                if path.startswith("keys/"):
                    subprocess.run(
                        ["gpg", "--batch", "--no-tty", "--import"],
                        input=data,
                        capture_output=True,
                        env={**os.environ, "GNUPGHOME": self.home},
                        check=False,
                    )
        return {**os.environ, "GNUPGHOME": self.home}

    def verify(self, block):
        """Return (signed message text, None) or (None, error)."""
        r = subprocess.run(
            ["gpg", "--batch", "--no-tty", "--status-fd", "2", "--decrypt"],
            input=block.encode(),
            capture_output=True,
            env=self._env(),
            check=False,
        )
        valid = [
            ln.split()
            for ln in r.stderr.decode(errors="replace").splitlines()
            if ln.startswith("[GNUPG:] VALIDSIG")
        ]
        if r.returncode != 0 or not valid:
            return None, "signature does not verify against keys/"
        signer = {norm_fpr(valid[0][2]), norm_fpr(valid[0][-1])}
        if not signer & self.pinned:
            return None, f"signer {valid[0][-1]} is not pinned in .vault/config.toml"
        return r.stdout.decode(), None

    def close(self):
        if self.home:
            subprocess.run(
                ["gpgconf", "--homedir", self.home, "--kill", "all"],
                capture_output=True,
                check=False,
            )
            shutil.rmtree(self.home, ignore_errors=True)


def review_blocks(d):
    """Signed blocks in the ## Review section, newest first: [(lineno, text)]."""
    blocks, cur = [], None
    for n, t, _, section in d.lines:
        if section != "Review":
            continue
        if FENCE.match(t):
            if cur is None:
                cur = (n, [])
            else:
                blocks.append((cur[0], "\n".join(cur[1]) + "\n"))
                cur = None
        elif cur is not None:
            cur[1].append(t)
    return [b for b in blocks if SIGNED_BEGIN in b[1]]


def parse_message(text):
    lines = text.strip().splitlines()
    if not lines or lines[0] != "vault-review v1":
        return None
    fields = dict(ln.split(": ", 1) for ln in lines[1:] if ": " in ln)
    return fields if all(k in fields for k in MESSAGE_KEYS) else None


def check_review(d, files, verifier, add):
    messages = []
    for n, block in review_blocks(d):
        text, err = verifier.verify(block)
        fields = parse_message(text) if text else None
        if err or fields is None:
            add(
                "E_SIGNATURE",
                d.path,
                n,
                err or "signed text is not a vault-review v1 message",
            )
        messages.append(fields)
    if d.fm["review"] == "unreviewed":
        return  # older blocks may cover older hashes
    latest = messages[0] if messages else None
    if latest is None:
        if not messages:
            add(
                "E_REVIEW_MISMATCH",
                d.path,
                1,
                f"review is {d.fm['review']} but no signed block exists",
            )
        return  # a bad latest block is already E_SIGNATURE
    actual = (
        hashlib.sha256(files[d.fm["raw"]]).hexdigest() if d.fm["raw"] in files else None
    )
    want = {"path": d.fm["raw"], "sha256": d.fm["sha256"], "verdict": d.fm["review"]}
    for key, value in want.items():
        if latest[key] != value:
            add(
                "E_REVIEW_MISMATCH",
                d.path,
                1,
                f"latest block {key} {latest[key]!r} differs from note {value!r}",
            )
    if actual and latest["sha256"] != actual:
        add(
            "E_REVIEW_MISMATCH",
            d.path,
            1,
            "latest block sha256 differs from the raw file",
        )


def parse_docs(files):
    return [
        Doc(p, b.decode("utf-8", "replace"))
        for p, b in sorted(files.items())
        if p.endswith(".md") and p.count("/") == 1 and p.split("/")[0] in FOLDER_KIND
    ]


def lint_vault(files, cfg=None):
    """files maps vault-relative paths to bytes. Return a sorted list of Findings."""
    out = []

    def add(code, path, line, msg):
        out.append(Finding(code, path, line, msg))

    cfg = cfg or tomllib.loads(files[".vault/config.toml"].decode())
    verifier = Verifier(files, cfg)
    docs = parse_docs(files)
    good = []
    for d in docs:
        if d.err:
            add("E_FRONTMATTER", d.path, 1, d.err)
            continue
        n = len(out)
        check_frontmatter(d, add)
        if len(out) == n:
            good.append(d)
    by_id = {}
    for d in good:
        stem = d.path.rsplit("/", 1)[1][:-3]
        if d.id != stem:
            add("E_DUPLICATE_ID", d.path, 1, f"id {d.id} differs from filename {stem}")
        if d.id in by_id:
            add(
                "E_DUPLICATE_ID",
                d.path,
                1,
                f"id {d.id} also used by {by_id[d.id].path}",
            )
        by_id.setdefault(d.id, d)
    for d in good:
        if "superseded_by" in d.fm and d.fm["superseded_by"] not in by_id:
            add(
                "E_FRONTMATTER",
                d.path,
                1,
                f"superseded_by {d.fm['superseded_by']} does not exist",
            )

    index_id, edges = cfg["index_id"], {}
    if index_id not in by_id:
        add("E_UNREACHABLE", ".vault/config.toml", 1, f"index hub {index_id} not found")
    for d in good:
        live = d.fm.get("status") != "superseded"
        body_links = 0
        for n, t, s in d.scan():
            for m in LINK.finditer(t):
                target = by_id.get(m[1].strip())
                if not target:
                    add("E_BROKEN_LINK", d.path, n, f"no note with id {m[1]}")
                    continue
                if m[2] is not None and m[2] != target.fm["title"]:
                    add(
                        "W_STALE_ALIAS",
                        d.path,
                        n,
                        f"alias {m[2]!r} differs from title {target.fm['title']!r}",
                    )
                if live and target.fm.get("status") == "superseded":
                    add(
                        "W_SUPERSEDED_LINK",
                        d.path,
                        n,
                        f"links {target.id}, superseded by {target.fm['superseded_by']}",
                    )
                if n != d.lines[0][0]:
                    body_links += 1
            if MARKER.search(t):
                add("W_MARKER", d.path, n, MARKER.search(t)[0])
        if d.kind == "note" and body_links == 0:
            add(
                "W_DEAD_END",
                d.path,
                d.lines[0][0],
                "links nothing besides its Hubs line",
            )
        if d.kind == "note":
            check_citations(d, add)
            words = sum(len(t.split()) for _, t, s in d.scan() if s != "References")
            if words > cfg.get("word_count_warn", 400):
                add("W_WORD_COUNT", d.path, d.lines[0][0], f"{words} words")
        if d.id == index_id:
            continue
        first_no, first = d.lines[0][0], d.lines[0][1].rstrip()
        parts = first[6:].split(" · ") if first.startswith("Hubs: ") else None
        links = [LINK.fullmatch(p) for p in parts] if parts else [None]
        if None in links:
            add(
                "E_HUBS_LINE",
                d.path,
                first_no,
                "first body line must be: Hubs: [[ID|Hub]] · [[ID|Hub]]",
            )
            continue
        for m in links:
            target = by_id.get(m[1].strip())
            if target and (target.kind != "hub" or target.id == d.id):
                add("E_HUBS_LINE", d.path, first_no, f"{m[1]} is not another hub")
            elif target:
                edges.setdefault(target.id, set()).add(d.id)  # hub -> its members
    reach, todo = {index_id}, [index_id]
    while todo:
        for member in edges.get(todo.pop(), ()):
            if member not in reach:
                reach.add(member)
                todo.append(member)
    for d in good:
        if d.id not in reach:
            add(
                "E_UNREACHABLE",
                d.path,
                1,
                "does not reach the Index hub through Hubs lines",
            )

    raw_paths = set()
    for d in good:
        if d.kind != "source":
            continue
        raw, want = d.fm["raw"], d.fm["sha256"]
        raw_paths.add(raw)
        if not raw.startswith(f"raw/{d.id}-"):
            add("E_FRONTMATTER", d.path, 1, f"raw must be raw/{d.id}-<name>")
        if raw not in files:
            add("E_SHA256", d.path, 1, f"raw file {raw} is missing")
        elif hashlib.sha256(files[raw]).hexdigest() != want:
            add("E_SHA256", d.path, 1, f"{raw} does not match sha256 in the note")
        check_review(d, files, verifier, add)
    for p in files:
        if (
            p.startswith("raw/")
            and not Path(p).name.startswith(".")
            and p not in raw_paths
        ):
            add("E_RAW_ORPHAN", p, 1, "no source note names this file")
    verifier.close()
    return sorted(out, key=lambda f: (f.path, f.line, f.code))


def load_worktree(root):
    files = {}
    for folder in (*FOLDER_KIND, "raw", "keys"):
        for p in sorted((root / folder).rglob("*")):
            if p.is_file():
                files[p.relative_to(root).as_posix()] = p.read_bytes()
    cfg = root / ".vault" / "config.toml"
    if cfg.is_file():
        files[".vault/config.toml"] = cfg.read_bytes()
    return files


def load_staged(root):
    """Read the staged tree (git index) through one git cat-file process."""
    ls = subprocess.run(
        ["git", "-C", str(root), "ls-files", "-z"], capture_output=True, check=True
    ).stdout
    names = [
        n
        for n in ls.decode().split("\0")
        if n.split("/")[0] in (*FOLDER_KIND, "raw", "keys") or n == ".vault/config.toml"
    ]
    req = "".join(f":{n}\n" for n in names).encode()
    data = subprocess.run(
        ["git", "-C", str(root), "cat-file", "--batch"],
        input=req,
        capture_output=True,
        check=True,
    ).stdout
    files, pos = {}, 0
    for n in names:
        eol = data.index(b"\n", pos)
        size = int(data[pos:eol].split()[2])
        files[n] = data[eol + 1 : eol + 1 + size]
        pos = eol + 1 + size + 1
    return files


def lint(root, staged=False, out=print):
    root = Path(root).resolve()
    files = load_staged(root) if staged else load_worktree(root)
    if ".vault/config.toml" not in files:
        raise SystemExit(f"{root}: no .vault/config.toml; run vault.py init first")
    found = lint_vault(files)
    for f in found:
        out(f"{f.path}:{f.line}: {f.code} {f.msg}")
    errors = sum(f.code.startswith("E_") for f in found)
    out(f"{errors} errors, {len(found) - errors} warnings")
    return errors


def fix(root, out=print):
    """Rewrite stale link aliases in place to the targets' current titles."""
    root = Path(root).resolve()
    docs = [d for d in parse_docs(load_worktree(root)) if not d.err]
    titles = {
        d.id: d.fm["title"]
        for d in docs
        if isinstance(d.id, str) and isinstance(d.fm.get("title"), str)
    }

    def current(m):
        title = titles.get(m[1].strip())
        if title is None or m[2] is None or m[2] == title:
            return m[0]
        return f"[[{m[1]}|{title}]]"

    changed = 0
    for d in docs:
        text = (root / d.path).read_text(encoding="utf-8")
        lines = text.split("\n")
        for n, t, _ in d.scan():
            lines[n - 1] = LINK.sub(current, t)
        new = "\n".join(lines)
        if new != text:
            (root / d.path).write_text(new, encoding="utf-8")
            out(f"fixed {d.path}")
            changed += 1
    return changed


def review(root, src_id, verdict, *, key=None, yes=False, commit=True, out=print):
    """Human only: sign a verdict on a source's raw file and record it in the note."""
    root = Path(root).resolve()
    if verdict not in ("reviewed", "disputed"):
        raise SystemExit("verdict must be reviewed or disputed")
    note = root / "sources" / f"{src_id}.md"
    if not note.is_file():
        raise SystemExit(f"no source note {note}")
    text = note.read_text(encoding="utf-8")
    d = Doc(f"sources/{src_id}.md", text)
    if d.err or d.kind != "source":
        raise SystemExit(f"{note} is not a valid source note: {d.err}")
    raw = root / d.fm["raw"]
    if not raw.is_file():
        raise SystemExit(f"raw file {raw} is missing")
    sha = hashlib.sha256(raw.read_bytes()).hexdigest()
    if sha != d.fm["sha256"]:
        raise SystemExit(
            "raw file differs from the sha256 in the note; fix the note first"
        )
    out(f"path:   {d.fm['raw']}")
    out(f"size:   {raw.stat().st_size} bytes")
    out(f"type:   {mimetypes.guess_type(raw.name)[0] or 'unknown'}")
    out(f"sha256: {sha}")
    if not yes:
        if input("Open the file to inspect it? [y/N] ").strip().lower() == "y":
            opener = "open" if platform.system() == "Darwin" else "xdg-open"
            subprocess.run([opener, str(raw)], check=False)
        if input(f"Sign verdict '{verdict}'? [y/N] ").strip().lower() != "y":
            raise SystemExit("aborted")
    cfg = tomllib.loads((root / ".vault" / "config.toml").read_text())
    pinned = cfg.get("pinned_fingerprints", [])
    key = key or (pinned[0] if len(pinned) == 1 else None)
    if not pinned:
        raise SystemExit(
            "no key pinned. Run: gpg --armor --export FPR > keys/me.asc, "
            "then add FPR to pinned_fingerprints in .vault/config.toml"
        )
    date = datetime.now(UTC).strftime("%Y-%m-%d")
    message = f"vault-review v1\npath: {d.fm['raw']}\nsha256: {sha}\nverdict: {verdict}\ndate: {date}\n"
    cmd = ["gpg", "--clearsign", "--armor", *(["--local-user", key] if key else [])]
    signed = subprocess.run(
        cmd, input=message.encode(), stdout=subprocess.PIPE, check=True
    ).stdout.decode()
    block = f"```\n{signed.rstrip()}\n```\n\n"
    text = re.sub(r"(?m)^review:.*$", f"review: {verdict}", text, count=1)
    if re.search(r"(?m)^## Review[ \t]*$", text):
        text = re.sub(
            r"(?m)^## Review[ \t]*\n\n?",
            lambda m: "## Review\n\n" + block,
            text,
            count=1,
        )
    else:
        text = text.rstrip("\n") + "\n\n## Review\n\n" + block
    note.write_text(text, encoding="utf-8")
    out(f"signed {src_id}: {verdict}")
    if commit:
        subprocess.run(["git", "-C", str(root), "add", d.path], check=True)
        subprocess.run(
            ["git", "-C", str(root), "commit", "-m", f"review: {src_id} {verdict}"],
            check=True,
        )


# ----------------------------------------------------------------------- cli


def main(argv=None):
    ap = argparse.ArgumentParser(prog="vault.py")
    sub = ap.add_subparsers(dest="cmd", required=True)
    p = sub.add_parser("init", help="set up a vault (run once per clone)")
    p.add_argument("dir", nargs="?", default=".")
    p.add_argument("-y", "--yes", action="store_true", help="answer yes to prompts")
    p = sub.add_parser("lint", help="check the vault's invariants")
    p.add_argument(
        "--staged", action="store_true", help="check the staged tree (hook mode)"
    )
    p.add_argument("--root", default=".")
    p = sub.add_parser("fix", help="rewrite stale link aliases")
    p.add_argument("--root", default=".")
    p = sub.add_parser("review", help="human only: sign a verdict on a source")
    p.add_argument("id")
    p.add_argument("verdict", choices=("reviewed", "disputed"))
    p.add_argument("--root", default=".")
    p.add_argument("--key", help="signing key (default: the one pinned key)")
    p.add_argument("-y", "--yes", action="store_true", help="skip prompts (tests)")
    p.add_argument("--no-commit", action="store_true")
    args = ap.parse_args(argv)
    if args.cmd == "init":
        return 1 if init(args.dir, yes=args.yes) else 0
    if args.cmd == "lint":
        return 1 if lint(args.root, args.staged) else 0
    if args.cmd == "fix":
        fix(args.root)
        return 0
    if args.cmd == "review":
        review(
            args.root,
            args.id,
            args.verdict,
            key=args.key,
            yes=args.yes,
            commit=not args.no_commit,
        )
        return 0


if __name__ == "__main__":
    sys.exit(main())
