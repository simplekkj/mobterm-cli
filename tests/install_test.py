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
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
REPO = "https://github.com/simplekkj/mobterm-cli"
VERSION = "v1.2.3"  # Local fixture, never a published-version claim.
PAYLOAD = b'#!/bin/sh\necho "installer must not execute me" >&2\nexit 99\n'
DIST = None
COMMIT = None
MOCK_CURL = r'''
import os, pathlib, shutil, sys
args = sys.argv[1:]
url = args[-1]
repo = "https://github.com/simplekkj/mobterm-cli"
with open(os.environ["REQUESTS"], "a") as log:
    log.write(url + "\n")
assert args[0] == "-q" and "-fsSL" in args
assert args[args.index("--proto") + 1] == "=https"
assert args[args.index("--proto-redir") + 1] == "=https"
if os.environ.get("HTTP_FAIL") in ("all", url.rsplit("/", 1)[-1]):
    sys.exit(22)  # curl's HTTP 404 failure
if url == repo + "/releases/latest":
    assert args[args.index("-o") + 1] == "/dev/null"
    assert args[args.index("-w") + 1] == "%{url_effective}"
    print(os.environ["LATEST_URL"], end="")
else:
    prefix = repo + "/releases/download/" + os.environ["FIXTURE_VERSION"] + "/"
    assert url.startswith(prefix), url
    name = url[len(prefix):]
    assert "/" not in name
    shutil.copyfile(pathlib.Path(os.environ["ASSETS"]) / name,
                    args[args.index("-o") + 1])
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
        for tool in ("awk", "tar", "mktemp", "mkdir", "cat", "chmod", "mv", "rm", "gzip",
                     "sha256sum", "shasum"):
            source = shutil.which(tool)
            if source:
                (self.bin / tool).symlink_to(source)
        self.write_tool("curl", MOCK_CURL)
        self.write_tool("uname", 'import os, sys\nprint(os.environ["SYSTEM"] if sys.argv[1] == "-s" else os.environ["ARCH"])\n')
        self.env = dict(os.environ, HOME=str(self.home), TMPDIR=str(self.work),
                        PYTHONDONTWRITEBYTECODE="1",
                        PATH=str(self.bin), SYSTEM="Linux", ARCH="x86_64",
                        ASSETS=str(self.assets), FIXTURE_VERSION=VERSION,
                        LATEST_URL=REPO + "/releases/tag/" + VERSION,
                        REQUESTS=str(self.root / "requests"),
                        CODEX_HOME=str(self.home / ".codex"),
                        CLAUDE_CONFIG_DIR=str(self.home / ".claude"),
                        MOBTERM_CONFIG_DIR=str(self.home / ".config/mobterm"))
        self.sentinels = {}
        for name in (".codex/config.toml", ".claude/settings.json", ".config/mobterm/state.json",
                     ".ssh/config", ".profile", ".zshrc"):
            path = self.home / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(b"untouched test configuration\n")
            self.sentinels[path] = path.read_bytes()
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
            for filename, kind, data in entries or [("mobterm-cli", tarfile.REGTYPE, PAYLOAD),
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

    def run_install(self, *args, success=True, stdin=False):
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
        self.assertEqual(list(self.work.iterdir()), [], "temporary downloads leaked")
        self.assertEqual(list(self.home.glob(".local/bin/.mobterm-cli-install.*")), [], "staged binary leaked")
        for path, original in self.sentinels.items():
            self.assertEqual(path.read_bytes(), original)
        self.assertEqual({path.relative_to(self.home).as_posix() for path in self.home.rglob("*") if path.is_file()},
                         {path.relative_to(self.home).as_posix() for path in self.sentinels} |
                         ({".local/bin/mobterm-cli"} if self.destination.is_file() else set()),
                         "unexpected configuration or hook file created")
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
                    self.assertEqual(self.destination.read_bytes(), PAYLOAD)
                    self.assertEqual(self.destination.stat().st_mode & 0o777, 0o755)
                    urls = Path(self.env["REQUESTS"]).read_text().splitlines()
                    self.assertEqual(urls[-2:], [REPO + f"/releases/download/{VERSION}/checksums.txt",
                                                REPO + f"/releases/download/{VERSION}/mobterm-cli_{VERSION}_{target_system}_{target_arch}.tar.gz"])
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
                     ("--version", VERSION, "--version", VERSION)):
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

    def test_latest_redirect_validation(self):
        self.old_binary()
        for url in (REPO + "/releases/latest", REPO + "/releases/tag/v1.2.3-rc1",
                    "http://github.com/simplekkj/mobterm-cli/releases/tag/" + VERSION,
                    "https://github.com/other/mobterm-cli/releases/tag/" + VERSION,
                    REPO + "/releases/tag/" + VERSION + "?x=1",
                    REPO + "/releases/tag/" + VERSION + "/../../other"):
            with self.subTest(url=url):
                self.env["LATEST_URL"] = url
                self.assert_preserved()

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

    def test_real_packages(self):
        if DIST is None:
            self.skipTest("pass --dist and --commit to test real package outputs")
        self.env.update(FIXTURE_VERSION="v0.0.0", LATEST_URL=REPO + "/releases/tag/v0.0.0")
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
