import io
import json
import unittest
from contextlib import redirect_stderr, redirect_stdout
from unittest.mock import patch

from select_agent_samples import MANIFEST, main, select_samples


class SelectAgentSamplesTest(unittest.TestCase):
    def setUp(self):
        self.samples = json.loads(MANIFEST.read_text())

    def test_fixed_platform_samples(self):
        minimum = select_samples(self.samples, "minimum", "Linux")
        self.assertEqual(minimum["codex"], "0.100.0")
        self.assertEqual(select_samples(self.samples, "macos-config", "Darwin"),
                         {**minimum, "codex": "0.163.0"})
        for system in ("Darwin", "Linux"):
            self.assertEqual(select_samples(self.samples, "historical", system),
                             {"codex": "0.162.1", "claude": "2.1.282"})

    def test_darwin_rejects_old_selection_in_any_real_job(self):
        for group, version in (("minimum", "0.100.0"), ("historical", "0.118.9"),
                               ("macos-config", "0.118.0")):
            with self.subTest(group=group):
                self.samples[group]["codex"] = version
                with self.assertRaisesRegex(ValueError, "signing change"):
                    select_samples(self.samples, group, "Darwin")

    def test_darwin_minimum_command_fails_before_emitting_install_environment(self):
        output, diagnostics = io.StringIO(), io.StringIO()
        with (patch("sys.argv", ["select_agent_samples.py", "minimum"]),
              patch("platform.system", return_value="Darwin"),
              redirect_stdout(output), redirect_stderr(diagnostics),
              self.assertRaises(SystemExit) as failure):
            main()
        self.assertNotEqual(failure.exception.code, 0)
        self.assertEqual(output.getvalue(), "")
        self.assertIn("refused macOS Codex 0.100.0", diagnostics.getvalue())

    def test_new_certificate_boundary_still_selects_exact_stable_versions(self):
        self.samples["macos-config"]["codex"] = "0.119.0"
        self.assertEqual(select_samples(self.samples, "macos-config", "Darwin")["codex"], "0.119.0")
        for version in ("latest", "0.119.0-beta.1", "0.119"):
            with self.subTest(version=version):
                self.samples["macos-config"]["codex"] = version
                with self.assertRaisesRegex(ValueError, "exact stable"):
                    select_samples(self.samples, "macos-config", "Darwin")


if __name__ == "__main__":
    unittest.main()
