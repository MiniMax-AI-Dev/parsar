package deepseekharness

import (
	"errors"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func promptContent(req proto.PromptRequestPayload, resumeFallback string) ([]promptContentPart, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, errors.New("deepseekharness: empty prompt")
	}
	sections := make([]string, 0, 3)
	if system := systemPreamble(req.AgentOptions); system != "" {
		sections = append(sections, system)
	}
	if resumeFallback = strings.TrimSpace(resumeFallback); resumeFallback != "" {
		sections = append(sections, resumeFallback)
	}
	sections = append(sections, prompt)
	parts := []promptContentPart{{Type: "text", Text: strings.Join(sections, "\n\n")}}
	for _, att := range req.Attachments {
		if !isSupportedImageMedia(att.MIME) {
			continue
		}
		parts = append(parts, promptContentPart{Type: "image", MediaType: att.MIME, Data: att.DataBase64})
	}
	return parts, nil
}

var supportedImageMedia = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

func isSupportedImageMedia(mime string) bool {
	return supportedImageMedia[strings.ToLower(strings.TrimSpace(mime))]
}

func systemPreamble(opts map[string]any) string {
	if override := stringOpt(opts, "override_system_prompt"); override != "" {
		return override
	}
	return stringOpt(opts, "system_prompt")
}
