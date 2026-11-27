#!/usr/bin/env python3
"""Check what Git will publish. Run from the repository root with Gitleaks 8.30.1."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import quote


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
    checked = commit(os.environ["GITHUB_SHA"])
    if checked != git("rev-parse", "HEAD").decode().strip():
        raise CheckError("checkout differs from event commit")
    if os.environ["GITHUB_EVENT_NAME"] == "pull_request":
        pr = event["pull_request"]
        base, head = commit(pr["base"]["sha"]), commit(pr["head"]["sha"])
        # PR checkouts use GitHub's test merge. Scan it as well as the PR commits.
        git("merge-base", "--is-ancestor", base, checked)
        git("merge-base", "--is-ancestor", head, checked)
        return base, head, checked
    if os.environ["GITHUB_EVENT_NAME"] != "push" or commit(event["after"]) != checked:
        raise CheckError("unsupported event or checkout")
    if event["before"] != ZERO:
        return commit(event["before"]), checked, checked
    # New branches and release tags have an all-zero before. Only subtract
    # the known historical baseline when it is present in this ancestry.
    if HISTORY_FLOOR in git("rev-list", checked).decode().splitlines():
        return HISTORY_FLOOR, checked, checked
    return None, checked, checked


def sources(args):
    if not args.github_event and args.base is None and args.head is None:
        yield None, git("ls-files", "--stage", "-z", "--full-name")
        return
    if git("rev-parse", "--is-shallow-repository").strip() != b"false":
        raise CheckError("commit checks require complete history")
    if args.github_event:
        base, head, tree = event_range()
    else:
        base, head = commit(args.base), commit(args.head)
        tree = head
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
        hidden_names = set()

        def add_file(name, content, is_name=False):
            # Anonymous filenames keep untrusted paths out of scanner diagnostics
            # and include lockfiles/configs in the scan. Preserve filename-specific
            # detectors in the pinned Gitleaks release.
            leaf = Path(name).name.lower()
            suffix = Path(leaf).suffix
            if leaf.endswith("nuget.config"):
                suffix = ".nuget.config"
            elif suffix not in {".yml", ".yaml", ".tf", ".hcl", ".php", ".p12", ".pfx"}:
                suffix = ".txt"
            key = str(len(locations)) + (".txt" if is_name else suffix)
            (corpus / key).write_bytes(content)
            locations[key] = name, is_name
            for i, line in enumerate(content.splitlines(), 1):
                ids = [rule for rule, regex in CONTENT_RULES.items() if regex.search(line)]
                if any(marker in line for marker in markers):
                    ids.append("private-content")
                for rule in ids:
                    findings.add((name, i, rule))
                    if is_name:
                        hidden_names.add(name)

        seen = set()
        names = set()
        for message, data in sources(args):
            if message:
                add_file(message, data)
                continue
            for name, oid in tracked(data):
                if name not in names:
                    names.add(name)
                    add_file(name, os.fsencode(name), is_name=True)
                if (name, oid) not in seen:
                    seen.add((name, oid))
                    add_file(name, git("cat-file", "blob", oid))

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
            name, is_name = locations[key]
            rule, number = row["RuleID"], row["StartLine"]
            if not re.fullmatch(r"[\w-]+", rule) or not isinstance(number, int):
                raise CheckError("invalid Gitleaks location")
            if not is_name and rule == "generic-api-key" and name in VECTOR_LINES and number > 0:
                raw = (corpus / key).read_bytes()
                lines = raw.split(b"\n")
                if number <= len(lines):
                    digest = hashlib.sha256(lines[number - 1]).hexdigest()
                    if digest == VECTOR_LINES[name]:
                        continue
            findings.add((name, number, rule))
            if is_name:
                hidden_names.add(name)

        for name, line, rule in sorted(findings):
            label = "<redacted path>" if name in hidden_names else name
            # A path is untrusted in logs. Encode characters used by shells
            # and GitHub workflow commands as well as terminal control bytes.
            print("boundary", quote(os.fsencode(label), safe="/-._"), line, rule)
        print(f"Public boundary: {len(seen)} tracked file versions, {len(findings)} findings.")
        return 1 if findings else 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gitleaks", default="gitleaks")
    parser.add_argument("--base", help="exclude commits reachable from this full SHA")
    parser.add_argument("--head", help="include commits reachable from this full SHA")
    parser.add_argument("--github-event", action="store_true", help="use GitHub's PR or push metadata")
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
