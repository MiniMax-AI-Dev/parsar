# Shared test Feishu App (internal distribution)

> **Who this is for:** internal colleagues who cloned the parsar repo, got it
> running with mock auth via `INSTALL.md`, and now want to experience real
> Feishu login / group bot interaction without registering their own app on
> the Feishu Open Platform.
>
> **Who this is NOT for:** anyone about to deploy Parsar for a real team /
> customer — go through [`feishu-prod.md`](feishu-prod.md) and register your
> own app. The shared app is for internal trial only; do not expose its
> callback domain publicly and do not post it into groups.

---

## Why we do not commit the App ID/Secret into the repo

`PARSAR_FEISHU_APP_SECRET` is a long-lived credential. Anyone who has it can:

- Impersonate this app against the Feishu API (query contacts, send messages);
- Decrypt event callbacks this app receives;
- Show up "signed as this app" in every log the Feishu console keeps.

The repo is open-source / widely distributed. **Any secret that lands in git
history is considered leaked** (even a follow-up commit that deletes it —
reflog / forks / already-cloned checkouts still hold it), and would require
a Feishu-console secret-reset. So this app's real credentials are only
distributed through internal Feishu channels (§1 below) and **never enter
git**.

---

## 1. Where to get the shared app credentials

> **Status:** the App ID is filled in below; the Secret / Verification Token /
> Encrypt Key **do not enter git** and live in the 1Password team vault under
> `parsar-oss shared Feishu app` (the owner has already distributed it — DM
> the owner if you cannot see the entry).

| Field | Value |
|---|---|
| `PARSAR_FEISHU_APP_ID` | `cli_a9488c841e79dcee` |
| `PARSAR_FEISHU_APP_SECRET` | 1Password → `parsar-oss shared Feishu app` → `app_secret` field |
| `PARSAR_FEISHU_VERIFICATION_TOKEN` | 1Password → same entry → `verification_token` field |
| `PARSAR_FEISHU_ENCRYPT_KEY` | 1Password → same entry → `encrypt_key` field (empty means event encryption is disabled on this app) |
| `PARSAR_FEISHU_REDIRECT_URI` | See §2 below |
| Shared-app admin URL (for the owner) | `<TODO owner: the app admin URL on open.feishu.cn>` |
| Tenant that owns the shared app | `<TODO owner: e.g. xxx.feishu.cn>` |

**Distribution guidelines (for the owner):**

- Prefer 1Password / Feishu enterprise password vault / Bitwarden shared entries with per-member authorization.
- As a fallback, DM in Feishu, **not** groups, **not** email attachments.
- Do not write it into any document that can be screenshot / screen-recorded / shared (including this document itself).

If you cannot open the 1Password entry: DM the owner to add you — **do not**
have another colleague screenshot / copy the secret; the password manager
is the only safe path.

---

## 2. Callback URL (`REDIRECT_URI`)

The shared app's callback allowlist is pre-populated with a few common
local addresses. **Your `PARSAR_PUBLIC_URL` must match one of them**;
otherwise Feishu rejects the callback.

Pre-configured allowlist:

| Scenario | `PARSAR_PUBLIC_URL` | `PARSAR_FEISHU_REDIRECT_URI` |
|---|---|---|
| Local docker compose default | `http://localhost:8080` | `http://localhost:8080/api/v1/auth/feishu/callback` |
| Local, custom port | `http://localhost:<port>` | `http://localhost:<port>/api/v1/auth/feishu/callback` |
| Tunneled / temporary public URL | `<TODO owner: e.g. https://parsar-test.example.com>` | Same + `/api/v1/auth/feishu/callback` |

**Colleagues on localhost do not need a tunnel** — Feishu OAuth's browser
redirect callback goes through the user's browser, not a Feishu-server
callback into your box, so `localhost` is valid (as long as it is in the
allowlist).

If you need an address that is not in the allowlist: DM the owner to add
it. **Do not modify the app-admin config yourself** (it would affect every
colleague using this app).

---

## 3. Switching from mock to the shared app

Precondition: parsar is already running in mock mode per
[`../../INSTALL.md`](../../INSTALL.md), `deploy/compose/.env` exists and
`docker compose up` works.

### 3.1 Turn mock off; set the four Feishu variables

```bash
cd deploy/compose

# Turn mock off
sed -i.bak 's/^PARSAR_FEISHU_MOCK=.*/PARSAR_FEISHU_MOCK=false/' .env

# Fill the four Feishu variables (replace <…> with the values from the owner)
cat >> .env <<'EOF'

# --- Shared test app (from docs/deploy/shared-feishu-app.md) ---
PARSAR_FEISHU_APP_ID=<cli_xxxxx>
PARSAR_FEISHU_APP_SECRET=<xxx>
PARSAR_FEISHU_VERIFICATION_TOKEN=<xxx>
PARSAR_FEISHU_ENCRYPT_KEY=
PARSAR_FEISHU_REDIRECT_URI=http://localhost:8080/api/v1/auth/feishu/callback
EOF

rm -f .env.bak
```

If the four `PARSAR_FEISHU_*` lines already exist in `.env` (e.g. from a
prior run), **overwrite them rather than appending duplicates** — docker
compose uses the last-seen value, but `grep` during triage sees both and
that is easy to misread.

### 3.2 Restart the server so the new env takes effect

```bash
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env \
  up -d --force-recreate parsar-server
sleep 6
docker logs parsar-server 2>&1 | tail -20
```

**Expected:** no `PARSAR_FEISHU_*_required` or similar errors in the logs;
server status `Up X seconds (healthy)`.

### 3.3 Verify in the browser

Open `http://localhost:8080` and click "Feishu login". Expected:

1. Redirect to the Feishu OAuth consent page (domain `passport.feishu.cn` or `accounts.feishu.cn`).
2. After consenting, redirect back to `http://localhost:8080/api/v1/auth/feishu/callback`.
3. Land on the parsar home page. The top-right shows **your real Feishu name / avatar**, not the mock `admin@example.com`.

---

## 4. Common troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Feishu consent page reports `redirect_uri not in allowlist` | Your `PARSAR_PUBLIC_URL` / `PARSAR_FEISHU_REDIRECT_URI` is not in the §2 allowlist | Switch back to `http://localhost:8080`, or DM the owner to add yours |
| Callback succeeds but the page reports `email scope required` | The shared app has not requested / been approved for `contact:user.email:readonly` | Ask the owner — that is an app-level setting, not one you can fix in `.env` |
| Logs repeatedly show `verification token mismatch` | Wrong `PARSAR_FEISHU_VERIFICATION_TOKEN` in `.env` | Re-check with the owner; the Feishu console value is authoritative |
| Login works but @Bot gets no response in a group | The event subscription does not point at your parsar instance (the shared app can only have one callback URL) | Expected — the shared app routes events only to the owner's staging box, not yours. To receive events yourself, go through [`feishu-prod.md`](feishu-prod.md) and register your own app |
| Want to switch back to mock | `sed -i.bak 's/^PARSAR_FEISHU_MOCK=.*/PARSAR_FEISHU_MOCK=true/' .env` and restart server | Leaving the shared-app credentials in `.env` is fine — `MOCK=true` makes the server ignore them |

---

## 5. Tear down / clean up

Once you no longer need the shared app:

```bash
cd deploy/compose
# Delete the four Feishu variable lines (or keep them and switch MOCK back to true)
sed -i.bak '/^PARSAR_FEISHU_APP_ID=/d;
            /^PARSAR_FEISHU_APP_SECRET=/d;
            /^PARSAR_FEISHU_VERIFICATION_TOKEN=/d;
            /^PARSAR_FEISHU_ENCRYPT_KEY=/d;
            /^PARSAR_FEISHU_REDIRECT_URI=/d' .env
rm -f .env.bak

# Switch back to mock (optional)
sed -i.bak 's/^PARSAR_FEISHU_MOCK=.*/PARSAR_FEISHU_MOCK=true/' .env
rm -f .env.bak

# Restart
docker compose -f deploy/compose/compose.example.yml --env-file .env \
  up -d --force-recreate parsar-server
```

`.env` is already covered by `.gitignore` (the root `.gitignore` lines 7–9
`.env` / `.env.*`) so it will not enter git, but if you copied the secrets
elsewhere (scratch notes, chat logs), clear those too.

---

## 6. Going to a real deployment?

Do not keep using the shared app. The shared app is "internal trial for
several people" — **its callback URL, permission scope, and tenant belong
to the owner**. A real team deployment requires:

- Your own app in your own tenant (otherwise every user has to join the owner's Feishu tenant);
- Your own callback domain + HTTPS;
- Your own secret manager, rather than DM-based distribution.

Full flow: [`feishu-prod.md`](feishu-prod.md).
