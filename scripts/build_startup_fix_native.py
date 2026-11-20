import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

ROOT = Path(__file__).resolve().parents[1]
OPS = Path(r'D:\Codex\Operations\ClashRefactor\20261028-startup-fix\mobile-native')
GO = r'D:\Codex\Tools\go\bin\go.exe'
LLVM = Path(r'C:\Users\rog\AppData\Local\Android\Sdk\ndk\27.1.12297006\toolchains\llvm\prebuilt\windows-x86_64\bin')
OPS.mkdir(parents=True, exist_ok=True)


def run(args, cwd, env, name):
    with (OPS / name).open('w', encoding='utf-8') as log:
        result = subprocess.run(args, cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT, creationflags=subprocess.CREATE_NO_WINDOW)
    if result.returncode:
        print((OPS / name).read_text(encoding='utf-8', errors='replace')[-6000:], flush=True)
        raise RuntimeError(name + ' failed: ' + str(result.returncode))


def main():
    env = os.environ.copy()
    env.update(GOCACHE=r'D:\Codex\Tools\go-cache', GOMODCACHE=r'D:\Codex\Tools\go-mod-cache', CGO_ENABLED='0', HTTPS_PROXY='http://127.0.0.1:7897', HTTP_PROXY='http://127.0.0.1:7897')
    run([GO, 'vet', '.'], ROOT / 'core', env, 'wrapper-vet.log')
    env.update(GOOS='android', GOARCH='arm64', CGO_ENABLED='1', CC=str(LLVM / 'clang.exe'), CXX=str(LLVM / 'clang++.exe'), CGO_CFLAGS='--target=aarch64-linux-android21 -O2 -fPIC', CGO_CXXFLAGS='--target=aarch64-linux-android21 -O2 -fPIC', CGO_LDFLAGS='--target=aarch64-linux-android21 -Wl,-z,max-page-size=16384')
    started = time.monotonic()
    native = OPS / 'libclash.so'
    args = [GO, 'build', '-p', '3', '-tags', 'with_gvisor', '-buildmode=c-shared', '-ldflags', '-s -w -X github.com/metacubex/mihomo/constant.Version=QualityTrial-20261028-startup.1-android -X github.com/metacubex/mihomo/component/trialpolicy.Mode=manual', '-o', str(native), '.']
    run(args, ROOT / 'core', env, 'native-build.log')
    report = {'built': True, 'nativeOnly': True, 'versionCode': 2026102801, 'sha256': hashlib.sha256(native.read_bytes()).hexdigest(), 'bytes': native.stat().st_size, 'buildSeconds': round(time.monotonic()-started, 3), 'goCommand': args, 'androidApi': 21, 'flutterRebuilt': False, 'realDeviceAccepted': False}
    (OPS / 'build.json').write_text(json.dumps(report, indent=2), encoding='utf-8')
    print(json.dumps(report), flush=True)


if __name__ == '__main__':
    main()
