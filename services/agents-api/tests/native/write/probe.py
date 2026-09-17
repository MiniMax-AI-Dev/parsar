#!/usr/bin/env python3
"""Private raw harness qualification; synthetic bytes, no public admission."""
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import time

root = Path(os.environ['PARSAR_NATIVE_ENV_PROOF'])
local = Path(os.environ['PARSAR_WRITE_LOCAL'])
workspace = local / 'workspace'
ipc = Path(os.environ['PARSAR_CODEX_HARNESS_IPC_ROOT'])
artifact = os.environ['PARSAR_CODEX_HARNESS_ARTIFACT']
observations = []


def rpc(process, identifier, method, params):
    process.stdin.write(json.dumps(dict(id=identifier, method=method, params=params)).encode() + b'\n')
    process.stdin.flush()
    while True:
        line = process.stdout.readline()
        if not line:
            raise RuntimeError('harness ended before RPC response')
        result = json.loads(line)
        if result.get('id') == identifier:
            if 'error' in result:
                raise RuntimeError('native RPC rejected ' + method)
            return result['result']


def request(path, body, *, size=None, detach=False):
    frame = dict(environment_id=os.environ['PARSAR_CODEX_HARNESS_ENVIRONMENT'],
                 path=path, operation='write', size_bytes=len(body) if size is None else size)
    with socket.socket(socket.AF_UNIX) as client:
        client.settimeout(65)
        client.connect(str(ipc / 'files.sock'))
        client.sendall(json.dumps(frame).encode() + b'\n' + body)
        if detach:
            return
        with client.makefile('rb') as reader:
            return json.loads(reader.readline(8192))


def wait_file(path, expected):
    deadline = time.monotonic() + 65
    while time.monotonic() < deadline:
        if path.exists() and path.read_bytes() == expected:
            return
        time.sleep(.05)
    raise AssertionError('detached write not independently observed')


def exercise():
    for size in (0, 256 * 1024 + 13, 50 * 1024 * 1024):
        data = (bytes(range(256)) * (size // 256 + 1))[:size]
        start = time.monotonic()
        result = request('binary', data)
        assert result == {'write': {'size_bytes': size, 'committed': True}}, result
        assert (workspace / 'binary').read_bytes() == data
        observations.append(dict(size=size, seconds=time.monotonic()-start,
                                 sha256=hashlib.sha256(data).hexdigest(), response=result))
    original = local / 'staging' / 'original'
    original.write_bytes(b'original')
    os.link(original, workspace / 'linked')
    assert request('linked', b'replacement') == {'write': {'size_bytes': 11, 'committed': True}}
    assert original.read_bytes() == b'original'
    assert (workspace / 'linked').read_bytes() == b'replacement'
    assert original.stat().st_ino != (workspace / 'linked').stat().st_ino
    for path in ('../outside', '/etc/escape', 'a//b'):
        assert request(path, b'') == {'error': 'invalid_path'}
    assert request('oversized', b'', size=50 * 1024 * 1024 + 1) == {'error': 'invalid_request'}
    request('incomplete', b'x', size=3, detach=True)
    # The next serial request establishes completion of pre-admission handling.
    assert request('after-incomplete', b'') == {'write': {'size_bytes': 0, 'committed': True}}
    assert not (workspace / 'incomplete').exists()
    data = bytes(range(256)) * 4096
    request('detached', data, detach=True)
    wait_file(workspace / 'detached', data)
    assert request('after-detach', b'') == {'write': {'size_bytes': 0, 'committed': True}}
    assert sorted(p.name for p in (local / 'staging').iterdir()) == ['original']


for read_only in (False, True):
    args = [artifact]
    if read_only:
        args.append('--workspace-read-only')
    args += ['app-server', '--stdio']
    with (root / ('read-only.stderr' if read_only else 'write.stderr')).open('wb') as log:
        process = subprocess.Popen(args, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=log)
        try:
            rpc(process, 1, 'initialize', {'clientInfo': {'name': 'write_probe', 'version': '1'}, 'capabilities': {'experimentalApi': True}})
            process.stdin.write(b'{"method":"initialized"}\n')
            process.stdin.flush()
            rpc(process, 2, 'environment/info', {'environmentId': 'remote'})
            if read_only:
                assert request('read-only-denied', b'') == {'error': 'unsupported'}
                assert not (workspace / 'read-only-denied').exists()
            else:
                exercise()
            process.stdin.close()
            assert process.wait(timeout=10) == 0
        finally:
            if process.poll() is None:
                process.kill()
            process.wait(timeout=10)
        assert not ipc.exists()

(root / 'write-native.json').write_text(json.dumps(dict(
    artifact_sha256=hashlib.sha256(Path(artifact).read_bytes()).hexdigest(),
    synthetic=True, observations=observations,
    hard_link_preserved=True, incomplete_not_dispatched=True,
    caller_detach_retained=True, read_only_denied=True,
    public_admission=False), indent=2) + '\n')
