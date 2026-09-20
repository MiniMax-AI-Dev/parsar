package api

import (
	"encoding/base64"
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func decodeInitialFiles(raw json.RawMessage) ([]store.InitialFile, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) > 50 {
		return nil, store.ErrInvalidInput
	}
	files := make([]store.InitialFile, 0, len(entries))
	for _, entry := range entries {
		var in struct {
			Type   string  `json:"type"`
			Path   string  `json:"path"`
			Data   *string `json:"data"`
			FileID *string `json:"file_id"`
		}
		if decodeInputObject(entry, &in, "type", "path", "data", "file_id") != nil {
			return nil, store.ErrInvalidInput
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(entry, &fields)
		f := store.InitialFile{Type: in.Type, Path: in.Path}
		switch in.Type {
		case "inline":
			if _, exists := fields["file_id"]; exists || in.Data == nil || len(*in.Data) > base64.StdEncoding.EncodedLen(5<<20) {
				return nil, store.ErrInvalidInput
			}
			var err error
			f.Data, err = base64.StdEncoding.Strict().DecodeString(*in.Data)
			if err != nil {
				return nil, store.ErrInvalidInput
			}
		case "file_id":
			if _, exists := fields["data"]; exists || in.FileID == nil {
				return nil, store.ErrInvalidInput
			}
			f.FileID = *in.FileID
		default:
			return nil, store.ErrInvalidInput
		}
		files = append(files, f)
	}
	return files, store.ValidateInitialFiles(files)
}

func initialFileResponse(files []store.InitialFile) []json.RawMessage {
	metadata := make([]store.InitialFileMetadata, 0, len(files))
	for _, file := range files {
		m := store.InitialFileMetadata{Type: file.Type, Path: file.Path, FileID: file.FileID}
		if file.Type == "inline" {
			size := int64(len(file.Data))
			m.SizeBytes = &size
		}
		metadata = append(metadata, m)
	}
	return templateFileResponse(metadata)
}

func templateFileResponse(files []store.InitialFileMetadata) []json.RawMessage {
	result := make([]json.RawMessage, 0, len(files))
	for _, file := range files {
		body, _ := json.Marshal(file)
		result = append(result, body)
	}
	return result
}
