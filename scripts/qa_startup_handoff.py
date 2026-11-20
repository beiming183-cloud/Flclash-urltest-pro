import argparse
import collections
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time
import xml.etree.ElementTree as ET

import yaml

PKG = 'com.beiming.flclash.urltestpro096.dev'
ACTIVITY = PKG + '/com.follow.clash.MainActivity'
IMAGE = 'https://pbs.twimg.com/media/Bv6uxxaCcAEjWHD?format=jpg&name=orig'
IMAGE_SHA = 'ada9b8490869f05ea21cacf99b889638183c26884c804859849008764579f1f7'
FILES = '/data/user/0/' + PKG + '/files'


def main():
    cli = argparse.ArgumentParser()
    cli.add_argument('--serial', required=True)
    cli.add_argument('--apk', type=Path, required=True)
    cli.add_argument('--out', type=Path, required=True)
    args = cli.parse_args()
    if not re.fullmatch(r'emulator-\d+', args.serial):
        raise RuntimeError('This check only runs on a disposable emulator.')
    adb = [str(Path(os.environ['ANDROID_HOME']) / 'platform-tools/adb.exe'), '-s', args.serial]
    def device(*cmd, timeout=15):
        return subprocess.check_output(adb + list(cmd), timeout=timeout)
    assert device('shell', 'getprop', 'ro.kernel.qemu').strip() == b'1'
    assert device('shell', 'id', '-u').strip() == b'0'
    base = device('shell', 'pm', 'path', PKG).decode().strip().removeprefix('package:')
    digest = hashlib.sha256(args.apk.read_bytes()).hexdigest()
    assert device('shell', 'sha256sum', base).decode().split()[0] == digest
    report = {'apkSha256': digest, 'emulator': args.serial, 'cycles': [], 'passed': False}
    args.out.parent.mkdir(parents=True, exist_ok=True)
    port = None
    env = {k: v for k, v in os.environ.items() if k.lower() not in {'http_proxy', 'https_proxy', 'all_proxy', 'no_proxy'}}
    def save():
        args.out.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    def nodes():
        file = '/sdcard/qa-20261118-ui.xml'
        device('shell', 'uiautomator', 'dump', file)
        return list(ET.fromstring(device('exec-out', 'cat', file)).iter('node'))
    def tap(predicate):
        for node in nodes():
            data = node.attrib
            label = (data.get('content-desc') or data.get('text', '')).split('\n')[0]
            x, y, xx, yy = map(int, re.findall(r'\d+', data['bounds']))
            if predicate(label, y):
                stamp = time.monotonic()
                device('shell', 'input', 'tap', str((x + xx)//2), str((y + yy)//2))
                return stamp
        raise RuntimeError('Required emulator control unavailable')
    def running():
        data = device('shell', 'dumpsys', 'activity', 'services', PKG)
        return bool(re.search(rb'ServiceRecord\{[^\n]*\.VpnService\b', data))
    def vpn_button():
        return tap(lambda label, y: re.fullmatch(r'\d+:\d\d:\d\d', label))
    def repeat_until(fn, seconds=18):
        limit = time.monotonic() + seconds
        while time.monotonic() < limit:
            if fn(): return
            time.sleep(.3)
        raise RuntimeError('Emulator state did not converge')
    def probe(url, expected, max_secs=7):
        path = args.out.parent / ('qa-image.body' if expected == 'image' else 'qa-exit.body')
        command = ['curl.exe', '--disable', '--silent', '--show-error', '--proxy', 'http://127.0.0.1:' + port,
                   '--connect-timeout', '4', '--max-time', str(max_secs), '--output', str(path),
                   '--write-out', '%{http_code} %{time_total}', url]
        proc = subprocess.run(command, env=env, capture_output=True, timeout=max_secs + 3, text=True)
        code, delay = proc.stdout.split() if proc.stdout.strip() else ('000', '0')
        data = path.read_bytes() if proc.returncode == 0 and path.exists() else b''
        observed = ('image' if hashlib.sha256(data).hexdigest() == IMAGE_SHA else 'mismatch') if expected == 'image' else exits.get(data.decode(errors='replace').strip(), 'no_match')
        return {'ok': proc.returncode == 0 and code == '200' and observed == expected, 'result': observed,
                'status': int(code), 'elapsedSec': float(delay), 'bytes': len(data), 'curlExit': proc.returncode}
    def business(expected):
        attempts = []
        limit = time.monotonic() + 30
        while time.monotonic() < limit and len(attempts) < 9:
            attempts.append(probe('https://api.ipify.org', expected))
            if attempts[-1]['ok']: break
            time.sleep(.5)
        return attempts
    try:
        runtime = yaml.safe_load(device('exec-out', 'cat', FILES + '/config.yaml'))
        exits = {g['expected-exit']: g['name'] for g in runtime['proxy-groups'] if g['type'] == 'stable-failover'}
        assert set(exits.values()) == {'IP1', 'IP2'} and 'DOMAIN-SUFFIX,twimg.com,X-普通机场' in runtime['rules']
        report['runtimeProfile'] = {'groups': list(exits.values()), 'xMedia': 'X-普通机场', 'port': runtime['mixed-port']}
        port = device('forward', 'tcp:0', 'tcp:' + str(runtime['mixed-port'])).decode().strip()
        start_epoch = int(device('shell', 'date', '+%s'))
        for i in range(3):
            if i != 1:
                device('shell', 'am', 'force-stop', PKG)
                device('shell', 'am', 'start', '-W', '-n', ACTIVITY, timeout=30)
            tap(lambda label, y: label == '仪表盘' and y > 800)
            repeat_until(lambda: not running())
            stamp = vpn_button()
            repeat_until(running)
            row = {'cycle': ('cold', 'warm_restart', 'cold_again')[i], 'vpn': True, 'ip1': business('IP1')}
            row['firstVerifiedSec'] = round(time.monotonic() - stamp, 2)
            row['image'] = probe(IMAGE, 'image', 16)
            report['cycles'].append(row); save()
            if i == 2:
                tap(lambda label, y: label == '代理' and y > 800)
                tap(lambda label, y: label == '出口' and y < 280)
                tap(lambda label, y: label == 'IP2' and 280 <= y < 850)
                row['ip2'] = business('IP2')
                row['ip2Confirmed'] = probe('https://api.ipify.org', 'IP2')
                row['imageWithIP2Selected'] = probe(IMAGE, 'image', 16)
                tap(lambda label, y: label == 'IP1' and 280 <= y < 850)
                tap(lambda label, y: label == '仪表盘' and y > 800)
            vpn_button(); repeat_until(lambda: not running()); row['stopped'] = True; save()
        counts = collections.Counter()
        for line in device('exec-out', 'cat', FILES + '/stability-diagnostics/events.0.jsonl').splitlines():
            try: event = json.loads(line)
            except ValueError: continue
            if datetime.datetime.fromisoformat(event['wallClock'].replace('Z','+00:00')).timestamp() >= start_epoch:
                counts[(event['event'], event.get('reason',''),event.get('ok', False))] += 1 + event.get('repeatCount',0)
        report['startupEvents'] = [{'event': k[0], 'reason': k[1], 'ok': k[2], 'rows': n} for k,n in counts.items() if k[0] in {'dial_handoff','dial_final','empty_exit'}]
        report['passed'] = all(r['vpn'] and r['ip1'][-1]['ok'] and r['image']['ok'] and r['stopped'] for r in report['cycles']) and row['ip2'][-1]['ok'] and row['ip2Confirmed']['ok'] and row['imageWithIP2Selected']['ok']
        print(json.dumps(report, ensure_ascii=False), flush=True)
        if not report['passed']: raise RuntimeError('Startup/traffic acceptance failed')
    finally:
        save()
        if port: device('forward', '--remove', 'tcp:' + port)


if __name__ == '__main__':
    main()