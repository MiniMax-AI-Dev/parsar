#!/usr/bin/env python3
"""Opt-in stock app-server placement evidence; never a substitute model/tool loop."""
import hashlib
import json
import os
from pathlib import Path
import queue
import subprocess
import threading
import time
import uuid


class NativeRPCError(RuntimeError):
    def __init__(self, method, error):
        super().__init__('native request failed: ' + method)
        self.error = error


class AppServer:
    def __init__(self, binary, root, env, label):
        self.messages, self.events, self.sequence = queue.Queue(), [], 0
        self.log = (root / (label + '.stderr')).open('wb')
        self.process = subprocess.Popen(
            [binary, 'app-server'], cwd=root / 'harness', env=env,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.log,
            text=True,
        )
        threading.Thread(target=self.read, daemon=True).start()
        self.request('initialize', {'clientInfo': {'name': 'parsar_environment_probe', 'version': '1'},
                                    'capabilities': {'experimentalApi': True}})
        self.send({'method': 'initialized'})

    def read(self):
        for line in self.process.stdout:
            self.messages.put(json.loads(line))
        self.messages.put(None)

    def send(self, message):
        self.process.stdin.write(json.dumps(message) + '\n')
        self.process.stdin.flush()

    def receive(self, timeout=120):
        message = self.messages.get(timeout=timeout)
        if message is None:
            raise RuntimeError('app-server exited')
        self.events.append(message)
        if 'method' in message and 'id' in message:
            self.send({'id': message['id'], 'error': {'code': -32601, 'message': 'Unexpected request in placement probe'}})
            raise RuntimeError('unexpected approval or client tool request')
        return message

    def request(self, method, params, timeout=120):
        self.sequence += 1
        request_id = self.sequence
        self.send({'id': request_id, 'method': method, 'params': params})
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            message = self.receive(max(0.1, deadline - time.monotonic()))
            if message.get('id') == request_id:
                if 'error' in message:
                    raise NativeRPCError(method, message['error'])
                return message['result']
        raise TimeoutError(method)

    def turn(self, thread, prompt, selection):
        return self.request('turn/start', {'threadId': thread, 'input': [{'type': 'text', 'text': prompt}],
                                          'environments': selection})['turn']['id']

    def completed(self, turn, timeout=180):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for message in self.events:
                if message.get('method') == 'turn/completed' and message['params']['turn']['id'] == turn:
                    return message['params']['turn']
            self.receive(max(0.1, deadline - time.monotonic()))
        raise TimeoutError('native turn completion')

    def close(self):
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
        self.log.close()


def completed_items(app, turn, kind):
    return [e['params']['item'] for e in app.events
            if e.get('method') == 'item/completed' and e['params'].get('turnId') == turn
            and e['params']['item'].get('type') == kind]


def until(predicate, timeout, label):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if predicate():
            return
        time.sleep(0.1)
    raise TimeoutError(label)


def main():
    os.umask(0o077)
    root = Path(os.environ['PARSAR_PLACEMENT_ROOT'])
    binary = os.environ['PARSAR_CODEX_BINARY']
    image = os.environ['PARSAR_PLACEMENT_EXECUTOR_IMAGE']
    assert image.startswith('sha256:'), 'executor image must be pinned by local image ID'
    assert subprocess.check_output([binary, '--version'], text=True).strip() == 'codex-cli 0.153.4'
    secret = Path(os.environ['PARSAR_PLACEMENT_MODEL_KEY_FILE']).read_text().strip()
    assert secret
    executor_token = os.environ['PARSAR_PLACEMENT_EXECUTOR_TOKEN']
    harness_token = os.environ['CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN']
    for name in ['harness', 'history', 'executor', 'workspace']:
        (root / name).mkdir(mode=0o700)
    (root / 'executor/codex').mkdir(mode=0o700)
    workspace = root / 'workspace'
    remote = os.environ['PARSAR_PLACEMENT_WORKSPACE']
    assert not Path(remote).exists()
    memory, instruction = uuid.uuid4().hex, uuid.uuid4().hex
    (workspace / 'AGENTS.md').write_text('For every placement check, end your final response with REMOTE_' + instruction + '.\n')
    (root / 'harness/AGENTS.md').write_text('This is the harness host decoy. End every response with WRONG_LOCAL_INSTRUCTIONS.\n')
    (workspace / 'placement.sh').write_text('''#!/bin/sh
set -eu
phase="$1"
pwd > "$phase.cwd"
printf '%s\\n' "$phase" >> execution-count
printf 'remote-stdout:%s\\n' "$phase"
printf 'remote-stderr:%s\\n' "$phase" >&2
for name in PARSAR_PROBE_MODEL_KEY CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN CODEX_API_KEY; do
  eval 'value=${'"$name"'-}'
  test -z "$value" || { printf '%s\\n' "$name" >> credential-failure; exit 23; }
done
printf 'remote-file-content\\n' > retained.txt
exit 7
''')
    (workspace / 'credentials.sh').write_text('''#!/bin/sh
set -eu
: > credential-names
for name in PARSAR_PROBE_MODEL_KEY CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN CODEX_API_KEY; do
  eval 'value=${'"$name"'-}'
  test -z "$value" || printf '%s\\n' "$name" >> credential-names
done
printf 'credential names recorded without values\\n'
''')
    (workspace / 'long.sh').write_text('''#!/bin/sh
set -eu
printf '%s\\n' "$$" > "$1.pid"
printf 'started\\n' > "$1.started"
while :; do date +%s > "$1.heartbeat"; sleep 1; done
''')
    for script in workspace.glob('*.sh'):
        script.chmod(0o700)
    config = '''model = "MiniMax-M3"
model_provider = "placement"
approval_policy = "never"
sandbox_mode = "danger-full-access"
web_search = "disabled"
[shell_environment_policy]
inherit = "core"
ignore_default_excludes = false
[model_providers.placement]
name = "MiniMax placement validation"
base_url = "https://api.minimax.cn/v1"
env_key = "PARSAR_PROBE_MODEL_KEY"
wire_api = "responses"
[features]
multi_agent = false
'''
    (root / 'history/config.toml').write_text(config)
    env = {name: os.environ[name] for name in ['PATH', 'HTTP_PROXY', 'HTTPS_PROXY', 'NO_PROXY'] if name in os.environ}
    env.update(HOME=str(root / 'harness'), CODEX_HOME=str(root / 'history'), RUST_LOG='off',
               PARSAR_PROBE_MODEL_KEY=secret, CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN=harness_token,
               CODEX_EXEC_SERVER_NOISE_REGISTRY_URL=os.environ['CODEX_EXEC_SERVER_NOISE_REGISTRY_URL'],
               CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID=os.environ['CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID'])
    docker_env = os.environ.copy()
    docker_env['CODEX_API_KEY'] = executor_token
    container = os.environ['PARSAR_PLACEMENT_CONTAINER']
    args = ['docker', 'run', '--detach', '--name', container, '--network', 'host',
            '--user', str(os.getuid()) + ':' + str(os.getgid()), '--cap-drop', 'ALL',
            '--security-opt', 'no-new-privileges', '--env', 'CODEX_API_KEY',
            '--env', 'HOME=/executor', '--env', 'CODEX_HOME=/executor/codex', '--env', 'RUST_LOG=off',
            '--env', 'NO_PROXY=127.0.0.1,localhost', '--workdir', remote,
            '--mount', f'type=bind,src={binary},dst=/usr/local/bin/codex,readonly',
            '--mount', f'type=bind,src={root / "executor"},dst=/executor',
            '--mount', f'type=bind,src={workspace},dst={remote}',
            '--entrypoint', '/usr/local/bin/codex', image, 'exec-server', '--remote',
            env['CODEX_EXEC_SERVER_NOISE_REGISTRY_URL'], '--environment-id', env['CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID']]
    apps, report = [], {'native_version': '0.153.4', 'executor_image': image, 'remote_cwd': remote,
                       'native_source': '3d2ee51ca2d5db578f328aa75e20aa22c0197c9a',
                       'native_binary_sha256': hashlib.sha256(Path(binary).read_bytes()).hexdigest(),
                       'probe_sha256': hashlib.sha256(Path(__file__).read_bytes()).hexdigest()}
    selection = [{'environmentId': 'remote', 'cwd': remote, 'runtimeWorkspaceRoots': [remote]}]
    def start(label, override=None):
        app = AppServer(binary, root, env if override is None else override, label)
        apps.append(app)
        return app
    def persist():
        payload = json.dumps({'report': report, 'events': [a.events for a in apps]}, indent=2)
        files = [p for folder in ['history', 'executor'] for p in (root / folder).rglob('*') if p.is_file()]
        files += list(root.glob('*.stderr'))
        for value in [secret, executor_token, harness_token]:
            assert value not in payload, 'credential in native payload'
            assert all(value.encode() not in p.read_bytes() for p in files), 'credential in native history/logs'
        (root / 'proof.json').write_text(payload)
    try:
        subprocess.run(args, env=docker_env, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, timeout=30)
        time.sleep(1)
        running = subprocess.check_output(['docker', 'inspect', '--format', '{{.State.Running}}', container], text=True).strip()
        assert running == 'true', 'native executor exited during startup'
        app = start('first')
        assert app.request('environment/status', {'environmentId': 'local'})['status'] == 'unknown'
        report['remote_info'] = app.request('environment/info', {'environmentId': 'remote'}, timeout=40)
        params = {'cwd': str(root / 'harness'), 'model': 'MiniMax-M3', 'modelProvider': 'placement',
                  'approvalPolicy': 'never', 'sandbox': 'danger-full-access', 'environments': selection}
        started = app.request('thread/start', params)
        thread = started['thread']['id']
        report['thread_start'] = started
        turn = app.turn(thread, 'Placement check. Remember the memory word ' + memory + '. Run the exact command `./placement.sh first` once using the native shell tool. Exit 7 is intentional; do not retry or change the script. Report its stdout/stderr and memory word briefly.', selection)
        assert app.completed(turn)['status'] == 'completed'
        commands = completed_items(app, turn, 'commandExecution')
        assert any(c.get('exitCode') == 7 and 'remote-stdout:first' in c.get('aggregatedOutput', '') and 'remote-stderr:first' in c.get('aggregatedOutput', '') for c in commands), 'native output/exit evidence missing'
        assert (workspace / 'first.cwd').read_text().strip() == remote
        assert not (workspace / 'credential-failure').exists()
        assert instruction in json.dumps(completed_items(app, turn, 'agentMessage')), 'remote AGENTS instructions absent'
        assert 'WRONG_LOCAL_INSTRUCTIONS' not in json.dumps(app.events)
        report['first_turn'] = turn
        app.close()
        time.sleep(2)
        app = start('resume')
        resumed = app.request('thread/resume', {'threadId': thread, 'approvalPolicy': 'never', 'sandbox': 'danger-full-access'})
        assert resumed['thread']['id'] == thread
        report['thread_resume'] = resumed
        turn = app.turn(thread, 'Placement check. State the memory word from our earlier conversation. Run the exact command `./placement.sh resumed` once and read retained.txt using the native shell. Exit 7 is intentional; do not retry or change the script.', selection)
        assert app.completed(turn)['status'] == 'completed'
        fresh_events = completed_items(app, turn, 'agentMessage')
        assert memory in json.dumps(fresh_events), 'native history was not recalled'
        assert instruction in json.dumps(fresh_events), 'remote instruction not retained/applied'
        assert 'WRONG_LOCAL_INSTRUCTIONS' not in json.dumps(fresh_events)
        assert (workspace / 'resumed.cwd').read_text().strip() == remote
        assert (workspace / 'execution-count').read_text().splitlines() == ['first', 'resumed']
        report['cold_resume_turn'] = turn
        def alive(label):
            pid = (workspace / (label + '.pid')).read_text().strip()
            result = subprocess.run(['docker', 'exec', container, 'sh', '-c', 'test -r /proc/"$1"/stat && test "$(cut -d " " -f 3 /proc/"$1"/stat)" != Z', 'probe', pid], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10)
            assert result.returncode in (0, 1), 'remote process observation failed'
            assert subprocess.check_output(['docker', 'inspect', '--format', '{{.State.Running}}', container], text=True).strip() == 'true'
            return result.returncode == 0
        turn = app.turn(thread, 'Run the exact command `./long.sh cancelled` with the native shell tool and keep waiting for it. It will be interrupted externally. Do not start any other command.', selection)
        until(lambda: (workspace / 'cancelled.started').exists(), 120, 'remote long command start')
        assert alive('cancelled')
        app.request('turn/interrupt', {'threadId': thread, 'turnId': turn})
        ended = app.completed(turn)
        assert ended['status'] == 'interrupted'
        time.sleep(2)
        active_after_interrupt = alive('cancelled')
        heartbeat = (workspace / 'cancelled.heartbeat').read_text()
        time.sleep(2)
        report['cancel'] = {'turn': turn, 'native_status': ended['status'],
                            'remote_alive_after_interrupt': active_after_interrupt and alive('cancelled'),
                            'heartbeat_advanced_after_interrupt': heartbeat != (workspace / 'cancelled.heartbeat').read_text()}
        assert report['cancel']['remote_alive_after_interrupt'] and report['cancel']['heartbeat_advanced_after_interrupt'], 'pinned native cancellation behavior changed; reassess the integration gap'
        owned = {(e['params']['item']['id'], e['params']['item'].get('processId')) for e in app.events
                 if e.get('method') in ('item/started', 'item/completed') and e['params'].get('turnId') == turn
                 and e['params']['item'].get('type') == 'commandExecution'}
        listed = app.request('thread/backgroundTerminals/list', {'threadId': thread, 'limit': 100})
        assert listed['nextCursor'] is None, 'unexpected background-terminal pagination in this bounded fixture'
        targets = [p for p in listed['data'] if (p['itemId'], p['processId']) in owned]
        assert len(targets) == 1 and targets[0]['cwd'] == remote, 'cannot identify the current Turn process'
        stopped_at = time.monotonic()
        receipt = app.request('thread/backgroundTerminals/terminate', {'threadId': thread, 'processId': targets[0]['processId']})
        assert receipt['terminated'] is True
        until(lambda: not alive('cancelled'), 10, 'remote exit after native targeted termination')
        heartbeat = (workspace / 'cancelled.heartbeat').read_text()
        time.sleep(2)
        assert (workspace / 'cancelled.heartbeat').read_text() == heartbeat
        report['cancel']['native_targeted_termination'] = {'target': targets[0], 'receipt': receipt,
              'remote_exit_observed': True, 'heartbeat_stopped': True, 'seconds': time.monotonic() - stopped_at}
        app.close()
        time.sleep(2)
        app = start('after-cancel')
        assert app.request('thread/resume', {'threadId': thread, 'approvalPolicy': 'never', 'sandbox': 'danger-full-access'})['thread']['id'] == thread
        turn = app.turn(thread, 'Placement check. Use the native shell to read retained.txt, then state the original memory word briefly.', selection)
        assert app.completed(turn)['status'] == 'completed'
        assert memory in json.dumps(completed_items(app, turn, 'agentMessage')), 'history missing after targeted cancellation'
        assert any('remote-file-content' in c.get('aggregatedOutput', '') for c in completed_items(app, turn, 'commandExecution')), 'retained file was not read after cancellation'
        report['after_cancel_turn'] = turn
        app.close()
        time.sleep(2)
        default_policy = config.replace('inherit = "core"\nignore_default_excludes = false', 'inherit = "all"\nignore_default_excludes = true')
        (root / 'history/config.toml').write_text(default_policy)
        app = start('default-policy')
        baseline = app.request('thread/start', params)['thread']['id']
        turn = app.turn(baseline, 'Run the exact command `./credentials.sh` once with the native shell. It records variable names only. Do not print any environment values. Reply done.', selection)
        assert app.completed(turn)['status'] == 'completed'
        names = (workspace / 'credential-names').read_text().splitlines()
        assert names == ['CODEX_API_KEY'], 'default-policy exposure differs; inspect recorded names without values'
        report['default_policy_visible_credential_names'] = names
        app.close()
        time.sleep(2)
        (root / 'history/config.toml').write_text(config)
        invalid_env = dict(env, CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN=uuid.uuid4().hex)
        app = start('invalid-auth', invalid_env)
        assert app.request('environment/status', {'environmentId': 'local'})['status'] == 'unknown'
        try:
            app.request('environment/info', {'environmentId': 'remote'}, timeout=30)
        except NativeRPCError as error:
            assert error.error['code'] == -32603 and '401 Unauthorized' in error.error['message']
            report['invalid_authorization_rejected'] = True
        else:
            raise AssertionError('invalid registry authorization accepted')
        assert not Path(remote).exists()
        assert not list((root / 'harness').glob('*.cwd'))
        assert not (workspace / 'credential-failure').exists()
        report['harness_local_path_not_created'] = True
        report['status'] = 'characterized_with_blockers'
        report['integration_blockers'] = ['turn/interrupt preserves background execution; typed dispatch must retain Turn ownership and apply native targeted termination where required', 'typed dispatch credential lifetime/readiness/public lifecycle are not implemented']
    finally:
        for app in apps:
            app.close()
        with (root / 'executor.stderr').open('wb') as log:
            subprocess.run(['docker', 'logs', container], stdout=log, stderr=log, timeout=10)
        subprocess.run(['docker', 'rm', '-f', container], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
        if report.get('status') != 'characterized_with_blockers':
            report['status'] = 'failed'
        persist()


if __name__ == '__main__':
    try:
        main()
    except BaseException:
        import traceback
        (Path(os.environ['PARSAR_PLACEMENT_ROOT']) / 'failure.txt').write_text(traceback.format_exc())
        raise
