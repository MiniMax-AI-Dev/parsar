"Verify pinned Session metadata replacement through the actual HTTP service."

import json
import uuid

import httpx2
from openai import AuthenticationError, BadRequestError, ConflictError, NotFoundError


def without_metadata(session):
    return {key: value for key, value in session.to_dict().items() if key != "metadata"}


def verify_session_metadata(client, other, invalid, spec, expect_error):
    sessions = client.beta.agents.sessions
    original = {"old": "remove", "keep": "replace"}
    retry = {"Idempotency-Key": str(uuid.uuid4())}
    first = sessions.create(**spec, metadata=original, extra_headers=retry)
    fixed = without_metadata(first)
    headers = {"Authorization": f"Bearer {client.api_key}", "OpenAI-Beta": "agents=v1"}
    url = str(client.base_url).rstrip("/") + "/agents/sessions/" + first.id

    def assert_metadata(session, expected):
        assert session.metadata == expected
        assert without_metadata(session) == fixed
        assert sessions.retrieve(first.id) == session
        assert next(item for item in sessions.list(limit=1) if item.id == first.id) == session
        return session

    assert_metadata(sessions.update(first.id), original)
    with httpx2.Client(trust_env=False, timeout=10) as raw:
        for metadata in [{"keep": "new", "empty": ""}, None, {},
                         {"🧪" * 64: "界" * 512, **{str(i): "🧪" * 512 for i in range(15)}}]:
            expected = metadata or {}
            assert_metadata(sessions.update(first.id, metadata=metadata), expected)
            # Reset between HTTP and SDK updates so both must actually replace values.
            sessions.update(first.id, metadata=original)
            response = raw.post(url, headers=headers, json={"metadata": metadata})
            assert response.status_code == 200 and response.headers["cache-control"] == "no-store"
            assert response.json()["metadata"] == expected
            current = assert_metadata(sessions.retrieve(first.id), expected)
            assert response.json() == current.to_dict()
            assert sessions.update(first.id) == current
            assert raw.post(url, headers=headers, json={}).json() == current.to_dict()
            # Updating metadata must not rewrite or reapply the original creation request.
            assert sessions.create(**spec, metadata=original, extra_headers=retry) == current
            expect_error(ConflictError, lambda: sessions.create(**spec, metadata=metadata, extra_headers=retry))

        invalid_fields = [
            {"metadata": []}, {"metadata": "value"}, {"metadata": False},
            {"metadata": {"key": None}}, {"metadata": {"key": 1}},
            {"metadata": {"key": []}}, {"metadata": {"key": {}}},
            {"metadata": {str(i): "value" for i in range(17)}},
            {"metadata": {"界" * 65: "value"}}, {"metadata": {"key": "🧪" * 513}},
            {"agent": spec["agent"]}, {"environment": spec["environment"]},
            {"tenant_id": str(uuid.uuid4())}, {"Metadata": {}},
        ]
        for fields in invalid_fields:
            response = raw.post(url, headers=headers, json=fields)
            assert response.status_code == 400 and response.json()["error"]["code"] == "invalid_request"
            expect_error(BadRequestError, lambda: sessions.update(first.id, extra_body=fields))
            assert sessions.retrieve(first.id) == current
        for body in ["", "null", "[]", "1", "{}{}", '{"metadata":']:
            response = raw.post(url, headers=headers, content=body)
            assert response.status_code == 400 and response.json()["error"]["code"] == "invalid_request"
        oversized = json.dumps({"metadata": {"key": "x" * (1024 * 1024)}})
        response = raw.post(url, headers=headers, content=oversized)
        assert response.status_code == 413 and response.json()["error"]["code"] == "request_too_large"
        assert raw.patch(url, headers=headers, json={"metadata": {}}).status_code == 405
        for path in [url + "?tenant_id=other", url + "?unsupported=1"]:
            assert raw.post(path, headers=headers, json={}).status_code == 400
        for target in [other, invalid]:
            expected_error = AuthenticationError if target is invalid else NotFoundError
            for fields in [{}, {"metadata": None}, {"metadata": {"tenant_id": "untrusted"}}]:
                expect_error(expected_error, lambda: target.beta.agents.sessions.update(first.id, **fields))
        expect_error(NotFoundError, lambda: sessions.update(str(uuid.uuid4()), metadata={}))
        expect_error(NotFoundError, lambda: sessions.update(str(uuid.uuid4())))
        expect_error(BadRequestError, lambda: sessions.update("invalid-id", metadata={}))
        expect_error(BadRequestError, lambda: sessions.update(first.id, extra_headers={"OpenAI-Beta": ""}))
        assert sessions.retrieve(first.id) == current
    print("Session metadata: pinned SDK and raw HTTP replacement, clearing, omission, limits, authentication, tenant isolation, unchanged configuration and creation retry identity passed.")
    return current


def verify_active_session_metadata(client, session_id):
    sessions = client.beta.agents.sessions
    before = sessions.retrieve(session_id)
    assert before.status == "in_progress"
    updated = sessions.update(session_id, metadata={"label": "active execution"})
    assert updated.metadata == {"label": "active execution"}
    assert without_metadata(updated) == without_metadata(before)
    return updated
