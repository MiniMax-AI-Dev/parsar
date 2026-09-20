package dev

import "github.com/MiniMax-AI-Dev/parsar/server/internal/audit"

type AuditIngester interface {
	Emit(audit.Event) error
}
