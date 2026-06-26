// Package oss is the Aliyun OSS client used by the capability
// subsystem for plugin zip storage.
//
// Single bucket per deployment. Objects are keyed
// `<prefix>/<workspaceID>/<uuid>/<filename>` so cross-tenant reads
// can be rejected on key shape alone. The bucket MUST be private —
// all access is mediated by V4-signed presigned URLs with a short
// TTL, so a leaked download URL is bounded by the TTL window.
package oss
