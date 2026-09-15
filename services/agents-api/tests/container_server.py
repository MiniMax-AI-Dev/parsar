#!/usr/bin/env python3
"""Run the existing official-client suite against a read-only Linux container."""

import os

# Match ownership of the suite's private key file without granting root access.
# The image's default UID is separately exercised by deployment acceptance.
assert os.getuid() != 0, "Run container acceptance as an unprivileged host user"
keys = os.environ["AGENTS_API_KEYS_FILE"]
args = [
    "docker", "run", "--rm", "--read-only", "--network=host",
    "--cap-drop=ALL", "--security-opt=no-new-privileges",
    "--user", f"{os.getuid()}:{os.getgid()}",
    "--mount", f"type=bind,source={keys},target=/run/keys.json,readonly",
    "--env", "AGENTS_API_KEYS_FILE=/run/keys.json",
]
credential_key = os.environ.get("AGENTS_API_CREDENTIAL_KEY_FILE")
if credential_key:
    args.extend([
        "--mount", f"type=bind,source={credential_key},target=/run/credential.key,readonly",
        "--env", "AGENTS_API_CREDENTIAL_KEY_FILE=/run/credential.key",
    ])
for name in ("AGENTS_API_DATABASE_URL", "AGENTS_API_ADDR", "AGENTS_API_ENGINE"):
    args.extend(["--env", name])
args.append(os.environ["AGENTS_API_IMAGE"])
# Docker forwards termination to the API and --rm removes the stopped container.
os.execvp(args[0], args)
