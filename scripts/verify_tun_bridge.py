import ctypes
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
OPS = Path('D:/Codex/Operations/FlClashAudit/20261027-fix/bridge-test')
LLVM = Path('C:/Users/rog/AppData/Local/Android/Sdk/ndk/27.1.12297006/toolchains/llvm/prebuilt/windows-x86_64/bin')
OPS.mkdir(parents=True, exist_ok=True)


def check(source, name):
    text = source.read_text(encoding='utf-8')
    start = text.index('Java_com_follow_clash_core_Core_startTun(')
    end = text.index('\n}\n', start) + 2
    function = 'void ' + text[start:end]
    shim = '''
using jobject = void*;
using jstring = char*;
using jint = int;
struct JNIEnv {
    int thrown = 0;
    void* FindClass(const char*) { return this; }
    int ThrowNew(void*, const char*) { thrown++; return 0; }
    void DeleteLocalRef(void*) {}
};
static bool result;
void* new_global(void* p) { return p; }
char* get_string(char* p) { return p; }
bool startTUN(void*, int, char*, char*, char*) { return result; }
'''
    harness = '''
extern "C" __declspec(dllexport) int verify(int success) {
    result = success != 0;
    JNIEnv env;
    Java_com_follow_clash_core_Core_startTun(&env, nullptr, 10, nullptr, nullptr, nullptr, nullptr);
    return env.thrown;
}
'''
    cpp, obj, dll = [OPS / (name + suffix) for suffix in ('.cpp', '.obj', '.dll')]
    cpp.write_text(shim + function + harness, encoding='utf-8')
    subprocess.run([LLVM/'clang++.exe', '--target=x86_64-pc-windows-msvc', '-c', '-ffreestanding', '-fno-exceptions', '-fno-rtti', str(cpp), '-o', str(obj)], check=True, creationflags=subprocess.CREATE_NO_WINDOW)
    subprocess.run([LLVM/'ld.lld.exe', '-flavor', 'link', '/dll', '/noentry', '/out:'+str(dll), str(obj)], check=True, creationflags=subprocess.CREATE_NO_WINDOW)
    lib = ctypes.CDLL(str(dll))
    lib.verify.argtypes = [ctypes.c_int]
    lib.verify.restype = ctypes.c_int
    return {'failedStartExceptions': lib.verify(0), 'successfulStartExceptions': lib.verify(1)}


before = check(OPS.parent/'before/android/core/src/main/cpp/core.cpp', 'before')
after = check(ROOT/'android/core/src/main/cpp/core.cpp', 'after')
assert before == {'failedStartExceptions': 0, 'successfulStartExceptions': 0}
assert after == {'failedStartExceptions': 1, 'successfulStartExceptions': 0}
result = {'before': before, 'after': after, 'passed': True, 'scope': 'actual JNI start function with simulated core result and JNI exception boundary; not Android runtime'}
(OPS/'result.json').write_text(json.dumps(result, indent=2), encoding='utf-8')
print(json.dumps(result))
