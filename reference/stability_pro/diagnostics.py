import io
import json
import re
import threading
import time
import zipfile
from collections import Counter
from pathlib import Path


EVENTS = {'probe', 'switch', 'cooldown', 'promote', 'network_changed', 'tls_check',
          'budget_exhausted', 'user_mark', 'snapshot', 'debug'}
ERRORS = {'none', 'network_offline', 'dial_timeout', 'relay_dial_timeout', 'residential_auth',
          'connect_rejected', 'tls_aborted', 'cert_untrusted', 'cert_hostname', 'cert_expired',
          'unexpected_exit', 'service_http_401', 'service_http_403', 'service_http_429',
          'websocket_idle', 'stream_reset'}
ENUMS = {
    'event': EVENTS,
    'error': ERRORS,
    'source': {'active', 'passive', 'user'},
    'phase': {'relay', 'connect', 'socks5', 'tls', 'http', 'establishment'},
    'serviceTag': {'latency_primary', 'latency_secondary', 'chatgpt_tls', 'android_chat_tls', 'exit'},
    'reason': {'initial', 'failure', 'replenish', 'recovery', 'network', 'budget', 'user'},
}
IDS = {'groupId', 'nodeId', 'relayId', 'primaryId', 'previousId', 'networkId'}
NUMBERS = {'routeGeneration', 'networkEpoch', 'latencyMs', 'ewmaMs', 'p95Ms',
           'lastSuccessAgeMs', 'sampleCount', 'reliabilityLower', 'cooldownMs', 'budgetUsed',
           'budgetLimit', 'inFlight', 'sanCount', 'repeated'}
BOOLEANS = {'ok', 'tlsVerified'}
HASHES = {'certSha256', 'spkiSha256'}


class Diagnostics:
    def __init__(self, directory: Path, *, file_bytes: int = 1024 * 1024, files: int = 4,
                 record_bytes: int = 4096, clock=time.time, monotonic=time.monotonic):
        if file_bytes < 1024 or not 1 <= files <= 4 or not 256 <= record_bytes <= 4096:
            raise ValueError('invalid bounded log settings')
        self.directory = Path(directory)
        self.directory.mkdir(parents=True, exist_ok=True)
        self.file_bytes = file_bytes
        self.files = files
        self.record_bytes = min(record_bytes, file_bytes)
        self.clock = clock
        self.monotonic = monotonic
        self.debug_until = 0.0
        self.dropped = 0
        self.lock = threading.RLock()
        if any(p.exists() and p.stat().st_size > file_bytes for p in self.paths()):
            raise ValueError('existing log exceeds configured budget; use a new owned directory')

    def paths(self) -> list[Path]:
        return [self.directory / f'events-{i}.jsonl' for i in range(self.files)]

    def enable_debug(self) -> None:
        with self.lock:
            self.debug_until = self.monotonic() + 600

    def debug_enabled(self) -> bool:
        return self.monotonic() < self.debug_until

    def normalize(self, event: dict) -> dict:
        safe = {'schemaVersion': 1, 'wallClock': self.clock(), 'monotonicMs': round(self.monotonic() * 1000)}
        for key, value in event.items():
            if key in ENUMS and isinstance(value, str) and value in ENUMS[key]:
                safe[key] = value
            elif key in IDS and isinstance(value, str) and re.fullmatch(r'[A-Za-z0-9_-]{1,64}', value):
                safe[key] = value
            elif key in NUMBERS and type(value) in (int, float) and 0 <= value <= 10 ** 12:
                safe[key] = value
            elif key in BOOLEANS and type(value) is bool:
                safe[key] = value
            elif key in HASHES and isinstance(value, str) and re.fullmatch(r'[a-fA-F0-9]{64}', value):
                safe[key] = value.lower()
        return safe

    def write(self, event: dict) -> bool:
        with self.lock:
            safe = self.normalize(event)
            if 'event' not in safe or (safe['event'] == 'debug' and not self.debug_enabled()):
                self.dropped += 1
                return False
            line = (json.dumps(safe, separators=(',', ':'), allow_nan=False) + '\n').encode('utf-8')
            if len(line) > self.record_bytes:
                self.dropped += 1
                return False
            current = self.paths()[0]
            if current.exists() and current.stat().st_size + len(line) > self.file_bytes:
                paths = self.paths()
                paths[-1].unlink(missing_ok=True)
                for i in range(len(paths) - 2, -1, -1):
                    if paths[i].exists():
                        paths[i].replace(paths[i + 1])
            with current.open('ab') as stream:
                stream.write(line)
            return True

    def export(self, output: Path, *, max_bytes: int = 2 * 1024 * 1024, minutes: int = 15) -> dict:
        if max_bytes < 2048 or max_bytes > 2 * 1024 * 1024:
            raise ValueError('invalid export size')
        with self.lock:
            events = []
            for path in reversed(self.paths()):
                if path.exists():
                    for line in path.read_text(encoding='utf-8').splitlines():
                        try:
                            value = json.loads(line)
                        except (ValueError, TypeError):
                            continue
                        if value['wallClock'] >= self.clock() - minutes * 60:
                            events.append(value)
            original_count = len(events)
            summary = {
                'eventCounts': dict(Counter(e['event'] for e in events)),
                'errorCounts': dict(Counter(e.get('error', 'none') for e in events)),
                'droppedThisProcess': self.dropped,
            }
            encoded = [json.dumps(e, separators=(',', ':')).encode('utf-8') + b'\n' for e in events]
            retained = []
            total = 0
            for line in reversed(encoded):
                if total + len(line) > min(1024 * 1024, max_bytes // 2):
                    break
                retained.append(line)
                total += len(line)
            retained.reverse()
            manifest = {
                'schemaVersion': 1, 'implementation': 'offline-reference',
                'requestedMinutes': minutes, 'availableEvents': original_count,
                'retainedEvents': len(retained), 'truncatedEvents': original_count - len(retained),
                'earliestAvailableWallClock': min((e['wallClock'] for e in events), default=None),
                'latestAvailableWallClock': max((e['wallClock'] for e in events), default=None),
                'earliestExportedWallClock': json.loads(retained[0])['wallClock'] if retained else None,
                'rotatedHistoryMayBeMissing': True,
            }
            buffer = io.BytesIO()
            with zipfile.ZipFile(buffer, 'w', compression=zipfile.ZIP_DEFLATED) as archive:
                archive.writestr('manifest.json', json.dumps(manifest, indent=2))
                archive.writestr('summary.json', json.dumps(summary, indent=2))
                archive.writestr('events.jsonl', b''.join(retained))
                archive.writestr('README.txt', 'Offline reference diagnostics. No raw credentials, URLs, accounts or chat data.\n')
            data = buffer.getvalue()
            if len(data) > max_bytes:
                raise ValueError('metadata exceeds export budget; no oversized file written')
            Path(output).parent.mkdir(parents=True, exist_ok=True)
            Path(output).write_bytes(data)
            return manifest
