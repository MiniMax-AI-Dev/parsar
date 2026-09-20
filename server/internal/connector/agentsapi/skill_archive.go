package agentsapi

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
	"gopkg.in/yaml.v3"
)

// Build accepts rootless ZIPs and descriptive slug/title metadata. Normalize only
// those product conventions; preserve file bytes and modes, and let Core's shared
// validator reject native activation controls or unsafe archives.
func protocolSkillArchive(body []byte, metadata agentskill.Metadata) ([]byte, error) {
	if _, err := agentskill.Read(body, metadata); err == nil {
		return body, nil
	}
	if len(body) > agentskill.MaxArchiveBytes {
		return nil, agentskill.ErrInvalid
	}
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil || len(reader.File) > agentskill.MaxFiles {
		return nil, agentskill.ErrInvalid
	}
	flat := false
	for _, entry := range reader.File {
		if entry.Name == "SKILL.md" {
			flat = true
		}
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	total := 0
	for _, entry := range reader.File {
		if !entry.Mode().IsRegular() && !entry.FileInfo().IsDir() {
			return nil, agentskill.ErrInvalid
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, agentskill.ErrInvalid
		}
		data, err := io.ReadAll(io.LimitReader(stream, int64(agentskill.MaxExpandedBytes-total)+1))
		_ = stream.Close()
		total += len(data)
		if err != nil || total > agentskill.MaxExpandedBytes {
			return nil, agentskill.ErrInvalid
		}
		if entry.Name == "SKILL.md" || strings.Count(entry.Name, "/") == 1 && strings.HasSuffix(entry.Name, "/SKILL.md") {
			data, err = protocolSkillManifest(data, metadata)
			if err != nil {
				return nil, err
			}
		}
		header := entry.FileHeader
		if flat {
			header.Name = metadata.Name + "/" + header.Name
		}
		target, err := writer.CreateHeader(&header)
		if err != nil {
			return nil, agentskill.ErrInvalid
		}
		if _, err := target.Write(data); err != nil {
			return nil, agentskill.ErrInvalid
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	normalized := output.Bytes()
	if _, err := agentskill.Read(normalized, metadata); err != nil {
		return nil, err
	}
	return normalized, nil
}

func protocolSkillManifest(body []byte, metadata agentskill.Metadata) ([]byte, error) {
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, agentskill.ErrInvalid
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return nil, agentskill.ErrInvalid
	}
	end += 4
	var fields map[string]any
	if yaml.Unmarshal([]byte(text[4:end]), &fields) != nil {
		return nil, agentskill.ErrInvalid
	}
	if trigger, exists := fields["trigger"]; exists && trigger != "" && trigger != nil {
		return nil, agentskill.ErrInvalid
	}
	delete(fields, "slug")
	delete(fields, "title")
	delete(fields, "trigger")
	fields["name"] = metadata.Name
	encoded, err := yaml.Marshal(fields)
	if err != nil {
		return nil, agentskill.ErrInvalid
	}
	return []byte("---\n" + string(encoded) + "---" + text[end+4:]), nil
}
