import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from stability_pro.engine import Budget, Candidate, Engine


def engine(relays=('r1', 'r2', 'r3', 'r4')):
    return Engine('fixed-IP1', [Candidate(f'n{i}', r, 'fixed-IP1') for i, r in enumerate(relays)])


def qualify(value, node, latency=300, start=0):
    for t in (start, start + 5, start + 10):
        value.observe(node, t, True, latency, tls_ok=True, exit_ok=True)


def qualify_many(value, latencies):
    for t in (0, 5, 10):
        for node, latency in latencies.items():
            value.observe(node, t, True, latency, tls_ok=True, exit_ok=True)


class ElectionTests(unittest.TestCase):
    def test_exit_isolation(self):
        with self.assertRaises(ValueError):
            Engine('IP1', [Candidate('n1', 'r1', 'IP2')])

    def test_empty_group_rejected(self):
        with self.assertRaises(ValueError):
            Engine('IP1', [])

    def test_duplicate_identity_rejected(self):
        with self.assertRaises(ValueError):
            Engine('IP1', [Candidate('n', 'r', 'IP1')] * 2)

    def test_single_fast_probe_does_not_qualify(self):
        value = engine()
        value.observe('n0', 0, True, 10, tls_ok=True, exit_ok=True)
        self.assertIsNone(value.primary)

    def test_missing_tls_or_exit_does_not_qualify(self):
        value = engine()
        for t in (0, 5, 10):
            value.observe('n0', t, True, 200)
        self.assertFalse(value.eligible('n0', 10))

    def test_time_spread_required(self):
        value = engine()
        for t in (0, 1, 2):
            value.observe('n0', t, True, 200, tls_ok=True, exit_ok=True)
        self.assertIsNone(value.primary)

    def test_healthy_primary_stays(self):
        value = engine()
        qualify_many(value, {'n0': 300, 'n1': 100})
        self.assertEqual(value.primary, 'n0')

    def test_three_hot_candidates_and_diverse_relays(self):
        value = engine(('r1', 'r1', 'r2', 'r3'))
        qualify_many(value, {f'n{i}': 100 + i * 20 for i in range(4)})
        self.assertEqual(value.hot, ['n0', 'n2', 'n3'])

    def test_failure_switches_to_verified_standby(self):
        value = engine()
        qualify_many(value, {'n0': 300, 'n1': 300})
        value.observe('n0', 11, False)
        self.assertEqual(value.primary, 'n1')
        self.assertFalse(value.stats['n0'].cooldown_until)

    def test_two_independent_failures_cool_down(self):
        value = engine()
        qualify(value, 'n0')
        value.observe('n0', 11, False)
        value.observe('n0', 15, False)
        self.assertEqual(value.stats['n0'].cooldown_until, 75)

    def test_duplicate_failure_is_not_two_failures(self):
        value = engine()
        qualify(value, 'n0')
        value.observe('n0', 11, False)
        self.assertFalse(value.observe('n0', 12, False))
        self.assertEqual(value.stats['n0'].consecutive_failures, 1)

    def test_recovery_needs_multiple_probes_after_cooldown(self):
        value = engine()
        qualify(value, 'n0')
        value.observe('n0', 11, False)
        value.observe('n0', 15, False)
        value.observe('n0', 76, True, 100, tls_ok=True, exit_ok=True)
        self.assertFalse(value.eligible('n0', 76))
        qualify(value, 'n0', start=81)
        self.assertTrue(value.eligible('n0', 91))

    def test_certificate_error_quarantines_until_explicit_release(self):
        value = engine()
        qualify(value, 'n0')
        value.observe('n0', 11, True, 200, tls_ok=False)
        qualify(value, 'n0', start=12)
        self.assertFalse(value.eligible('n0', 22))
        value.release_quarantine('n0')
        qualify(value, 'n0', start=23)
        self.assertTrue(value.eligible('n0', 33))

    def test_wrong_exit_quarantines(self):
        value = engine()
        qualify(value, 'n0')
        value.observe('n0', 11, True, 200, exit_ok=False)
        self.assertIsNone(value.primary)
        self.assertTrue(value.stats['n0'].quarantined)

    def test_stale_standby_does_not_activate(self):
        value = engine()
        qualify_many(value, {'n0': 300, 'n1': 300})
        self.assertEqual(value.elect(41), [])

    def test_network_change_revalidates_and_ignores_old_results(self):
        value = engine()
        qualify(value, 'n0')
        generation = value.generation
        value.network_changed()
        self.assertFalse(value.observe('n0', 11, True, 100, generation=generation))
        self.assertIsNone(value.primary)

    def test_offline_does_not_penalize_every_candidate(self):
        value = engine()
        qualify(value, 'n0')
        before = value.stats['n0'].failure
        self.assertFalse(value.observe('n0', 11, False, offline=True))
        self.assertEqual(before, value.stats['n0'].failure)

    def test_latency_window_is_bounded(self):
        value = engine()
        for i in range(1000):
            value.observe('n0', i * 5, True, 200, tls_ok=True, exit_ok=True)
        self.assertEqual(len(value.stats['n0'].latencies), 64)
        self.assertLessEqual(len(value.events), 512)

    def test_scores_decay(self):
        value = engine()
        qualify(value, 'n0')
        s = value.stats['n0']
        before = s.success
        s.decay(86410)
        self.assertAlmostEqual(s.success, before / 2)

    def test_zero_delay_not_treated_as_fastest(self):
        with self.assertRaises(ValueError):
            engine().observe('n0', 0, True, 0)

    def test_same_attempt_retries_backup_before_payload(self):
        value = engine()
        qualify_many(value, {f'n{i}': 300 for i in range(3)})
        calls = []

        def dial(node):
            calls.append(node)
            if node == 'n0':
                raise TimeoutError()
            return 'established'

        chosen, connection = value.attempt_establishment(lambda: 11, dial, 20)
        self.assertEqual((chosen, connection), ('n1', 'established'))
        self.assertEqual(calls, ['n0', 'n1'])

    def test_payload_is_not_replayed(self):
        with self.assertRaises(RuntimeError):
            engine().attempt_establishment(lambda: 0, lambda n: n, 10, payload_sent=True)

    def test_deadline_stops_further_attempts(self):
        value = engine()
        qualify_many(value, {'n0': 300, 'n1': 300})
        current = [11]
        calls = []

        def dial(node):
            calls.append(node)
            current[0] = 21
            raise TimeoutError()

        with self.assertRaises(ConnectionError):
            value.attempt_establishment(lambda: current[0], dial, 20)
        self.assertEqual(calls, ['n0'])

    def test_all_failed_has_no_direct_escape(self):
        value = engine()
        qualify(value, 'n0')
        with self.assertRaises(ConnectionError):
            value.attempt_establishment(lambda: 11, lambda _: (_ for _ in ()).throw(TimeoutError()), 20)
        self.assertIsNone(value.primary)

    def test_budget_is_bounded_and_recovers(self):
        budget = Budget(limit=2, window=60)
        self.assertTrue(budget.take(0))
        self.assertTrue(budget.take(1))
        self.assertFalse(budget.take(2))
        self.assertTrue(budget.take(60))


if __name__ == '__main__':
    unittest.main()
