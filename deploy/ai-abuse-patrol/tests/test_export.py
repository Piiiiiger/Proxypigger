import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

BASE = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('patrol_export', BASE / 'ai-abuse-patrol/scripts/export.py')
exporter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(exporter)


class ExportTests(unittest.TestCase):
    def test_fixed_command_and_symlink_boundaries(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / 'state'
            run_id = '20261019T000000000000Z'
            run = root / 'runs' / run_id
            run.mkdir(parents=True)
            (root / 'latest-status.json').write_text(json.dumps({
                'status': 'reviewed', 'report': str(run / 'report.md')}))
            for name in ['report.json', 'evidence.json', 'identities.json']:
                (run / name).write_text('{}')
            with patch.object(exporter, 'ROOT', str(root)), patch.dict(os.environ, {'SSH_ORIGINAL_COMMAND': 'ai-patrol-export-v2'}):
                self.assertEqual(exporter.export()['run_id'], run_id)
                secret = Path(temp) / 'outside.json'
                secret.write_text('{"private":"must not export"}')
                (run / 'identities.json').unlink()
                (run / 'identities.json').symlink_to(secret)
                with self.assertRaises(OSError):
                    exporter.export()
            with patch.dict(os.environ, {'SSH_ORIGINAL_COMMAND': 'cat /etc/shadow'}):
                with self.assertRaises(ValueError):
                    exporter.export()
