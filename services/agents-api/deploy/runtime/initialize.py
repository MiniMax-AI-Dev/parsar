"""Trusted hosted initialization. Invoke with /usr/bin/python3 -I -S.

Core owns sequencing and the completion ledger. This helper executes one bounded
operation; it never retries, schedules, selects a harness or interprets templates.
"""
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys

ROOT = Path('/environment')
CONFIG = ROOT / 'initialization'
PACKAGES = ROOT / 'packages'
ENV_FILE = CONFIG / 'tool-env.sh'
CAPABILITIES = CONFIG / 'capabilities'
SKILLS = CAPABILITIES / 'skills'
MAX_INPUT = 32 * 1024 * 1024
BASE_ENV = {'PATH': '/usr/local/bin:/usr/bin:/bin', 'HOME': '/tmp', 'LANG': 'C.UTF-8'}
DIRECTORY = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC


def open_directory(parent, name):
    """Do not follow user-created directory aliases outside the Runtime roots."""
    return os.open(name, DIRECTORY, dir_fd=parent)


def roots():
    root = os.open('/', DIRECTORY)
    try:
        environment = open_directory(root, 'environment')
    finally:
        os.close(root)
    try:
        for name in ('workspace', 'packages', 'initialization'):
            child = open_directory(environment, name)
            os.close(child)
    finally:
        os.close(environment)


def install_skill(request):
    name, files = request['name'], request['files']
    if not isinstance(name, str) or not re.fullmatch('[a-z0-9]+(?:[-_][a-z0-9]+)*', name) or len(name) > 64 or not isinstance(files, list) or not 0 < len(files) <= 1000:
        raise ValueError('invalid skill')
    directory = os.open(SKILLS, DIRECTORY)
    try:
        os.mkdir(name, mode=0o700, dir_fd=directory)
        skill = open_directory(directory, name)
    finally:
        os.close(directory)
    total = 0
    try:
        for file in files:
            relative = file['path']
            if not isinstance(relative, str) or len(relative) > 4096 or any(c in relative for c in ('\\', '\x00', '\r', '\n')):
                raise ValueError('invalid skill path')
            parts = relative.split('/')
            if any(not part or part in ('.', '..') for part in parts):
                raise ValueError('invalid skill path')
            body = base64.b64decode(file['data'], validate=True)
            total += len(body)
            if total > 20 * 1024 * 1024:
                raise ValueError('skill too large')
            parent = os.dup(skill)
            try:
                for part in parts[:-1]:
                    try:
                        os.mkdir(part, mode=0o700, dir_fd=parent)
                    except FileExistsError:
                        pass
                    child = open_directory(parent, part)
                    os.close(parent)
                    parent = child
                # A fresh installation cannot replace an existing member.
                try:
                    os.stat(parts[-1], dir_fd=parent, follow_symlinks=False)
                except FileNotFoundError:
                    pass
                else:
                    raise ValueError('duplicate skill member')
                result = subprocess.run(['/usr/local/bin/agents-api-codex-write', str(SKILLS / name), relative,
                                         str(len(body)), str(ROOT / 'staging')],
                                        input=body + hashlib.sha256(body).digest(), env=BASE_ENV,
                                        capture_output=True, check=True)
                receipt = json.loads(result.stdout)
                if result.stderr or receipt != {'version': 1, 'outcome': 'completed', 'size_bytes': len(body)}:
                    raise ValueError('skill write unconfirmed')
                fd = os.open(parts[-1], os.O_RDONLY | os.O_NOFOLLOW, dir_fd=parent)
                try:
                    os.fchmod(fd, 0o500 if file.get('executable', False) else 0o400)
                    os.fsync(fd)
                finally:
                    os.close(fd)
            finally:
                os.close(parent)
        os.fsync(skill)
    finally:
        os.close(skill)


def configure(env):
    if not isinstance(env, dict) or any(
        not isinstance(name, str) or not re.fullmatch('[A-Za-z_][A-Za-z0-9_]*', name)
        or not isinstance(value, str) or '\x00' in value
        for name, value in env.items()
    ):
        raise ValueError('invalid environment')
    # These Runtime-owned defaults are applied inside the sandbox, never to its
    # launcher. Public reserved-name validation belongs to Core.
    values = {
        'PATH': '/environment/packages/npm/bin:/environment/packages/python/bin:' + BASE_ENV['PATH'],
        **env,
        'PYTHONPATH': '/environment/packages/python' + (':' + env['PYTHONPATH'] if env.get('PYTHONPATH') else ''),
    }
    materialized = json.dumps(values, ensure_ascii=True)
    script = ''.join('export ' + name + '=' + shlex.quote(value) + '\n' for name, value in sorted(values.items()))
    environment = os.open(CONFIG, DIRECTORY)
    try:
        os.mkdir('capabilities', mode=0o700, dir_fd=environment)
        capabilities = open_directory(environment, 'capabilities')
        try:
            os.mkdir('skills', mode=0o700, dir_fd=capabilities)
        finally:
            os.close(capabilities)
    finally:
        os.close(environment)
    directory = os.open(CONFIG, DIRECTORY)
    try:
        fd = os.open('tool-env.sh', os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o400, dir_fd=directory)
        with os.fdopen(fd, 'w') as stream:
            stream.write(script)
            stream.flush()
            os.fsync(stream.fileno())
        fd = os.open('tool-env.json', os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o400, dir_fd=directory)
        with os.fdopen(fd, 'w') as stream:
            stream.write(materialized)
            stream.flush()
            os.fsync(stream.fileno())
        os.fsync(directory)
    finally:
        os.close(directory)


def sandbox(network, cwd):
    if network not in ('enabled', 'disabled') or not isinstance(cwd, str) or not cwd.startswith('/') or '\x00' in cwd:
        raise ValueError('invalid execution configuration')
    # One packaging contract for every Provider/harness. No native state, daemon
    # credential, staging payload or parent process is visible in this mount map.
    args = ['/usr/bin/bwrap', '--unshare-user', '--unshare-pid', '--unshare-ipc', '--unshare-uts',
            '--die-with-parent', '--new-session', '--cap-drop', 'ALL', '--clearenv',
            '--ro-bind', '/usr', '/usr', '--symlink', 'usr/bin', '/bin',
            '--symlink', 'usr/sbin', '/sbin', '--symlink', 'usr/lib', '/lib',
            '--symlink', 'usr/lib64', '/lib64', '--dir', '/etc',
            '--ro-bind', '/etc/resolv.conf', '/etc/resolv.conf',
            '--ro-bind', '/etc/hosts', '/etc/hosts', '--ro-bind', '/etc/ssl', '/etc/ssl',
            '--proc', '/proc', '--dev', '/dev', '--tmpfs', '/tmp', '--dir', '/home',
            '--dir', '/environment', '--bind', str(ROOT / 'workspace'), '/workspace',
            '--bind', str(PACKAGES), str(PACKAGES), '--ro-bind', str(CONFIG), str(CONFIG)]
    if network == 'disabled':
        args += ['--unshare-net']
    for key, value in BASE_ENV.items():
        args += ['--setenv', key, value]
    return args + ['--chdir', cwd, '--']


def run(request):
    action = request['action']
    if action == 'configure':
        configure(request['env'])
        return
    if action == 'skill':
        install_skill(request)
        return
    args = sandbox(request['network'], request.get('cwd', '/workspace'))
    if action == 'setup':
        command = request['command']
        if not isinstance(command, str) or not command or '\x00' in command:
            raise ValueError('invalid setup command')
        # Source confidential values only inside isolation. eval preserves the
        # shell's cwd, unlike replacing the native shell with a child wrapper.
        args += ['/bin/bash', '--noprofile', '--norc', '-c',
                 '. /environment/initialization/tool-env.sh && eval -- "$1"', '--', command]
    elif action in ('npm', 'python'):
        packages = request['packages']
        if not isinstance(packages, list) or not packages or any(
            not isinstance(package, str) or not package or '\x00' in package or package.startswith('-')
            for package in packages
        ):
            raise ValueError('invalid packages')
        command = ['npm', 'install', '--global', '--prefix', str(PACKAGES / 'npm'), '--', *packages]
        if action == 'python':
            command = ['/usr/bin/python3', '-m', 'pip', 'install', '--disable-pip-version-check',
                       '--no-input', '--target', str(PACKAGES / 'python'), '--', *packages]
        args += ['/bin/bash', '--noprofile', '--norc', '-c',
                 '. /environment/initialization/tool-env.sh && exec "$@"', '--', *command]
    else:
        raise ValueError('invalid action')
    # Setup/package output can contain arbitrary confidential values. The public
    # completion receipt deliberately contains no child stdout/stderr or command.
    subprocess.run(args, env=BASE_ENV, stdin=subprocess.DEVNULL,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)


def main():
    try:
        raw = sys.stdin.buffer.read(MAX_INPUT + 1)
        if len(raw) > MAX_INPUT:
            raise ValueError('input too large')
        request = json.loads(raw)
        if not isinstance(request, dict) or request.get('version') != 1:
            raise ValueError('invalid version')
        roots()
        run(request)
    except Exception:
        # Never serialize an exception that could contain input or process args.
        print('{"version":1,"outcome":"failed"}')
        return 1
    print('{"version":1,"outcome":"completed"}')
    return 0


if __name__ == '__main__':
    sys.exit(main())
