#!/usr/bin/python3 -I
"""Trusted entry into an Environment's installed system tools."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path('/environment/packages/system')
CONFIG = Path('/environment/initialization')
SEED = Path('/opt/agents-runtime/system-root.tar.gz')
BASE_ENV = {'PATH': '/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin',
            'HOME': '/tmp', 'TMPDIR': '/tmp', 'LANG': 'C.UTF-8'}
MARKER = CONFIG / 'system-root.json'


def installed():
    try:
        if json.loads(MARKER.read_text()) != {'version': 1}:
            raise ValueError('invalid system tools receipt')
        return True
    except FileNotFoundError:
        return False


def sandbox(cwd, *, writable=False, network='enabled', workspace='/workspace', scratch=None):
    if not isinstance(cwd, str) or not cwd.startswith('/') or '\x00' in cwd:
        raise ValueError('invalid working directory')
    args = ['/usr/bin/bwrap', '--unshare-user', '--uid', '0', '--gid', '0',
            '--unshare-pid', '--unshare-ipc', '--unshare-uts', '--die-with-parent',
            '--cap-drop', 'ALL', '--bind' if writable else '--ro-bind', str(ROOT), '/',
            '--ro-bind', '/etc/resolv.conf', '/etc/resolv.conf',
            '--ro-bind', '/etc/hosts', '/etc/hosts', '--ro-bind', '/etc/ssl', '/etc/ssl',
            '--proc', '/proc', '--dev', '/dev', '--tmpfs', '/tmp', '--tmpfs', '/home',
            '--bind', workspace, '/workspace', '--dir', '/environment',
            '--bind', workspace, '/environment/workspace',
            '--bind', '/environment/packages', '/environment/packages',
            '--ro-bind', str(ROOT), str(ROOT), '--ro-bind', str(CONFIG), str(CONFIG)]
    if network == 'disabled':
        args += ['--unshare-net']
    elif network != 'enabled':
        raise ValueError('invalid network configuration')
    if scratch:
        args += ['--bind', scratch, scratch]
    return args + ['--chdir', cwd, '--']


def initialization_sandbox(cwd, network='enabled', *, writable=False):
    args = sandbox(cwd, writable=writable, network=network, workspace='/environment/workspace')
    args[1:1] = ['--new-session', '--clearenv']
    for key, value in {**BASE_ENV, 'DEBIAN_FRONTEND': 'noninteractive'}.items():
        args[-1:-1] = ['--setenv', key, value]
    return args


def install(packages):
    if not isinstance(packages, list) or not packages or any(
        not isinstance(p, str) or not p or p.startswith('-') or '\x00' in p for p in packages
    ):
        raise ValueError('invalid packages')
    manifest = json.loads(SEED.with_name('system-root.json').read_text())
    with SEED.open('rb') as stream:
        digest = hashlib.file_digest(stream, 'sha256').hexdigest()
    if manifest != {'version': 1, 'sha256': digest, 'size_bytes': SEED.stat().st_size}:
        raise ValueError('invalid system seed')
    # The immutable build artifact predates credentials; never snapshot a live Runtime.
    ROOT.mkdir(mode=0o700)
    subprocess.run(['/usr/bin/tar', '--no-same-owner', '--no-same-permissions', '-xzf', str(SEED), '-C', str(ROOT)],
                   check=True, env=BASE_ENV, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    args = initialization_sandbox('/workspace', writable=True)
    # Namespace root maps only to the unprivileged Runtime UID; _apt is unmapped.
    apt = ['/usr/bin/apt-get', '-o', 'APT::Sandbox::User=root', '-o', 'Acquire::Retries=0']
    for command in (apt + ['update'], apt + ['install', '-y', '--no-install-recommends', '--', *packages]):
        subprocess.run(args + ['/bin/bash', '--noprofile', '--norc', '-c',
                              '. /environment/initialization/tool-env.sh && exec "$@"', '--', *command],
                       check=True, env=BASE_ENV, stdin=subprocess.DEVNULL,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    fd = os.open(MARKER, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o400)
    with os.fdopen(fd, 'w') as stream:
        stream.write('{"version":1}\n')
        stream.flush()
        os.fsync(stream.fileno())


def main():
    if len(sys.argv) != 2 or not installed():
        raise ValueError('system tools unavailable')
    env = dict(BASE_ENV)
    scratch = os.environ.get('PARSAR_RUNTIME_TOOL_SCRATCH')
    if scratch:
        path = Path(scratch)
        temporary = Path(os.environ.get('TMPDIR', scratch))
        if not path.is_absolute() or path == Path('/') or path.resolve(strict=True) != path or not path.is_dir():
            raise ValueError('invalid Runtime scratch')
        if temporary.resolve(strict=True) != temporary or not temporary.is_dir() or not temporary.is_relative_to(path):
            raise ValueError('invalid native temporary directory')
        env['TMPDIR'] = str(temporary)
    # Native sandbox networking already selected the proxy and namespace.
    for key in ('HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'NO_PROXY',
                'http_proxy', 'https_proxy', 'all_proxy', 'no_proxy'):
        if key in os.environ:
            env[key] = os.environ[key]
    args = sandbox(os.getcwd(), scratch=scratch)
    args += ['/bin/bash', '--noprofile', '--norc', '-c',
             '. /environment/initialization/tool-env.sh && eval -- "$1"', '--', sys.argv[1]]
    os.execve(args[0], args, env)


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('Initialized system tools unavailable', file=sys.stderr)
        sys.exit(1)
