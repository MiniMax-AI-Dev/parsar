"""Run through a real native tool, with non-secret canaries supplied by acceptance."""
import glob
import hashlib
import json
import os
import subprocess
import urllib.error
import urllib.request
from pathlib import Path

fixture = json.loads(Path('/workspace/isolation-fixture.json').read_text())
paths = ['/environment/staging/canary',
         '/home/runtime/.parsar/parsar-daemon/default/auth.json',
         fixture['history_path'],
         '/proc/1/root/home/runtime/.parsar/parsar-daemon/default/auth.json',
         '/root/.parsar/e2b/ready.json', '/var/run/docker.sock']


def read(path):
    try:
        with open(path, 'rb') as stream:
            return stream.read(65536)
    except OSError:
        return None


result = {'private_readable': {path: read(path) is not None for path in paths}}
assert not any(result['private_readable'].values()), result
result['pid_namespace'] = os.readlink('/proc/self/ns/pid')
result['pid_namespace_isolated'] = result['pid_namespace'] != fixture['outer_pid_namespace']
assert result['pid_namespace_isolated'], 'native tools share outer daemon PID namespace'
result['pid1_comm'] = (read('/proc/1/comm') or b'').decode(errors='replace').strip()
result['pid1_env_readable'] = read('/proc/1/environ') is not None
leaks = []
for path in glob.glob('/proc/[0-9]*/environ') + glob.glob('/proc/[0-9]*/cmdline'):
    data = read(path)
    if data is None:
        continue
    for field in data.split(b'\x00'):
        value = field.split(b'=', 1)[-1]
        if hashlib.sha256(value).hexdigest() in fixture['secret_hashes']:
            leaks.append(path)
result['sensitive_process_leaks'] = leaks
assert not leaks, 'protected outer process credential accessible'
for name, command in [('sudo', ['sudo', '-n', 'id', '-u']),
                      ('privileged_account', ['su', 'user', '-c', 'id -u'])]:
    try:
        process = subprocess.run(command, input='', capture_output=True, text=True, timeout=8)
        result[name + '_denied'] = process.returncode != 0
    except FileNotFoundError:
        result[name + '_unavailable'] = True
        result[name + '_denied'] = True
    assert result[name + '_denied'], 'native shell gained privileged account'
try:
    urllib.request.urlopen('http://127.0.0.1:49983/envs', timeout=5)
except urllib.error.HTTPError as error:
    assert error.code in [401, 403], error.code
    result['envd_denied'] = True
except (urllib.error.URLError, PermissionError, TimeoutError):
    result['envd_denied'] = True
else:
    raise AssertionError('unauthenticated envd authority accessible')
result['passed'] = True
Path('/workspace/isolation-result.json').write_text(json.dumps(result))
print('ISOLATION-PASSED')
