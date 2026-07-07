-- +goose Up
-- ==============================================================
-- 000006_email_password_identity — Local email/password identity
-- ==============================================================
-- Background:
--   Parsar previously supported only Feishu OIDC. Open-source users
--   without a Feishu tenant had no way to complete first-time
--   bootstrap. This migration is an anchor for provider='email' in
--   auth_identities, backing:
--     * POST /api/v1/bootstrap  -- first-time registration (count==0)
--     * POST /api/v1/auth/login -- subsequent email+password login
--
-- Data layout (no schema change, comment only):
--   users(email, name, status, ...)                       -- existing
--   auth_identities(provider='email', subject=<email>,
--                   metadata={"password_hash":"$2a$12$...",
--                             "hashed_at":"<RFC3339>"})   -- new use
--   user_sessions                                          -- existing
--
-- Why metadata jsonb rather than a new password_credentials table:
--   1. auth_identities.provider already declares 'email' in 000001.
--   2. Multi-identity model stays intact: one user can bind email
--      AND feishu at the same time.
--   3. Store layer exposes GetPasswordHashByEmail only, so the hash
--      never rides along with a full metadata dump into logs.
--
-- (provider, subject) uniqueness is already enforced by
-- uk_auth_identities_provider_subject in 000001.
-- ==============================================================

COMMENT ON COLUMN auth_identities.metadata IS
  'Identity-provider profile. provider=email uses {"password_hash":"<bcrypt>","hashed_at":"<RFC3339>","last_used_at":"<RFC3339>"}; OIDC providers stash the userinfo response.';

-- +goose Down
COMMENT ON COLUMN auth_identities.metadata IS 'Identity provider extra info';
