import json
import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / 'docs/stability-pro'
REQUIRED = [
    'README.md', 'README_UPSTREAM.md', 'LICENSE', 'core/Clash.Meta/LICENSE',
    'AGENTS.md', 'pubspec.yaml', 'setup.dart',
    'docs/stability-pro/START_HERE.zh-CN.md', 'docs/stability-pro/SPEC.zh-CN.md',
    'docs/stability-pro/INTEGRATION.zh-CN.md', 'docs/stability-pro/SSL.zh-CN.md',
    'docs/stability-pro/LOGGING.zh-CN.md', 'docs/stability-pro/BUILD_AND_ACCEPTANCE.zh-CN.md',
    'docs/stability-pro/UPSTREAM.md', 'docs/stability-pro/PROGRESS.zh-CN.md',
    'fixtures/stability-pro/observed-incident.json', 'fixtures/stability-pro/profile.example.yaml',
    'reference/stability_pro/engine.py', 'reference/stability_pro/diagnostics.py',
    'core/Clash.Meta/adapter/outboundgroup/urltest.go',
    'core/Clash.Meta/adapter/outboundgroup/parser.go', 'core/Clash.Meta/constant/adapters.go',
    'core/Clash.Meta/adapter/outbound/http.go', 'core/Clash.Meta/adapter/outbound/socks5.go',
    'core/route.go', 'lib/providers/route_state.dart', 'lib/providers/app.dart',
    'android/app/src/main/kotlin/com/follow/clash/ServiceState.kt',
]


def verify():
    for name in REQUIRED:
        if not (ROOT / name).is_file():
            raise AssertionError(f'missing handoff/source file: {name}')
    docs = [ROOT / 'README.md'] + list(DOCS.glob('*.md'))
    links = 0
    for p in docs:
        text = p.read_text(encoding='utf-8')
        for target in re.findall(r'\]\(([^)]+)\)', text):
            if target.startswith(('https://', 'http://', '#')):
                continue
            path = (p.parent / target.split('#')[0]).resolve()
            if not path.exists():
                raise AssertionError(f'broken link in {p.name}: {target}')
            links += 1
    if not (ROOT / 'core/Clash.Meta/go.mod').exists() or (ROOT / 'core/Clash.Meta/.git').exists():
        raise AssertionError('core must be vendored without nested Git metadata')
    if '尚未接入' not in (ROOT / 'README.md').read_text(encoding='utf-8'):
        raise AssertionError('handoff status must distinguish runtime from reference implementation')
    workflows = list((ROOT / '.github/workflows').glob('*'))
    if {p.name for p in workflows} != {'stability-reference.yml', 'stability-android.yml'}:
        raise AssertionError('unexpected workflow enabled')
    incident = json.loads((ROOT / 'fixtures/stability-pro/observed-incident.json').read_text())
    if not incident['unknown'] or not incident['excluded']:
        raise AssertionError('incident must preserve uncertainty and sanitization boundary')
    yaml = (ROOT / 'fixtures/stability-pro/profile.example.yaml').read_text()
    if '198.51.100.10' not in yaml or 'OFFLINE EXAMPLE ONLY' not in yaml:
        raise AssertionError('example must use reserved, non-working endpoints')
    for folder in ('reference', 'fixtures/stability-pro', 'docs/stability-pro'):
        for p in (ROOT / folder).rglob('*'):
            if p.is_file() and p.suffix in {'.md', '.py', '.json', '.yaml'}:
                text = p.read_text(encoding='utf-8')
                if re.search(r'-----BEGIN (?:OPENSSH |RSA |EC )?PRIVATE KEY-----', text):
                    raise AssertionError(f'private key in new handoff files: {p.name}')
    result = {'requiredFiles': len(REQUIRED), 'localLinks': links, 'runtimeIntegrated': (ROOT / 'core/Clash.Meta/adapter/outboundgroup/stable.go').exists(),
              'androidBuilt': False, 'onlyReferenceCiEnabled': False}
    print(json.dumps(result, ensure_ascii=False))
    return result


if __name__ == '__main__':
    verify()
