#!/usr/bin/env python3
"""One-shot trusted bootstrap; never a model/tool execution service."""
import json
import os
from pathlib import Path
import subprocess

root = Path('/root/.parsar/e2b')
root.mkdir(mode=0o700, parents=True, exist_ok=True)
root.chmod(0o700)
source = root / 'bootstrap.json'
receipt = root / 'ready.json'
if receipt.exists():
    raise RuntimeError('Runtime initialization cannot be replayed')
bootstrap = json.loads(source.read_text())
# E2B finalization makes /usr/local world-writable after template commands.
# Restore trusted executable ownership before launching the unprivileged Runtime.
subprocess.run(['chown', '-R', 'root:root', '/usr/local'], check=True)
subprocess.run(['chmod', '-R', 'go-w', '/usr/local'], check=True)
os.chmod('/usr/local/bin/agents-api-tool-root', 0o555)
# The provider also injects these root service/boot files with mode 0777.
for protected in ['/usr/bin/envd', '/etc/inittab', '/etc/init.d/rcS']:
    # Some cloud images omit rcS after boot; no absent startup file needs access.
    if protected == '/etc/init.d/rcS' and not Path(protected).exists():
        continue
    os.chown(protected, 0, 0)
    os.chmod(protected, 0o755)
# E2B also provisions an unused passwordless sudo account. Only root bootstrap
# and the explicit runtime account are used by this deployment.
subprocess.run(['usermod', '--lock', '--shell', '/usr/sbin/nologin', 'user'], check=True)
environment = json.loads(Path('/etc/parsar-runtime-env.json').read_text())
environment.update(
    PATH='/usr/local/bin:/usr/bin:/bin',
    PARSAR_RUNTIME_ENVIRONMENT_ID=bootstrap['EnvironmentID'],
    PARSAR_RUNTIME_SESSION_ID=bootstrap['session_id'],
    PARSAR_RUNTIME_NETWORK_ACCESS=bootstrap['network_access'] or 'enabled',
)
subprocess.run(['mount', '--bind', '/environment/workspace', '/workspace'], check=True)
profile = Path('/home/runtime/.parsar/parsar-daemon/default')
profile.mkdir(mode=0o700, parents=True, exist_ok=True)
for directory in [Path('/home/runtime'), Path('/home/runtime/.parsar'), profile.parent, profile,
                  Path('/environment/workspace'), Path('/environment/staging'),
                  Path('/environment/initialization'), Path('/environment/packages')]:
    os.chown(directory, 1000, 1000)
    directory.chmod(0o700)
auth = profile / 'auth.json'
with auth.open('x') as stream:
    json.dump({key: bootstrap[key] for key in ['server_url', 'runtime_id', 'runner_credential']}, stream)
auth.chmod(0o600)
os.chown(auth, 1000, 1000)
source.unlink()
log = Path('/home/runtime/.parsar/parsar-daemon/default/daemon.log')
with log.open('xb') as stream:
    os.fchmod(stream.fileno(), 0o600)
    os.fchown(stream.fileno(), 1000, 1000)
    subprocess.Popen(['/usr/local/bin/parsar-daemon', 'connect', '--profile', 'default'],
                     cwd='/environment/workspace', env=environment, user=1000, group=1000,
                     extra_groups=[], start_new_session=True, stdin=subprocess.DEVNULL,
                     stdout=stream, stderr=subprocess.STDOUT, umask=0o077)
# Last mutating step. A lost response may observe this receipt but never rerun
# credential injection or daemon startup. A connected daemon is checked by Core.
temporary = root / 'ready.tmp'
with temporary.open('x') as stream:
    os.fchmod(stream.fileno(), 0o600)
    json.dump({key: bootstrap[key] for key in
               ['TenantID', 'EnvironmentID', 'AllocationID', 'session_id', 'runtime_id']}, stream)
    stream.flush()
    os.fsync(stream.fileno())
os.replace(temporary, receipt)
