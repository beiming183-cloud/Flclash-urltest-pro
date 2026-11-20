import concurrent.futures
import json
from pathlib import Path
import socket
import subprocess
import time
import urllib.request

import yaml

ADB = 'C:/Users/rog/AppData/Local/Android/Sdk/platform-tools/adb.exe'
PACKAGE = 'com.beiming.flclash.urltestpro096.dev'
OPS = Path('D:/Codex/Operations/FlClashAudit/20261028-startup-emulator/final')
OPS.mkdir(exist_ok=True)


def adb(*args):
    return subprocess.run([ADB, '-s', 'emulator-5584', *args], capture_output=True, creationflags=subprocess.CREATE_NO_WINDOW)


def request_until_ready(started, expected_exit):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({'https':'http://127.0.0.1:18964'}))
    errors = []
    while time.monotonic()-started < 20:
        try:
            with opener.open('https://api.ipify.org', timeout=3) as response:
                ip = response.read(128).decode()
                if ip.strip() == expected_exit:
                    return {'ok':True, 'firstHTTPSSeconds':round(time.monotonic()-started,3), 'exit':ip, 'priorFailures':len(errors)}
                errors.append('unexpected exit')
        except Exception as error:
            errors.append(type(error).__name__)
        time.sleep(0.3)
    return {'ok':False, 'errors':errors, 'seconds':round(time.monotonic()-started,3)}


results = []
for index in range(3):
    trial = OPS/f'cold-acceptance-{index+1}'
    trial.mkdir(exist_ok=True)
    adb('shell','am','force-stop',PACKAGE)
    adb('logcat','-c')
    time.sleep(1)
    launch = adb('shell','am','start','-W','-n',PACKAGE+'/com.follow.clash.MainActivity')
    (trial/'launch.txt').write_bytes(launch.stdout+launch.stderr)
    time.sleep(5)
    (trial/'before-click.png').write_bytes(adb('exec-out','screencap','-p').stdout)
    active_config = adb('exec-out','su','0','cat','/data/data/'+PACKAGE+'/files/config.yaml').stdout
    if len(active_config) < 1000:
        raise RuntimeError('No effective profile, cannot accept this trial')
    groups = yaml.safe_load(active_config)['proxy-groups']
    expected_exit = next(g['expected-exit'] for g in groups if g['name'] == 'IP1')
    started = time.monotonic()
    adb('shell','input','tap','632','984')
    with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
        request = pool.submit(request_until_ready, started, expected_exit)
        port_ready = None
        for sample in range(20):
            if port_ready is None:
                try:
                    with socket.create_connection(('127.0.0.1',18964),timeout=0.2) as conn:
                        conn.settimeout(0.2)
                        conn.sendall(b'CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n')
                        if conn.recv(32).startswith(b'HTTP/1.1 200'):
                            port_ready=round(time.monotonic()-started,3)
                except Exception:
                    pass
            if sample in [1,5,10,19]:
                (trial/f'after-click-{sample:02d}.png').write_bytes(adb('exec-out','screencap','-p').stdout)
            time.sleep(0.5)
        network = request.result()
    pid = adb('shell','pidof',PACKAGE).stdout.decode().strip()
    logs = adb('logcat','-d')
    (trial/'logcat.txt').write_bytes(logs.stdout)
    crashed = 'panic: runtime error' in logs.stdout.decode(errors='replace') or 'FATAL EXCEPTION' in logs.stdout.decode(errors='replace')
    result = {'trial':index+1,'alive':bool(pid),'crashed':crashed,'effectiveConfigBytes':len(active_config),'firstCONNECTSeconds':port_ready,**network}
    results.append(result)
    print(json.dumps(result),flush=True)
(OPS/'cold-acceptance.json').write_text(json.dumps(results,indent=2),encoding='utf-8')
if not all(result['alive'] and not result['crashed'] and result['ok'] for result in results):
    raise SystemExit(1)
