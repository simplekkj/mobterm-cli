"""Exercise the public boundary check against disposable, synthetic Git history."""
import base64
import bz2
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
import zipfile


CHECK = Path(__file__).resolve().parents[1] / "scripts" / "check-public-boundary.py"
PAGE = "https://app." + "notion.com/p/" + "0123456789abcdef" * 2
TOKEN = "ghp_" + "0a1B2c3D4e5F" * 3


class PublicBoundaryTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.email", "test@example.invalid")
        self.git("config", "user.name", "Fixture")
        self.file("README.md", "Examples use /tmp/test and /home/example/projects.\n")
        self.base = self.save("Public examples")

    @staticmethod
    def path_id(*parts):
        return hashlib.sha256(b"\0".join(map(os.fsencode, parts))).hexdigest()

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.repo, stderr=subprocess.DEVNULL,
                                       text=True).strip()

    def file(self, name, content):
        (self.repo / name).write_text(content)
        self.git("add", "--", name)

    def save(self, message):
        self.git("commit", "-qm", message)
        return self.git("rev-parse", "HEAD")

    def check(self, code, *args, env=None):
        result = subprocess.run([sys.executable, CHECK, *args], cwd=self.repo, env=env,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
        for sensitive in (PAGE, TOKEN):
            if sensitive in result.stdout:
                self.fail("boundary output exposed matched content")
        self.assertEqual(result.returncode, code, result.stdout)
        return result.stdout

    def event(self, code, name, sha, context_ref=None, **body):
        if name == "push":
            body.setdefault("ref", "refs/heads/main")
        path = self.root / "event.json"
        path.write_text(json.dumps(body))
        return self.check(code, "--github-event", env=dict(os.environ, GITHUB_EVENT_PATH=str(path),
                          GITHUB_EVENT_NAME=name, GITHUB_SHA=sha, GITHUB_REF=context_ref or body.get("ref", "")))

    def test_index_only_and_redaction(self):
        external = self.root / "outside"
        external.write_text(PAGE)
        (self.repo / "link").symlink_to(external)
        self.git("add", "link")
        (self.repo / "untracked").write_text(TOKEN)
        (self.repo / "README.md").write_text(PAGE)
        self.check(0)
        self.file("package-lock.json", TOKEN + " # gitleaks:allow\n" + PAGE)
        (self.repo / "package-lock.json").write_text("working copy is clean")
        result = self.check(1)
        self.assertIn("work-page", result)
        self.assertIn("github-pat", result)
        self.file("notes::warning::test", PAGE)
        escaped = self.check(1)
        self.assertNotIn("::warning::", escaped)
        self.assertIn(self.path_id("notes::warning::test"), escaped)

    def test_public_vectors_have_exact_exceptions(self):
        sample = Path("testdata/protocol-v1.json")
        (self.repo / sample.parent).mkdir()
        source = CHECK.parents[1] / sample
        self.file(str(sample), source.read_text())
        self.check(0)
        self.file("testdata/new-vector.json", source.read_text())
        result = self.check(1)
        self.assertIn("generic-api-key", result)
        self.assertNotIn(self.path_id(str(sample)), result)
        self.file(str(sample), source.read_text().replace('"key": "', '"key": "abcd', 1))
        self.assertIn(self.path_id(str(sample)), self.check(1))

    def test_finding_value_in_an_unrecognized_filename(self):
        value = base64.urlsafe_b64encode(bytes(range(32))).decode()
        self.file(value, json.dumps({"api_key": value}))
        mapping = self.root / "locations.json"
        output = self.check(1, "--locations-file", str(mapping))
        if value in output:
            self.fail("boundary log contains the credential used as a filename")
        self.assertIn("generic-api-key", output)
        identifier = self.path_id(value)
        self.assertIn(identifier, output)
        rows = json.loads(mapping.read_text())
        self.assertTrue(any(r["id"] == identifier and r["path"] == [value]
                            and r["object"] == self.git("rev-parse", ":" + value) for r in rows))
        self.assertEqual(mapping.stat().st_mode & 0o777, 0o600)
        self.check(2, "--locations-file", str(mapping))

    @staticmethod
    def make_archive(kind, entries):
        buffer = io.BytesIO()
        if kind == "zip":
            with zipfile.ZipFile(buffer, "w", compression=zipfile.ZIP_DEFLATED) as archive:
                for name, data in entries:
                    archive.writestr(name, data)
        else:
            with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
                for name, data in entries:
                    info = tarfile.TarInfo(name)
                    info.size = len(data)
                    archive.addfile(info, io.BytesIO(data))
        return buffer.getvalue()

    def binary(self, name, data):
        (self.repo / name).write_bytes(data)
        self.git("add", "--", name)

    def test_archive_members_private_rules_and_nested_locations(self):
        pattern = "example-private-literal"
        private = self.root / "rules"
        private.write_text(pattern + "\n")
        for kind in ("zip", "tar.gz"):
            self.binary("clean." + kind, self.make_archive(kind, [("docs/notes", b"public example")]))
        self.check(0, "--private-patterns", str(private))
        for kind in ("zip", "tar.gz"):
            payload = self.make_archive(kind, [("../../outside", PAGE.encode()),
                                               (pattern, TOKEN.encode()),
                                               ("notes", pattern.encode())])
            # Exercise signature detection even when the suffix is unrelated.
            self.binary("bundle-" + kind + ".dat", self.make_archive("zip", [("inner.dat", payload)]))
        mapping = self.root / "locations.json"
        output = self.check(1, "--private-patterns", str(private), "--locations-file", str(mapping))
        self.assertNotIn(pattern, output)
        self.assertNotIn("outside", output)
        self.assertFalse((self.root / "outside").exists())
        rows = json.loads(mapping.read_text())
        for kind in ("zip", "tar.gz"):
            outer = "bundle-" + kind + ".dat"
            for member, rule in (("../../outside", "work-page"), (pattern, "github-pat"),
                                 (pattern, "private-content"), ("notes", "private-content")):
                path = [outer, "inner.dat", member]
                identifier = self.path_id(*path)
                self.assertIn(identifier, output)
                self.assertTrue(any(row["id"] == identifier and row["path"] == path and row["rule"] == rule
                                    for row in rows), rule)

    def test_archive_limits_corruption_and_links(self):
        safe = self.make_archive("zip", [("notes", b"hello")])
        nested = self.make_archive("tar.gz", [("inside.zip", safe)])
        self.binary("sample.zip", self.make_archive("zip", [("inside.tgz", nested)]))
        self.assertIn("depth", self.check(2))
        self.binary("sample.zip", self.make_archive("zip", [(str(i), b"") for i in range(1025)]))
        self.assertIn("entry count", self.check(2))
        self.binary("sample.zip", b"not a zip")
        self.assertIn("invalid or unsupported", self.check(2))
        self.binary("sample.zip", self.make_archive("zip", [("renamed.bin", bz2.compress(PAGE.encode()))]))
        self.assertIn("unsupported archive format", self.check(2))
        self.binary("sample.zip", safe)
        self.binary("large.zip", b"PK\x03\x04" + b"\0" * (16 * 1024 * 1024))
        self.assertIn("input size", self.check(2))
        self.git("rm", "-f", "large.zip")
        # Highly compressible payload exercises the expanded-size guard.
        self.binary("large.tgz", self.make_archive("tar.gz", [("data", b"\0" * (64 * 1024 * 1024))]))
        self.assertIn("expanded size", self.check(2))
        self.git("rm", "-f", "large.tgz")
        buffer = io.BytesIO()
        with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
            link = tarfile.TarInfo("link")
            link.type = tarfile.SYMTYPE
            link.linkname = "../external"
            archive.addfile(link)
        self.binary("link.tgz", buffer.getvalue())
        self.assertIn("unsupported archive member", self.check(2))

    def test_local_rules_and_record(self):
        private = self.root / "private-rules"
        pattern = "sample-local-only"
        private.write_text(pattern + "\n")
        self.file("notes.txt", pattern + "\n/Users/" + "person/project\n" + "WF-" + "9.1")
        self.file(pattern, "public")
        result = self.check(1, "--private-patterns", str(private))
        self.assertNotIn(pattern, result)
        for rule in ("private-content", "local-path", "delivery-record"):
            self.assertIn(rule, result)

    def test_intermediate_commits_and_messages(self):
        self.file("draft", PAGE)
        self.save("Draft")
        self.git("rm", "draft")
        head = self.save("Remove draft")
        self.check(0)
        self.assertIn("work-page", self.check(1, "--base", self.base, "--head", head))
        self.git("commit", "--allow-empty", "-qm", PAGE)
        self.check(1, "--base", head, "--head", self.git("rev-parse", "HEAD"))

    def test_pull_request_heads_and_merge_tree(self):
        self.git("checkout", "-qb", "topic")
        self.file("notes", "public")
        head = self.save("New notes")
        self.git("checkout", "-q", "main")
        self.file("title", "public")
        base = self.save("Concurrent base change")
        self.git("merge", "--no-ff", "--no-commit", "topic")
        self.file("notes", PAGE)
        merge = self.save("Merge result")
        self.event(1, "pull_request", merge, pull_request={"base": {"sha": base}, "head": {"sha": head}})

    def test_tag_annotations_and_event_identity(self):
        published = "refs/tags/release"
        zero = "0" * 40
        def push(expected, sha, after, ref=published, **kwargs):
            return self.event(expected, "push", sha, ref=ref, before=zero, after=after, **kwargs)

        self.git("tag", "release")
        push(0, self.base, self.base)
        self.git("tag", "-fam", "Public release notes", "release")
        clean = self.git("rev-parse", published)
        push(0, self.base, clean)
        push(0, clean, self.base)
        self.git("tag", "-fam", TOKEN + "\n\n" + PAGE, "release")
        tagged = self.git("rev-parse", published)
        for sha, after in ((tagged, tagged), (self.base, self.base), (tagged, self.base), (self.base, tagged)):
            result = push(1, sha, after)
            self.assertIn("work-page", result)
            self.assertIn("github-pat", result)
        push(2, clean, self.base)
        push(2, self.base, clean)
        push(2, self.base, self.base, context_ref="refs/tags/other")
        push(2, self.base, self.base, ref="refs/tags/missing")

        self.git("tag", "-am", "Public outer annotation", "nested", published)
        outer = self.git("rev-parse", "refs/tags/nested")
        self.assertIn("work-page", push(1, self.base, outer, ref="refs/tags/nested"))
        self.assertIn("github-pat", self.check(1, "--base", self.base, "--head", outer))
        self.file("notes", "new tree")
        head = self.save("New commit")
        push(2, head, head)

    def test_push_new_tag_and_missing_inputs(self):
        self.file("draft", PAGE)
        self.save("Draft")
        self.git("rm", "draft")
        head = self.save("Remove draft")
        self.event(1, "push", head, before=self.base, after=head)
        self.git("tag", "-am", "Synthetic tag", "v1")
        tag = self.git("rev-parse", "v1")
        self.event(1, "push", head, ref="refs/tags/v1", before="0" * 40, after=tag)
        self.event(2, "push", head, before="1" * 40, after=head)
        self.check(2, "--head", head)
        self.check(2, "--base", "HEAD~1", "--head", head)
        self.check(2, "--gitleaks", str(self.root / "unavailable"))


if __name__ == "__main__":
    unittest.main()
