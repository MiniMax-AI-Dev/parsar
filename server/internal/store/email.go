package store

import "strings"

// normalizeEmail is the single canonical form used for users.email
// and auth_identities.subject (provider='email'). RFC 5321 says the
// mailbox local-part is case-sensitive in theory; in practice all
// mainstream providers fold it, and every OIDC IdP returns email
// claims lower-cased. Storing the folded form makes:
//
//   * login lookups deterministic — POST /auth/login can lower-case
//     the request email once and hit exactly one users row;
//   * duplicate detection safe — "Admin@Example.com" and "ADMIN@example.com"
//     become the same row under the users.email UNIQUE constraint;
//   * IdP linking simple — the OIDC subject already matches.
//
// Whitespace is also trimmed here so a stray copy/paste space cannot
// silently produce two "same-looking" rows.
//
// Call this at every write site (bootstrap, invite, OIDC upsert). Read
// paths lower-case the query key the same way; the fold is one-way,
// so the two sides stay symmetric.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func NormalizeEmail(email string) string {
	return normalizeEmail(email)
}
