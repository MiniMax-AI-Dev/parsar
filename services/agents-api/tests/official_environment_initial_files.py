"""Real-deployment checks for confidential initial files and frozen metadata."""
import base64
import json
import secrets


def initial_files(client, foreign, http, agent, template_id=None):
    inline = secrets.token_bytes(40)
    source_body = bytes(range(256))
    source = client.files.create(file=('initial.bin', source_body), purpose='user_data')
    files = [{'type': 'inline', 'path': '/workspace/initial-inline.bin',
              'data': base64.b64encode(inline).decode()},
             {'type': 'file_id', 'path': '/workspace/initial-source.bin', 'file_id': source.id}]
    endpoint = str(client.base_url).rstrip('/')
    headers = {'Authorization': 'Bearer ' + client.api_key, 'OpenAI-Beta': 'agents=v1'}
    foreign_headers = {**headers, 'Authorization': 'Bearer ' + foreign.api_key}
    attempted = http.post(endpoint + '/agents/sessions', headers=foreign_headers,
                          json={'agent': agent, 'environment': {'type': 'openai_hosted', 'files': [files[1]]}})
    assert attempted.status_code == 404 and source.id not in attempted.text
    if template_id:
        response = client.beta.agents.environments.templates.with_raw_response.update(template_id, files=files)
        body = response.http_response.json()
        assert body['files'] == [{'type': 'inline', 'path': files[0]['path'], 'size_bytes': len(inline)},
                                 {'type': 'file_id', 'path': files[1]['path'], 'file_id': source.id}]
        assert files[0]['data'] not in json.dumps(body)
        listing = client.beta.agents.environments.templates.list().to_dict()
        assert files[0]['data'] not in json.dumps(listing)
        environment = {'type': 'openai_hosted', 'environment_template_id': template_id}
    else:
        environment = {'type': 'openai_hosted', 'network': {'access': 'disabled'}, 'files': files}
    return environment, source.id, {files[0]['path']: inline, files[1]['path']: source_body}


def verify_initial_snapshot(client, http, session, expected, source_id):
    metadata = [value.to_dict() for value in session.environment.files]
    assert len(metadata) == 2 and len({value['id'] for value in metadata}) == 2
    assert {value['path']: value['size_bytes'] for value in metadata} == {p: len(b) for p, b in expected.items()}
    assert metadata[0]['type'] == 'inline' and 'data' not in metadata[0] and 'file_id' not in metadata[0]
    assert metadata[1]['type'] == 'file_id' and metadata[1]['file_id'] == source_id
    endpoint = str(client.base_url).rstrip('/') + '/agents/environments/' + session.environment.id
    response = http.get(endpoint, headers={'Authorization': 'Bearer ' + client.api_key, 'OpenAI-Beta': 'agents=v1'})
    assert response.status_code == 200 and response.json()['files'] == metadata
    client.files.delete(source_id)


def assert_initial_bytes_script(expected):
    return ''.join('assert Path(' + repr(path) + ').read_bytes() == bytes.fromhex(' + repr(body.hex()) + ')\n'
                   for path, body in expected.items())
