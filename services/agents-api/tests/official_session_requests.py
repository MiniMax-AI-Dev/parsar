"""Check Session-create field presence against the pinned SDK request types."""

import uuid

import httpx2
from openai import BadRequestError


def verify_session_create_requests(client, spec):
    # SessionCreateParamsBase permits null metadata, not null string values;
    # agent_id is str and stream is a boolean literal union without None.
    sessions = client.beta.agents.sessions
    before = {session.id for session in sessions.list()}
    invalid = [
        {"stream": None}, {"stream": "false"}, {"stream": 0},
        {"agent_id": None}, {"agent_id": 0},
        {"metadata": {"label": None}},
        {"metadata": {"empty": "", "label": None}},
        {"metadata": {"label": 0}}, {"metadata": []},
    ]
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        for fields in invalid:
            response = raw.post(str(client.base_url).rstrip("/") + "/agents/sessions",
                                headers=headers, json={**spec, **fields})
            assert response.status_code == 400, (fields, response.status_code)
            assert response.json()["error"]["code"] == "invalid_request"
            try:
                sessions.create(**spec, extra_body=fields)
            except BadRequestError as error:
                assert error.body["code"] == "invalid_request"
            else:
                raise AssertionError(f"Official client accepted invalid fields: {fields}")
        assert {session.id for session in sessions.list()} == before

        key = {"Idempotency-Key": str(uuid.uuid4())}
        first = sessions.create(**spec, extra_headers=key)
        assert first.metadata == {}
        for fields in [{}, {"stream": False}, {"metadata": None}, {"metadata": {}},
                       {"stream": False, "metadata": None}]:
            assert sessions.create(**spec, extra_body=fields, extra_headers=key) == first
            response = raw.post(str(client.base_url).rstrip("/") + "/agents/sessions",
                                headers={**headers, **key}, json={**spec, **fields})
            assert response.status_code == 200
            assert response.json()["id"] == first.id and response.json()["metadata"] == {}
        metadata = {"empty": "", "label": "中文🧪"}
        preserved = sessions.create(**spec, metadata=metadata)
        assert sessions.retrieve(preserved.id).metadata == metadata
    print("Session create requests: raw HTTP and pinned SDK null rejection, no writes on rejection, default equivalence, idempotency and exact metadata passed.")
    return [first, preserved]
