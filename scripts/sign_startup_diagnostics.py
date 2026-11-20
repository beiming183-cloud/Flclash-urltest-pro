import hashlib
import os
from pathlib import Path
import subprocess
import sys
import tempfile

import win32crypt


def main():
    folder = Path(__file__).resolve().parent
    apk = folder / 'FlClash-2026112001-unsigned.apk'
    expected_hash = (folder / 'SHA256SUMS.txt').read_text().split()[0]
    if hashlib.sha256(apk.read_bytes()).hexdigest() != expected_hash:
        raise ValueError('Package checksum mismatch')
    signing = Path(r'D:\Codex\UserData\Signing\flclash096')
    sdk = Path(os.environ['LOCALAPPDATA']) / 'Android/Sdk/build-tools/36.0.0'
    java_home = Path(os.environ.get('JAVA_HOME', r'C:\Program Files\Eclipse Adoptium\jdk-17.0.17.10-hotspot'))
    signer = [str(java_home / 'bin/java.exe'), '-jar', str(sdk / 'lib/apksigner.jar')]
    dest = folder / 'FlClash-0.8.96-LinkedAirport-2026112001-arm64.apk'
    secret = win32crypt.CryptUnprotectData((signing / 'password.dpapi').read_bytes(), None, None, None, 0)[1]
    encoding = 'utf-16-le' if b'\x00' in secret[:32] else 'utf-8'
    env = dict(os.environ, FLCLASH_SIGN_PASS=secret.decode(encoding).strip('\x00\r\n'))
    with tempfile.TemporaryDirectory(dir=folder) as tmp:
        candidate = Path(tmp) / 'signed.apk'
        subprocess.run(signer + ['sign', '--ks', str(signing / 'release.jks'), '--ks-pass', 'env:FLCLASH_SIGN_PASS',
                                 '--key-pass', 'env:FLCLASH_SIGN_PASS', '--out', str(candidate), str(apk)], env=env, check=True)
        info = subprocess.check_output(signer + ['verify', '--verbose', '--print-certs', str(candidate)], text=True)
        fingerprint = 'b63b2557ca6ee673514ece8652782154050277800bda2336a1b007237ff5e7b7'
        if 'signer #1 certificate sha-256 digest: ' + fingerprint not in info.lower().splitlines():
            raise ValueError('Unexpected signer certificate')
        info = subprocess.check_output([sdk / 'aapt2.exe', 'dump', 'badging', candidate], encoding='utf-8')
        if "name='com.beiming.flclash.urltestpro096.dev' versionCode='2026112001'" not in info:
            raise ValueError('Unexpected package or version')
        subprocess.run([sdk / 'zipalign.exe', '-c', '-P', '16', '4', candidate], check=True)
        candidate.replace(dest)
    print(dest)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        sys.exit(str(error))
