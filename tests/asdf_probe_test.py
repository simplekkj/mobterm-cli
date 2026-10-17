#!/usr/bin/env python3
"""Real asdf + official Node/npm Claude selection, with no user config or login.

Downloads fixed public sources/packages into --work-dir; never uses existing asdf
state. Tests HOME/project selection and Agent/runtime shims, not model delivery.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tarfile
import urllib.request

ASDF_SHA = 'cb72590e9f9691b6e651aecfe4b0c9334adff41b'  # v0.20.2
PLUGIN_SHA = '779c8dc84b3bdab38c2c80622d315c2c3267f74b'
SAMPLES = (('22.22.2', '2.1.301'), ('22.14.0', '2.0.36'))


def fetch_archive(url, destination, digest=None):
    archive = destination.with_suffix('.tar.gz')
    with urllib.request.urlopen(url, timeout=60) as response:
        archive.write_bytes(response.read())
    actual = hashlib.sha256(archive.read_bytes()).hexdigest()
    if digest is not None:
        assert actual == digest, 'Source checksum mismatch'
    print(f'{archive.name} sha256={actual}', flush=True)
    destination.mkdir()
    with tarfile.open(archive) as tar:
        tar.extractall(destination, filter='data')
    return next(destination.iterdir())


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--work-dir', type=Path, required=True)
    parser.add_argument('--asdf', type=Path, help='Optional locally built v0.20.2 executable')
    args = parser.parse_args()
    binary = args.binary.resolve()
    root = args.work_dir.resolve()
    root.mkdir(mode=0o700)  # Deliberately refuse reuse of existing state.
    home = root / "home with ' spaces"
    home.mkdir()
    tools = root / 'tools'
    tools.mkdir()
    env = {k: os.environ[k] for k in ('PATH', 'LANG', 'LC_ALL') if k in os.environ}
    env.update(HOME=str(home), XDG_CONFIG_HOME=str(home / '.config'),
               CODEX_HOME=str(home / '.codex'), CLAUDE_CONFIG_DIR=str(home / '.claude'),
               MOBTERM_CONFIG_DIR=str(home / '.config/mobterm'),
               DISABLE_UPDATES='1', DISABLE_AUTOUPDATER='1',
               CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1', GOTOOLCHAIN='go1.27.1')

    def run(argv, *, cwd=home, process_env=None, timeout=60):
        result = subprocess.run([str(a) for a in argv], cwd=cwd, env=process_env or env,
                                capture_output=True, timeout=timeout)
        assert result.returncode == 0, f'{Path(argv[0]).name} {argv[1:3]} failed ({result.returncode})'
        return result.stdout

    if args.asdf:
        shutil.copyfile(args.asdf.resolve(), tools / 'asdf')
        (tools / 'asdf').chmod(0o700)
    else:
        source = fetch_archive(f'https://codeload.github.com/asdf-vm/asdf/tar.gz/{ASDF_SHA}', root / 'asdf-source')
        run([shutil.which('go'), 'build', '-o', tools / 'asdf', './cmd/asdf'], cwd=source, timeout=300)
    print('asdf source: ' + ASDF_SHA, flush=True)
    plugin = fetch_archive(f'https://codeload.github.com/asdf-vm/asdf-nodejs/tar.gz/{PLUGIN_SHA}', root / 'nodejs-source')
    print('asdf-nodejs source: ' + PLUGIN_SHA, flush=True)
    system = {'Darwin': 'darwin', 'Linux': 'linux'}[platform.system()]
    arch = {'x86_64': 'x64', 'arm64': 'arm64', 'aarch64': 'arm64'}[platform.machine()]
    nodes = []
    for node_version, agent_version in SAMPLES:
        name = f'node-v{node_version}-{system}-{arch}.tar.gz'
        base = f'https://nodejs.org/dist/v{node_version}/'
        with urllib.request.urlopen(base + 'SHASUMS256.txt', timeout=60) as response:
            checksums = response.read().decode().splitlines()
        digest = next(line.split()[0] for line in checksums if line.split()[-1] == name)
        node = fetch_archive(base + name, root / ('node-' + node_version), digest)
        install_env = dict(env, PATH=str(node / 'bin') + ':' + env['PATH'],
                           npm_config_cache=str(root / 'npm-cache'))
        run([node / 'bin/node', node / 'lib/node_modules/npm/bin/npm-cli.js',
             'install', '--global', '--prefix', node, '--no-audit', '--no-fund',
             '@anthropic-ai/claude-code@' + agent_version], process_env=install_env, timeout=180)
        nodes.append(node)

    # A wrong global Agent is deliberately available after shims/direct npm.
    global_bin = root / 'global'
    global_bin.mkdir()
    (global_bin / 'claude').write_text('#!/bin/sh\nprintf "0.0.1 (Claude Code)\\n"\n')
    (global_bin / 'claude').chmod(0o700)
    project = root / 'project'
    project.mkdir()
    (home / '.tool-versions').write_text('nodejs home\n')
    (project / '.tool-versions').write_text('nodejs project\n')
    (home / '.claude').mkdir()
    settings = home / '.claude/settings.json'
    settings.write_text(json.dumps({'env': {'USER_SETTING': 'keep'}, 'hooks': {
        'Stop': [{'hooks': [{'type': 'command', 'command': 'true'}]}]}}) + '\n')
    sentinel = home / '.claude.json'
    sentinel.write_text('{"numStartups":3}\n')
    run([binary, 'status', '--installation-json'])
    snapshots = {p: p.read_bytes() for p in (home / '.tool-versions', project / '.tool-versions', sentinel, settings)}
    original_settings = json.loads(settings.read_bytes())

    for explicit in (False, True):
        data = root / 'explicit data' if explicit else home / '.asdf'
        (data / 'plugins').mkdir(parents=True)
        shutil.copytree(plugin, data / 'plugins/nodejs')
        for selection, node in zip(('home', 'project'), nodes):
            target = data / 'installs/nodejs' / selection
            target.parent.mkdir(parents=True, exist_ok=True)
            target.symlink_to(node, target_is_directory=True)
        if explicit:
            env['ASDF_DATA_DIR'] = str(data)
        else:
            env.pop('ASDF_DATA_DIR', None)
        env['PATH'] = f'{tools}:{data / "shims"}:{global_bin}:/usr/bin:/bin'
        for selection in ('home', 'project'):
            run([tools / 'asdf', 'reshim', 'nodejs', selection])
        for cwd, (node_version, agent_version), node in zip((home, project), SAMPLES, nodes):
            for mode in ('agent-shim', 'node-shim'):
                # A direct npm symlink that still has to find Node through asdf.
                if mode == 'node-shim':
                    direct = root / f'direct-{explicit}-{node_version}'
                    direct.mkdir()
                    (direct / 'claude').symlink_to(node / 'bin/claude')
                    env['PATH'] = f'{tools}:{direct}:{data / "shims"}:{global_bin}:/usr/bin:/bin'
                else:
                    env['PATH'] = f'{tools}:{data / "shims"}:{global_bin}:/usr/bin:/bin'
                assert run(['node', '--version'], cwd=cwd).decode().strip() == 'v' + node_version
                # --version in the original HOME would migrate old Claude. Only
                # resolve its path there; execute it through the isolated CLI.
                assert Path(run([tools / 'asdf', 'which', 'claude'], cwd=cwd).decode().strip()).samefile(node / 'bin/claude')
                state = home / '.config/mobterm'
                before_state = {p: p.read_bytes() for p in state.iterdir() if p.is_file()}
                result = json.loads(run([binary, 'status', '--json'], cwd=cwd))
                capability = result['capabilities']['claude']
                assert capability['supported'] and capability['actual_version'] == agent_version + ' (Claude Code)'
                assert before_state == {p: p.read_bytes() for p in state.iterdir() if p.is_file()}
                for p, before in snapshots.items():
                    assert p.read_bytes() == before, 'Probe changed user/selection config'
                for action in ('install', 'install'):
                    run([binary, 'hooks', action, '--agent', 'claude', '--json'], cwd=cwd)
                owned = {p: p.read_bytes() for p in state.iterdir() if p.is_file()}
                installed_settings = settings.read_bytes()
                run([binary, 'status', '--json'], cwd=cwd)
                assert settings.read_bytes() == installed_settings
                assert owned == {p: p.read_bytes() for p in state.iterdir() if p.is_file()}
                # Real native configuration loading, with disposable HOME/config.
                config_home = root / f'config-{explicit}-{node_version}-{mode}'
                config_home.mkdir()
                config_env = dict(env, HOME=str(config_home), CLAUDE_CONFIG_DIR=str(config_home / '.claude'),
                                  ASDF_DATA_DIR=str(data), ASDF_NODEJS_VERSION=('home' if cwd == home else 'project'))
                (config_home / '.claude').mkdir()
                (config_home / '.claude/settings.json').write_bytes(settings.read_bytes())
                run(['claude', 'mcp', 'list'], cwd=config_home, process_env=config_env)
                for action in ('uninstall', 'uninstall'):
                    run([binary, 'hooks', action, '--agent', 'claude', '--json'], cwd=cwd)
                assert json.loads(settings.read_bytes()) == original_settings
                (config_home / '.claude/settings.json').write_bytes(settings.read_bytes())
                run(['claude', 'mcp', 'list'], cwd=config_home, process_env=config_env)
                snapshots[settings] = settings.read_bytes()  # Installer may preserve values with new formatting.
                for p, before in snapshots.items():
                    assert p.read_bytes() == before, 'Lifecycle changed unrelated config'
                print(f'PASS data={"explicit" if explicit else "default"} selection={cwd.name} '
                      f'mode={mode} Node={node_version} Claude={agent_version}', flush=True)


if __name__ == '__main__':
    main()
