"""Local Session creator retry policy with no-input, synthetic configurations.

Cross-subject conflicts are local policy, not verified hosted API semantics.
These checks exercise storage and HTTP behavior without model execution.
"""

import uuid

import httpx2
from openai import BadRequestError, ConflictError, NotFoundError

from official_session_creation_stream import event_data
from official_session_metadata import without_metadata


def assert_no_creator_fields(payload):
    private = {"creator", "creator_kind", "creator_id", "subject_kind", "subject_id"}
    assert private.isdisjoint(payload), payload


def verify_session_creators(client, owner, other, rotated, peer, same_id, spec, expect_error):
    sessions = owner.beta.agents.sessions
    endpoint = str(owner.base_url).rstrip("/") + "/agents/sessions"
    auth = {"Authorization": "Bearer " + owner.api_key, "OpenAI-Beta": "agents=v1"}
    forged = {"X-User-ID": "test-peer", "X-Subject-Kind": "user", "X-Subject-ID": "test-peer",
              "X-Creator-Kind": "user", "X-Creator-ID": "test-peer"}
    metadata = {"creator_kind": "user", "creator_id": "test-peer"}
    recovered = []
    with client(rotated) as replacement, client(peer) as collaborator, client(same_id) as typed_peer, \
            httpx2.Client(trust_env=False, timeout=10) as raw:

        def check_retries(request, key, current):
            for caller in (owner, replacement):
                assert caller.beta.agents.sessions.create(**request, extra_headers=key) == current
            for caller in (collaborator, typed_peer):
                expect_error(ConflictError, lambda: caller.beta.agents.sessions.create(**request, extra_headers=key))
                headers = auth | key | forged | {"Authorization": "Bearer " + caller.api_key}
                with raw.stream("POST", endpoint, headers=headers, json=request | {"stream": True}) as response:
                    assert response.status_code == 409
                    assert response.headers["content-type"].split(";")[0] == "application/json"
                    response.read()
                    assert response.json()["error"]["code"] == "idempotency_conflict"
                    assert_no_creator_fields(response.json()["error"])
            response = raw.post(endpoint, headers=auth | key, json=request)
            assert response.status_code == 200 and response.json() == current.to_dict()
            assert_no_creator_fields(response.json())

        # Inline requests reach the authoritative creation upsert. Untrusted
        # metadata and forwarded identity headers cannot choose the creator.
        request = spec | {"metadata": metadata}
        key = {"Idempotency-Key": str(uuid.uuid4())}
        first = sessions.create(**request, extra_headers=key | forged)
        assert first.status == "idle" and first.metadata == metadata
        check_retries(request, key, first)
        foreign = other.beta.agents.sessions.create(**request, extra_headers=key)
        assert foreign.id != first.id
        expect_error(NotFoundError, lambda: other.beta.agents.sessions.retrieve(first.id))
        expect_error(NotFoundError, lambda: collaborator.beta.agents.sessions.retrieve(foreign.id))

        # Project peers retain reads and metadata writes without taking ownership.
        for caller in (collaborator, typed_peer):
            assert caller.beta.agents.sessions.retrieve(first.id) == first
            assert first.id in {item.id for item in caller.beta.agents.sessions.list()}
            assert list(caller.beta.agents.sessions.turns.list(first.id)) == []
            assert list(caller.beta.agents.sessions.items.list(first.id)) == []
        current = collaborator.beta.agents.sessions.update(first.id, metadata={"creator_id": "test-peer"})
        assert without_metadata(current) == without_metadata(first)
        assert_no_creator_fields(current.to_dict())
        check_retries(request, key, current)
        recovered.append((request, key, current))
        response = raw.get(endpoint + "/" + current.id, headers=auth | forged)
        assert response.status_code == 200 and response.json() == current.to_dict()
        assert_no_creator_fields(response.json())
        response = raw.get(endpoint, headers=auth, params={"agent_id": current.agent.id})
        assert response.status_code == 200
        assert [item["id"] for item in response.json()["data"]] == [current.id]
        assert_no_creator_fields(response.json()["data"][0])
        for fields in ({"creator_kind": "user", "creator_id": "test-peer"},
                       {"creator": {"kind": "user", "id": "test-peer"}}):
            expect_error(BadRequestError, lambda: sessions.create(**spec, extra_body=fields))
            expect_error(BadRequestError, lambda: sessions.update(current.id, extra_body=fields))
        check_retries(request, key, current)

        # Saved references recover before source resolution, even after a peer
        # changes or deletes the source Agent.
        source = owner.beta.agents.create(model="creator-fixture-model", instructions="Frozen source.")
        request = {"agent_id": source.id, "environment": {"type": "none"}, "metadata": metadata}
        key = {"Idempotency-Key": str(uuid.uuid4())}
        saved = sessions.create(**request, extra_headers=key | forged)
        check_retries(request, key, saved)
        collaborator.beta.agents.update(source.id, model="updated-fixture-model", instructions="Changed source.")
        check_retries(request, key, saved)
        assert saved.agent.model == "creator-fixture-model" and saved.agent.instructions == "Frozen source."
        collaborator.beta.agents.delete(source.id)
        expect_error(NotFoundError, lambda: sessions.create(**request))
        saved = typed_peer.beta.agents.sessions.update(saved.id, metadata={"shared": "updated"})
        check_retries(request, key, saved)
        recovered.append((request, key, saved))

        # Streaming creation also records the authenticated typed subject. Two
        # users with different IDs cannot share a retry; peers may still delete.
        key = {"Idempotency-Key": str(uuid.uuid4())}
        headers = auth | key | forged | {"Authorization": "Bearer " + typed_peer.api_key}
        with raw.stream("POST", endpoint, headers=headers, json=spec | {"stream": True}) as response:
            assert response.status_code == 200 and response.headers["content-type"] == "text/event-stream"
            created = event_data(response.iter_lines())
            assert created["type"] == "agent.session.created"
            assert_no_creator_fields(created["session"])
        streamed = typed_peer.beta.agents.sessions.create(**spec, extra_headers=key)
        assert streamed.id == created["session"]["id"]
        for caller in (owner, collaborator):
            expect_error(ConflictError, lambda: caller.beta.agents.sessions.create(**spec, extra_headers=key))
        deleted = collaborator.beta.agents.sessions.delete(streamed.id)
        assert deleted.deleted is True and deleted.id == streamed.id
        expect_error(NotFoundError, lambda: typed_peer.beta.agents.sessions.retrieve(streamed.id))

    print("Session creator local policy: typed subjects, credential rotation, project sharing, immutable retries, source update/deletion, private wire fields and pre-SSE JSON conflicts passed; no model execution or hosted semantics verified.")
    return recovered


def verify_creator_recovery(client, rotated, peer, same_id, retries, expect_error):
    with client(rotated) as creator, client(peer) as collaborator, client(same_id) as typed_peer:
        for request, key, current in retries:
            assert creator.beta.agents.sessions.create(**request, extra_headers=key) == current
            for caller in (collaborator, typed_peer):
                assert caller.beta.agents.sessions.retrieve(current.id) == current
                expect_error(ConflictError, lambda: caller.beta.agents.sessions.create(**request, extra_headers=key))
    print("Session creator local policy: same-subject credential and cross-subject retry behavior survived service restart.")
