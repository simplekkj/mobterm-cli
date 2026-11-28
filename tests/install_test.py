#!/usr/bin/env python3
"""Offline black-box installer tests; all network calls and HOME are isolated."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import platform
import shutil
import signal
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
API = "https://api.github.com/repos/example-org/renamed-cli"
LOCATOR = "https://api.github.com/repos/mobterm/mobterm-cli"
VERSION = "v1.2.3"  # Local fixture, never a published-version claim.
REQUEST_ID = "aca1c461-b782-4737-8a0f-b3c2ed009f83"
DIST = None
COMMIT = None


def executable_header(system, arch):
    if system == "linux":
        machine = 62 if arch == "amd64" else 183
        return b"\x7fELF\x02\x01\x01" + bytes(9) + bytes((2, 0, machine, 0))
    cpu = 7 if arch == "amd64" else 12
    return bytes.fromhex("cf fa ed fe") + bytes((cpu, 0, 0, 1)) + bytes(4) + bytes((2, 0, 0, 0)) + bytes(4)


PAYLOAD = executable_header("linux", "amd64")
MOCK_CURL = r'''
import hashlib, json, os, pathlib, sys
from urllib.parse import urlsplit

env = os.environ
args = sys.argv[1:]
url = args[-1]
assets = pathlib.Path(env["ASSETS"])
fixtures = pathlib.Path(env["RELEASE_FIXTURES"])
repo = json.loads((fixtures / "release-repository.json").read_text())
release = json.loads((fixtures / "release-stable.json").read_text())
original = "https://api.github.com/repos/mobterm/mobterm-cli"
canonical = "https://api.github.com/repos/" + repo["full_name"]
cdn = "https://release-assets.githubusercontent.com:443/github-production-release-asset/"
with open(env["REQUESTS"], "a") as log:
    log.write(url + "\n")
assert args[0] == "-q" and "--globoff" in args and "-L" not in args
assert args[args.index("--proto") + 1] == "=https"
headers = pathlib.Path(args[args.index("-D") + 1])
destination = pathlib.Path(args[args.index("-o") + 1])
headers.write_text("")
version = env["FIXTURE_VERSION"]
release["tag_name"] = version
for item in release["assets"]:
    item["name"] = item["name"].replace("v1.2.3", version)
    path = assets / item["name"]
    if path.exists() and env.get("INCLUDE_DIGEST"):
        item["digest"] = "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()
redirect = ""
status = 200
body = b""
if url in (original, canonical):
    kind = "locator" if url == original else "repository"
    if url == original and env.get("MOVED"):
        redirect = canonical
    if url == canonical and env.get("REUSED"):
        repo["id"] += 1
    body = json.dumps(repo).encode()
elif url.startswith(canonical + "/releases/assets/") or url.startswith(cdn):
    assert "Accept: application/octet-stream" in args
    asset_id = int(urlsplit(url).path.split("/")[-1])
    item = next(a for a in release["assets"] if a["id"] == asset_id)
    kind = item["name"]
    if env.get("CDN") and url.startswith(canonical):
        redirect = cdn + str(asset_id) + "?temporary=signature"
    else:
        body = (assets / kind).read_bytes()
elif url in (canonical + "/releases/latest", canonical + "/releases/tags/" + version):
    kind = "release"
    assert "Accept: application/vnd.github+json" in args
    body = json.dumps(release).encode()
else:
    raise ValueError(url)
if kind == env.get("OVERRIDE_KIND"):
    if "OVERRIDE_BODY" in env:
        body = pathlib.Path(env["OVERRIDE_BODY"]).read_bytes()
    if "OVERRIDE_REDIRECT" in env:
        redirect = env["OVERRIDE_REDIRECT"]
    if "OVERRIDE_STATUS" in env:
        status = int(env["OVERRIDE_STATUS"])
        headers.write_text(env.get("OVERRIDE_HEADERS", ""))
if env.get("HTTP_FAIL") in ("all", kind):
    sys.exit(7)
if redirect:
    status = 302
    body = b""
destination.write_bytes(body)
if kind == env.get("OVERRIDE_KIND") and "CURL_EXIT" in env:
    sys.exit(int(env["CURL_EXIT"]))
print(status)
print(redirect)
'''


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="mobterm-install-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.home = self.root / "home with spaces"
        self.bin = self.root / "tools"
        self.assets = self.root / "assets"
        self.work = self.root / "tmp"
        for directory in (self.home, self.bin, self.assets, self.work):
            directory.mkdir()
        for tool in ("awk", "tar", "mktemp", "mkdir", "rmdir", "cat", "chmod", "mv", "rm", "gzip",
                     "sha256sum", "shasum", "wc", "tr", "cmp", "date", "od"):
            source = shutil.which(tool)
            if source:
                (self.bin / tool).symlink_to(source)
        self.write_tool("curl", MOCK_CURL)
        self.write_tool("uname", 'import os, sys\nprint(os.environ["SYSTEM"] if sys.argv[1] == "-s" else os.environ["ARCH"])\n')
        self.env = dict(os.environ, HOME=str(self.home), TMPDIR=str(self.work),
                        PYTHONDONTWRITEBYTECODE="1",
                        PATH=str(self.bin), SYSTEM="Linux", ARCH="x86_64",
                        ASSETS=str(self.assets), FIXTURE_VERSION=VERSION,
                        RELEASE_FIXTURES=str(ROOT / "testdata"),
                        REQUESTS=str(self.root / "requests"),
                        CODEX_HOME=str(self.home / ".codex"),
                        CLAUDE_CONFIG_DIR=str(self.home / ".claude"),
                        MOBTERM_CONFIG_DIR=str(self.home / ".config/mobterm"))
        for name in (".codex/config.toml", ".claude/settings.json", ".config/mobterm/state.json",
                     ".ssh/config", ".profile", ".zshrc"):
            path = self.home / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"untouched test configuration\n")
            path.chmod(0o640)
        self.destination = self.home / ".local/bin/mobterm-cli"
        self.make_archive()

    def write_tool(self, name, code):
        path = self.bin / name
        if path.is_symlink():
            path.unlink()
        path.write_text("#!" + sys.executable + "\n" + code)
        path.chmod(0o755)

    def make_archive(self, entries=None, system="linux", arch="amd64"):
        name = f"mobterm-cli_{VERSION}_{system}_{arch}.tar.gz"
        with tarfile.open(self.assets / name, "w:gz", format=tarfile.USTAR_FORMAT) as archive:
            for filename, kind, data in entries or [("mobterm-cli", tarfile.REGTYPE, executable_header(system, arch)),
                                                    ("LICENSE", tarfile.REGTYPE, b"license")]:
                entry = tarfile.TarInfo(filename)
                entry.type, entry.mode = kind, 0o755
                if kind in (tarfile.SYMTYPE, tarfile.LNKTYPE):
                    entry.linkname = "LICENSE"
                else:
                    entry.size = len(data)
                archive.addfile(entry, io.BytesIO(data))
        self.update_checksums()

    def update_checksums(self):
        (self.assets / "checksums.txt").write_text("".join(
            f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n"
            for path in sorted(self.assets.glob("*.tar.gz"))))

    def old_binary(self):
        self.destination.parent.mkdir(parents=True, exist_ok=True)
        self.destination.write_bytes(b"old executable")
        self.destination.chmod(0o755)

    def home_snapshot(self):
        snapshot = {}
        for path in self.home.rglob("*"):
            mode = path.lstat().st_mode
            contents = (os.readlink(path) if stat.S_ISLNK(mode) else
                        path.read_bytes() if stat.S_ISREG(mode) else None)
            snapshot[path.relative_to(self.home).as_posix()] = (mode, contents)
        return snapshot

    def run_install(self, *args, success=True, stdin=False, extra_files=(), cleanup_expected=True):
        before = self.home_snapshot()
        command = [shutil.which("sh")]
        if stdin:
            command += ["-s", "--", *args]
        else:
            command += [str(ROOT / "install.sh"), *args]
        result = subprocess.run(command, env=self.env, input=(ROOT / "install.sh").read_bytes() if stdin else None,
                                capture_output=True, timeout=30)
        if success:
            self.assertEqual(result.returncode, 0, result.stderr.decode())
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout.decode())
        if cleanup_expected:
            self.assertEqual(list(self.work.iterdir()), [], "temporary downloads leaked")
            self.assertEqual({path.relative_to(self.home).as_posix()
                              for path in self.home.glob(".local/bin/.mobterm-cli-install.*")},
                             {name for name in before if name.startswith(".local/bin/.mobterm-cli-install.")},
                             "staged binary leaked or historical file removed")
        after = self.home_snapshot()
        for name, original in before.items():
            if success and name == ".local/bin/mobterm-cli":
                continue
            self.assertEqual(after.get(name), original, f"existing user path changed: {name}")
        self.assertLessEqual(set(after) - set(before),
                             {".local", ".local/bin", ".local/bin/mobterm-cli"} | set(extra_files),
                             "unexpected user path created")
        return result

    def assert_preserved(self, *args):
        result = self.run_install(*args, success=False)
        self.assertEqual(self.destination.read_bytes(), b"old executable")
        self.assertEqual(self.destination.stat().st_mode & 0o777, 0o755)
        return result

    def test_platforms_latest_fixed_and_repeat(self):
        for system, machine, target_system, target_arch in (
            ("Linux", "x86_64", "linux", "amd64"), ("Linux", "aarch64", "linux", "arm64"),
            ("Darwin", "x86_64", "darwin", "amd64"), ("Darwin", "arm64", "darwin", "arm64"),
            ("Linux", "arm64", "linux", "arm64")):
            with self.subTest(system=system, machine=machine):
                self.env.update(SYSTEM=system, ARCH=machine)
                self.make_archive(system=target_system, arch=target_arch)
                for args in ((), ("--version", VERSION), ("--version", VERSION)):
                    self.env["PATH"] = str(self.bin)  # PATH hint needed
                    result = self.run_install(*args, stdin=True)
                    output = result.stdout.decode()
                    self.assertIn(VERSION, output)
                    self.assertIn(str(self.destination), output)
                    self.assertIn('"' + str(self.destination) + '" version', output)
                    self.assertIn("PATH does not include", output)
                    self.assertEqual(self.destination.read_bytes(), executable_header(target_system, target_arch))
                    self.assertEqual(self.destination.stat().st_mode & 0o777, 0o755)
                    urls = Path(self.env["REQUESTS"]).read_text().splitlines()
                    asset_id = 101 + (2 if target_system == "darwin" else 0) + (target_arch == "arm64")
                    self.assertEqual(urls[-2:], [API + "/releases/assets/105", API + f"/releases/assets/{asset_id}"])
                self.env["PATH"] += ":" + str(self.destination.parent)
                self.assertNotIn("PATH does not include", self.run_install("--version", VERSION).stdout.decode())

    def test_shasum_fallback(self):
        if not (self.bin / "shasum").exists():
            self.skipTest("shasum unavailable on this host")
        (self.bin / "sha256sum").unlink(missing_ok=True)
        self.run_install("--version", VERSION)

    def test_no_sha_tool(self):
        self.old_binary()
        for tool in ("sha256sum", "shasum"):
            (self.bin / tool).unlink(missing_ok=True)
        self.assert_preserved()
        self.assertFalse(Path(self.env["REQUESTS"]).exists())

    def test_invalid_arguments_and_versions(self):
        self.old_binary()
        for args in (("--version",), ("--pair", "secret"), ("--version", ""),
                     ("--version", "v01.2.3"), ("--version", "1.2.3"),
                     ("--version", "v1.2.3-rc1"), ("--version", "v1.2.3\nv2.3.4"),
                     ("--version", "v1.2.3; touch /tmp/unexpected"),
                     ("--version", VERSION, "--version", VERSION), ("--expected-release-id", "74"),
                     ("--version", VERSION, "--expected-release-id", "01"),
                     ("--version", VERSION, "--expected-release-id", "1e2"),
                     ("--request-id", "invalid"), ("--request-id", "-"*36),
                     ("--request-id", REQUEST_ID, "--request-id", REQUEST_ID)):
            with self.subTest(args=args):
                self.assert_preserved(*args)
        self.assertFalse(Path(self.env["REQUESTS"]).exists())

    def test_unknown_platforms(self):
        self.old_binary()
        for system, arch in (("FreeBSD", "x86_64"), ("Linux", "i686"), ("Darwin", "riscv64")):
            with self.subTest(system=system, arch=arch):
                self.env.update(SYSTEM=system, ARCH=arch)
                self.assert_preserved()
        self.assertFalse(Path(self.env["REQUESTS"]).exists())

    def test_http_failures(self):
        self.old_binary()
        for failure in ("all", "checksums.txt", f"mobterm-cli_{VERSION}_linux_amd64.tar.gz"):
            with self.subTest(failure=failure):
                self.env["HTTP_FAIL"] = failure
                self.assert_preserved()

    def assert_marker(self, phase, code):
        result = self.assert_preserved("--version", VERSION, "--expected-release-id", "74", "--request-id", REQUEST_ID)
        markers = [line for line in result.stderr.decode().splitlines() if line.startswith("MOBTERM_CLI_INSTALL_ERROR")]
        self.assertEqual(markers, [f"MOBTERM_CLI_INSTALL_ERROR_V1 {REQUEST_ID} {phase} {code}"])
        return result

    def override(self, kind, content):
        path = self.root / "metadata.json"
        path.write_bytes(content if isinstance(content, bytes) else json.dumps(content).encode())
        self.env.update(OVERRIDE_KIND=kind, OVERRIDE_BODY=str(path))

    def test_confirmed_release_after_transfer(self):
        self.env.update(MOVED="1", CDN="1", INCLUDE_DIGEST="1")
        result = self.run_install("--version", VERSION, "--request-id", REQUEST_ID, "--expected-release-id", "74", stdin=True)
        self.assertIn(f"Installed mobterm-cli {VERSION}", result.stdout.decode())
        requests = Path(self.env["REQUESTS"]).read_text().splitlines()
        self.assertEqual(requests[:2], [LOCATOR, API])
        self.assertNotIn(API + "/releases/latest", requests)
        self.assertIn(API + "/releases/tags/" + VERSION, requests)
        self.assertIn("https://release-assets.githubusercontent.com:443/github-production-release-asset/101?temporary=signature", requests)
        self.assertNotIn(b"signature", result.stdout + result.stderr)

    def test_release_identity_and_schema(self):
        self.old_binary()
        release = json.loads((ROOT / "testdata/release-stable.json").read_text())
        for field, val, code in (
                ("url", API + "/releases/75", "source_mismatch"),
                ("id", 75, "source_mismatch"), ("tag_name", "v1.2.4", "source_mismatch"),
                ("draft", True, "release_unavailable"), ("prerelease", True, "release_unavailable"),
                ("draft", 0, "metadata"), ("assets", {}, "metadata")):
            with self.subTest(field=field, val=val):
                doc = dict(release, **{field: val})
                if field == "id":
                    doc["url"] = API + "/releases/75"
                self.override("release", doc)
                self.assert_marker("before_replace", code)
        for field in ("draft", "prerelease", "id"):
            with self.subTest(absent=field):
                self.override("release", {key: val for key, val in release.items() if key != field})
                self.assert_marker("before_replace", "metadata")
        for raw in (b'{"id":1416491919,"\\u0069d":1416491919}', b'{"owner":{"id":1416491919}}',
                    b'{"id":true,"full_name":"example-org/renamed-cli"}',
                    b'{"id":"1416491919","full_name":"example-org/renamed-cli"}',
                    b'{"id":', b'{"nested":{"id":10}, "id": }',
                    b'[]', b'{"oops":"\\u0xx0"}', b'{"bad":"\xff"}',
                    b'{"id":1416491919,"full_name":"example-org/renamed-cli"}\x00extra'):
            with self.subTest(raw=raw[:60]):
                self.override("locator", raw)
                self.assert_marker("before_replace", "metadata")
        self.override("locator", b'{"notes":[{"id":"ignore me"}],"id":17,"owner":{"id":1416491919},"full_name":"example-org/renamed-cli"}')
        self.assert_marker("before_replace", "source_mismatch")
        self.override("locator", b'{"\\u0069d":1416491919,"notes":["\\uD83D\\uDE00",true,null,1.0e2,{"assets":7}], "full_name":"example-org\\/renamed-cli"}')
        self.run_install("--version", VERSION)
        self.env["REUSED"] = "1"
        self.old_binary()
        self.assert_marker("before_replace", "source_mismatch")

    def test_download_size_failure_codes(self):
        self.old_binary()
        for kind, limit, code in (("locator", 262144, "metadata"), ("checksums.txt", 16384, "integrity")):
            for count, exit_code, expected in (
                    (limit + 1, 0, code), (2 * limit + 1, 23, code),
                    (0, 63, code), (limit, 23, "network")):
                with self.subTest(kind=kind, count=count, exit_code=exit_code):
                    self.override(kind, b" " * count)
                    self.env["CURL_EXIT"] = str(exit_code)
                    self.assert_marker("before_replace", expected)

    def test_release_assets_and_digests(self):
        self.old_binary()
        for key, val, code in (
                ("url", "https://api.github.com/repos/other/cli/releases/assets/101", "source_mismatch"),
                ("url", API + "/releases/assets/102", "source_mismatch"),
                ("id", "101", "metadata"), ("id", 0, "metadata"),
                ("name", "checksums.txt", "metadata"), ("name", "other.tar.gz", "metadata"),
                ("digest", "sha256:bad", "metadata"), ("digest", "sha256:" + "0"*64, "integrity")):
            with self.subTest(key=key, val=val):
                doc = json.loads((ROOT / "testdata/release-stable.json").read_text())
                doc["assets"][0][key] = val
                self.override("release", doc)
                self.assert_marker("before_replace", code)
        doc = json.loads((ROOT / "testdata/release-stable.json").read_text())
        doc["assets"][-1]["digest"] = "sha256:" + "1"*64
        self.override("release", doc)
        self.assert_marker("before_replace", "integrity")

    def test_each_redirect_and_http_error(self):
        self.old_binary()
        for kind, url in (
                ("release", "https://objects.githubusercontent.com/meta"),
                ("locator", "https://github.com/new/repo"),
                ("checksums.txt", "http://github.com/pkg"),
                ("checksums.txt", "https://api.github.com@elsewhere.invalid/pkg"),
                ("checksums.txt", "https://user@github.com/pkg"),
                ("checksums.txt", "https://example.invalid/pkg?temporary=secret"),
                ("checksums.txt", "https://api.github.com:444/pkg"),
                ("checksums.txt", API + "/releases/assets/105")):
            with self.subTest(kind=kind, redirect=url):
                self.env.update(OVERRIDE_KIND=kind, OVERRIDE_REDIRECT=url)
                self.assert_marker("before_replace", "source_mismatch")
                if url != API + "/releases/assets/105":
                    self.assertNotIn(url, Path(self.env["REQUESTS"]).read_text().splitlines())
        del self.env["OVERRIDE_REDIRECT"]
        for kind, status, headers, code in (
                ("locator", 404, "", "source_mismatch"), ("release", 404, "", "release_unavailable"),
                ("release", 429, "", "rate_limited"), ("release", 403, "X-RateLimit-Remaining: 0\r\n", "rate_limited"),
                ("release", 403, "", "network"), ("checksums.txt", 503, "", "network")):
            with self.subTest(kind=kind, status=status, headers=headers):
                self.env.update(OVERRIDE_KIND=kind, OVERRIDE_STATUS=str(status), OVERRIDE_HEADERS=headers)
                self.assert_marker("before_replace", code)

    def test_secondary_rate_limit_response(self):
        self.old_binary()
        self.env.update(OVERRIDE_STATUS="403", OVERRIDE_HEADERS="X-RateLimit-Remaining: 12\r\n")
        message = "You have exceeded a secondary rate limit"
        detail = "private-server-detail"
        body = json.dumps({"message": f"{message}. {detail}"}).encode()
        padded = body.ljust(8192)
        for response, expected in (
                (body, "rate_limited"), (padded, "rate_limited"),
                ({"message": "YOU HAVE TRIGGERED A SECONDARY RATE LIMIT"}, "rate_limited"),
                ({"message": "Access denied.", "documentation": message}, "network"),
                ({"error": {"message": message}}, "network"), ({"message": [message]}, "network"),
                ({"message": "See documentation: " + message}, "network"),
                ({"message": message + "er"}, "network"),
                (b'{"message":"denied","\\u006dessage":"' + message.encode() + b'"}', "network"),
                (body[:-1], "network"), (padded + b" ", "network")):
            with self.subTest(response=str(response)[:90], expected=expected):
                self.override("release", response)
                result = self.assert_marker("before_replace", expected)
                self.assertNotIn(detail.encode(), result.stdout + result.stderr)
        self.override("checksums.txt", body)
        self.assert_marker("before_replace", "rate_limited")
        self.env["CDN"] = "1"
        result = self.assert_marker("before_replace", "network")
        self.assertNotIn(detail.encode(), result.stdout + result.stderr)

    def test_wrong_platform_and_mv_phase(self):
        self.old_binary()
        self.make_archive([("mobterm-cli", tarfile.REGTYPE, executable_header("darwin", "amd64"))])
        self.assert_marker("before_replace", "integrity")
        self.make_archive()
        self.intercept_tool("mv", 'sys.exit(37)')
        self.assert_marker("unknown", "storage")

    def test_bad_checksums(self):
        self.old_binary()
        path = self.assets / "checksums.txt"
        good = path.read_text()
        name = f"mobterm-cli_{VERSION}_linux_amd64.tar.gz"
        for contents in ("", good + good, "0" * 64 + "  " + name + "\n",
                         "xyz  " + name + "\n", "a" * 63 + "  " + name + "\n",
                         good.strip() + " unexpected\n", good + "xyz  " + name + "\n"):
            with self.subTest(contents=contents):
                path.write_text(contents)
                self.assert_preserved()
        path.write_text(good.replace("  " + name, " *" + name))
        self.run_install()

    def test_unsafe_archives(self):
        self.old_binary()
        regular = ("mobterm-cli", tarfile.REGTYPE, PAYLOAD)
        for entries in (
            [("LICENSE", tarfile.REGTYPE, b"license")],
            [("mobterm-cli", tarfile.REGTYPE, b"")], [regular, regular],
            [regular, ("../escape", tarfile.REGTYPE, b"bad")],
            [regular, ("/absolute", tarfile.REGTYPE, b"bad")],
            [regular, ("nested/file", tarfile.REGTYPE, b"bad")],
            [regular, ("unexpected.sh", tarfile.REGTYPE, b"bad")],
            [("mobterm-cli", tarfile.SYMTYPE, b"")],
            [("mobterm-cli", tarfile.LNKTYPE, b"")],
            [("mobterm-cli", tarfile.DIRTYPE, b"")],
            [("mobterm-cli", tarfile.FIFOTYPE, b"")],
            [regular, ("LICENSE", tarfile.SYMTYPE, b"")]):
            with self.subTest(entries=[(name, kind) for name, kind, _ in entries]):
                self.make_archive(entries)
                self.assert_preserved()
        self.assertFalse((self.root / "escape").exists())

    def test_corrupt_archive(self):
        self.old_binary()
        path = self.assets / f"mobterm-cli_{VERSION}_linux_amd64.tar.gz"
        for data in (b"not gzip", path.read_bytes()[:40]):
            with self.subTest(data_length=len(data)):
                path.write_bytes(data)
                self.update_checksums()  # Integrity passes; archive validation must fail.
                self.assert_preserved()

    def test_destination_types(self):
        self.destination.parent.mkdir(parents=True)
        for target in (self.root / "missing", self.home / ".profile", self.work):
            with self.subTest(target=target):
                self.destination.symlink_to(target)
                self.run_install(success=False)
                self.assertTrue(self.destination.is_symlink())
                self.destination.unlink()
        self.destination.mkdir()
        self.run_install(success=False)
        self.destination.rmdir()
        os.mkfifo(self.destination)
        self.run_install(success=False)
        self.destination.unlink()
        self.destination.parent.rmdir()
        self.destination.parent.symlink_to(self.work, target_is_directory=True)
        self.run_install(success=False)
        self.assertTrue(self.destination.parent.is_symlink())

    def test_unwritable_paths(self):
        if os.geteuid() == 0:
            self.skipTest("root bypasses ordinary Unix write permissions")
        self.home.chmod(0o555)
        try:
            self.run_install(success=False)
            self.assertFalse((self.home / ".local").exists())
        finally:
            self.home.chmod(0o755)
        self.old_binary()
        self.destination.parent.chmod(0o555)
        try:
            self.assert_preserved()
        finally:
            self.destination.parent.chmod(0o755)
        self.destination.chmod(0o555)
        try:
            self.run_install(success=False)
            self.assertEqual(self.destination.read_bytes(), b"old executable")
        finally:
            self.destination.chmod(0o755)

    def test_staging_failures(self):
        self.old_binary()
        for tool in ("mktemp", "cat", "chmod", "mv"):
            with self.subTest(tool=tool):
                source = shutil.which(tool)
                # Fail only staging creation for mktemp; allow download temp creation.
                condition = 'if "-d" not in sys.argv:' if tool == "mktemp" else 'if True:'
                self.write_tool(tool, 'import os, sys\n' + condition + '\n    sys.exit(1)\nos.execv(' + repr(source) + ', [' + repr(source) + '] + sys.argv[1:])\n')
                self.assert_preserved()
                (self.bin / tool).unlink()
                (self.bin / tool).symlink_to(source)

    def intercept_tool(self, tool, code):
        source = shutil.which(tool)
        self.write_tool(tool, 'import os, pathlib, signal, subprocess, sys\n'
                        'args = sys.argv[1:]\nreal = ' + repr(source) + '\n' + code +
                        '\nos.execv(real, [real] + args)\n')

    def restore_tool(self, tool):
        (self.bin / tool).unlink()
        (self.bin / tool).symlink_to(shutil.which(tool))

    def test_first_install_early_failures(self):
        for failure in ("all", "checksums.txt", f"mobterm-cli_{VERSION}_linux_amd64.tar.gz"):
            with self.subTest(failure=failure):
                self.env["HTTP_FAIL"] = failure
                self.run_install(success=False)
                self.assertFalse((self.home / ".local").exists())
        del self.env["HTTP_FAIL"]
        checks = self.assets / "checksums.txt"
        good = checks.read_bytes()
        checks.write_text("0" * 64 + f"  mobterm-cli_{VERSION}_linux_amd64.tar.gz\n")
        self.run_install(success=False)
        self.assertFalse((self.home / ".local").exists())
        checks.write_bytes(good)
        for entries in ([("mobterm-cli", tarfile.REGTYPE, b"")],
                        [("mobterm-cli", tarfile.SYMTYPE, b"")],
                        [("../escape", tarfile.REGTYPE, b"unsafe")]):
            with self.subTest(entries=entries):
                self.make_archive(entries)
                self.run_install(success=False)
                self.assertFalse((self.home / ".local").exists())

    def test_transaction_failures_first_and_existing_install(self):
        stages = (
            ("work creation", "mktemp", 'if "-d" in args: sys.exit(37)'),
            ("archive listing", "tar", 'if "-tzf" in args: sys.exit(37)'),
            ("archive extraction", "tar", 'if "-xOzf" in args: sys.exit(37)'),
            ("local creation", "mkdir", 'if args[-1].endswith("/.local"): sys.exit(37)'),
            ("bin creation", "mkdir", 'if args[-1].endswith("/.local/bin"): sys.exit(37)'),
            ("staging creation", "mktemp", 'if "-d" not in args: sys.exit(37)'),
            ("partial staging write", "cat", 'sys.stdout.buffer.write(b"partial executable"); sys.exit(37)'),
            ("chmod", "chmod", 'sys.exit(37)'),
            ("move", "mv", 'sys.exit(37)'),
        )
        for existing in (False, True):
            for stage, tool, code in stages:
                if existing and tool == "mkdir":
                    continue  # Existing directories must never be recreated.
                with self.subTest(existing=existing, stage=stage):
                    if existing:
                        self.old_binary()
                        self.destination.chmod(0o751)
                        (self.home / ".local").chmod(0o750)
                        self.destination.parent.chmod(0o711)
                    before = self.home_snapshot()
                    self.intercept_tool(tool, code)
                    try:
                        self.run_install(success=False)
                        self.assertEqual(self.home_snapshot(), before)
                    finally:
                        self.restore_tool(tool)
                        if existing:
                            shutil.rmtree(self.home / ".local")

    def test_preexisting_empty_directories_retained(self):
        for existing_bin in (False, True):
            with self.subTest(existing_bin=existing_bin):
                local = self.home / ".local"
                local.mkdir(mode=0o751)
                if existing_bin:
                    self.destination.parent.mkdir(mode=0o711)
                before = self.home_snapshot()
                self.intercept_tool("chmod", 'sys.exit(37)')
                try:
                    self.run_install(success=False)
                    self.assertEqual(self.home_snapshot(), before)
                finally:
                    self.restore_tool("chmod")
                    shutil.rmtree(local)

    def test_historical_staging_files_retained(self):
        self.destination.parent.mkdir(parents=True)
        historical = self.destination.parent / ".mobterm-cli-install.historical"
        historical.write_bytes(b"not owned by this installation")
        historical.chmod(0o640)
        self.intercept_tool("mv", 'sys.exit(37)')
        try:
            self.run_install(success=False)
        finally:
            self.restore_tool("mv")
        self.run_install()

    def test_install_directory_types(self):
        external = self.root / "external"
        external.mkdir()
        sentinel = external / "user-file"
        sentinel.write_bytes(b"external content")
        sentinel.chmod(0o640)
        for component in (self.home / ".local", self.destination.parent):
            component.parent.mkdir(exist_ok=True)
            for kind in ("symlink", "dangling symlink", "file", "fifo"):
                with self.subTest(component=component.name, kind=kind):
                    if kind in ("symlink", "dangling symlink"):
                        component.symlink_to(external if kind == "symlink" else self.root / "missing")
                    elif kind == "file":
                        component.write_bytes(b"user file")
                    else:
                        os.mkfifo(component)
                    before = self.home_snapshot()
                    self.run_install(success=False)
                    self.assertEqual(self.home_snapshot(), before)
                    self.assertEqual(list(external.iterdir()), [sentinel])
                    self.assertEqual(sentinel.read_bytes(), b"external content")
                    self.assertEqual(sentinel.stat().st_mode & 0o777, 0o640)
                    component.unlink()

    def test_failed_mkdir_does_not_claim_concurrently_created_directory(self):
        for directory in (".local", ".local/bin"):
            with self.subTest(directory=directory):
                self.intercept_tool("mkdir", 'if args[-1].endswith(' + repr("/" + directory) + '):\n'
                                    '    subprocess.run([real] + args, check=True)\n    sys.exit(37)')
                try:
                    result = self.run_install(success=False)
                    self.assertTrue((self.home / directory).is_dir())
                    self.assertEqual(list((self.home / directory).iterdir()), [])
                    self.assertIn(b"warning: Installation directory appeared", result.stderr)
                finally:
                    self.restore_tool("mkdir")
                    shutil.rmtree(self.home / ".local")

    def test_caught_signals_before_commit(self):
        for caught in (signal.SIGHUP, signal.SIGINT, signal.SIGTERM):
            for stage in ("mktemp work", "mkdir local", "mkdir bin", "mktemp stage", "cat", "mv"):
                for existing in (False, True):
                    if existing and stage.startswith("mkdir"):
                        continue
                    with self.subTest(signal=caught.name, stage=stage, existing=existing):
                        if existing:
                            self.old_binary()
                            self.destination.chmod(0o751)
                        before = self.home_snapshot()
                        tool = stage.split()[0]
                        send = f'os.kill(os.getppid(), {int(caught)})\n'
                        if tool == "mktemp":
                            condition = '"-d" in args' if stage == "mktemp work" else '"-d" not in args'
                            code = ('if ' + condition + ':\n'
                                    '    result = subprocess.run([real] + args, check=True, capture_output=True)\n'
                                    '    sys.stdout.buffer.write(result.stdout)\n    sys.stdout.flush()\n'
                                    '    ' + send + '    sys.exit(0)')
                        elif tool == "mkdir":
                            suffix = "/.local" if stage == "mkdir local" else "/.local/bin"
                            code = ('if args[-1].endswith(' + repr(suffix) + '):\n'
                                    '    subprocess.run([real] + args, check=True)\n'
                                    '    ' + send + '    sys.exit(0)')
                        else:
                            code = send + 'sys.exit(37)'
                        self.intercept_tool(tool, code)
                        try:
                            result = self.run_install(success=False)
                            self.assertEqual(result.returncode, 1)
                            self.assertEqual(self.home_snapshot(), before)
                        finally:
                            self.restore_tool(tool)
                            if existing:
                                shutil.rmtree(self.home / ".local")

    def test_nonempty_new_directories_retained_with_warning(self):
        for directory in (".local", ".local/bin"):
            with self.subTest(directory=directory):
                foreign = directory + "/user-file"
                self.intercept_tool("mv", 'pathlib.Path(os.environ["HOME"], ' + repr(foreign) +
                                    ').write_bytes(b"concurrent user data")\nsys.exit(37)')
                try:
                    result = self.run_install(success=False, extra_files=(foreign,))
                    self.assertIn(b"Cannot atomically install", result.stderr)
                    self.assertIn(b"warning:", result.stderr)
                    self.assertIn(b"retained for manual inspection", result.stderr)
                    self.assertEqual(result.returncode, 1)
                    self.assertEqual((self.home / foreign).read_bytes(), b"concurrent user data")
                    self.assertFalse(self.destination.exists())
                    if directory == ".local":
                        self.assertFalse(self.destination.parent.exists())
                finally:
                    self.restore_tool("mv")
                    shutil.rmtree(self.home / ".local")

    def test_cleanup_failures_reported_without_masking_error(self):
        for tool in ("rmdir", "rm"):
            with self.subTest(tool=tool):
                self.intercept_tool("mv", 'sys.exit(37)')
                self.intercept_tool(tool, 'sys.exit(42)')
                try:
                    extras = ()
                    # A failed rm deliberately leaves the exact stage/downloads behind.
                    if tool == "rm":
                        self.intercept_tool("mktemp", 'if "-d" not in args:\n'
                                            '    path = pathlib.Path(os.environ["HOME"], ".local/bin/.mobterm-cli-install.test")\n'
                                            '    path.touch(mode=0o600)\n    print(path)\n    sys.exit(0)')
                        extras = (".local/bin/.mobterm-cli-install.test",)
                    result = self.run_install(success=False, extra_files=extras, cleanup_expected=False)
                    self.assertEqual(result.returncode, 1)
                    self.assertIn(b"Cannot atomically install", result.stderr)
                    self.assertIn(b"warning:", result.stderr)
                    if tool == "rm":
                        self.assertIn(b"Cannot remove staged executable", result.stderr)
                        self.assertIn(b"Cannot remove private download directory", result.stderr)
                    else:
                        self.assertIn(b"Cannot remove newly created bin", result.stderr)
                        self.assertIn(b"Cannot remove newly created .local", result.stderr)
                finally:
                    self.restore_tool("mv")
                    self.restore_tool(tool)
                    if tool == "rm":
                        self.restore_tool("mktemp")
                    shutil.rmtree(self.home / ".local")
                    for path in self.work.iterdir():
                        shutil.rmtree(path)

    def test_signal_after_successful_move_preserves_commit(self):
        for caught in (signal.SIGHUP, signal.SIGINT, signal.SIGTERM):
            with self.subTest(signal=caught.name):
                self.intercept_tool("mv", 'subprocess.run([real] + args, check=True)\n'
                                    f'os.kill(os.getppid(), {int(caught)})\nsys.exit(0)')
                try:
                    result = self.run_install(success=False)
                    self.assertEqual(self.destination.read_bytes(), PAYLOAD)
                    self.assertEqual(self.destination.stat().st_mode & 0o777, 0o755)
                    self.assertNotIn(b"warning:", result.stderr)
                finally:
                    self.restore_tool("mv")
                    shutil.rmtree(self.home / ".local")

    def test_cleanup_failure_after_commit_preserves_success(self):
        self.intercept_tool("rm", 'sys.exit(42)')
        result = self.run_install(cleanup_expected=False)
        self.assertIn(b"warning: Cannot remove private download directory", result.stderr)
        self.assertEqual(self.destination.read_bytes(), PAYLOAD)
        self.assertEqual(self.destination.stat().st_mode & 0o777, 0o755)

    def test_real_packages(self):
        if DIST is None:
            self.skipTest("pass --dist and --commit to test real package outputs")
        self.env.update(FIXTURE_VERSION="v0.0.0", INCLUDE_DIGEST="1")
        archives = sorted(DIST.glob("mobterm-cli_v0.0.0_*.tar.gz"))
        self.assertEqual(len(archives), 4)
        shutil.copyfile(DIST / "checksums.txt", self.assets / "checksums.txt")
        for path in archives:
            shutil.copyfile(path, self.assets / path.name)
        native = ("darwin" if platform.system() == "Darwin" else "linux",
                  "arm64" if platform.machine() in ("arm64", "aarch64") else "amd64")
        for system in ("linux", "darwin"):
            for arch in ("amd64", "arm64"):
                with self.subTest(system=system, arch=arch):
                    self.env.update(SYSTEM="Linux" if system == "linux" else "Darwin",
                                    ARCH="x86_64" if arch == "amd64" else "arm64")
                    path = DIST / f"mobterm-cli_v0.0.0_{system}_{arch}.tar.gz"
                    with tarfile.open(path, "r:gz") as archive:
                        self.assertEqual(archive.getnames(), ["mobterm-cli", "LICENSE", "README.md", "THIRD_PARTY.md", "build-info.json"])
                        self.assertEqual(archive.getmember("mobterm-cli").mode, 0o755)
                        binary = archive.extractfile("mobterm-cli").read()
                        metadata = json.load(archive.extractfile("build-info.json"))
                        self.assertEqual(metadata, dict(version="v0.0.0", commit=COMMIT, os=system, arch=arch))
                    self.run_install("--version", "v0.0.0")
                    self.assertEqual(self.destination.read_bytes(), binary)
                    self.assertEqual(self.destination.stat().st_mode & 0o777, 0o755)
                    if (system, arch) == native:
                        result = subprocess.run([str(self.destination), "version"], env=self.env,
                                                capture_output=True, text=True, check=True)
                        self.assertEqual(result.stdout.strip(), f"mobterm-cli v0.0.0 {COMMIT} {system}/{arch}")
                        print("Native installed package version:", result.stdout.strip())


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dist", type=Path)
    parser.add_argument("--commit")
    args = parser.parse_args()
    if bool(args.dist) != bool(args.commit):
        parser.error("--dist and --commit must be supplied together")
    DIST = args.dist.resolve() if args.dist else None
    COMMIT = args.commit
    unittest.main(argv=[sys.argv[0]], verbosity=2)
