# Security Policy

## Supported versions

Parsar is pre-1.0 and ships from `main`. Security fixes land on `main` and
are cut into the next tagged release. Older tags do not receive backports.

## Reporting a vulnerability

**Please do not open a public GitHub issue for security problems.**

Use GitHub's private vulnerability reporting instead:

➡️ <https://github.com/MiniMax-AI-Dev/parsar/security/advisories/new>

When filing, include:

- A description of the issue and its impact (what an attacker can do).
- A minimal reproduction — exact commands, requests, or steps.
- The version / commit SHA you tested against.
- Whether you would like public credit, and how to contact you.

## What to expect

- We will acknowledge the report within **5 business days**.
- We aim to ship a fix or a documented mitigation within **30 days** for
  high-severity issues, longer for low-severity. We will keep you updated.
- After the fix is released, we will publish a GitHub Security Advisory
  crediting you (unless you prefer to stay anonymous).

## Out of scope

The following are not considered vulnerabilities for the purposes of this
policy:

- Findings that require physical access to a developer's machine.
- Self-hosted misconfiguration (e.g. exposing the admin API to the
  internet without an auth proxy).
- Reports against third-party dependencies — file those upstream and let
  us know so we can bump.

Thank you for helping keep Parsar and its users safe.
