package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type environmentFileCursor struct {
	Version     int    `json:"v"`
	Binding     string `json:"b"`
	Fingerprint string `json:"f"`
	Offset      int    `json:"o"`
}

func environmentFilesDigest(value any) string {
	data, _ := json.Marshal(value)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func decodeEnvironmentFileCursor(token, binding string) (environmentFileCursor, error) {
	var cursor environmentFileCursor
	if len(token) > 1024 {
		return cursor, store.ErrInvalidInput
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil {
		return cursor, store.ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.Version != 1 || cursor.Binding != binding || len(cursor.Fingerprint) != 64 || cursor.Offset <= 0 {
		return environmentFileCursor{}, store.ErrInvalidInput
	}
	return cursor, nil
}

func environmentFilePage(files []v1.EnvironmentFile, options environmentFileOptions) (v1.EnvironmentFileList, error) {
	fingerprint := environmentFilesDigest(files)
	start := 0
	if cursor := options.cursor; cursor != nil {
		if cursor.Fingerprint != fingerprint || cursor.Offset >= len(files) || cursor.Offset%options.limit != 0 {
			return v1.EnvironmentFileList{}, store.ErrInvalidInput
		}
		start = cursor.Offset
	}
	end := min(start+options.limit, len(files))
	response := v1.EnvironmentFileList{Data: files[start:end]}
	if end < len(files) {
		data, _ := json.Marshal(environmentFileCursor{Version: 1, Binding: options.binding, Fingerprint: fingerprint, Offset: end})
		token := base64.RawURLEncoding.EncodeToString(data)
		response.Next = &token
	}
	return response, nil
}
