"""Shared real-deployment assertions for confidential environment initialization."""
import json
import secrets

from openai import NotFoundError


def setup_configuration(proxy=None, *, system_packages=False):
    marker = 'setup-private-' + secrets.token_hex(16) + "' $()"
    env = {'SETUP_VALUE': marker}
    if proxy:
        env.update(HTTPS_PROXY=proxy, HTTP_PROXY=proxy, https_proxy=proxy, http_proxy=proxy)
    first = """test -f /workspace/initial-inline.bin && mkdir -p /workspace/setup-sub && python3 - <<'SCRIPT'
import os
from pathlib import Path
import packaging
assert packaging.__version__ == '26.0'
assert os.environ['SETUP_VALUE']
Path('/workspace/setup-sub/order').write_text('first')
SCRIPT
semver 1.2.3 > /workspace/setup-version
"""
    second = "test \"$(cat order)\" = first && printf second > order && printf initialized > /workspace/setup-once"
    packages = {'npm': ['semver@7.7.2'], 'python': ['packaging==26.0']}
    if system_packages:
        packages['system'] = ['jq', 'build-essential', 'libpq-dev']
        first = """printf '{"value":42}' | jq -e '.value == 42' &&
printf '#include <libpq-fe.h>\\nint main(void){return PQlibVersion() > 0 ? 0 : 1;}\\n' > /workspace/system-library.c &&
cc -I/usr/include/postgresql /workspace/system-library.c -lpq -o /workspace/system-library &&
/workspace/system-library &&
""" + first
    return {
        'env': env, 'packages': packages,
        'setup_commands': [{'command': first}, {'command': second, 'cwd': '/workspace/setup-sub'}],
    }, marker


def attach_setup(client, foreign, http, environment, configuration, template_id=None, network="disabled"):
    endpoint = str(client.base_url).rstrip('/')
    if template_id:
        raw = client.beta.agents.environments.templates.with_raw_response.update(
            template_id, network={'access': network}, **configuration)
        resource = raw.http_response.json()
        assert resource['packages'] == {'system': [], **configuration['packages']}
        headers = {'Authorization': 'Bearer ' + foreign.api_key, 'OpenAI-Beta': 'agents=v1'}
        rejected = http.get(endpoint + '/agents/environments/templates/' + template_id, headers=headers)
        assert rejected.status_code == 404
        metadata = [resource, client.beta.agents.environments.templates.list().to_dict()]
        assert configuration['env']['SETUP_VALUE'] not in json.dumps(metadata)
        assert all('env' not in value and 'setup_commands' not in value for value in [resource])
        return environment
    return {**environment, 'network': {'access': network}, **configuration}


def verify_setup_metadata(client, session, configuration):
    resource = client.beta.agents.environments.retrieve(session.environment.id).to_dict()
    assert session.environment.to_dict()['packages'] == {'system': [], **configuration['packages']}
    for value in [session.to_dict(), resource]:
        assert configuration['env']['SETUP_VALUE'] not in json.dumps(value)
        environment = value.get('environment', value)
        assert 'env' not in environment and 'setup_commands' not in environment


def native_setup_script(marker, network_target=None, *, system_packages=False):
    script = f'''from pathlib import Path
import os, subprocess, socket
import packaging
assert packaging.__version__ == '26.0'
assert os.environ['SETUP_VALUE'] == {marker!r}
assert Path('/workspace/setup-sub/order').read_text() == 'second'
assert Path('/workspace/setup-once').read_text() == 'initialized'
for cwd in ['/workspace', '/workspace/setup-sub']:
    assert subprocess.check_output(['semver', '1.2.3'], cwd=cwd).strip() == b'1.2.3'
    subprocess.run(['python3', '-c', 'import packaging; assert packaging.__version__ == "26.0"'], cwd=cwd, check=True)
'''
    if system_packages:
        script += '''assert Path('/workspace').samefile('/environment/workspace')
for cwd in ['/environment/workspace', '/environment/workspace/setup-sub']:
    assert subprocess.check_output(['jq', '-r', '.value'], input=b'{"value":42}', cwd=cwd).strip() == b'42'
assert subprocess.check_output(['jq', '-r', '.value'], input=b'{"value":42}').strip() == b'42'
subprocess.run(['/workspace/system-library'], check=True)
for path in ['/usr/bin/system-package-write', '/environment/packages/system/usr/bin/system-package-write']:
    try:
        Path(path).write_text('changed')
    except OSError:
        pass
    else:
        raise AssertionError('installed system root is writable')
'''
    if network_target:
        script += f'''try:
    connection = socket.create_connection({network_target!r}, timeout=2)
except OSError:
    pass
else:
    connection.close()
    raise AssertionError('runtime network policy was not applied after setup')
'''
    return script


def verify_system_package_configuration(client, http):
    templates = client.beta.agents.environments.templates
    template = templates.create(packages={'system': ['jq'], 'npm': ['semver@7.7.2']})
    endpoint = str(client.base_url).rstrip('/') + '/agents/environments/templates/' + template.id
    headers = {'Authorization': 'Bearer ' + client.api_key, 'OpenAI-Beta': 'agents=v1'}
    try:
        expected = {'system': ['jq'], 'npm': ['semver@7.7.2'], 'python': []}
        assert http.get(endpoint, headers=headers).json()['packages'] == expected
        assert templates.update(template.id, name='System tools').to_dict()['packages'] == expected
        assert templates.update(template.id, packages={'system': ['libpq-dev']}).to_dict()['packages'] == {
            'system': ['libpq-dev'], 'npm': [], 'python': []}
        empty = {'system': [], 'npm': [], 'python': []}
        for packages in [{'system': None}, {'system': []}, None]:
            assert templates.update(template.id, packages=packages).to_dict()['packages'] == empty
        for packages in [{'system': [None]}, {'system': ['']}, {'system': ['-unsafe-option']}]:
            response = http.post(endpoint, headers=headers, json={'packages': packages})
            assert response.status_code == 400
            assert '-unsafe-option' not in response.text
            assert templates.retrieve(template.id).to_dict()['packages'] == empty
    finally:
        templates.delete(template.id)


def verify_setup_failure(client, http, agent, until):
    """Actual Provider initialization must fail before any native Turn starts."""
    sessions = client.beta.agents.sessions
    for command in [{'command': 'echo confidential-setup-failure >&2; exit 7'},
                    {'command': 'touch /workspace/unexpected', 'cwd': '/missing-setup-cwd'}]:
        session = sessions.create(agent=agent, environment={
            'type': 'openai_hosted', 'setup_commands': [command,
                {'command': 'touch /workspace/unexpected-later-step'}]})
        try:
            until(lambda: client.beta.agents.environments.retrieve(session.environment.id).status == 'failed', 180)
            assert sessions.turns.list(session.id).data == []
            resource = sessions.retrieve(session.id).to_dict()
            assert 'confidential-setup-failure' not in json.dumps(resource)
            assert 'setup_commands' not in resource['environment']
        finally:
            sessions.delete(session.id)


def verify_saved_agent_setup_identity(client, http):
    sessions = client.beta.agents.sessions
    agent = client.beta.agents.create(model='kimi-k3')
    created = []
    secret = 'intent-canary-' + secrets.token_hex(12)
    variants = [
        {'type': 'openai_hosted'},
        {'type': 'openai_hosted', 'env': {'VALUE': secret}},
        {'type': 'openai_hosted', 'env': {'VALUE': secret + '-changed'}},
        {'type': 'openai_hosted', 'setup_commands': [{'command': 'true'}]},
        {'type': 'openai_hosted', 'setup_commands': [{'command': 'false'}]},
        {'type': 'openai_hosted', 'env': {'VALUE': secret}, 'setup_commands': [{'command': 'true'}]},
    ]
    try:
        for index in [0, 1, 3]:
            key = 'setup-intent-' + secrets.token_hex(12)
            original = sessions.create(agent_id=agent.id, environment=variants[index],
                                       extra_headers={'Idempotency-Key': key})
            created.append((original, key, index))
        client.beta.agents.delete(agent.id)
        endpoint = str(client.base_url).rstrip('/') + '/agents/sessions'
        for original, key, index in created:
            assert sessions.create(agent_id=agent.id, environment=variants[index],
                                   extra_headers={'Idempotency-Key': key}).id == original.id
            for changed, environment in enumerate(variants):
                if changed == index:
                    continue
                response = http.post(endpoint, json={'agent_id': agent.id, 'environment': environment},
                                     headers={'Authorization': 'Bearer ' + client.api_key,
                                              'OpenAI-Beta': 'agents=v1', 'Idempotency-Key': key})
                assert response.status_code == 409, (index, changed, response.status_code)
                assert secret not in response.text
    finally:
        for session, _, _ in created:
            sessions.delete(session.id)
        try:
            client.beta.agents.delete(agent.id)
        except NotFoundError:
            pass
