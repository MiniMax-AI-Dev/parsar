package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const skillUploadAudience = "parsar/skill-upload/v1"

// SkillUploadSigner issues upload-only credentials for one running task.
type SkillUploadSigner struct {
	key []byte
}

func NewSkillUploadSigner(masterKey string) *SkillUploadSigner {
	if strings.TrimSpace(masterKey) == "" {
		return nil
	}
	mac := hmac.New(sha256.New, []byte(masterKey))
	mac.Write([]byte(skillUploadAudience))
	return &SkillUploadSigner{key: mac.Sum(nil)}
}

func (s *SkillUploadSigner) Token(runID string) (string, error) {
	if s == nil || len(s.key) == 0 {
		return "", errors.New("skill upload is unavailable")
	}
	if _, err := uuid.Parse(runID); err != nil {
		return "", errors.New("skill upload requires a run UUID")
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   runID,
		Audience:  jwt.ClaimStrings{skillUploadAudience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}).SignedString(s.key)
}

func (s *SkillUploadSigner) Verify(token string) (string, error) {
	if s == nil || len(s.key) == 0 {
		return "", errors.New("skill upload is unavailable")
	}
	claims := &jwt.RegisteredClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(_ *jwt.Token) (any, error) {
		return s.key, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience(skillUploadAudience), jwt.WithExpirationRequired())
	if err != nil {
		return "", errors.New("invalid or expired skill upload credential")
	}
	if _, err := uuid.Parse(claims.Subject); err != nil {
		return "", errors.New("invalid skill upload run")
	}
	return claims.Subject, nil
}
