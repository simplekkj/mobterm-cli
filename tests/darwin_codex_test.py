"""Exercise the real runner entry with controlled packages and assessment tools."""

from contextlib import redirect_stderr
import io
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import darwin_codex as guard
import linux_lifecycle_test as lifecycle


class DarwinCodexTest(unittest.TestCase):
    version = '0.171.0'

    def setUp(self):
        temp = tempfile.TemporaryDirectory(prefix="codex guard's test ")
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name).resolve()
        self.prefix = self.root / 'isolated prefix'
        self.bin = self.prefix / 'bin'
        self.bin.mkdir(parents=True)
        self.package = self.prefix / 'lib/node_modules/@openai/codex'
        self.platform_package = self.package / 'node_modules/@openai/codex-darwin-x64'
        self.launcher = self.package / 'bin/codex.js'
        self.native = self.platform_package / 'vendor/x86_64-apple-darwin/bin/codex'
        self.calls = self.root / 'calls'
        self.calls.touch()
        self.write_sentinel(self.launcher, 'wrapper')
        self.write_sentinel(self.native, 'native')
        (self.bin / 'codex').symlink_to(self.launcher)
        self.link = self.root / 'prefix symlink'
        self.link.symlink_to(self.prefix, target_is_directory=True)
        self.write_manifest()
        (self.platform_package / 'package.json').write_text(json.dumps({
            'name': '@openai/codex', 'version': self.version + '-darwin-x64',
            'cpu': ['x64'], 'os': ['darwin']}))
        self.enterContext(patch.dict(guard.LAUNCHER_SHA256, {self.version: guard.digest(self.launcher)}))
        self.pin_digest()
        self.enterContext(patch('platform.system', return_value='Darwin'))
        self.enterContext(patch('platform.machine', return_value='x86_64'))
        self.search_path = str(self.link / 'bin') + ':' + os.defpath

    def pin_digest(self):
        self.enterContext(patch.dict(guard.NATIVE_SHA256, {
            (self.version, 'x64'): guard.digest(self.native)}))

    def write_sentinel(self, path, tag, *, version=None, code=0):
        path.parent.mkdir(exist_ok=True, parents=True)
        path.write_text('#!/bin/sh\n' + f'echo {tag} "$@" >> {shlex.quote(str(self.calls))}\n'
                        f'echo codex-cli {version or self.version}\nexit {code}\n')
        path.chmod(0o700)

    def write_manifest(self, version=None):
        (self.package / 'package.json').write_text(json.dumps({
            'name': '@openai/codex', 'bin': {'codex': 'bin/codex.js'},
            'version': version or self.version,
            'optionalDependencies': {'@openai/codex-darwin-x64': f'npm:@openai/codex@{self.version}-darwin-x64'}
        }))

    def runner_error(self, message, *, version=None):
        argv = ['linux_lifecycle_test.py', '--agent-prefix', str(self.link), '--config-only',
                '--expect-codex-version', version or self.version, '--expect-claude-version', '2.0.36',
                '--binary', sys.executable]
        diagnostics = io.StringIO()
        with (patch.object(sys, 'argv', argv), patch.dict(os.environ, PATH=self.search_path),
              redirect_stderr(diagnostics), self.assertRaises(SystemExit) as failure):
            lifecycle.main()
        self.assertNotEqual(failure.exception.code, 0)
        self.assertIn(message, diagnostics.getvalue())

    def test_old_expected_or_actual_package_never_executes(self):
        with patch.object(guard, 'assess_native') as assessment:
            self.runner_error('predates', version='0.118.9')
            self.write_manifest(version='0.100.0')
            self.runner_error('package version')
            assessment.assert_not_called()
        self.assertEqual(self.calls.read_text(), '')

    def test_competing_path_and_redirected_target_never_execute(self):
        other = self.root / 'other/codex'
        self.write_sentinel(other, 'competitor')
        with patch.object(guard, 'assess_native') as assessment:
            original = self.search_path
            self.search_path = str(other.parent) + ':' + original
            self.runner_error('PATH entry')
            self.search_path = original
            self.native.unlink()
            self.native.symlink_to(other)
            self.runner_error('redirected')
            self.native.unlink()
            self.write_sentinel(self.native, 'wrong-bytes')
            self.runner_error('SHA-256 mismatch')
            assessment.assert_not_called()
        self.assertEqual(self.calls.read_text(), '')

    def test_unknown_launcher_at_expected_path_never_executes(self):
        self.write_sentinel(self.launcher, 'unexpected')
        self.runner_error('PATH entry')
        self.assertEqual(self.calls.read_text(), '')

    def test_rejected_or_inconclusive_policy_never_executes(self):
        accepted_signature = subprocess.CompletedProcess([], 0, '', '')
        policies = [
            (subprocess.CompletedProcess([], 1, 'Scan completed, but failed because the software has been altered.', ''), 'refused'),
            (subprocess.CompletedProcess([], 0, 'unknown result', ''), 'inconclusive'),
            (FileNotFoundError(), 'unavailable'),
            (subprocess.TimeoutExpired([], 30), 'timed out'),
        ]
        for result, message in policies:
            with (self.subTest(message=message),
                  patch.object(subprocess, 'run', side_effect=[accepted_signature, result]) as run):
                self.runner_error(message)
                self.assertEqual([c.args[0][0] for c in run.call_args_list],
                                 ['/usr/bin/codesign', '/usr/bin/gktool'])
                self.assertTrue(all(c.args[0][-1] == self.native and c.kwargs['timeout'] == 30
                                    for c in run.call_args_list))
        with patch.object(subprocess, 'run', return_value=subprocess.CompletedProcess([], 1, '', '')) as run:
            self.runner_error('verification failed')
            self.assertEqual(run.call_count, 1)
        self.assertEqual(self.calls.read_text(), '')

    def test_acceptance_pins_every_invocation_and_detects_replacement(self):
        bin_dir = self.root / 'guard bin'
        bin_dir.mkdir()
        allowed = 'Scan completed and software is allowed by system policy.'
        with (patch.object(subprocess, 'run', side_effect=[subprocess.CompletedProcess([], 0, '', ''),
                                                          subprocess.CompletedProcess([], 0, allowed, '')]),
              redirect_stderr(io.StringIO())):
            search = guard.pin_codex(self.link, self.version, self.search_path, bin_dir)
        for args in (['--version'], ['mcp', 'list', '--json']):
            subprocess.run(['codex', *args], env={'PATH': search}, capture_output=True, check=True, timeout=5)
        other = self.root / 'replacement'
        self.write_sentinel(other, 'replacement')
        other.replace(self.native)
        refused = subprocess.run(['codex', '--version'], env={'PATH': search}, capture_output=True, timeout=5)
        self.assertNotEqual(refused.returncode, 0)
        self.assertIn(b'changed since assessment', refused.stderr)
        self.assertEqual(self.calls.read_text().splitlines(), ['native --version', 'native mcp list --json'])

    def test_real_program_mismatch_or_nonzero_after_allow_is_failure(self):
        with patch.object(guard, 'assess_native'):
            for version, code, reason in (('0.171.1', 0, 'mismatch'), (self.version, 7, 'exit 7')):
                with self.subTest(reason=reason):
                    self.write_sentinel(self.native, 'native', version=version, code=code)
                    self.pin_digest()
                    self.runner_error(reason)
        self.assertEqual(self.calls.read_text().splitlines(), ['native --version', 'native --version'])


if __name__ == '__main__':
    unittest.main()
