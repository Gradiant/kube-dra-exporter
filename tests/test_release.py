"""Regression checks for chart/app/image release consistency."""
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

import yaml

from scripts.check_release import check_release

ROOT = Path(__file__).resolve().parents[1]


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.chart_dir = Path(self.directory.name)
        self.metadata = {'version': '1.2.3', 'appVersion': '1.2.3'}
        self.values = {'image': {'tag': ''}}
        self.write()

    def write(self):
        (self.chart_dir / 'Chart.yaml').write_text(yaml.safe_dump(self.metadata))
        (self.chart_dir / 'values.yaml').write_text(yaml.safe_dump(self.values))

    def test_unified_chart_and_app_versions(self):
        for tag in (None, '1.2.3', 'v1.2.3'):
            self.assertEqual(check_release(self.chart_dir, tag), ('1.2.3', '1.2.3'))

    def test_chart_and_app_must_match(self):
        self.metadata['appVersion'] = '1.2.4'
        self.write()
        with self.assertRaisesRegex(ValueError, 'version and appVersion must match'):
            check_release(self.chart_dir)

    def test_mismatched_release_tags(self):
        for tag in ('1.2.4', 'v1.2.4'):
            with self.subTest(tag=tag), self.assertRaisesRegex(ValueError, 'does not match'):
                check_release(self.chart_dir, tag)

    def test_invalid_tag_versions(self):
        for tag in ('1.2', '01.2.3', 'v1.2', '1.2.3-01', 'v1.2.3+build', 'chart-v1.2.3', 'release-1.2.3', ''):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                check_release(self.chart_dir, tag)

    def test_prerelease_versions(self):
        self.metadata = {'version': '1.2.3-rc.1', 'appVersion': '1.2.3-rc.1'}
        self.write()
        check_release(self.chart_dir, '1.2.3-rc.1')
        check_release(self.chart_dir, 'v1.2.3-rc.1')

    def test_image_overrides(self):
        for tag in ('', None, '1.2.3'):
            self.values['image']['tag'] = tag
            self.write()
            check_release(self.chart_dir)
        for tag in ('old', '1.2.2', 1.2):
            self.values['image']['tag'] = tag
            self.write()
            with self.assertRaisesRegex(ValueError, 'image.tag'):
                check_release(self.chart_dir)

    def test_production_image_override(self):
        (self.chart_dir / 'prod-values.yaml').write_text(yaml.safe_dump({'image': {'tag': '1.2.2'}}))
        with self.assertRaisesRegex(ValueError, 'prod-values.yaml'):
            check_release(self.chart_dir)

    def test_digest_override(self):
        self.values['image']['digest'] = 'sha256:deadbeef'
        self.write()
        with self.assertRaisesRegex(ValueError, 'image.digest'):
            check_release(self.chart_dir)

    def test_invalid_app_version(self):
        for version in (None, 2.3, 'latest', '2.3.4+build'):
            self.metadata['appVersion'] = version
            self.write()
            with self.subTest(version=version), self.assertRaises(ValueError):
                check_release(self.chart_dir)

    def test_cli_blocks_mismatched_tag(self):
        result = subprocess.run([sys.executable, str(ROOT / 'scripts/check_release.py'), '--chart', str(self.chart_dir), '--release-tag', '1.2.4'], capture_output=True, text=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn('does not match', result.stderr)


if __name__ == '__main__':
    unittest.main()
