import argparse
import json
from pathlib import Path
import re
import subprocess
import time
import xml.etree.ElementTree as ET

ADB = 'C:/Users/rog/AppData/Local/Android/Sdk/platform-tools/adb.exe'
PACKAGE = 'com.beiming.flclash.urltestpro096.dev'
OPS = Path('D:/Codex/Operations/FlClashAudit/20261028-startup-emulator')


def adb(*args):
    return subprocess.run([ADB, '-s', 'emulator-5584', *args], capture_output=True, creationflags=subprocess.CREATE_NO_WINDOW)


def ui():
    adb('shell', 'uiautomator', 'dump', '/sdcard/qa-window.xml')
    return adb('exec-out', 'cat', '/sdcard/qa-window.xml').stdout


def tap_label(text):
    for node in ET.fromstring(ui()).iter('node'):
        if node.get('text') == text or node.get('content-desc', '').split('\n')[0] == text:
            x, y, xx, yy = map(int, re.findall(r'\d+', node.get('bounds')))
            adb('shell', 'input', 'tap', str((x+xx)//2), str((y+yy)//2))
            return True
    return False


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--label', required=True)
    parser.add_argument('--seconds', type=int, default=65)
    args = parser.parse_args()
    output = OPS/args.label
    output.mkdir(exist_ok=True)
    adb('logcat', '-c')
    started = time.monotonic()
    adb('shell', 'input', 'tap', '632', '984')
    rows = []
    index = 0
    while time.monotonic()-started < args.seconds:
        raw = ui()
        (output/f'ui-{index:02d}.xml').write_bytes(raw)
        nodes = list(ET.fromstring(raw).iter('node'))
        texts = [node.get('text') or node.get('content-desc') for node in nodes if node.get('text') or node.get('content-desc')]
        if any('Connection request' in text or '连接请求' in text for text in texts):
            tap_label('OK') or tap_label('确定')
        services = adb('shell', 'dumpsys', 'activity', 'services', PACKAGE).stdout.decode(errors='replace')
        (output/f'services-{index:02d}.txt').write_text(services, encoding='utf-8')
        runtime = [text for text in texts if re.fullmatch(r'\d+:\d\d:\d\d', text)]
        row = {'seconds': round(time.monotonic()-started, 3), 'runtime': runtime, 'connecting': any(text in ['连接中', 'Connecting'] for text in texts), 'vpnService': 'VpnService' in services}
        rows.append(row)
        print(json.dumps(row), flush=True)
        if index in [0, 2, 5, 10, 20]:
            (output/f'screen-{index:02d}.png').write_bytes(adb('exec-out', 'screencap', '-p').stdout)
        index += 1
        time.sleep(1)
    (output/'logcat.txt').write_bytes(adb('logcat', '-d').stdout)
    (output/'result.json').write_text(json.dumps(rows, indent=2), encoding='utf-8')


if __name__ == '__main__':
    main()
