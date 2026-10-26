#!/usr/bin/env python3
"""Black-box lifecycle checks using real, pinned Agent executables.

Run as an ordinary user with --binary PATH. No Agent credentials are read:
each test has a disposable HOME. Pairing uses a loopback HTTPS fixture, not APNs.
Requires Python 3.11+, openssl and explicitly selected real Agents on PATH.
Pass --expect-codex-version and --expect-claude-version for package provenance;
production compatibility is checked by CLI status and hook installation.
The full suite requires Linux; --config-only also runs on macOS and excludes
the HTTPS fixture, which relies on Linux's SSL_CERT_FILE trust loading.
"""
import argparse
import base64
import copy
import hashlib
import http.server
import json
import os
from pathlib import Path
import platform
import secrets
import shlex
import shutil
import ssl
import struct
import subprocess
import tempfile
import threading
import tomllib
import unittest

BINARY = None
AGENT_PATH = None


def frame(value):
    raw = json.dumps(value).encode()
    return struct.pack('>I', len(raw)) + raw


class LifecycleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="mobterm-linux-lifecycle-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.home = self.root / "home with ' spaces"
        self.home.mkdir(mode=0o700)
        self.binary = self.home / '.local/bin/mobterm-cli'
        self.binary.parent.mkdir(parents=True)
        shutil.copyfile(BINARY, self.binary)
        self.binary.chmod(0o755)
        # Allowlist ordinary process settings; never inherit real Agent credentials.
        self.env = {key: os.environ[key] for key in ('PATH', 'LANG', 'LC_ALL') if key in os.environ}
        self.env.update(HOME=str(self.home), PATH=AGENT_PATH,
                        DISABLE_UPDATES='1', DISABLE_AUTOUPDATER='1',
                        CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1',
                        MOBTERM_CONFIG_DIR=str(self.home / '.config/mobterm'),
                        CODEX_HOME=str(self.home / '.codex'),
                        CLAUDE_CONFIG_DIR=str(self.home / '.claude'))
        self.codex = Path(self.env['CODEX_HOME']) / 'config.toml'
        self.claude = Path(self.env['CLAUDE_CONFIG_DIR']) / 'settings.json'
        for path in (self.codex, self.claude):
            path.parent.mkdir(mode=0o700)
        self.original_codex = b'# user comment\nmodel = "gpt-5"\n[projects."/workspace"]\ntrust_level = "trusted" # keep\n'
        self.original_claude = {'env': {'USER_SETTING': 'keep'}, 'permissions': {'allow': ['Bash(git status)']},
                                'hooks': {'Stop': [{'hooks': [{'type': 'command', 'command': 'true'}]}],
                                          'Notification': [{'matcher': 'auth_success', 'hooks': [
                                              {'type': 'command', 'command': 'true', 'timeout': 4}]}]}}
        self.codex.write_bytes(self.original_codex)
        self.claude.write_text(json.dumps(self.original_claude))
        self.codex.chmod(0o640)
        self.claude.chmod(0o640)
        self.sentinels = {}
        for name in ('.profile', '.bashrc', '.ssh/config', '.codex/hooks.json', '.claude.json'):
            path = self.home / name
            path.parent.mkdir(exist_ok=True)
            content = {'hooks': {}} if name == '.codex/hooks.json' else {'numStartups': 3}
            path.write_text(json.dumps(content) if name.endswith('.json') else '# user-owned sentinel\n')
            self.sentinels[path] = path.read_bytes()

    def tearDown(self):
        for path, content in self.sentinels.items():
            self.assertEqual(path.read_bytes(), content, str(path))

    def cli(self, *args, data=None, code=0):
        result = subprocess.run([str(self.binary), *args], input=data, env=self.env,
                                capture_output=True, timeout=20, cwd=self.home)
        # Do not include potentially secret stdout/frame values in assertion messages.
        self.assertEqual(result.returncode, code, f'{args[:3]}: {result.stderr.decode()[:250]}')
        return result

    def hook(self, agent, action='install', code=0):
        return self.cli('hooks', action, '--agent', agent, '--json', code=code)

    def test_initialization_default_paths_and_permissions(self):
        for key in ('MOBTERM_CONFIG_DIR', 'CODEX_HOME', 'CLAUDE_CONFIG_DIR'):
            self.env.pop(key)
        first = json.loads(self.cli('status', '--installation-json').stdout)
        second = json.loads(self.cli('status', '--installation-json').stdout)
        self.assertEqual(first['installation_id'], second['installation_id'])
        state = self.home / '.config/mobterm'
        self.assertEqual(state.stat().st_mode & 0o777, 0o700)
        for path in state.iterdir():
            if path.is_file():
                self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        status = json.loads(self.cli('status', '--json').stdout)
        for agent in ('codex', 'claude'):
            self.assertTrue(status['capabilities'][agent]['installed'])
            self.assertTrue(status['capabilities'][agent]['supported'])
            self.hook(agent)
            self.hook(agent, 'uninstall')
        self.assertEqual(self.codex.read_bytes(), self.original_codex)
        self.assertEqual(json.loads(self.claude.read_bytes()), self.original_claude)

    def test_codex_notify_chain_and_later_edits(self):
        collector = self.home / 'existing notifier.py'
        output = self.home / 'notifier-result.json'
        collector.write_text('import json,sys\nfrom pathlib import Path\n'
                             'Path(sys.argv[1]).write_text(json.dumps(sys.argv[2:]))\nsys.exit(7)\n')
        argv = ['python3', str(collector), str(output), 'argument with $() and quotes']
        original = ('notify = ' + json.dumps(argv) + ' # user notifier\n').encode() + self.original_codex
        self.codex.write_bytes(original)
        self.hook('codex')
        installed = self.codex.read_bytes()
        self.hook('codex')
        self.assertEqual(self.codex.read_bytes(), installed)
        notify = tomllib.loads(installed.decode())['notify']
        payload = json.dumps({'type': 'agent-turn-complete', 'thread-id': 'synthetic-thread', 'turn-id': 'synthetic-turn'})
        result = subprocess.run([*notify, payload], env=self.env, capture_output=True, timeout=20, cwd=self.home)
        self.assertEqual(result.returncode, 7)
        self.assertEqual(json.loads(output.read_bytes()), [argv[-1], payload])
        later = b'\n[features]\nmulti_agent = true # later user edit\n'
        self.codex.write_bytes(installed + later)
        self.hook('codex', 'uninstall')
        self.hook('codex', 'uninstall')
        self.assertEqual(self.codex.read_bytes(), original + later)
        self.assertEqual(self.codex.stat().st_mode & 0o777, 0o640)

    def test_claude_existing_hooks_and_later_edits(self):
        self.hook('claude')
        installed = self.claude.read_bytes()
        self.hook('claude')
        self.assertEqual(self.claude.read_bytes(), installed)
        settings = json.loads(installed)
        self.assertEqual(settings['hooks']['Stop'], self.original_claude['hooks']['Stop'])
        for event in ('Notification', 'UserPromptSubmit'):
            managed = [hook for entry in settings['hooks'][event] for hook in entry['hooks']
                       if 'mobterm-cli' in hook.get('command', '')]
            self.assertEqual(len(managed), 1)
            payload = json.dumps({'session_id': 'synthetic-session', 'hook_event_name': event,
                                  'notification_type': 'permission_prompt'}).encode()
            result = subprocess.run(['sh', '-c', managed[0]['command']], input=payload,
                                    env=self.env, capture_output=True, timeout=20, cwd=self.home)
            self.assertEqual(result.returncode, 0)
        settings['user_added'] = {'value': 42}
        settings['hooks']['Notification'].append({'matcher': 'idle_prompt', 'hooks': [
            {'type': 'command', 'command': 'true', 'timeout': 3}]})
        self.claude.write_text(json.dumps(settings))
        self.hook('claude', 'uninstall')
        self.hook('claude', 'uninstall')
        expected = copy.deepcopy(self.original_claude)
        expected['user_added'] = {'value': 42}
        expected['hooks']['Notification'].append(settings['hooks']['Notification'][-1])
        self.assertEqual(json.loads(self.claude.read_bytes()), expected)
        self.assertEqual(self.claude.stat().st_mode & 0o777, 0o640)

    def test_claude_preexisting_identical_command_is_not_owned(self):
        command = shlex.quote(str(self.binary)) + ' notify --source claude'
        original = {'hooks': {'Notification': [{'matcher': 'user-matcher', 'hooks': [
            {'type': 'command', 'command': command, 'timeout': 10}]}]}}
        self.claude.write_text(json.dumps(original))
        self.hook('claude')
        self.hook('claude', 'uninstall')
        self.assertEqual(json.loads(self.claude.read_bytes()), original)

    def test_edited_owned_hooks_are_preserved(self):
        for agent, path in (('codex', self.codex), ('claude', self.claude)):
            self.hook(agent)
            if agent == 'codex':
                path.write_bytes(path.read_bytes().replace(b'--mobterm-chain', b'--user-change'))
            else:
                settings = json.loads(path.read_bytes())
                settings['hooks']['UserPromptSubmit'][-1]['hooks'][0]['timeout'] = 77
                path.write_text(json.dumps(settings))
            edited = path.read_bytes()
            self.hook(agent, 'uninstall', code=1)
            self.assertEqual(path.read_bytes(), edited)

    def test_invalid_configs_and_symlinks_unchanged(self):
        for agent, path, invalids in (
                ('codex', self.codex, [b'notify="bad"\n', b'notify=[1]\n', b'[broken']),
                ('claude', self.claude, [b'{broken', b'[]', b'{"hooks":42}'])):
            for data in invalids:
                path.write_bytes(data)
                self.hook(agent, code=1)
                self.assertEqual(path.read_bytes(), data)
            target = path.with_suffix('.user')
            path.rename(target)
            path.symlink_to(target)
            self.hook(agent, code=1)
            self.assertEqual(target.read_bytes(), invalids[-1])

    def test_claude_null_hooks_do_not_crash(self):
        original = b'{"hooks":null,"env":{"KEEP":"yes"}}'
        self.claude.write_bytes(original)
        self.hook('claude', code=1)
        self.assertEqual(self.claude.read_bytes(), original)

    def test_legacy_claude_ownership_requires_review(self):
        self.hook('claude')
        record_path = self.home / '.config/mobterm/hooks-claude.json'
        record = json.loads(record_path.read_bytes())
        record.pop('claude_owned')  # Shape written by the public v0.1.0 release.
        record_path.write_text(json.dumps(record))
        original = self.claude.read_bytes()
        saved = record_path.read_bytes()
        for action in ('install', 'uninstall'):
            self.hook('claude', action, code=1)
            self.assertEqual(self.claude.read_bytes(), original)
            self.assertEqual(record_path.read_bytes(), saved)

    def test_custom_config_directories(self):
        for key, filename in (('CODEX_HOME', 'config.toml'), ('CLAUDE_CONFIG_DIR', 'settings.json')):
            directory = self.root / (key + ' custom')
            directory.mkdir(mode=0o700)
            self.env[key] = str(directory)
            agent = 'codex' if key == 'CODEX_HOME' else 'claude'
            self.hook(agent)
            self.assertTrue((directory / filename).is_file())
            self.hook(agent, 'uninstall')
        self.assertEqual(self.codex.read_bytes(), self.original_codex)
        self.assertEqual(json.loads(self.claude.read_bytes()), self.original_claude)

    def test_native_agents_accept_installed_configuration(self):
        for agent in ('codex', 'claude'):
            self.hook(agent)
            # Native configuration loading without model requests or login.
            args = ['codex', 'mcp', 'list', '--json'] if agent == 'codex' else ['claude', 'mcp', 'list']
            result = subprocess.run(args, env=self.env, capture_output=True, timeout=30, cwd=self.home)
            self.assertEqual(result.returncode, 0, result.stderr.decode()[:300])
            self.hook(agent, 'uninstall')
        self.binary.unlink()
        for args in (['codex', 'mcp', 'list', '--json'], ['claude', 'mcp', 'list']):
            result = subprocess.run(args, env=self.env, capture_output=True, timeout=30, cwd=self.home)
            self.assertEqual(result.returncode, 0, result.stderr.decode()[:300])

    def test_setup_https_repeat_failure_and_credentials_retained_on_hook_removal(self):
        cert, key = self.root / 'cert.pem', self.root / 'key.pem'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                        '-keyout', str(key), '-out', str(cert), '-subj', '/CN=localhost',
                        '-addext', 'subjectAltName=DNS:localhost,IP:127.0.0.1'],
                       check=True, capture_output=True, timeout=20, cwd=self.home)
        token = secrets.token_urlsafe(32)
        calls = []
        installation = json.loads(self.cli('status', '--installation-json').stdout)['installation_id']
        pair_id = 'pair_linux_fixture_0000000000001'
        test = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass  # Never log Authorization or frames.

            def respond(self, method):
                test.assertEqual(self.headers.get('Authorization'), 'Bearer ' + token)
                body = json.loads(self.rfile.read(int(self.headers.get('Content-Length', '0'))) or b'{}')
                calls.append((method, self.path))
                if self.path.endswith('/claim'):
                    test.assertEqual(body, {'installation_id': installation, 'claim_id': pair_id})
                value = ({'event_id': body['event_id'], 'state': 'apns_accepted'}
                         if self.path.endswith('/events') else
                         {'pair_id': pair_id, 'status': 'active', 'installation_id': installation})
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(json.dumps(value).encode())

            def do_POST(self):
                self.respond('POST')

            def do_GET(self):
                self.respond('GET')

        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cert, key)
        server.socket = context.wrap_socket(server.socket, server_side=True)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.server_close)
        self.addCleanup(server.shutdown)
        self.env['SSL_CERT_FILE'] = str(cert)
        packet = {'v': 1, 'api_url': 'https://127.0.0.1:' + str(server.server_port),
                  'pair_id': pair_id, 'installation_id': installation, 'send_token': token,
                  'encryption_key': base64.b64encode(secrets.token_bytes(32)).decode()}
        for _ in range(2):
            response = json.loads(self.cli('setup', '--stdin-frame', data=frame(packet)).stdout)
            self.assertTrue(response['local_saved'])
            self.assertEqual(response['status'], 'active')
        pairs = self.home / '.config/mobterm/pairs.json'
        saved = hashlib.sha256(pairs.read_bytes()).digest()
        self.assertEqual(pairs.stat().st_mode & 0o777, 0o600)
        bad = dict(packet, installation_id='wrong_installation_0000000000')
        self.cli('setup', '--stdin-frame', data=frame(bad), code=1)
        for raw in (b'', struct.pack('>I', 20000), struct.pack('>I', 12) + b'{}'):
            self.cli('setup', '--stdin-frame', data=raw, code=1)
        self.assertEqual(hashlib.sha256(pairs.read_bytes()).digest(), saved)
        status = json.loads(self.cli('status', '--json', '--pair', pair_id).stdout)
        self.assertEqual(status['status'], 'active')
        self.assertEqual(status['pair_id'], pair_id)
        self.assertEqual(status['installation_id'], installation)
        self.assertTrue(status['local_saved'])
        result = json.loads(self.cli('test', '--pair', pair_id, '--json').stdout)
        self.assertEqual(result['state'], 'apns_accepted')  # Fixture response only.
        for agent in ('codex', 'claude'):
            self.hook(agent)
            self.hook(agent, 'uninstall')
        self.assertEqual(hashlib.sha256(pairs.read_bytes()).digest(), saved)
        self.assertEqual(sum(path.endswith('/claim') for _, path in calls), 2)
        self.assertEqual(self.codex.read_bytes(), self.original_codex)
        self.assertEqual(json.loads(self.claude.read_bytes()), self.original_claude)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True)
    parser.add_argument('--expect-codex-version', required=True)
    parser.add_argument('--expect-claude-version', required=True)
    parser.add_argument('--config-only', action='store_true',
                        help='run hook/configuration checks without the Linux HTTPS fixture')
    args, remaining = parser.parse_known_args()
    if platform.system() not in ('Linux', 'Darwin'):
        parser.error('These checks require Linux or macOS')
    if platform.system() != 'Linux' and not args.config_only:
        parser.error('The HTTPS fixture requires Linux; use --config-only on macOS')
    BINARY = str(Path(args.binary).resolve(strict=True))
    AGENT_PATH = os.environ['PATH']
    with tempfile.TemporaryDirectory(prefix='mobterm-agent-versions-') as home:
        env = dict(PATH=AGENT_PATH, HOME=home, CODEX_HOME=home + '/.codex',
                   CLAUDE_CONFIG_DIR=home + '/.claude', DISABLE_UPDATES='1',
                   DISABLE_AUTOUPDATER='1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1')
        for agent, expected in (('codex', 'codex-cli ' + args.expect_codex_version),
                                ('claude', args.expect_claude_version + ' (Claude Code)')):
            result = subprocess.run([agent, '--version'], env=env,
                                    capture_output=True, text=True, timeout=20, cwd=home)
            if result.returncode or result.stdout.strip() != expected:
                parser.error('Selected real Agent version required: ' + expected)
    tests = unittest.defaultTestLoader.getTestCaseNames(LifecycleTests)
    if args.config_only:
        tests.remove('test_setup_https_repeat_failure_and_credentials_retained_on_hook_removal')
    unittest.main(argv=[__file__, *remaining],
                  defaultTest=['LifecycleTests.' + name for name in tests])
