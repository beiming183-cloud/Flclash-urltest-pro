import json
import sys
import tempfile
import threading
import unittest
import zipfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from stability_pro.diagnostics import Diagnostics


class DiagnosticTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.now = [1000.0]
        self.log = Diagnostics(self.root / 'logs', file_bytes=2048,
                               clock=lambda: self.now[0], monotonic=lambda: self.now[0])

    def test_sensitive_fields_and_raw_errors_are_dropped(self):
        self.log.write({'event': 'probe', 'nodeId': 'node-1', 'ok': False,
                        'password': 'FAKE_SECRET', 'url': 'https://fake.invalid/token/FAKE_SECRET',
                        'message': 'Authorization FAKE_SECRET', 'error': 'raw FAKE_SECRET',
                        'issuer': 'private cert name'})
        text = self.log.paths()[0].read_text()
        self.assertNotIn('FAKE_SECRET', text)
        self.assertNotIn('private cert', text)
        self.assertNotIn('error', json.loads(text))

    def test_rotate_and_restart_stay_bounded(self):
        for i in range(2000):
            self.log.write({'event': 'probe', 'nodeId': 'node-1', 'latencyMs': i, 'error': 'none'})
        self.assertLessEqual(sum(p.stat().st_size for p in self.log.paths() if p.exists()), 8192)
        restarted = Diagnostics(self.root / 'logs', file_bytes=2048)
        restarted.write({'event': 'switch', 'reason': 'failure'})
        for p in restarted.paths():
            if p.exists():
                self.assertLessEqual(p.stat().st_size, 2048)
                for line in p.read_text().splitlines():
                    json.loads(line)

    def test_debug_auto_disables_after_ten_minutes(self):
        self.assertFalse(self.log.write({'event': 'debug'}))
        self.log.enable_debug()
        self.assertTrue(self.log.write({'event': 'debug'}))
        self.now[0] += 600
        self.assertFalse(self.log.write({'event': 'debug'}))

    def test_export_is_small_and_parseable(self):
        for i in range(100):
            self.log.write({'event': 'switch', 'nodeId': 'node-1', 'reason': 'failure', 'latencyMs': i})
        output = self.root / 'diagnostic.zip'
        manifest = self.log.export(output, max_bytes=4096)
        self.assertLessEqual(output.stat().st_size, 4096)
        self.assertGreater(manifest['truncatedEvents'], 0)
        with zipfile.ZipFile(output) as archive:
            self.assertEqual(set(archive.namelist()), {'events.jsonl', 'summary.json', 'manifest.json', 'README.txt'})
            lines = archive.read('events.jsonl').decode().splitlines()
            self.assertEqual(len(lines), manifest['retainedEvents'])
            for line in lines:
                json.loads(line)

    def test_expired_events_not_exported(self):
        self.log.write({'event': 'probe', 'error': 'dial_timeout'})
        self.now[0] += 901
        manifest = self.log.export(self.root / 'empty.zip')
        self.assertEqual(manifest['availableEvents'], 0)

    def test_non_whitelisted_ids_and_certificate_text_removed(self):
        self.log.write({'event': 'tls_check', 'nodeId': 'user@private.invalid',
                        'certSha256': 'certificate PEM', 'tlsVerified': False,
                        'error': 'cert_hostname'})
        line = json.loads(self.log.paths()[0].read_text())
        self.assertNotIn('nodeId', line)
        self.assertNotIn('certSha256', line)
        self.assertEqual(line['error'], 'cert_hostname')

    def test_threads_do_not_corrupt_lines(self):
        def worker():
            for _ in range(100):
                self.log.write({'event': 'snapshot', 'ok': True})
        threads = [threading.Thread(target=worker) for _ in range(4)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        for p in self.log.paths():
            if p.exists():
                for line in p.read_text().splitlines():
                    json.loads(line)

    def test_record_cap_rejects_oversized_normalized_event(self):
        log = Diagnostics(self.root / 'small', record_bytes=256)
        event = {'event': 'tls_check', 'nodeId': 'n' * 64, 'relayId': 'r' * 64,
                 'certSha256': 'a' * 64, 'spkiSha256': 'b' * 64}
        self.assertFalse(log.write(event))
        self.assertEqual(log.dropped, 1)

    def test_real_four_mib_cap_after_more_than_sixteen_mib(self):
        log = Diagnostics(self.root / 'full')
        event = {'event': 'probe', 'nodeId': 'n' * 64, 'relayId': 'r' * 64,
                 'certSha256': 'a' * 64, 'spkiSha256': 'b' * 64,
                 'latencyMs': 300, 'error': 'dial_timeout', 'phase': 'tls'}
        estimated = len(json.dumps(log.normalize(event), separators=(',', ':'))) + 1
        count = 17 * 1024 * 1024 // estimated + 1
        for _ in range(count):
            self.assertTrue(log.write(event))
        self.assertLessEqual(sum(p.stat().st_size for p in log.paths()), 4 * 1024 * 1024)


if __name__ == '__main__':
    unittest.main()
