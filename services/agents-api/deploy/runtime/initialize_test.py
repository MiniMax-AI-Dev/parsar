"""Real Linux initialization checks; run inside a disposable packaged Runtime.

The fixture must expose writable workspace/packages/initialization roots and
support the same nested isolation as its deployed Provider. No model is mocked;
these checks exercise initialization only, not public native-model acceptance.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import time

HELPER = '/usr/local/bin/agents-api-runtime-initialize'
CANARY = 'private-initialization-canary-47a8'


def invoke(action, *, succeeds=True, **fields):
    payload = json.dumps({'version': 1, 'action': action, 'network': 'enabled', **fields})
    result = subprocess.run(['/usr/bin/python3', '-I', '-S', HELPER], input=payload,
                            text=True, capture_output=True, timeout=120)
    expected = 'completed' if succeeds else 'failed'
    assert result.returncode == (0 if succeeds else 1), (action, result.returncode)
    assert result.stderr == '', (action, 'unexpected stderr')
    assert json.loads(result.stdout) == {'version': 1, 'outcome': expected}, action
    assert CANARY not in result.stdout + result.stderr, 'confidential output exposed'


def main():
    for name in ('workspace', 'packages', 'initialization', 'private', 'staging'):
        Path('/environment', name).mkdir(exist_ok=True)
    Path('/environment/private/credential').write_text(CANARY)
    Path('/environment/staging/request').write_text(CANARY)
    os.environ['DAEMON_PRIVATE_CANARY'] = CANARY
    invoke('configure', env={'INITIALIZATION_VALUE': CANARY, 'WITH_QUOTES': "'\n$(false)"})
    # Re-entry must not replace confidential configuration after any effects.
    invoke('configure', succeeds=False, env={'INITIALIZATION_VALUE': 'changed'})
    invoke('setup', command='printf "%s" "$INITIALIZATION_VALUE" > first; printf secret; printf secret >&2')
    assert Path('/environment/workspace/first').read_text() == CANARY
    check = '''import os, pathlib, socket
for path in ('/environment/private/credential', '/environment/staging/request', '/home/runtime/.parsar'):
    assert not pathlib.Path(path).exists(), path
assert 'DAEMON_PRIVATE_CANARY' not in os.environ
assert os.environ['INITIALIZATION_VALUE'] == 'private-initialization-canary-47a8'
assert os.environ['WITH_QUOTES'] == "'\\n$(false)"
for p in pathlib.Path('/proc').glob('[0-9]*/environ'):
    assert b'DAEMON_PRIVATE_CANARY=' not in p.read_bytes()
for path in ('/usr/bin/untrusted', '/environment/initialization/tool-env.sh'):
    try: pathlib.Path(path).write_text('bad')
    except OSError: pass
    else: raise AssertionError(path)
pathlib.Path('/environment/packages/visible').write_text('ok')
assert len(socket.if_nameindex()) == 1
'''
    Path('/environment/workspace/check.py').write_text(check)
    invoke('setup', network='disabled', command='/usr/bin/python3 /workspace/check.py')
    # Shell cwd is explicit and ordered effects survive between invocations.
    Path('/environment/workspace/sub').mkdir()
    invoke('setup', cwd='/workspace/sub', command='test -f ../first && pwd > second')
    assert Path('/environment/workspace/sub/second').read_text() == '/workspace/sub\n'
    invoke('setup', succeeds=False, command='echo secret; echo secret >&2; exit 7')
    invoke('setup', succeeds=False, cwd='/missing', command='touch /workspace/should-not-exist')
    assert not Path('/environment/workspace/should-not-exist').exists()
    invoke('setup', command='setsid /bin/bash -c "sleep 2; touch /workspace/descendant" >/dev/null 2>&1 &')
    time.sleep(3)
    assert not Path('/environment/workspace/descendant').exists(), 'detached setup descendant survived'
    if '--packages' in sys.argv:
        # Actual public registries, not synthetic package fixtures.
        invoke('npm', packages=['is-number@7.0.0'])
        invoke('python', packages=['packaging==26.0'])
        invoke('setup', cwd='/workspace/sub', command="node -e \"if (!require('/environment/packages/npm/lib/node_modules/is-number')(42)) process.exit(1)\" && python3 -c 'import packaging; assert packaging.__version__ == \"26.0\"'")
    print(json.dumps({'initialization': 'passed', 'real_packages': '--packages' in sys.argv}))


if __name__ == '__main__':
    main()
