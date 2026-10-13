import argparse
from pathlib import Path

from stability_pro.diagnostics import Diagnostics
from stability_pro.engine import Candidate, Engine


def main():
    parser = argparse.ArgumentParser(description='Offline election and diagnostic export demonstration')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    logger = Diagnostics(args.output / 'logs')
    value = Engine('fixed-IP1', [Candidate(f'node-{i}', f'relay-{i}', 'fixed-IP1') for i in range(4)])
    for t in (0, 5, 10):
        for i in range(4):
            value.observe(f'node-{i}', t, True, 200 + i * 30, tls_ok=True, exit_ok=True)
            logger.write({'event': 'probe', 'groupId': 'IP1', 'nodeId': f'node-{i}',
                          'ok': True, 'latencyMs': 200 + i * 30, 'tlsVerified': True})
    old = value.primary
    value.observe(old, 11, False)
    logger.write({'event': 'switch', 'groupId': 'IP1', 'previousId': old,
                  'primaryId': value.primary, 'reason': 'failure'})
    logger.export(args.output / 'diagnostic-example.zip')
    print(f'Offline example: {old} -> {value.primary}; three candidates: {value.hot}')
    print(f'Diagnostic ZIP: {args.output / "diagnostic-example.zip"}')


if __name__ == '__main__':
    main()
