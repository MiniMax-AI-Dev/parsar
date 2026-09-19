"""Opt-in standalone acceptance using actual E2B, native harnesses and model APIs.

Pass one private operator JSON configuration path. Provider file/command calls in
this fixture observe effects or inject faults; public execution and Files always
use the deployed Core and daemon. No synthetic model server is provided.
"""
import base64
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import tempfile
import time
import traceback
import uuid

import httpx2
from e2b import Sandbox, SandboxQuery
from openai import OpenAI

from official_environment_files import verify_environment_files, verify_file_tenant_isolation
from official_session_artifacts import verify_session_artifacts

config = json.loads(Path(sys.argv[1]).read_text())
root = Path(config['proof_root'])
package = Path(config['package'])
root.mkdir(parents=True, exist_ok=True)
run = Path(tempfile.mkdtemp(prefix=config['engine'] + '-', dir=root))
run.chmod(0o700)
pin = json.loads((package / 'upstream.json').read_text())
distribution = importlib.metadata.distribution('openai')
assert distribution.version == pin['sdk_version']
assert json.loads(distribution.read_text('direct_url.json'))['vcs_info']['commit_id'] == pin['commit']
e2b_key = Path(config['e2b_key_file']).read_text().strip()
model_key = Path(config['model_key_file']).read_text().strip()
tokens = [secrets.token_hex(32), secrets.token_hex(32)]
provider = str(uuid.uuid4())
created = []
sources = []
cloud = {}
handles = []
process = None
record = {'engine': config['engine'], 'model': config['model'], 'template': config['template'],
          'started': time.time(), 'checks': [], 'provider': provider,
          'core_sha256': hashlib.sha256((package / 'bin/agents-api').read_bytes()).hexdigest(),
          'protocol': pin, 'real_e2b': True, 'real_model': True}
base = 'http://127.0.0.1:' + str(config['port'])
public = config['core_public_url'].rstrip('/')
http = httpx2.Client(trust_env=False, timeout=360)
client = OpenAI(base_url=base + '/v1', api_key=tokens[0], max_retries=0,
                _strict_response_validation=True, http_client=http)
foreign = OpenAI(base_url=base + '/v1', api_key=tokens[1], max_retries=0,
                 _strict_response_validation=True, http_client=http)
sessions, files = client.beta.agents.sessions, client.beta.agents.environments.files
headers = {'Authorization': 'Bearer ' + tokens[0], 'OpenAI-Beta': 'agents=v1'}


def private(name, value):
    path = run / name
    path.write_text(json.dumps(value))
    path.chmod(0o600)
    return str(path)


keys = [dict(token_sha256=hashlib.sha256(token.encode()).hexdigest(), tenant_id=str(uuid.uuid4()),
             organization_id='e2b-acceptance', project_id=provider + '-' + str(i),
             subject_kind='user', subject_id='caller-' + str(i)) for i, token in enumerate(tokens)]
env = {'HOME': str(Path.home()), 'PATH': '/usr/bin:/bin', 'PARSAR_HOME': str(run / 'state'),
       'AGENTS_API_DATABASE_URL': Path(config['database_file']).read_text().strip(),
       'AGENTS_API_KEYS_FILE': private('keys.json', keys), 'AGENTS_API_ADDR': '127.0.0.1:' + str(config['port']),
       'AGENTS_API_DAEMON_WS_URL': public.replace('https://', 'wss://') + '/api/v1/agent-daemon/ws',
       'AGENTS_API_ENGINE': config['engine'],
       'AGENTS_API_EXECUTION_OPTIONS_FILE': config['options_file'],
       'AGENTS_API_MANAGED_RUNTIMES_FILE': private('managed.json', {'core_url': public + '/api/v1',
        'default_provider': provider, 'e2b': {provider: {'api_key_file': config['e2b_key_file'],
        'template': config['template'], 'lease_seconds': 7200}}})}
if os.getenv('HTTPS_PROXY'):
    env['HTTPS_PROXY'] = os.environ['HTTPS_PROXY']


def until(fn, timeout=120):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = fn()
        if result:
            return result
        time.sleep(.3)
    raise AssertionError('Timed out: ' + fn.__name__)


def start():
    global process
    output = (run / ('core-' + str(len(handles)) + '.log')).open('w')
    handles.append(output)
    process = subprocess.Popen([str(package / 'bin/agents-api')], cwd=package, env=env,
                               stdout=output, stderr=subprocess.STDOUT)
    def ready():
        assert process.poll() is None, 'Core exited'
        try:
            return http.get(base + '/v1/agents/sessions', headers=headers).status_code == 200
        except httpx2.TransportError:
            return False
    until(ready, 30)


def stop(crash=False):
    global process
    if process and process.poll() is None:
        process.kill() if crash else process.terminate()
        process.wait(timeout=45)
    process = None


def owned():
    pages = Sandbox.list(query=SandboxQuery(metadata={'io.parsar.agents-api.installation': provider}), api_key=e2b_key)
    result = []
    while pages.has_next:
        result.extend(pages.next_items())
    return result


def runtime(eid):
    if eid not in cloud:
        matching = [a for a in owned() if a.metadata.get('io.parsar.agents-api.environment') == eid]
        assert len(matching) == 1, 'Missing or duplicate owned E2B VM'
        cloud[eid] = Sandbox.connect(matching[0].sandbox_id, timeout=7200, api_key=e2b_key)
    return cloud[eid]


def read(vm, path):
    return vm.files.read(path, user='runtime', format='bytes')


def exists(vm, path):
    return vm.files.exists(path, user='runtime')


def upload(eid, path, data):
    if isinstance(data, str):
        data = data.encode()
    result = files.create(eid, type='inline', path=path, data=base64.b64encode(data).decode())
    assert result.size_bytes == len(data)
    return len(data)


def message(text):
    return {'type': 'agent.session.input.message', 'input': [{'role': 'user', 'content': [{'type': 'input_text', 'text': text}]}]}


def waitturn(sid, n, status='completed'):
    def finished():
        turns = sessions.turns.list(sid, limit=100, order='asc').data
        if len(turns) < n:
            return False
        turn = turns[n - 1]
        if turn.status in ['completed', 'cancelled', 'failed']:
            assert turn.status == status, ('Unexpected terminal Turn', turn.to_dict())
            return turn
        return False
    return until(finished, 240)


def prompt(sid, text, n):
    with sessions.events.stream(sid, timeout=300) as stream:
        sessions.events.create(sid, events=[message(text)], idempotency_key='prompt-' + str(n))
        types = []
        for event in stream:
            types.append(event.type)
            if event.type == 'agent.session.failed' or (event.type == 'agent.session.idle' and 'agent.session.turn.completed' in types):
                break
        assert 'agent.session.turn.completed' in types, types
        assert types.index('agent.session.turn.created') < types.index('agent.session.turn.completed')
        assert types[-1] == 'agent.session.idle', types
    return waitturn(sid, n)


def connected(eid):
    return until(lambda: client.beta.agents.environments.retrieve(eid).status == 'connected')


def native_id(sid):
    sql = "SELECT native_session_id FROM session_devices WHERE session_id='" + sid + "'"
    return subprocess.check_output(config['psql_command'] + ['-At', '-c', sql], text=True).strip()


def restart_runtime(vm):
    vm.commands.run('pkill -KILL -u 1000 || true', user='root')
    script = '''import json,subprocess
from pathlib import Path
root=Path('/root/.parsar/e2b');r=json.loads((root/'ready.json').read_text())
e=json.loads(Path('/etc/parsar-runtime-env.json').read_text())
e.update(PATH='/usr/local/bin:/usr/bin:/bin',PARSAR_RUNTIME_ENVIRONMENT_ID=r['EnvironmentID'],PARSAR_RUNTIME_SESSION_ID=r['session_id'],PARSAR_RUNTIME_NETWORK_ACCESS='enabled')
f=open('/home/runtime/.parsar/parsar-daemon/default/restarted.log','ab')
subprocess.Popen(['/usr/local/bin/parsar-daemon','connect','--profile','default'],cwd='/environment/workspace',env=e,user=1000,group=1000,extra_groups=[],start_new_session=True,stdin=subprocess.DEVNULL,stdout=f,stderr=f,umask=0o077)
'''
    vm.files.write('/root/.parsar/e2b/restart-proof.py', script, user='root')
    vm.commands.run('/usr/bin/python3 /root/.parsar/e2b/restart-proof.py', user='root')


def check(name):
    record['checks'].append(name)
    print(name, flush=True)


try:
    with (run / 'migrate.log').open('w') as output:
        subprocess.run([str(package / 'bin/agents-api-migrate')], cwd=package, env=env,
                       stdout=output, stderr=subprocess.STDOUT, check=True)
    start()
    for authorization in [None, 'Bearer invalid-e2b-key']:
        h = {'OpenAI-Beta': 'agents=v1'}
        if authorization:
            h['Authorization'] = authorization
        assert http.get(base + '/v1/agents/sessions', headers=h).status_code == 401
    check('independent_deployment_and_authentication')
    agent = {'model': config['model'], 'instructions': 'Run the exact requested native shell commands. Never modify supplied scripts or repeat interrupted commands. Preserve conversation history.'}
    session = sessions.create(agent=agent, environment={'type': 'openai_hosted'}, extra_headers={'Idempotency-Key': 'idle'})
    created.append(session.id)
    eid = session.environment.id
    assert sessions.create(agent=agent, environment={'type': 'openai_hosted'}, extra_headers={'Idempotency-Key': 'idle'}).id == session.id
    connected(eid)
    vm = runtime(eid)
    expected_init = Path(__file__).parents[1] / 'deploy/e2b/init.py'
    deployed_init = vm.files.read('/opt/parsar-e2b/init.py', user='root', format='bytes')
    assert deployed_init == expected_init.read_bytes(), 'Template bootstrap differs from this checkout'
    record['bootstrap_sha256'] = hashlib.sha256(deployed_init).hexdigest()
    protection = vm.commands.run("""python3 - <<'CHECK'
import os,subprocess
for path in ['/usr/local', '/usr/local/bin', '/usr/local/bin/parsar-daemon',
             '/opt/parsar-e2b', '/opt/parsar-e2b/init.py', '/usr/bin/envd',
             '/etc/inittab', '/etc/init.d/rcS']:
    if path == '/etc/init.d/rcS' and not os.path.exists(path):
        continue
    stat = os.stat(path)
    assert stat.st_uid == 0 and stat.st_mode & 0o022 == 0, path
    assert not os.access(path, os.W_OK), path
result = subprocess.run(['su', 'user', '-c', 'id -u'], input='', text=True, capture_output=True, timeout=8)
assert result.returncode != 0
print('protected')
CHECK""", user='runtime')
    assert protection.stdout == 'protected\n'
    check('actual_runtime_code_ownership_and_privileged_account_denial')
    for resource in ['/agents/sessions/' + session.id, '/agents/environments/' + eid]:
        assert http.get(base + '/v1' + resource, headers={**headers, 'Authorization': 'Bearer ' + tokens[1]}).status_code == 404
    marker, memory = secrets.token_hex(24), secrets.token_hex(24)
    expected_files = {}
    for name, data in [('input.txt', marker.encode()), ('binary.bin', bytes(range(256))), ('empty', b'')]:
        expected_files['/workspace/' + name] = upload(eid, '/workspace/' + name, data)
    source = client.files.create(file=('source.bin', b'source-bytes\x00\xff'), purpose='user_data')
    sources.append(source.id)
    files.create(eid, type='file_id', file_id=source.id, path='/workspace/source.bin')
    expected_files['/workspace/source.bin'] = len(b'source-bytes\x00\xff')
    _, continuation = verify_environment_files(client, http, eid, '/workspace', expected_files)
    verify_file_tenant_isolation(client, foreign, http, eid, '/workspace', continuation, list(expected_files))
    check('public_session_binding_files_bytes_sort_pages_and_tenant_isolation')
    script = 'from pathlib import Path\np=Path("/workspace/outputs");p.mkdir(exist_ok=True)\n(p/"a.bin").write_bytes(Path("/workspace/binary.bin").read_bytes());(p/"empty").write_bytes(b"")\nprint(Path("/workspace/input.txt").read_text())\n'
    upload(eid, '/workspace/publish.py', script)
    first = prompt(session.id, 'Run exactly `python3 /workspace/publish.py`. Remember this conversation-only marker: ' + memory, 1)
    identity = native_id(session.id)
    assert identity
    expected_artifacts = {first.id: {'/workspace/outputs/a.bin': bytes(range(256)), '/workspace/outputs/empty': b''}}
    verify_session_artifacts(client, foreign, http, session.id, eid, expected_artifacts)
    committed = {item.id: item.to_dict() for item in sessions.items.list(session.id, limit=100).data}
    check('real_native_execution_and_immutable_artifacts_sdk_http')
    # The native isolation script is the same actual-tool probe used to qualify all profiles.
    history = config['native_history_root'] + '/e2b-isolation-canary'
    vm.files.write(history, 'synthetic-private-history', user='runtime')
    vm.files.write('/environment/staging/canary', 'synthetic-private-staging', user='runtime')
    auth = json.loads(vm.files.read('/home/runtime/.parsar/parsar-daemon/default/auth.json', user='runtime'))
    fixture = {'outer_pid_namespace': vm.commands.run('readlink /proc/self/ns/pid', user='root').stdout.strip(),
               'history_path': history,
               'secret_hashes': [hashlib.sha256(value.encode()).hexdigest() for value in [model_key, auth['runner_credential']]]}
    upload(eid, '/workspace/isolation-fixture.json', json.dumps(fixture))
    isolation = Path(__file__).with_name('e2b_native_isolation.py').read_text()
    upload(eid, '/workspace/isolation.py', isolation)
    second = prompt(session.id, 'Run exactly `python3 /workspace/isolation.py`. Do not modify it.', 2)
    assert json.loads(read(vm, '/workspace/isolation-result.json'))['passed']
    expected_artifacts[second.id] = expected_artifacts[first.id]
    verify_session_artifacts(client, foreign, http, session.id, eid, expected_artifacts)
    check('real_native_credential_history_process_and_envd_isolation')
    long_script = 'from pathlib import Path\nimport os,time\np=Path("/workspace");f=(p/"starts").open("a");f.write("started\\n");f.flush();os.fsync(f.fileno());f.close()\nwhile True:\n (p/"heartbeat").write_text(str(time.time_ns()))\n time.sleep(.2)\n'
    upload(eid, '/workspace/long.py', long_script)
    sessions.events.create(session.id, events=[message('Run exactly `python3 /workspace/long.py` and wait. Do not background it.')], idempotency_key='cancel-work')
    until(lambda: exists(vm, '/workspace/heartbeat'))
    for _ in range(2):
        sessions.events.create(session.id, events=[{'type': 'agent.session.input.cancel'}], idempotency_key='cancel')
    waitturn(session.id, 3, 'cancelled')
    heartbeat = read(vm, '/workspace/heartbeat')
    time.sleep(2)
    assert read(vm, '/workspace/heartbeat') == heartbeat
    verify_session_artifacts(client, foreign, http, session.id, eid, expected_artifacts)
    check('public_cancel_retry_stops_effects_without_publishing_cancelled_outputs')
    for number, fault in [(4, 'core'), (5, 'runtime')]:
        request = [message('Run exactly `python3 /workspace/long.py` once and wait. Do not restart it.')]
        before = read(vm, '/workspace/starts')
        sessions.events.create(session.id, events=request, idempotency_key='crash-' + fault)
        until(lambda: read(vm, '/workspace/starts') != before)
        count = read(vm, '/workspace/starts')
        if fault == 'core':
            stop(crash=True)
            start()
        else:
            restart_runtime(vm)
        waitturn(session.id, number, 'failed')
        connected(eid)
        def stable():
            value = read(vm, '/workspace/heartbeat')
            time.sleep(.6)
            return value if read(vm, '/workspace/heartbeat') == value else False
        stopped = until(stable, 45)
        sessions.events.create(session.id, events=request, idempotency_key='crash-' + fault)
        time.sleep(1)
        assert read(vm, '/workspace/starts') == count and read(vm, '/workspace/heartbeat') == stopped
        assert len(sessions.turns.list(session.id).data) == number
        assert native_id(session.id) == identity
        current = {item.id: item.to_dict() for item in sessions.items.list(session.id, limit=100).data}
        assert all(current[key] == value for key, value in committed.items())
        verify_session_artifacts(client, foreign, http, session.id, eid, expected_artifacts)
        check(fault + '_crash_queries_history_artifacts_and_no_automatic_or_retry_replay')
    prompt(session.id, 'Reply with the conversation-only marker I asked you to remember. Do not run any tools or previous commands.', 6)
    answers = [item for item in sessions.items.list(session.id, order='asc', limit=100).data if item.type == 'message' and item.role == 'assistant']
    assert memory in ''.join(part.text for part in answers[-1].content if part.type == 'output_text')
    assert native_id(session.id) == identity
    check('same_native_history_continues_after_core_and_runtime_recovery')
    disabled = sessions.create(agent=agent, environment={'type': 'openai_hosted', 'network': {'access': 'disabled'}})
    created.append(disabled.id)
    connected(disabled.environment.id)
    restricted = runtime(disabled.environment.id)
    network_script = 'import urllib.request,urllib.error,json\ntry:\n urllib.request.urlopen("https://api.moonshot.cn/v1/models",timeout=8)\nexcept urllib.error.HTTPError as e:\n assert e.code==403,e.code\nexcept (urllib.error.URLError,PermissionError,TimeoutError):pass\nelse:raise AssertionError("native network was allowed")\nopen("/workspace/network-result.json","w").write(json.dumps({"blocked":True}))\n'
    upload(disabled.environment.id, '/workspace/network.py', network_script)
    prompt(disabled.id, 'Run exactly `python3 /workspace/network.py`. Do not modify it.', 1)
    assert json.loads(read(restricted, '/workspace/network-result.json')) == {'blocked': True}
    check('real_model_execution_with_disabled_native_tool_network')
    record['passed'] = True
except BaseException:
    record['passed'] = False
    record['failure'] = traceback.format_exc()
finally:
    cleanup_errors = []
    if process is not None and process.poll() is None:
        for source_id in sources:
            try:
                client.files.delete(source_id)
            except Exception:
                cleanup_errors.append('source_delete_failed')
        for sid in created:
            try:
                sessions.delete(sid)
            except Exception:
                cleanup_errors.append('public_delete_failed')
        try:
            until(lambda: not owned(), 90)
        except Exception:
            cleanup_errors.append('owned_cleanup_not_confirmed')
    stop()
    for handle in handles:
        handle.close()
    # A failed deployment cannot leave billable instances behind. Record fallback
    # reclamation separately so it cannot masquerade as passing Core cleanup.
    for allocation in owned():
        Sandbox.kill(allocation.sandbox_id, api_key=e2b_key)
        cleanup_errors.append('direct_cleanup_required')
    record.update(cleanup_errors=cleanup_errors, elapsed=time.time() - record['started'])
    for path in run.iterdir():
        if path.is_file():
            text = path.read_text()
            for secret in [e2b_key, model_key, *tokens]:
                text = text.replace(secret, '[REDACTED]')
            path.write_text(text)
    (run / 'result.json').write_text(json.dumps(record, indent=2))
    (root / (config['engine'] + '-latest.json')).write_text(json.dumps({'run': str(run), 'passed': record['passed'], 'cleanup_errors': cleanup_errors}))
    print(json.dumps(record, indent=2), flush=True)
    sys.exit(0 if record['passed'] and not cleanup_errors else 1)
