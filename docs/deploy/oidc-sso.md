# Generic OIDC SSO deployment guide

This guide configures third-party login through standard OpenID Connect
providers such as Google, Microsoft Entra ID, Okta, Auth0, Keycloak, or an
internal corporate IdP.

Feishu stays on its own guide because its token exchange is Feishu-specific:
see [feishu-prod.md](./feishu-prod.md). GitHub login is not part of this guide
because GitHub OAuth Apps are OAuth2, not generic OIDC.

## 1. What Parsar supports

Parsar can load one or more OIDC providers from environment variables:

```bash
PARSAR_AUTH_OIDC_PROVIDERS=google,company
```

Each provider gets its own login routes:

```text
GET /api/v1/auth/oidc/google/start
GET /api/v1/auth/oidc/google/callback

GET /api/v1/auth/oidc/company/start
GET /api/v1/auth/oidc/company/callback
```

The login page discovers enabled providers through
`GET /api/v1/auth/providers`, so no frontend configuration is required.

The local identity binding uses the provider id:

```text
auth_identities.provider = "oidc:google"
auth_identities.subject  = "<id_token.sub>"
```

Do not reuse the same provider id for different issuers.

## 2. Create an OIDC application in your IdP

In your identity provider console, create an OIDC / OAuth client with:

| Field | Value |
|---|---|
| Application type | Web application / confidential client |
| Flow | Authorization code |
| Redirect URI | `https://<your-parsar-host>/api/v1/auth/oidc/<provider-id>/callback` |
| Scopes | `openid email profile` |

Record:

- Issuer URL, for example `https://accounts.google.com` or
  `https://login.microsoftonline.com/<tenant-id>/v2.0`
- Client ID
- Client Secret

The issuer URL must be the issuer root that exposes:

```text
<issuer>/.well-known/openid-configuration
```

## 3. Configure Parsar

Provider ids must match:

```text
^[a-z][a-z0-9_-]{0,62}$
```

The env prefix is the uppercased provider id, with non-alphanumeric characters
converted to `_`. For example, provider id `company-sso` uses
`PARSAR_AUTH_OIDC_COMPANY_SSO_*`.

### 3.1 Google example

Register this redirect URI in Google Cloud Console:

```text
https://parsar.example.com/api/v1/auth/oidc/google/callback
```

Set:

```bash
PARSAR_PUBLIC_URL=https://parsar.example.com

PARSAR_AUTH_OIDC_PROVIDERS=google
PARSAR_AUTH_OIDC_GOOGLE_LABEL=Google
PARSAR_AUTH_OIDC_GOOGLE_ISSUER_URL=https://accounts.google.com
PARSAR_AUTH_OIDC_GOOGLE_CLIENT_ID=<google-oauth-client-id>
PARSAR_AUTH_OIDC_GOOGLE_CLIENT_SECRET=<google-oauth-client-secret>
PARSAR_AUTH_OIDC_GOOGLE_SCOPES="openid email profile"

# Optional: restrict logins to organization email domains.
PARSAR_AUTH_OIDC_GOOGLE_ALLOWED_DOMAINS=example.com
```

### 3.2 Microsoft Entra ID example

Use your tenant-specific issuer, not `common`, when you want one organization:

```bash
PARSAR_PUBLIC_URL=https://parsar.example.com

PARSAR_AUTH_OIDC_PROVIDERS=entra
PARSAR_AUTH_OIDC_ENTRA_LABEL="Microsoft Entra ID"
PARSAR_AUTH_OIDC_ENTRA_ISSUER_URL=https://login.microsoftonline.com/<tenant-id>/v2.0
PARSAR_AUTH_OIDC_ENTRA_CLIENT_ID=<application-client-id>
PARSAR_AUTH_OIDC_ENTRA_CLIENT_SECRET=<client-secret-value>
PARSAR_AUTH_OIDC_ENTRA_SCOPES="openid email profile"
PARSAR_AUTH_OIDC_ENTRA_ALLOWED_DOMAINS=example.com
```

### 3.3 Keycloak example

For a realm named `engineering`:

```bash
PARSAR_PUBLIC_URL=https://parsar.example.com

PARSAR_AUTH_OIDC_PROVIDERS=keycloak
PARSAR_AUTH_OIDC_KEYCLOAK_LABEL=Keycloak
PARSAR_AUTH_OIDC_KEYCLOAK_ISSUER_URL=https://keycloak.example.com/realms/engineering
PARSAR_AUTH_OIDC_KEYCLOAK_CLIENT_ID=parsar
PARSAR_AUTH_OIDC_KEYCLOAK_CLIENT_SECRET=<keycloak-client-secret>
PARSAR_AUTH_OIDC_KEYCLOAK_SCOPES="openid email profile"
```

## 4. Multiple providers

Separate provider ids with commas:

```bash
PARSAR_AUTH_OIDC_PROVIDERS=google,company

PARSAR_AUTH_OIDC_GOOGLE_LABEL=Google
PARSAR_AUTH_OIDC_GOOGLE_ISSUER_URL=https://accounts.google.com
PARSAR_AUTH_OIDC_GOOGLE_CLIENT_ID=<id>
PARSAR_AUTH_OIDC_GOOGLE_CLIENT_SECRET=<secret>
PARSAR_AUTH_OIDC_GOOGLE_ALLOWED_DOMAINS=example.com

PARSAR_AUTH_OIDC_COMPANY_LABEL="Company SSO"
PARSAR_AUTH_OIDC_COMPANY_ISSUER_URL=https://idp.company.example
PARSAR_AUTH_OIDC_COMPANY_CLIENT_ID=<id>
PARSAR_AUTH_OIDC_COMPANY_CLIENT_SECRET=<secret>
PARSAR_AUTH_OIDC_COMPANY_ALLOWED_DOMAINS=company.example
```

The login page will show one button per enabled provider.

## 5. Optional settings

```bash
# Defaults to the provider id.
PARSAR_AUTH_OIDC_<ID>_LABEL="Company SSO"

# Defaults to:
# ${PARSAR_PUBLIC_URL}/api/v1/auth/oidc/<id>/callback
PARSAR_AUTH_OIDC_<ID>_REDIRECT_URI=https://parsar.example.com/api/v1/auth/oidc/<id>/callback

# Defaults to: openid email profile
PARSAR_AUTH_OIDC_<ID>_SCOPES="openid email profile"

# Comma-separated domain allowlist. Empty means any verified email domain.
PARSAR_AUTH_OIDC_<ID>_ALLOWED_DOMAINS=example.com,example.org

# Defaults to true. Only set false for an IdP that cannot emit email_verified
# and is otherwise trusted by your organization.
PARSAR_AUTH_OIDC_<ID>_REQUIRE_VERIFIED_EMAIL=false
```

## 6. Docker Compose self-hosting

When using `deploy/compose/compose.selfhost.yml`, put the OIDC variables in
the same `.env` file used by compose:

```bash
cd deploy/compose
cp .env.example .env
$EDITOR .env
docker compose -f compose.selfhost.yml --env-file .env up -d
```

The compose service reads `.env` as a container `env_file`, so dynamic
provider-specific variables such as `PARSAR_AUTH_OIDC_COMPANY_CLIENT_ID` are
available inside `parsar-server`.

## 7. Verify the deployment

Check the public provider list:

```bash
curl -fsS https://parsar.example.com/api/v1/auth/providers
```

Expected shape:

```json
{
  "providers": [
    {
      "id": "password",
      "type": "password",
      "label": "Email password",
      "enabled": true,
      "login_url": "/login"
    },
    {
      "id": "oidc:google",
      "type": "oidc",
      "label": "Google",
      "enabled": true,
      "login_url": "/api/v1/auth/oidc/google/start"
    }
  ]
}
```

Then open the Parsar login page and click the new provider button.

Workspace owners/admins can also open Settings -> Authentication to see
callback URL and missing-env diagnostics.

## 8. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Provider does not appear on login page | Missing required env | Check Settings -> Authentication or `/api/v1/workspaces/{id}/auth/providers` |
| Callback returns `OIDC exchange failed` | Redirect URI mismatch or bad client secret | Make the IdP redirect URI exactly match Parsar's callback URL |
| Callback returns `nonce mismatch` | Stale callback or cookies blocked | Restart login from the Parsar login page; ensure browser accepts first-party cookies |
| Callback returns `email is not verified` | IdP did not emit `email_verified=true` | Enable email/profile claims in the IdP, or explicitly set `REQUIRE_VERIFIED_EMAIL=false` only for trusted IdPs |
| Callback returns `email domain is not allowed` | Email domain not in allowlist | Update `PARSAR_AUTH_OIDC_<ID>_ALLOWED_DOMAINS` |
| Discovery fails | Wrong issuer URL | Open `<issuer>/.well-known/openid-configuration` in a browser or with curl |

