"""Pin the official npm Mach-O used by the Darwin real lifecycle suite."""

import hashlib
import json
import os
from pathlib import Path
import platform
import shlex
import shutil
import subprocess
import sys

from select_agent_samples import require_macos_codex_version


# SHA-256 of vendor/<triple>/bin/codex in each fixed @openai/codex npm
# platform tarball. Both root packages ship the same bin/codex.js.
NATIVE_SHA256 = {
    ('0.162.1', 'x64'): 'f86b8cfd3de94dc70763d251da3ff292e94f4a53ad41b81faab3619a7bcb626c',
    ('0.162.1', 'arm64'): '74c6a6d263f40d65f99c27a815275f5b4ed0bcc2baecc396b4cf3255498bdfc0',
    ('0.163.0', 'x64'): '73697f229103f463acaafd8659976caaa4892ec11e2c246c7fa97f93d523f90e',
    ('0.163.0', 'arm64'): '8813367285524ac798dd96d1b071aea30d58708c7226fd236cd275cf1cfdee31',
}
LAUNCHER_SHA256 = '61b0194f3bb6534439c8d26a3ed57d0805f84b884588b761795323eeb92fcf70'
SIGNING_REQUIREMENT = ('anchor apple generic and identifier "codex" and '
                       'certificate leaf[subject.OU] = "2DC432GLL2" and notarized')


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def identity(path):
    info = path.stat()
    return info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns


def package_matches(directory, **expected):
    manifest = directory / 'package.json'
    if manifest.resolve(strict=True) != manifest:
        raise ValueError('refused redirected Codex package metadata')
    values = json.loads(manifest.read_text())
    if not isinstance(values, dict) or any(values.get(k) != v for k, v in expected.items()):
        raise ValueError('refused Codex package version, platform or entry mismatch')
    return values


def assess_native(path):
    # spctl's app-bundle assessment rejects standalone Mach-O tools as "not an
    # app". Ask Gatekeeper to scan this exact native file and require its verdict.
    checks = [
        ('Developer ID and notarization', ['/usr/bin/codesign', '--verify', '--strict',
          '--check-notarization', '-R=' + SIGNING_REQUIREMENT, path]),
        ('Gatekeeper', ['/usr/bin/gktool', 'scan', path]),
    ]
    for label, command in checks:
        try:
            result = subprocess.run(command, capture_output=True, text=True, timeout=30,
                                    env={'PATH': os.defpath, 'LC_ALL': 'C'})
        except subprocess.TimeoutExpired as error:
            raise ValueError(f'{label} assessment timed out') from error
        except OSError as error:
            raise ValueError(f'{label} assessment unavailable') from error
        if label == 'Gatekeeper':
            lines = (result.stdout + '\n' + result.stderr).splitlines()
            for line in lines:
                if line.startswith(('Scan completed, but failed because',
                                    'Scan completed, and would be allowed but')):
                    raise ValueError('Gatekeeper refused Codex: ' + line)
            if result.returncode or 'Scan completed and software is allowed by system policy.' not in lines:
                raise ValueError(f'Gatekeeper assessment inconclusive (exit {result.returncode})')
        elif result.returncode:
            raise ValueError(f'{label} verification failed (exit {result.returncode})')


def pin_codex(prefix, version, search_path, bindir):
    require_macos_codex_version(version)
    if prefix is None:
        raise ValueError('macOS requires --agent-prefix pointing to the isolated global npm prefix')
    cpu = {'arm64': 'arm64', 'x86_64': 'x64'}.get(platform.machine())
    expected = NATIVE_SHA256.get((version, cpu))
    if expected is None:
        raise ValueError('no pinned native Codex digest for this macOS sample and architecture')
    prefix = Path(prefix).resolve(strict=True)
    package = prefix / 'lib/node_modules/@openai/codex'
    launcher = package / 'bin/codex.js'
    selected = shutil.which('codex', path=search_path)
    if (not selected or Path(selected).resolve(strict=True) != launcher
            or (prefix / 'bin/codex').resolve(strict=True) != launcher
            or digest(launcher) != LAUNCHER_SHA256):
        raise ValueError('refused PATH entry: expected the official launcher from --agent-prefix')
    manifest = package_matches(package, name='@openai/codex', version=version,
                               bin={'codex': 'bin/codex.js'})
    alias = '@openai/codex-darwin-' + cpu
    native_version = version + '-darwin-' + cpu
    dependencies = manifest.get('optionalDependencies')
    if not isinstance(dependencies, dict) or dependencies.get(alias) != 'npm:@openai/codex@' + native_version:
        raise ValueError('refused Codex platform dependency mismatch')
    native_package = package / 'node_modules' / alias
    package_matches(native_package, name='@openai/codex', version=native_version,
                    os=['darwin'], cpu=[cpu])
    triple = ('aarch64' if cpu == 'arm64' else 'x86_64') + '-apple-darwin'
    native = native_package / 'vendor' / triple / 'bin/codex'
    if native.resolve(strict=True) != native or not native.is_file() or not os.access(native, os.X_OK):
        raise ValueError('refused missing, redirected or non-executable Codex native target')
    initial = identity(native)
    if digest(native) != expected:
        raise ValueError('refused Codex native SHA-256 mismatch')
    assess_native(native)
    if identity(native) != initial:
        raise ValueError('refused Codex native file changed during assessment')

    # status and hooks resolve Codex in fresh subprocesses. Freeze their
    # selection too, checking the file identity immediately before each exec.
    guard = bindir / 'checked_exec.py'
    guard.write_text(
        'import os, sys\n'
        f'path = {str(native)!r}\n'
        's = os.stat(path)\n'
        f'if (s.st_dev, s.st_ino, s.st_size, s.st_mtime_ns, s.st_ctime_ns) != {initial!r}:\n'
        '    sys.exit("refused Codex native file changed since assessment")\n'
        'os.execv(path, [path, *sys.argv[1:]])\n'
    )
    entry = bindir / 'codex'
    # The shell only dispatches to our Python interpreter (possibly in a path
    # with spaces); neither npm's JS launcher nor a competing Codex is invoked.
    entry.write_text('#!/bin/sh\nexec ' + shlex.join([sys.executable, '-I', str(guard)]) + ' "$@"\n')
    entry.chmod(0o500)
    print(f'macOS Codex {version} {native.relative_to(prefix)} sha256={expected} '
          'Developer ID/notarization and Gatekeeper allowed', file=sys.stderr, flush=True)
    return str(bindir) + os.pathsep + search_path
