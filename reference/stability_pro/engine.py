from collections import deque
from dataclasses import dataclass, field
from math import ceil, sqrt
from typing import Callable


@dataclass(frozen=True)
class Candidate:
    node_id: str
    relay_id: str
    exit_key: str


@dataclass
class Stats:
    success: float = 0.0
    failure: float = 0.0
    observations: int = 0
    consecutive_failures: int = 0
    cooldown_level: int = 0
    cooldown_until: float = 0.0
    quarantined: bool = False
    last_ok: float = float('-inf')
    last_tls: float = float('-inf')
    last_exit: float = float('-inf')
    first_ok: float | None = None
    updated: float | None = None
    last_fault: float = float('-inf')
    last_fault_code: str = ''
    last_outcome_ok: bool = False
    ewma: float = 0.0
    latencies: deque = field(default_factory=lambda: deque(maxlen=64))

    def decay(self, now: float) -> None:
        if self.updated is not None:
            factor = 0.5 ** (max(0.0, now - self.updated) / 86400)
            self.success *= factor
            self.failure *= factor
        self.updated = now

    def lower_bound(self) -> float:
        n = self.success + self.failure
        if n == 0:
            return 0.0
        z = 1.96
        p = self.success / n
        return (p + z * z / (2 * n) - z * sqrt((p * (1 - p) + z * z / (4 * n)) / n)) / (1 + z * z / n)

    def p95(self) -> float:
        ordered = sorted(self.latencies)
        return ordered[ceil(len(ordered) * 0.95) - 1] if ordered else float('inf')


class Budget:
    def __init__(self, limit: int = 900, window: float = 3600):
        if limit < 1 or window <= 0:
            raise ValueError('invalid budget')
        self.limit = limit
        self.window = window
        self.timestamps: deque = deque()

    def take(self, now: float) -> bool:
        while self.timestamps and self.timestamps[0] <= now - self.window:
            self.timestamps.popleft()
        if len(self.timestamps) >= self.limit:
            return False
        self.timestamps.append(now)
        return True


class Engine:
    def __init__(self, exit_key: str, candidates: list[Candidate]):
        if not candidates or any(c.exit_key != exit_key for c in candidates):
            raise ValueError('mixed or empty fixed-exit group')
        if len({c.node_id for c in candidates}) != len(candidates):
            raise ValueError('duplicate candidate identity')
        self.nodes = {c.node_id: c for c in candidates}
        self.stats = {c.node_id: Stats() for c in candidates}
        self.generation = 1
        self.primary: str | None = None
        self.hot: list[str] = []
        self.events: deque = deque(maxlen=512)

    def eligible(self, node: str, now: float) -> bool:
        s = self.stats[node]
        return (
            not s.quarantined
            and now >= s.cooldown_until
            and s.observations >= 3
            and s.first_ok is not None
            and s.last_ok - s.first_ok >= 10
            and s.last_outcome_ok
            and 0 <= now - s.last_ok <= 30
            and 0 <= now - s.last_tls <= 600
            and 0 <= now - s.last_exit <= 1800
        )

    def observe(self, node: str, now: float, ok: bool, latency_ms: float | None = None,
                *, tls_ok: bool | None = None, exit_ok: bool | None = None,
                error: str = 'dial_timeout', generation: int | None = None,
                offline: bool = False) -> bool:
        if generation is not None and generation != self.generation:
            return False
        if offline:
            self.events.append({'event': 'network_offline', 'node': node})
            return False
        s = self.stats[node]
        if not ok and tls_ok is not False and exit_ok is not False:
            if now - s.last_fault < 3 and error == s.last_fault_code:
                return False
        if ok and (latency_ms is None or latency_ms <= 0):
            raise ValueError('successful probe requires positive latency')
        s.decay(now)
        if tls_ok is False or exit_ok is False:
            s.quarantined = True
            error = 'cert_untrusted' if tls_ok is False else 'unexpected_exit'
            ok = False
        s.last_outcome_ok = ok
        if ok:
            s.success += 1
            s.observations += 1
            s.first_ok = now if s.first_ok is None else s.first_ok
            s.last_ok = now
            s.consecutive_failures = 0
            s.ewma = latency_ms if not s.latencies else 0.2 * latency_ms + 0.8 * s.ewma
            s.latencies.append(latency_ms)
            if tls_ok is True:
                s.last_tls = now
            if exit_ok is True:
                s.last_exit = now
        else:
            s.failure += 1
            s.last_fault = now
            s.last_fault_code = error
            s.consecutive_failures += 1
            if s.consecutive_failures >= 2:
                s.cooldown_until = now + min(1800, 60 * 2 ** min(s.cooldown_level, 5))
                s.cooldown_level = min(s.cooldown_level + 1, 5)
                s.observations = 0
                s.first_ok = None
        self.events.append({'event': 'probe', 'node': node, 'ok': ok, 'error': '' if ok else error})
        self.elect(now)
        return True

    def elect(self, now: float) -> list[str]:
        for s in self.stats.values():
            s.decay(now)
        eligible = [n for n in self.nodes if self.eligible(n, now)]
        if eligible:
            best = max(self.stats[n].lower_bound() for n in eligible)
            eligible.sort(key=lambda n: (
                0 if self.stats[n].lower_bound() >= best - 0.03 else 1,
                self.stats[n].p95() if self.stats[n].lower_bound() >= best - 0.03 else -self.stats[n].lower_bound(),
                self.stats[n].ewma,
                n,
            ))
        old = self.primary
        chosen = [old] if old in eligible else []
        relays = {self.nodes[n].relay_id for n in chosen}
        for distinct in (True, False):
            for n in eligible:
                if len(chosen) == 3:
                    break
                if n in chosen or (distinct and self.nodes[n].relay_id in relays):
                    continue
                chosen.append(n)
                relays.add(self.nodes[n].relay_id)
        self.hot = chosen
        self.primary = chosen[0] if chosen else None
        if self.primary != old:
            self.events.append({'event': 'switch', 'previous': old, 'primary': self.primary})
        return chosen.copy()

    def network_changed(self) -> None:
        self.generation += 1
        self.primary = None
        self.hot = []
        for s in self.stats.values():
            s.observations = 0
            s.first_ok = None
            s.last_outcome_ok = False
            s.last_tls = float('-inf')
            s.last_exit = float('-inf')
        self.events.append({'event': 'network_changed', 'generation': self.generation})

    def release_quarantine(self, node: str) -> None:
        s = self.stats[node]
        s.quarantined = False
        s.observations = 0
        s.first_ok = None
        s.last_outcome_ok = False
        s.last_tls = float('-inf')
        s.last_exit = float('-inf')

    def attempt_establishment(self, now: Callable[[], float], dial: Callable[[str], object],
                              deadline: float, *, payload_sent: bool = False) -> tuple[str, object]:
        if payload_sent:
            raise RuntimeError('application payload cannot be replayed')
        self.elect(now())
        attempts = self.hot.copy()
        failures = []
        for node in attempts[:3]:
            if now() >= deadline:
                break
            if not self.eligible(node, now()):
                continue
            try:
                return node, dial(node)
            except (TimeoutError, ConnectionError) as exc:
                failures.append(type(exc).__name__)
                self.observe(node, now(), False, error='dial_timeout')
        raise ConnectionError('no validated residential path: ' + ','.join(failures))
