"""Select exact npm samples before installation; stdout can be used as an env file."""

import argparse
import json
import platform
import re
import sys
from pathlib import Path


MANIFEST = Path(__file__).resolve().parents[1] / "testdata/compatibility-samples.json"
STABLE_VERSION = re.compile(r"(?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*)){2}")


def require_macos_codex_version(version):
    if not isinstance(version, str) or not STABLE_VERSION.fullmatch(version):
        raise ValueError("macOS real tests require an exact stable Codex package version")
    if tuple(map(int, version.split("."))) < (0, 119, 0):
        raise ValueError(f"refused macOS Codex {version}: predates the 0.119.0 signing change; "
                         "select the macos-config sample")


def select_samples(samples, group, system):
    if group == "macos-config":
        if system != "Darwin":
            raise ValueError("macos-config requires macOS")
        selected = {**samples["minimum"], **samples[group]}
    else:
        selected = samples[group]
    if system == "Darwin":
        require_macos_codex_version(selected["codex"])
    for agent, version in selected.items():
        if (agent not in ("codex", "claude", "opencode", "pi")
                or not isinstance(version, str) or not STABLE_VERSION.fullmatch(version)):
            raise ValueError(f"expected fixed stable Agent versions in {group}")
    return selected


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("group", choices=("minimum", "current", "historical", "macos-config"))
    args = parser.parse_args()
    system = platform.system()
    try:
        selected = select_samples(json.loads(MANIFEST.read_text()), args.group, system)
    except ValueError as error:
        parser.exit(1, f"{error}\n")
    print(f"{system} {args.group}: " + ", ".join(f"{k} {v}" for k, v in selected.items()),
          file=sys.stderr)
    for agent, version in selected.items():
        print(f"{agent.upper()}_TEST_VERSION={version}")


if __name__ == "__main__":
    main()
