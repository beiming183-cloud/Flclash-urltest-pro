import copy
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import struct
import subprocess
import zipfile

import win32crypt

OPS = Path(r'D:\Codex\Operations\ClashRefactor\20261025-stable-fast\mobile-package')
NATIVE = OPS.parent / 'mobile-native/libclash.so'
OUTPUTS = Path(r'C:\Users\rog\Documents\Codex\2026-10-24\clash\outputs')
SOURCE = OUTPUTS / 'FlClash-0.8.96-Stability-2026102401-arm64.apk'
TOOLS = Path(r'C:\Users\rog\AppData\Local\Android\Sdk\build-tools\36.0.0')
LLVM = Path(r'C:\Users\rog\AppData\Local\Android\Sdk\ndk\27.1.12297006\toolchains\llvm\prebuilt\windows-x86_64\bin')
JAVA = Path(r'C:\Program Files\Eclipse Adoptium\jdk-17.0.17.10-hotspot\bin\java.exe')
SIGNING = Path(r'D:\Codex\UserData\Signing\flclash096')
NAME = 'FlClash-0.8.96-StableFast-2026102501-arm64.apk'
CERT = 'b63b2557ca6ee673514ece8652782154050277800bda2336a1b007237ff5e7b7'


def run(args, env=None):
    result = subprocess.run([str(arg) for arg in args], env=env, capture_output=True, text=True, creationflags=subprocess.CREATE_NO_WINDOW)
    if result.returncode:
        raise RuntimeError('Packaging tool failed: ' + result.stderr[-1800:])
    return result.stdout+result.stderr


def strings(data, offset):
    count = struct.unpack_from('<I', data, offset+8)[0]
    flags, start = struct.unpack_from('<II', data, offset+16)
    header = struct.unpack_from('<H', data, offset+2)[0]
    positions = struct.unpack_from('<'+'I'*count, data, offset+header)
    values = []
    for value in positions:
        p = offset+start+value
        if flags & 0x100:
            for _ in range(2):
                size = data[p]
                p += 1
                if size & 0x80:
                    size = ((size & 0x7f) << 8) | data[p]
                    p += 1
            values.append(data[p:p+size].decode('utf-8'))
        else:
            size = struct.unpack_from('<H', data, p)[0]
            p += 2
            if size & 0x8000:
                size = ((size & 0x7fff) << 16) | struct.unpack_from('<H', data, p)[0]
                p += 2
            values.append(data[p:p+2*size].decode('utf-16-le'))
    return values


def exports(path):
    output = run([LLVM / 'llvm-nm.exe', '--dynamic', '--defined-only', path])
    return {line.split()[-1] for line in output.splitlines() if len(line.split()) >= 3}


def main(*, work=OPS, native_path=NATIVE, out=OUTPUTS, source_apk=SOURCE,
         apk_name=NAME, source_version=2026102401, target_version=2026102501):
    work.mkdir(parents=True, exist_ok=True)
    out.mkdir(parents=True, exist_ok=True)
    if not (0 < source_version < target_version <= 2100000000) or Path(apk_name).name != apk_name:
        raise ValueError('invalid APK version or output name')
    original_signer = run([JAVA, '-jar', TOOLS / 'lib/apksigner.jar', 'verify', '--print-certs', source_apk])
    if CERT not in original_signer.lower():
        raise ValueError('unexpected source certificate')
    native = native_path.read_bytes()
    assert native[:5] == b'\x7fELF\x02' and struct.unpack_from('<H', native, 18)[0] == 183
    phoff = struct.unpack_from('<Q', native, 32)[0]
    phsize, phcount = struct.unpack_from('<HH', native, 54)
    alignments = []
    for index in range(phcount):
        start = phoff+index*phsize
        if struct.unpack_from('<I', native, start)[0] == 1:
            file_offset, address = struct.unpack_from('<QQ', native, start+8)
            alignment = struct.unpack_from('<Q', native, start+48)[0]
            assert alignment >= 16384 and (address-file_offset) % 16384 == 0
            alignments.append(alignment)
    unsigned = work / 'unsigned.apk'
    unchanged = []
    with zipfile.ZipFile(source_apk) as source:
        manifest = bytearray(source.read('AndroidManifest.xml'))
        offset, changed = 8, 0
        pool = []
        while offset < len(manifest):
            kind, header, length = struct.unpack_from('<HHI', manifest, offset)
            assert length >= header >= 8 and offset+length <= len(manifest)
            if kind == 1:
                pool = strings(manifest, offset)
            if kind == 0x0102:
                ext = offset+16
                attribute_start, attribute_size, count = struct.unpack_from('<HHH', manifest, ext+8)
                for index in range(count):
                    at = ext+attribute_start+index*attribute_size
                    name = struct.unpack_from('<I', manifest, at+4)[0]
                    if pool[name] == 'versionCode':
                        assert struct.unpack_from('<I', manifest, at+16)[0] == source_version
                        struct.pack_into('<I', manifest, at+8, 0xffffffff)
                        struct.pack_into('<I', manifest, at+16, target_version)
                        changed += 1
            offset += length
        assert changed == 1
        old_native = work / 'old-libclash.so'
        old_native.write_bytes(source.read('lib/arm64-v8a/libclash.so'))
        old_exports, new_exports = exports(old_native), exports(native_path)
        assert old_exports <= new_exports, sorted(old_exports-new_exports)
        with zipfile.ZipFile(unsigned, 'w') as output:
            for item in source.infolist():
                name = item.filename
                if name.startswith('META-INF/') and (name.upper().endswith(('.RSA', '.DSA', '.EC', '.SF')) or name.upper() == 'META-INF/MANIFEST.MF'):
                    continue
                value = bytes(manifest) if name == 'AndroidManifest.xml' else native if name == 'lib/arm64-v8a/libclash.so' else source.read(name)
                output.writestr(copy.copy(item), value)
                if name not in ('AndroidManifest.xml', 'lib/arm64-v8a/libclash.so'):
                    unchanged.append(name)
        with zipfile.ZipFile(unsigned) as updated:
            assert all(updated.read(name) == source.read(name) for name in unchanged)
    aligned = work / 'aligned.apk'
    run([TOOLS / 'zipalign.exe', '-P', '16', '-f', '4', unsigned, aligned])
    decrypted = win32crypt.CryptUnprotectData((SIGNING / 'password.dpapi').read_bytes(), None, None, None, 0)[1]
    password = decrypted.decode('utf-16-le' if b'\x00' in decrypted[:32] else 'utf-8').strip('\x00\r\n')
    env = os.environ.copy()
    env['FLCLASH_SIGN_STOREPASS'] = password
    env['FLCLASH_SIGN_KEYPASS'] = password
    signed = work / apk_name
    run([JAVA, '-jar', TOOLS / 'lib/apksigner.jar', 'sign', '--ks', SIGNING / 'release.jks', '--ks-pass', 'env:FLCLASH_SIGN_STOREPASS', '--key-pass', 'env:FLCLASH_SIGN_KEYPASS', '--out', signed, aligned], env)
    verification = run([JAVA, '-jar', TOOLS / 'lib/apksigner.jar', 'verify', '--verbose', '--print-certs', signed])
    assert CERT in verification.lower()
    run([TOOLS / 'zipalign.exe', '-c', '-P', '16', '-v', '4', signed])
    badging = run([TOOLS / 'aapt2.exe', 'dump', 'badging', signed])
    assert "name='com.beiming.flclash.urltestpro096.dev'" in badging and f"versionCode='{target_version}'" in badging
    (work / 'signature-verification.txt').write_text(verification, encoding='utf-8')
    shutil.copy2(signed, out / apk_name)
    digest = hashlib.sha256(signed.read_bytes()).hexdigest()
    report = {'passed': True, 'file': str(out / apk_name), 'sha256': digest, 'nativeSha256': hashlib.sha256(native).hexdigest(), 'certificateSHA256': CERT, 'versionCode': target_version, 'unchangedEntries': len(unchanged), 'oldNativeExports': len(old_exports), 'newNativeExports': len(new_exports), 'missingExports': [], 'loadAlignments': alignments, 'nativeCoreOnly': True, 'flutterRebuilt': False, 'realDeviceAccepted': False}
    (work / 'package.json').write_text(json.dumps(report, indent=2), encoding='utf-8')
    print(json.dumps(report), flush=True)


if __name__ == '__main__':
    import argparse
    parser = argparse.ArgumentParser()
    parser.add_argument('--work', type=Path, default=OPS)
    parser.add_argument('--native', dest='native_path', type=Path, default=NATIVE)
    parser.add_argument('--out', type=Path, default=OUTPUTS)
    parser.add_argument('--source', dest='source_apk', type=Path, default=SOURCE)
    parser.add_argument('--name', dest='apk_name', default=NAME)
    parser.add_argument('--source-version', type=int, default=2026102401)
    parser.add_argument('--target-version', type=int, default=2026102501)
    main(**vars(parser.parse_args()))
