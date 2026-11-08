#!/usr/bin/env python3
"""Self-check: python3 tests/test_vault.py"""

import hashlib
import io
import json
import os
import subprocess
import sys
import tarfile
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent / "scripts"))
import vault


def git_cfg(d, key):
    return subprocess.run(
        ["git", "-C", str(d), "config", key],
        check=False,
        capture_output=True,
        text=True,
    ).stdout.strip()


def test_init():
    with tempfile.TemporaryDirectory() as t:
        v = Path(t) / "v"
        quiet = lambda *_: None
        probs = vault.init(v, which=lambda n: "/bin/" + n, out=quiet)
        assert probs == [] or all("PyYAML" in p for p in probs), probs
        assert (v / ".zk/config.toml").is_file() and (v / "raw").is_dir()
        assert (v / "scripts/vault.py").is_file()
        assert git_cfg(v, "core.hooksPath") == ".githooks"
        assert (v / ".githooks/pre-commit").stat().st_mode & 0o100
        # second run keeps user edits and creates nothing
        (v / ".vault/config.toml").write_text("word_count_warn = 1\n")
        log = []
        vault.init(v, which=lambda n: "/bin/" + n, out=log.append)
        assert (v / ".vault/config.toml").read_text() == "word_count_warn = 1\n"
        assert not [l for l in log if l.startswith("created")], log
        assert any("kept your .vault/config.toml" in l for l in log), log


def test_init_zk_prompt():
    with tempfile.TemporaryDirectory() as t:
        calls = []
        which = lambda n: None if n == "zk" else "/bin/" + n
        quiet = lambda *_: None
        # non-interactive, no --yes: no install, problem reported
        probs = vault.init(
            Path(t) / "a",
            which=which,
            installer=lambda: calls.append(1),
            interactive=False,
            out=quiet,
        )
        assert not calls and any("zk missing" in p for p in probs), probs
        # --yes: installer runs
        vault.init(
            Path(t) / "b",
            yes=True,
            which=which,
            installer=lambda: calls.append(1) or Path("zk"),
            out=quiet,
        )
        assert calls == [1]

        # installer failure becomes a problem, not a crash
        def boom():
            raise RuntimeError("net down")

        probs = vault.init(
            Path(t) / "c", yes=True, which=which, installer=boom, out=quiet
        )
        assert any("net down" in p for p in probs), probs


def test_pick_asset():
    names = [
        "zk-v1-linux-amd64.tar.gz",
        "zk-v1-linux-arm64.tar.gz",
        "zk-v1-macos-arm64.tar.gz",
        "zk-v1-macos-x86_64.tar.gz",
    ]
    assets = [{"name": n} for n in names]
    assert vault.pick_asset(assets, "Linux", "x86_64")["name"] == names[0]
    assert vault.pick_asset(assets, "Linux", "aarch64")["name"] == names[1]
    assert vault.pick_asset(assets, "Darwin", "x86_64")["name"] == names[3]
    assert vault.pick_asset(assets, "Windows", "AMD64") is None


def fake_urlopen(tar_bytes, digest):
    import platform

    name = {"Linux": "linux", "Darwin": "macos"}[platform.system()]
    arch = {"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[platform.machine()]
    if name == "macos" and arch == "amd64":
        arch = "x86_64"
    release = {
        "assets": [
            {
                "name": f"zk-v1-{name}-{arch}.tar.gz",
                "browser_download_url": "dl",
                "digest": digest,
            }
        ]
    }

    def opener(url):
        return io.BytesIO(
            json.dumps(release).encode() if url == vault.ZK_API else tar_bytes
        )

    return opener


def test_install_zk():
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tf:
        data = b"#!/bin/sh\necho zk\n"
        info = tarfile.TarInfo("zk")
        info.size = len(data)
        tf.addfile(info, io.BytesIO(data))
    tar = buf.getvalue()
    good = "sha256:" + hashlib.sha256(tar).hexdigest()
    with tempfile.TemporaryDirectory() as t:
        dest = Path(t) / "bin"
        path = vault.install_zk(dest, fake_urlopen(tar, good))
        assert path.read_bytes() == data and path.stat().st_mode & 0o100
        for bad in ("sha256:" + "0" * 64, ""):
            try:
                vault.install_zk(Path(t) / "bin2", fake_urlopen(tar, bad))
            except RuntimeError:
                assert not (Path(t) / "bin2" / "zk").exists()
            else:
                raise AssertionError("bad digest accepted")


# ---------------------------------------------------------------- lint tests

IDX = "000000000000idx0"
HUB = f"Hubs: [[{IDX}|Index]]"


def doc(id_, title, kind, body, **extra):
    fm = "".join(f"{k}: {v}\n" for k, v in extra.items())
    return f"---\nid: {id_}\ntitle: {title}\nkind: {kind}\n{fm}---\n{body}\n".encode()


RAW = b"raw bytes\n\nwith newlines\n"
SHA = hashlib.sha256(RAW).hexdigest()


def base():
    files = vault.load_worktree(vault.KIT / "scaffold")
    files["sources/s1.md"] = doc(
        "s1",
        "Source one",
        "source",
        "Hubs: [[000000000000src0|Sources]]\n\nWhy.",
        raw="raw/s1-a.bin",
        sha256=SHA,
        review="unreviewed",
    )
    files["raw/s1-a.bin"] = RAW
    files["notes/n1.md"] = doc(
        "n1",
        "Leaders are unique",
        "note",
        f"{HUB}\n\nOne leader per term [1, p. 4].\n\n## References\n\n1. [[s1|Source one]]",
    )
    return files


def codes(files):
    return {f.code for f in vault.lint_vault(files)}


def with_(**changes):
    files = base()
    for path, value in changes.items():
        path = path.replace("__", "/").replace("_md", ".md")
        if value is None:
            del files[path]
        else:
            files[path] = value if isinstance(value, bytes) else value.encode()
    return files


def test_lint_clean():
    found = vault.lint_vault(base())
    assert found == [], found


def test_lint_errors():
    n = lambda body: doc("n2", "Two", "note", body)
    cases = {
        "E_BROKEN_LINK": with_(
            notes__n2_md=n(f"{HUB}\n\nSee [[nope|X]] and [[n1|Leaders are unique]].")
        ),
        "E_FRONTMATTER": with_(notes__n2_md="no frontmatter"),
        "E_WRONG_FOLDER": with_(notes__n2_md=doc("n2", "Two", "hub", HUB)),
        "E_DUPLICATE_ID": with_(notes__n2_md=doc("zz", "Two", "note", HUB)),
        "E_HUBS_LINE": with_(notes__n2_md=n("Intro\n\n[[n1|Leaders are unique]]")),
        "E_UNREACHABLE": with_(
            hubs__h1_md=doc("h1", "H1", "hub", "Hubs: [[h2|H2]]"),
            hubs__h2_md=doc("h2", "H2", "hub", "Hubs: [[h1|H1]]"),
            notes__n2_md=n("Hubs: [[h1|H1]]\n\n[[n1|Leaders are unique]]"),
        ),
        "E_CITATION": with_(
            notes__n2_md=n(
                f"{HUB}\n\nClaim [2].\n\n## References\n\n1. [[s1|Source one]]"
            )
        ),
        "E_RAW_ORPHAN": with_(**{"raw/stray.txt": b"x"}),
        "E_SHA256": with_(**{"raw/s1-a.bin": b"changed"}),
    }
    for code, files in cases.items():
        assert code in codes(files), (code, vault.lint_vault(files))
    # a Hubs line that links a non-hub is also E_HUBS_LINE
    assert "E_HUBS_LINE" in codes(
        with_(notes__n2_md=n("Hubs: [[n1|Leaders are unique]]\n\n[[n1|x]]"))
    )


def test_lint_citation_rules():
    def cite(body):
        return codes(with_(notes__n2_md=doc("n2", "Two", "note", f"{HUB}\n\n{body}")))

    ref = "\n\n## References\n\n"
    s1 = "[[s1|Source one]]"
    assert "E_CITATION" not in cite(f"A [1], B [2, p. 9].{ref}1. {s1}\n2. {s1}")
    assert "E_CITATION" in cite(
        f"A [2], B [1].{ref}1. {s1}\n2. {s1}"
    )  # first-use order
    assert "E_CITATION" in cite(f"A [1].{ref}1. {s1}\n2. {s1}")  # entry never cited
    assert "E_CITATION" in cite(
        f"A [1].{ref}1. {s1}\n\n## Later\n\ntext"
    )  # not last H2
    assert "E_CITATION" in cite("A [1].")  # no References
    assert "E_CITATION" not in cite("Plain text, no evidence.")
    assert "E_CITATION" not in cite("A [link](http://x) and [[n1|Leaders are unique]]")


def test_lint_warnings():
    n2 = lambda body: doc("n2", "Two", "note", f"{HUB}\n\n{body}")
    assert "W_STALE_ALIAS" in codes(with_(notes__n2_md=n2("See [[n1|Old title]].")))
    assert "W_DEAD_END" in codes(with_(notes__n2_md=n2("Nothing linked.")))
    assert "W_MARKER" in codes(
        with_(notes__n2_md=n2("TODO: later. [[n1|Leaders are unique]]"))
    )
    assert "W_WORD_COUNT" in codes(
        with_(notes__n2_md=n2("word " * 500 + "[[n1|Leaders are unique]]"))
    )
    old = doc(
        "n3",
        "Old",
        "note",
        f"{HUB}\n\n[[n1|Leaders are unique]]",
        status="superseded",
        superseded_by="n1",
    )
    found = codes(with_(notes__n3_md=old, notes__n2_md=n2("See [[n3|Old]].")))
    assert "W_SUPERSEDED_LINK" in found
    # Extract and code fences are not scanned
    src = doc(
        "s1",
        "Source one",
        "source",
        "Hubs: [[000000000000src0|Sources]]\n\n## Extract\n\nTODO: [[x]] [3]\n\n```\nGAP: [[y]]\n```",
        raw="raw/s1-a.bin",
        sha256=SHA,
        review="unreviewed",
    )
    assert vault.lint_vault(with_(sources__s1_md=src)) == []


def test_load_staged():
    with tempfile.TemporaryDirectory() as t:
        root = Path(t)
        files = base()
        for p, b in files.items():
            (root / p).parent.mkdir(parents=True, exist_ok=True)
            (root / p).write_bytes(b)
        subprocess.run(["git", "-C", t, "init", "-q"], check=True)
        subprocess.run(["git", "-C", t, "add", "."], check=True)
        (root / "notes/n1.md").write_text(
            "changed after staging"
        )  # staged tree must win
        staged = vault.load_staged(root)
        assert staged == files, set(staged) ^ set(files)
        assert vault.lint(root, staged=True, out=lambda *_: None) == 0


# ------------------------------------------------------ review, signatures, fix


def gpg_env(home):
    return {**os.environ, "GNUPGHOME": str(home)}


def make_key(home):
    home.chmod(0o700)
    run = lambda *a: subprocess.run(
        ["gpg", "--batch", *a], env=gpg_env(home), capture_output=True, check=True
    )
    run(
        "--passphrase",
        "",
        "--quick-gen-key",
        "vault-test@example.com",
        "default",
        "default",
        "never",
    )
    out = run("--with-colons", "--list-keys").stdout.decode()
    fpr = next(ln.split(":")[9] for ln in out.splitlines() if ln.startswith("fpr"))
    return fpr, run("--armor", "--export", fpr).stdout


def test_review_and_signatures():
    quiet = lambda *_: None
    with tempfile.TemporaryDirectory(dir="/tmp") as t:
        home, root = Path(t) / "g", Path(t) / "v"
        home.mkdir()
        fpr, pub = make_key(home)
        vault.init(root, which=lambda n: "/bin/" + n, out=quiet)
        (root / "keys/me.asc").write_bytes(pub)
        cfg = root / ".vault/config.toml"
        cfg.write_text(
            cfg.read_text().replace(
                "pinned_fingerprints = []", f'pinned_fingerprints = ["{fpr}"]'
            )
        )
        (root / "raw/s1-a.bin").write_bytes(RAW)
        (root / "sources/s1.md").write_bytes(
            doc(
                "s1",
                "S",
                "source",
                "Hubs: [[000000000000src0|Sources]]\n\nWhy.\n\n## Extract\n\ntext",
                raw="raw/s1-a.bin",
                sha256=SHA,
                review="unreviewed",
            )
        )
        lint = lambda mutate=None: codes_of(root, mutate)
        os.environ["GNUPGHOME"] = str(home)
        try:
            assert lint() == set()
            vault.review(root, "s1", "reviewed", yes=True, commit=False, out=quiet)
            assert lint() == set(), vault.lint_vault(vault.load_worktree(root))
            text = (root / "sources/s1.md").read_text()
            assert "review: reviewed" in text and text.index("## Extract") < text.index(
                "## Review"
            )

            def edit(subs):
                def mutate(files):
                    for path, (old, new) in subs.items():
                        files[path] = files[path].replace(old, new)

                return mutate

            assert "E_SHA256" in lint(
                edit({"raw/s1-a.bin": (b"raw", b"RAW")})
            ) and "E_REVIEW_MISMATCH" in lint(edit({"raw/s1-a.bin": (b"raw", b"RAW")}))
            assert lint(edit({".vault/config.toml": (fpr.encode(), b"0" * 40)})) >= {
                "E_SIGNATURE"
            }
            assert "E_SIGNATURE" in lint(lambda f: f.pop("keys/me.asc"))
            assert "E_SIGNATURE" in lint(
                edit({"sources/s1.md": (b"verdict: reviewed", b"verdict: disputed")})
            )
            assert "E_REVIEW_MISMATCH" in lint(
                edit({"sources/s1.md": (b"review: reviewed", b"review: disputed")})
            )
            # same-name raw update: new hash, unreviewed, old block kept
            new_sha = hashlib.sha256(b"new").hexdigest().encode()
            update = lambda files: (
                files.__setitem__("raw/s1-a.bin", b"new"),
                files.__setitem__(
                    "sources/s1.md",
                    # first match is the frontmatter; the signed block keeps the old hash
                    files["sources/s1.md"].replace(SHA.encode(), new_sha, 1),
                ),
            )
            assert (
                lint(
                    lambda f: (
                        update(f),
                        edit(
                            {
                                "sources/s1.md": (
                                    b"review: reviewed",
                                    b"review: unreviewed",
                                )
                            }
                        )(f),
                    )
                )
                == set()
            )
            assert "E_REVIEW_MISMATCH" in lint(
                update
            )  # claims reviewed with a stale block
            # second review goes first
            vault.review(root, "s1", "disputed", yes=True, commit=False, out=quiet)
            assert lint() == set()
            body = (root / "sources/s1.md").read_text()
            assert (
                body.count("BEGIN PGP SIGNED MESSAGE") == 2
                and "review: disputed" in body
            )

            # the hook accepts the signed commit
            git_env = {
                "GIT_AUTHOR_NAME": "t",
                "GIT_AUTHOR_EMAIL": "t@t",
                "GIT_COMMITTER_NAME": "t",
                "GIT_COMMITTER_EMAIL": "t@t",
            }
            os.environ.update(git_env)
            subprocess.run(["git", "-C", str(root), "add", "."], check=True)
            r = subprocess.run(
                ["git", "-C", str(root), "commit", "-qm", "review: s1"],
                capture_output=True,
                check=False,
            )
            assert r.returncode == 0, r.stdout + r.stderr
        finally:
            subprocess.run(
                ["gpgconf", "--homedir", str(home), "--kill", "all"], check=False
            )
            os.environ.pop("GNUPGHOME", None)


def codes_of(root, mutate=None):
    files = vault.load_worktree(root)
    if mutate:
        mutate(files)
    return {f.code for f in vault.lint_vault(files) if f.code.startswith("E_")}


def test_fix():
    with tempfile.TemporaryDirectory() as t:
        root = Path(t)
        for p, b in base().items():
            (root / p).parent.mkdir(parents=True, exist_ok=True)
            (root / p).write_bytes(b)
        (root / "notes/n2.md").write_bytes(
            doc(
                "n2",
                "Two",
                "note",
                f"{HUB}\n\nSee [[n1|Old title]] and [[n1]].\n\n```\n[[n1|Old title]]\n```",
            )
        )
        assert vault.fix(root, out=lambda *_: None) == 1
        text = (root / "notes/n2.md").read_text()
        assert (
            "[[n1|Leaders are unique]] and [[n1]]" in text
            and "[[n1|Old title]]\n```" in text
        )
        assert vault.fix(root, out=lambda *_: None) == 0


def test_skill_dry_run():
    """Follow the skills' commands in a real vault; lint reports 0 errors at each step."""
    quiet = lambda *_: None
    with tempfile.TemporaryDirectory(dir="/tmp") as t:
        home, root = Path(t) / "g", Path(t) / "v"
        home.mkdir()
        fpr, pub = make_key(home)
        vault.init(root, which=lambda n: "/bin/" + n, out=quiet)
        assert (root / "AGENTS.md").is_file()
        for skill in (
            "vault-ops/SKILL.md",
            "vault-ops/LINT.md",
            "vault-writing/SKILL.md",
        ):
            assert (root / ".agents/skills" / skill).is_file(), skill
        (root / "keys/me.asc").write_bytes(pub)
        cfg = root / ".vault/config.toml"
        cfg.write_text(
            cfg.read_text().replace(
                "pinned_fingerprints = []", f'pinned_fingerprints = ["{fpr}"]'
            )
        )

        def zk_new(folder, title):
            r = subprocess.run(
                ["zk", "new", folder, "--title", title, "--print-path", "--no-input"],
                cwd=root,
                capture_output=True,
                text=True,
                check=True,
            )
            return Path(r.stdout.strip())

        def lint_ok(step):
            found = vault.lint_vault(vault.load_worktree(root))
            errors = [f for f in found if f.code.startswith("E_")]
            assert not errors, (step, errors)

        def put(path, old, new, count=1):
            path.write_text(path.read_text().replace(old, new, count))

        os.environ["GNUPGHOME"] = str(home)
        try:
            # 1. ingest
            src = zk_new("sources", "Ongaro 2014")
            sid = src.stem
            raw = root / "raw" / f"{sid}-paper.txt"
            raw.write_bytes(b"one leader per term\n")
            put(src, f"raw/{sid}-NAME", f"raw/{sid}-paper.txt")
            put(
                src,
                "sha256: \n",
                f"sha256: {hashlib.sha256(raw.read_bytes()).hexdigest()}\n",
            )
            src.write_text(
                src.read_text()
                + "\nWhy: consensus background.\n\n- Leaders are unique per term (p. 4).\n\n## Extract\n\none leader per term\n"
            )
            lint_ok("ingest")
            # 2. review
            vault.review(root, sid, "reviewed", yes=True, commit=False, out=quiet)
            lint_ok("review")
            # 3. write a note with a citation and a hub link
            hub = f"[[{vault_cfg(root, 'sources_id')}|Sources]]"
            old = zk_new("notes", "Raft elects at most one leader per term")
            body = f"One leader per term [1, p. 4].\n\n## References\n\n1. [[{sid}|Ongaro 2014]]\n"
            put(old, "Hubs: \n", f"Hubs: {hub}\n\n{body}")
            lint_ok("note")
            # 4. supersede it
            new = zk_new(
                "notes",
                "Raft elects at most one leader per term except during partitions",
            )
            put(
                new,
                "Hubs: \n",
                f"Hubs: {hub}\n\nRefines [[{old.stem}|Raft elects at most one leader per term]]; one leader per term [1, p. 4].\n\n## References\n\n1. [[{sid}|Ongaro 2014]]\n",
            )
            put(
                old,
                "kind: note\n",
                f"kind: note\nstatus: superseded\nsuperseded_by: {new.stem}\n",
            )
            lint_ok("supersede")
            # 5. same-name raw update; old block stays
            raw.write_bytes(b"one leader per term, new edition\n")
            put(
                src,
                f"sha256: {first_sha(src)}",
                f"sha256: {hashlib.sha256(raw.read_bytes()).hexdigest()}",
            )
            put(src, "review: reviewed", "review: unreviewed")
            lint_ok("raw update")
            # 6. the hook accepts a commit
            os.environ.update(
                {
                    "GIT_AUTHOR_NAME": "t",
                    "GIT_AUTHOR_EMAIL": "t@t",
                    "GIT_COMMITTER_NAME": "t",
                    "GIT_COMMITTER_EMAIL": "t@t",
                }
            )
            subprocess.run(["git", "-C", str(root), "add", "."], check=True)
            r = subprocess.run(
                ["git", "-C", str(root), "commit", "-qm", "ingest: x"],
                capture_output=True,
                check=False,
            )
            assert r.returncode == 0, r.stdout + r.stderr
        finally:
            subprocess.run(
                ["gpgconf", "--homedir", str(home), "--kill", "all"], check=False
            )
            os.environ.pop("GNUPGHOME", None)


def vault_cfg(root, key):
    import tomllib

    return tomllib.loads((root / ".vault/config.toml").read_text())[key]


def first_sha(path):
    import re

    return re.search(r"(?m)^sha256: (\w+)$", path.read_text())[1]


if __name__ == "__main__":
    for name, fn in list(globals().items()):
        if name.startswith("test_"):
            fn()
            print("ok", name)
