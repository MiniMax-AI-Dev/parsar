"""Pinned SDK and raw HTTP acceptance for the basic Environment Template profile.

Run against the same independently deployed service used for actual native/model
acceptance. This module creates no fake Provider, Runtime or model endpoint.
"""
import uuid


def verify_environment_templates(client, foreign, http):
    api = client.beta.agents.environments.templates
    other = foreign.beta.agents.environments.templates
    base = str(client.base_url).rstrip('/') + '/agents/environments/templates'
    headers = {'Authorization': 'Bearer ' + client.api_key, 'OpenAI-Beta': 'agents=v1'}
    foreign_headers = {**headers, 'Authorization': 'Bearer ' + foreign.api_key}
    owned = []
    retained = []
    try:
        assert http.get(base, headers={'OpenAI-Beta': 'agents=v1'}).status_code == 401
        assert http.get(base, headers={'Authorization': 'Bearer ' + client.api_key}).status_code == 400
        assert api.list().data == []
        for values in ({}, {'packages': {}}, {'packages': {'npm': None}}, {'name': None, 'network': None, 'env': None, 'setup_commands': None},
                       {'name': ' preserved ', 'network': {'access': 'disabled'}, 'files': [],
                        'plugins': [], 'skills': [], 'packages': {'python': [], 'npm': None}}):
            response = api.with_raw_response.create(**values)
            body, template = response.http_response.json(), response.parse()
            owned.append(template.id)
            assert set(body) == {'id', 'object', 'created_at', 'updated_at', 'name', 'network',
                                 'packages', 'capability_directories', 'files', 'plugins', 'skills'}
            assert body['object'] == 'agent.environment.template'
            assert body['name'] == values.get('name')
            assert body['network'] == {'access': (values.get('network') or {}).get('access', 'enabled'),
                                       'allowed_domains': []}
            assert body['packages'] == {'python': [], 'npm': [], 'system': []}
            for field in ['capability_directories', 'files', 'plugins', 'skills']:
                assert body[field] == []
            assert body['created_at'] == body['updated_at']
            assert api.retrieve(template.id) == template
            assert http.get(base + '/' + template.id, headers=headers).json() == body
        before = api.retrieve(owned[-1])
        updated = api.update(owned[-1], name='changed')
        assert updated.name == 'changed' and updated.network == before.network
        assert updated.created_at == before.created_at
        updated = api.update(owned[-1], name=None, network=None)
        assert updated.name is None and updated.network.access == 'enabled'
        assert api.update(owned[-1]).to_dict() == updated.to_dict()
        assert [v.id for v in api.list(order='asc', limit=1)] == owned
        assert [v.id for v in api.list(order='desc', limit=2)] == owned[::-1]
        page = api.list(order='asc', limit=2)
        assert page.has_more and page.first_id == owned[0] and page.last_id == owned[1]
        assert [v.id for v in api.list(order='asc', after=page.last_id).data] == owned[2:]
        foreign_template = other.create()
        try:
            for method, path, body in [('GET', '/' + owned[0], None),
                                       ('POST', '/' + owned[0], {'name': 'forbidden'}),
                                       ('DELETE', '/' + owned[0], None),
                                       ('GET', '?after=' + owned[0], None)]:
                assert http.request(method, base + path, headers=foreign_headers, json=body).status_code == 404
            assert [v.id for v in other.list()] == [foreign_template.id]
        finally:
            other.delete(foreign_template.id)
        for query in ['limit=0', 'limit=101', 'limit=bad', 'order=wrong', 'limit=1&limit=2', 'unknown=1']:
            assert http.get(base + '?' + query, headers=headers).status_code == 400
        canary = 'template-private-' + uuid.uuid4().hex
        for body in [{'env': {'PATH': canary}}, {'setup_commands': [{'command': canary, 'cwd': 'relative'}]},
                     {'files': [{'type': 'inline', 'path': '/workspace/a', 'data': canary}]},
                     {'packages': {'system': ['-' + canary]}}, {'skills': [{'type': 'inline', 'data': canary}]},
                     {'plugins': [{'type': 'inline', 'data': canary}]},
                     {'capability_directories': ['/workspace']},
                     {'network': {'access': 'restricted', 'allowed_domains': ['example.com']}},
                     {'name': ''}, {'unknown': canary}]:
            for path in ['', '/' + owned[0]]:
                response = http.post(base + path, headers=headers, json=body)
                assert response.status_code == 400 and canary not in response.text
        assert [v.id for v in api.list(order='asc')] == owned
        assert canary not in http.get(base, headers=headers).text
        deleted = api.delete(owned.pop())
        assert deleted.deleted and deleted.object == 'agent.environment.template.deleted'
        for method in ['GET', 'DELETE']:
            assert http.request(method, base + '/' + deleted.id, headers=headers).status_code == 404
        enabled = api.create(name='real execution', network={'access': 'enabled'})
        retained.append(enabled.id)
        disabled = api.create(name='real network isolation', network={'access': 'disabled'})
        retained.append(disabled.id)
        return enabled.id, disabled.id
    except BaseException:
        for template_id in retained:
            api.delete(template_id)
        raise
    finally:
        for template_id in owned:
            api.delete(template_id)


def verify_template_session_rejections(client, foreign, http, agent, enabled, disabled):
    base = str(client.base_url).rstrip('/') + '/agents/sessions'
    headers = {'Authorization': 'Bearer ' + client.api_key, 'OpenAI-Beta': 'agents=v1'}
    for reference, override, status, token in [
        (disabled, {'network': {'access': 'enabled'}}, 400, client.api_key),
        (disabled, {'network': None}, 400, client.api_key),
        (str(uuid.uuid4()), {}, 404, client.api_key),
        (enabled, {}, 404, foreign.api_key),
    ]:
        response = http.post(base, headers={**headers, 'Authorization': 'Bearer ' + token},
                             json={'agent': agent, 'environment': {'type': 'openai_hosted',
                             'environment_template_id': reference, **override}})
        assert response.status_code == status, response.text
