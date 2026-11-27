"""Exercise the public boundary check against disposable, synthetic Git history."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


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

    def event(self, code, name, sha, **body):
        path = self.root / "event.json"
        path.write_text(json.dumps(body))
        return self.check(code, "--github-event", env=dict(os.environ, GITHUB_EVENT_PATH=str(path),
                          GITHUB_EVENT_NAME=name, GITHUB_SHA=sha))

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
        self.assertIn("notes%3A%3Awarning%3A%3Atest", escaped)

    def test_public_vectors_have_exact_exceptions(self):
        sample = Path("testdata/protocol-v1.json")
        (self.repo / sample.parent).mkdir()
        source = CHECK.parents[1] / sample
        self.file(str(sample), source.read_text())
        self.check(0)
        self.file("testdata/new-vector.json", source.read_text())
        result = self.check(1)
        self.assertIn("generic-api-key", result)
        self.assertNotIn("boundary testdata/protocol-v1.json ", result)
        self.file(str(sample), source.read_text().replace('"key": "', '"key": "abcd', 1))
        self.assertIn("boundary testdata/protocol-v1.json ", self.check(1))

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

    def test_push_new_tag_and_missing_inputs(self):
        self.file("draft", PAGE)
        self.save("Draft")
        self.git("rm", "draft")
        head = self.save("Remove draft")
        self.event(1, "push", head, before=self.base, after=head)
        self.git("tag", "-am", "Synthetic tag", "v1")
        tag = self.git("rev-parse", "v1")
        self.event(1, "push", head, before="0" * 40, after=tag)
        self.event(2, "push", head, before="1" * 40, after=head)
        self.check(2, "--head", head)
        self.check(2, "--base", "HEAD~1", "--head", head)
        self.check(2, "--gitleaks", str(self.root / "unavailable"))


if __name__ == "__main__":
    unittest.main()
