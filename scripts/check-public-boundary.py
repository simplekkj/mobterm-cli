#!/usr/bin/env python3
"""Check what Git will publish. Run from the repository root with Gitleaks 8.30.1."""
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import stat
import struct
import subprocess
import sys
import tarfile
import tempfile
import zipfile
import zlib


VERSION = "8.30.1"
# Last published release before the gate was adopted. For new refs, scan every
# subsequent reachable commit. Unrelated histories are checked from their root.
HISTORY_FLOOR = "97ff7c3abc32fa92468812f325874e9ab3499eab"
ZERO = "0" * 40
CONTENT_RULES = {
    "work-page": re.compile(
        rb"https?://(?:[\w-]+\.)*notion\.(?:com|so|site)/[^\s<>\x22)]*"
        rb"(?:[0-9a-f]{32}|[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12})", re.I),
    "local-path": re.compile(
        rb"(?<![.\w/])/(?:Users|home)/(?!example/|runner/|user/)[\w.-]+/[\w.-]+"),
    "delivery-record": re.compile(
        rb"(?:\bWF-\d+\.\d+\b|\b(?:IMP|FIX|REV)-R\d+-v\d+\.\d+\b)"),
}
# These published protocol vectors use the bytes 0 through 31 as their key.
# Pin the exact key/key_base64 line for each reviewed file. A changed value,
# another path, or any other detector must still fail.
LINE_DIGEST = "1a2557153af0b29be6cda114f75e229c40c5a10b2f1238595546f0a4555daf1b"
BASE64_LINE_DIGEST = "7230b6923176ff48487d8895a298232249750ce323904e79c059c79b9b107d9c"
VECTOR_LINES = {
    "testdata/approval-v2.json": LINE_DIGEST,
    "testdata/interaction-v1.json": BASE64_LINE_DIGEST,
    "testdata/message-body-v1.json": BASE64_LINE_DIGEST,
    "testdata/protocol-v1-extended.json": LINE_DIGEST,
    "testdata/protocol-v1-summary.json": LINE_DIGEST,
    "testdata/protocol-v1.json": LINE_DIGEST,
    "testdata/protocol-v2-summary.json": LINE_DIGEST,
    "testdata/protocol-v2.json": LINE_DIGEST,
    "testdata/quick-command-result-v1.json": LINE_DIGEST,
    "testdata/reply-v1.json": BASE64_LINE_DIGEST,
    "testdata/reply-v2.json": BASE64_LINE_DIGEST,
}


class CheckError(Exception):
    pass


def git(*args):
    return subprocess.run(["git", *args], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                          check=True).stdout


def commit(sha):
    if not isinstance(sha, str) or not re.fullmatch("[0-9a-f]{40}", sha) or sha == ZERO:
        raise CheckError("expected a full nonzero Git object ID")
    return git("rev-parse", "--verify", sha + "^{commit}").decode().strip()


def event_range():
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_bytes())
    event_sha = os.environ["GITHUB_SHA"]
    checked = commit(event_sha)
    if checked != git("rev-parse", "HEAD").decode().strip():
        raise CheckError("checkout differs from event commit")
    if os.environ["GITHUB_EVENT_NAME"] == "pull_request":
        pr = event["pull_request"]
        base, head = commit(pr["base"]["sha"]), commit(pr["head"]["sha"])
        # PR checkouts use GitHub's test merge. Scan it as well as the PR commits.
        git("merge-base", "--is-ancestor", base, checked)
        git("merge-base", "--is-ancestor", head, checked)
        return base, head, checked, None
    if os.environ["GITHUB_EVENT_NAME"] != "push" or commit(event["after"]) != checked:
        raise CheckError("unsupported event or checkout")
    ref = event["ref"]
    if ref != os.environ["GITHUB_REF"] or not ref.startswith(("refs/heads/", "refs/tags/")):
        raise CheckError("push ref differs from event context or is unsupported")
    tag = None
    if ref.startswith("refs/tags/"):
        git("check-ref-format", ref)
        tag = git("show-ref", "--verify", "--hash", ref).decode().strip()
        # GitHub can identify an annotated tag push by its object or its
        # peeled commit. A different tag on the same commit is not this event.
        if commit(tag) != checked or any(oid not in (tag, checked) for oid in (event_sha, event["after"])):
            raise CheckError("tag object differs from event")
    if event["before"] != ZERO:
        return commit(event["before"]), checked, checked, tag
    # New branches and release tags have an all-zero before. Only subtract
    # the known historical baseline when it is present in this ancestry.
    if HISTORY_FLOOR in git("rev-list", checked).decode().splitlines():
        return HISTORY_FLOOR, checked, checked, tag
    return None, checked, checked, tag


def sources(args):
    if not args.github_event and args.base is None and args.head is None:
        yield None, git("ls-files", "--stage", "-z", "--full-name")
        return
    if git("rev-parse", "--is-shallow-repository").strip() != b"false":
        raise CheckError("commit checks require complete history")
    if args.github_event:
        base, head, tree, tag = event_range()
    else:
        base, head = commit(args.base), commit(args.head)
        tree = head
        tag = args.head
    # Scan each published annotation, including an annotated tag pointing to
    # another tag. Use object IDs for report locations; contents may be private.
    while tag is not None and tag != head:
        raw = git("cat-file", "tag", tag)
        yield "tag " + tag, raw
        target = raw.partition(b"\n")[0]
        if not re.fullmatch(rb"object [0-9a-f]{40}", target):
            raise CheckError("invalid annotated tag target")
        tag = target[7:].decode()
    revs = git("rev-list", base + ".." + head if base else head).decode().splitlines()
    for rev in revs:
        yield "commit " + rev, git("show", "-s", "--format=%B", rev)
    for rev in dict.fromkeys([tree, *revs]):
        yield None, git("ls-tree", "-r", "-z", "--full-tree", rev)


def tracked(entries):
    for entry in entries.split(b"\0"):
        if not entry:
            continue
        info, name = entry.split(b"\t", 1)
        mode, second, third = info.split()
        if mode not in (b"100644", b"100755", b"120000"):
            raise CheckError("unsupported tracked entry")
        # ls-files returns mode/object/stage, ls-tree returns mode/type/object.
        if third == b"0":
            oid = second
        elif second == b"blob":
            oid = third
        else:
            raise CheckError("unmerged or unsupported tracked entry")
        yield os.fsdecode(name), oid


# Bound Git reads, decompression and entry count independently. The expansion
# budget includes nested containers and is shared by the whole invocation.
MIB = 1024 * 1024
MAX_BLOB = 64 * MIB
MAX_ARCHIVE = 16 * MIB
MAX_EXPANDED = 64 * MIB
MAX_MEMBERS = 1024
MAX_DEPTH = 2


def read_blob(oid):
    with subprocess.Popen(["git", "cat-file", "blob", oid], stdout=subprocess.PIPE,
                          stderr=subprocess.DEVNULL) as process:
        data = process.stdout.read(MAX_BLOB + 1)
        if len(data) > MAX_BLOB:
            process.kill()
            raise CheckError("Git blob exceeds scan limit")
        if process.wait():
            raise CheckError("cannot read Git blob")
    return data


def archive_format(name, data):
    if name.lower().endswith(".zip") or data.startswith((b"PK\x03\x04", b"PK\x05\x06", b"PK\x07\x08")):
        return "zip"
    if name.lower().endswith((".tar.gz", ".tgz", ".gz")) or data.startswith(b"\x1f\x8b"):
        return "tar.gz"
    if name.lower().endswith(".tar") or data[257:262] == b"ustar":
        return "tar"
    other_formats = (b"7z\xbc\xaf\x27\x1c", b"Rar!\x1a\x07", b"BZh",
                     b"\xfd7zXZ\x00", b"\x28\xb5\x2f\xfd")
    if (data.startswith(other_formats)
            or name.lower().endswith((".7z", ".rar", ".bz2", ".tbz", ".tbz2", ".xz", ".txz", ".zst"))):
        raise CheckError("unsupported archive format")
    return None


class ArchiveScan:
    def __init__(self, add_file):
        self.add_file = add_file
        self.remaining = MAX_EXPANDED
        self.entries = 0

    def add(self, parts, oid, data):
        kind = archive_format(parts[-1], data)
        if kind and (len(parts) > MAX_DEPTH or len(data) > MAX_ARCHIVE):
            raise CheckError("archive exceeds depth or input size limit")
        self.add_file(parts, oid, data)
        if not kind:
            return
        try:
            if kind == "zip":
                self.zip(parts, oid, data)
            else:
                if kind == "tar.gz":
                    with gzip.GzipFile(fileobj=io.BytesIO(data)) as stream:
                        data = self.read(stream)
                self.tar(parts, oid, data)
        except (EOFError, OSError, RuntimeError, ValueError, OverflowError, struct.error,
                tarfile.TarError, zipfile.BadZipFile, zlib.error) as exc:
            raise CheckError("archive is invalid or unsupported") from exc

    def read(self, stream):
        data = stream.read(self.remaining + 1)
        self.charge(len(data))
        return data

    def charge(self, size):
        if size > self.remaining:
            raise CheckError("archive exceeds expanded size limit")
        self.remaining -= size

    def metadata(self, parts, oid, data, is_name=False):
        self.charge(len(data))
        self.add_file(parts, oid, data, is_name=is_name)

    def entry(self, parts, oid, name, seen):
        self.entries += 1
        if self.entries > MAX_MEMBERS:
            raise CheckError("archive exceeds entry count limit")
        if "\0" in name or name in seen:
            raise CheckError("archive has ambiguous member names")
        seen.add(name)
        member = (*parts, name)
        self.metadata(member, oid, os.fsencode(name), is_name=True)
        return member

    def zip(self, parts, oid, data):
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            self.metadata(parts, oid, archive.comment)
            if len(archive.infolist()) > MAX_MEMBERS - self.entries:
                raise CheckError("archive exceeds entry count limit")
            seen = set()
            for item in archive.infolist():
                member = self.entry(parts, oid, item.orig_filename, seen)
                mode = stat.S_IFMT(item.external_attr >> 16) if item.create_system == 3 else 0
                if (mode not in (0, stat.S_IFDIR, stat.S_IFREG) or item.flag_bits & 1
                        or item.compress_type not in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED)):
                    raise CheckError("unsupported archive member type or encoding")
                if item.file_size > self.remaining:
                    raise CheckError("archive exceeds expanded size limit")
                self.metadata(member, oid, item.comment + b"\n" + item.extra)
                with archive.open(item) as stream:
                    content = self.read(stream)
                if len(content) != item.file_size:
                    raise CheckError("incomplete archive member")
                if not item.is_dir() or content:
                    self.add(member, oid, content)

    def tar(self, parts, oid, data):
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:") as archive:
            seen = set()
            for item in archive:
                member = self.entry(parts, oid, item.name, seen)
                metadata = [item.uname, item.gname]
                metadata.extend(k + "=" + v for k, v in item.pax_headers.items())
                self.metadata(member, oid, os.fsencode("\n".join(metadata)))
                if item.isdir() and item.size == 0:
                    continue
                if item.type not in (tarfile.REGTYPE, tarfile.AREGTYPE) or item.sparse is not None:
                    raise CheckError("unsupported archive member type")
                if item.size > self.remaining:
                    raise CheckError("archive exceeds expanded size limit")
                with archive.extractfile(item) as stream:
                    content = self.read(stream)
                if len(content) != item.size:
                    raise CheckError("incomplete archive member")
                self.add(member, oid, content)
            # tarfile stops at the first zero or invalid header. Refuse trailing
            # members it did not visit, including concatenated tar archives.
            if any(data[archive.offset:]) or len(data) - archive.offset < tarfile.BLOCKSIZE:
                raise CheckError("incomplete or concatenated tar archive")


def run(args):
    scanner = shutil.which(args.gitleaks)
    if not scanner or subprocess.run([scanner, "version"], check=True, stderr=subprocess.DEVNULL,
                                     stdout=subprocess.PIPE).stdout.strip() != VERSION.encode():
        raise CheckError("Gitleaks " + VERSION + " is required")
    scanner = os.path.abspath(scanner)
    markers = []
    if args.private_patterns:
        markers = [line for line in Path(args.private_patterns).read_bytes().splitlines() if line]
        if not markers:
            raise CheckError("empty private pattern file")
    with tempfile.TemporaryDirectory(prefix="public-boundary-") as tmp:
        root = Path(tmp)
        corpus = root / "input"
        corpus.mkdir()
        locations = {}
        findings = set()

        def add_file(parts, oid, content, is_name=False):
            # Anonymous filenames keep untrusted paths out of scanner diagnostics
            # and include lockfiles/configs in the scan. Preserve filename-specific
            # detectors in the pinned Gitleaks release.
            leaf = Path(parts[-1]).name.lower()
            suffix = Path(leaf).suffix
            if leaf.endswith("nuget.config"):
                suffix = ".nuget.config"
            elif suffix not in {".yml", ".yaml", ".tf", ".hcl", ".php", ".p12", ".pfx"}:
                suffix = ".txt"
            key = str(len(locations)) + (".txt" if is_name else suffix)
            (corpus / key).write_bytes(content)
            locations[key] = parts, oid, is_name
            for i, line in enumerate(content.splitlines(), 1):
                ids = [rule for rule, regex in CONTENT_RULES.items() if regex.search(line)]
                if any(marker in line for marker in markers):
                    ids.append("private-content")
                for rule in ids:
                    findings.add((parts, oid, i, rule))

        seen = set()
        archives = ArchiveScan(add_file)
        for message, data in sources(args):
            if message:
                add_file((message,), message.split()[-1], data)
                continue
            for name, oid in tracked(data):
                if (name, oid) not in seen:
                    seen.add((name, oid))
                    object_id = oid.decode("ascii")
                    add_file((name,), object_id, os.fsencode(name), is_name=True)
                    archives.add((name,), object_id, read_blob(oid))

        config = root / "config.toml"
        config.write_text("[extend]\nuseDefault = true\n")
        ignore = root / "empty-ignore"
        ignore.touch()
        report = root / "results.json"
        scan = subprocess.run(
            [scanner, "dir", ".", "--config", str(config), "--gitleaks-ignore-path", str(ignore),
             "--ignore-gitleaks-allow", "--no-banner", "--redact=100", "--exit-code=42",
             "--report-format=json", "--report-path", str(report)], cwd=corpus,
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if scan.returncode not in (0, 42):
            raise CheckError("Gitleaks failed")
        rows = json.loads(report.read_bytes())
        if bool(rows) != (scan.returncode == 42):
            raise CheckError("inconsistent Gitleaks report")
        for row in rows:
            key = row["File"]
            parts, oid, is_name = locations[key]
            name = parts[0]
            rule, number = row["RuleID"], row["StartLine"]
            if not re.fullmatch(r"[\w-]+", rule) or not isinstance(number, int):
                raise CheckError("invalid Gitleaks location")
            if (not is_name and len(parts) == 1 and rule == "generic-api-key"
                    and name in VECTOR_LINES and number > 0):
                raw = (corpus / key).read_bytes()
                lines = raw.split(b"\n")
                if number <= len(lines):
                    digest = hashlib.sha256(lines[number - 1]).hexdigest()
                    if digest == VECTOR_LINES[name]:
                        continue
            findings.add((parts, oid, number, rule))

        # Even a filename that passes detection can contain the exact value of
        # a finding elsewhere. Only stable, locally resolvable IDs enter logs.
        local_rows = []
        for parts, oid, line, rule in sorted(findings):
            identifier = hashlib.sha256(b"\0".join(map(os.fsencode, parts))).hexdigest()
            print("boundary", identifier, line, rule)
            if args.locations_file:
                local_rows.append(dict(id=identifier, path=parts, object=oid, line=line, rule=rule))
        if args.locations_file:
            fd = os.open(args.locations_file, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, "w") as output:
                json.dump(local_rows, output, indent=2)
                output.write("\n")
        print(f"Public boundary: {len(seen)} tracked file versions, {len(findings)} findings.")
        return 1 if findings else 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gitleaks", default="gitleaks")
    parser.add_argument("--base", help="exclude commits reachable from this full SHA")
    parser.add_argument("--head", help="include commits reachable from this full SHA")
    parser.add_argument("--github-event", action="store_true", help="use GitHub's PR or push metadata")
    parser.add_argument("--locations-file", help="create a private local JSON map of finding IDs (new file only)")
    parser.add_argument("--private-patterns", help="local UTF-8 file containing one literal per line")
    args = parser.parse_args()
    if args.github_event and (args.base or args.head):
        parser.error("choose either an explicit range or a GitHub event")
    try:
        if git("rev-parse", "--show-prefix").strip():
            raise CheckError("run from the repository root")
        return run(args)
    except CheckError as exc:
        print("Public boundary:", exc, file=sys.stderr)
    except (OSError, ValueError, KeyError, IndexError, TypeError, subprocess.SubprocessError):
        print("Public boundary: unable to verify input, Git history or scanner output.", file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main())
