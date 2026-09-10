//go:build !unix

package dev

import (
	"context"
	"errors"
)

type defaultSkillPreviewRunner struct{}

func (defaultSkillPreviewRunner) Run(context.Context, string, string, ...string) ([]byte, error) {
	return nil, errors.New("Skill previews require a Unix server with process-group cancellation support")
}
