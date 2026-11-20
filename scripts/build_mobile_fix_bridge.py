import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
OUT = Path('D:/Codex/Operations/ClashRefactor/20261027-mobile-fix/mobile-native')
LLVM = Path('C:/Users/rog/AppData/Local/Android/Sdk/ndk/27.1.12297006/toolchains/llvm/prebuilt/windows-x86_64/bin')
CPP = ROOT/'android/core/src/main/cpp'
args = [LLVM/'clang++.exe', '--target=aarch64-linux-android21', '-shared', '-fPIC', '-O2', '-std=c++17', '-fno-exceptions', '-fno-rtti', '-static-libstdc++', '-DLIBCLASH', '-I'+str(OUT), '-I'+str(ROOT/'core'), CPP/'core.cpp', CPP/'jni_helper.cpp', '-L'+str(OUT), '-lclash', '-Wl,-z,max-page-size=16384', '-Wl,-soname,libcore.so', '-Wl,--no-undefined', '-o', OUT/'libcore.so']
p = subprocess.run([str(a) for a in args], capture_output=True, text=True, creationflags=subprocess.CREATE_NO_WINDOW)
(OUT/'bridge-build.log').write_text(p.stdout+p.stderr, encoding='utf-8')
if p.returncode:
    raise RuntimeError(p.stderr)
result = {'built': True, 'sha256': hashlib.sha256((OUT/'libcore.so').read_bytes()).hexdigest(), 'command': [str(a) for a in args]}
(OUT/'bridge-build.json').write_text(json.dumps(result, indent=2), encoding='utf-8')
print(json.dumps(result))
