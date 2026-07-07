package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FeishuParsedMessageContent is the normalized readable body plus metadata
// extracted from Feishu's message.content JSON. Binary payloads are not
// downloaded here; stable keys are persisted for a later attachment worker.
type FeishuParsedMessageContent struct {
	Text     string
	Metadata map[string]any
}

// ParseFeishuMessageContent handles text, post, and image. Unsupported
// types fall back to best-effort text extraction so the inbound path
// stays tolerant of upstream payload drift.
func ParseFeishuMessageContent(messageType, content string, mentionKeys []string) FeishuParsedMessageContent {
	messageType = strings.ToLower(strings.TrimSpace(messageType))
	metadata := map[string]any{}
	if messageType != "" {
		metadata["message_type"] = messageType
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		text := stripFeishuMentionKeys(strings.TrimSpace(content), mentionKeys)
		return FeishuParsedMessageContent{Text: text, Metadata: metadata}
	}

	var text string
	switch messageType {
	case "image":
		imageKey := jsonMapString(decoded, "image_key")
		if imageKey != "" {
			metadata["image_keys"] = []string{imageKey}
			text = "[image]"
		}
	case "post":
		text, metadata = parseFeishuPostContent(decoded, metadata)
	default:
		text = jsonMapString(decoded, "text")
		if text == "" {
			// Older shims send post-shaped bodies without message_type.
			if hasFeishuPostContent(decoded) {
				text, metadata = parseFeishuPostContent(decoded, metadata)
			}
		}
	}
	if text == "" && len(decoded) > 0 && messageType != "image" {
		if raw, err := json.Marshal(decoded); err == nil {
			text = string(raw)
		}
	}
	text = stripFeishuMentionKeys(strings.TrimSpace(text), mentionKeys)
	return FeishuParsedMessageContent{Text: text, Metadata: metadata}
}

func parseFeishuPostContent(decoded map[string]any, metadata map[string]any) (string, map[string]any) {
	post := feishuPostBody(decoded)
	if post == nil {
		return "", metadata
	}
	content, _ := post["content"].([]any)
	lines := make([]string, 0, len(content))
	imageKeys := []string{}
	mentionKeys := []string{}
	atAll := false
	for _, rawLine := range content {
		nodes, _ := rawLine.([]any)
		parts := make([]string, 0, len(nodes))
		for _, rawNode := range nodes {
			node, _ := rawNode.(map[string]any)
			switch strings.ToLower(jsonMapString(node, "tag")) {
			case "text":
				parts = append(parts, jsonMapString(node, "text"))
			case "a":
				label := jsonMapString(node, "text")
				href := jsonMapString(node, "href")
				parts = append(parts, formatFeishuLink(label, href))
			case "mention_doc":
				label := firstNonBlank(jsonMapString(node, "title"), jsonMapString(node, "text"), jsonMapString(node, "token"), "document")
				href := jsonMapString(node, "href")
				parts = append(parts, formatFeishuLink(label, href))
			case "img":
				if key := jsonMapString(node, "image_key"); key != "" {
					imageKeys = append(imageKeys, key)
					parts = append(parts, "[image]")
				}
			case "at":
				userID := jsonMapString(node, "user_id")
				if userID == "@_all" {
					atAll = true
					parts = append(parts, "@everyone")
					continue
				}
				if userID != "" {
					mentionKeys = append(mentionKeys, userID)
				}
				name := firstNonBlank(jsonMapString(node, "user_name"), jsonMapString(node, "name"), jsonMapString(node, "text"))
				if name != "" {
					parts = append(parts, "@"+name)
				}
			}
		}
		line := strings.TrimSpace(strings.Join(parts, ""))
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(imageKeys) > 0 {
		metadata["image_keys"] = imageKeys
	}
	if len(mentionKeys) > 0 {
		metadata["post_mention_keys"] = mentionKeys
	}
	if atAll {
		metadata["at_all"] = true
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), metadata
}

func feishuPostBody(decoded map[string]any) map[string]any {
	for _, key := range []string{"zh_cn", "en_us"} {
		if body, _ := decoded[key].(map[string]any); body != nil {
			return body
		}
	}
	if _, ok := decoded["content"]; ok {
		return decoded
	}
	return nil
}

func hasFeishuPostContent(decoded map[string]any) bool {
	return feishuPostBody(decoded) != nil
}

func stripFeishuMentionKeys(text string, keys []string) string {
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		text = strings.ReplaceAll(text, key, "")
	}
	return strings.TrimSpace(text)
}

func jsonMapString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	raw := m[key]
	if raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func formatFeishuLink(label, href string) string {
	label = strings.TrimSpace(label)
	href = strings.TrimSpace(href)
	switch {
	case label != "" && href != "":
		return "[" + label + "](" + href + ")"
	case href != "":
		return href
	default:
		return label
	}
}

// feishuInteractiveRawFallbackMaxBytes caps the raw-JSON fallback we
// emit for an interactive card body. The LLM digests the card JSON
// fine in the P2P path that did this implicitly via
// ParseFeishuMessageContent's default fallback; we mirror that here
// for the quote-chain path. 4 KiB covers our largest known alert card
// (~3 KiB) with headroom, and stays an order of magnitude under the
// outer 16 KiB quote-chain envelope so a single card hop can't crowd
// out other ancestors.
const feishuInteractiveRawFallbackMaxBytes = 4 * 1024

// FeishuFetchedMessageText returns the readable text of a Feishu message
// body plus any image_keys carried by that body. text=="" with imageKeys
// non-empty is the "image-only hop" case — the chain walker keeps the
// hop (renders an [image:N] placeholder) instead of skipping it.
// text=="" with no imageKeys means truly skippable (merge_forward,
// share_chat, sticker — handled at a higher layer or just dropped).
//
// imageKeys order matches the on-screen order of the images so the
// caller can render placeholders that line up with the downloaded
// attachment slice.
//
// interactive cards: emit the raw card JSON (capped). It's not pretty
// but LLMs read it fine — the P2P path has shipped a value via the
// same fallback in ParseFeishuMessageContent's default case for
// months without complaint; the quote-chain path was the lone hold-out
// silently dropping cards.
func FeishuFetchedMessageText(msgType, bodyContent string) (text string, imageKeys []string) {
	msgType = strings.ToLower(strings.TrimSpace(msgType))
	bodyContent = strings.TrimSpace(bodyContent)
	if bodyContent == "" {
		return "", nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(bodyContent), &decoded); err != nil {
		// Legacy bare-string text bodies — the inbound parser tolerates
		// this shape, so the quote-chain probe must too.
		if msgType == "text" {
			return bodyContent, nil
		}
		return "", nil
	}
	switch msgType {
	case "text":
		return strings.TrimSpace(jsonMapString(decoded, "text")), nil
	case "post":
		body, meta := parseFeishuPostContent(decoded, map[string]any{})
		return strings.TrimSpace(body), liftImageKeysFromMetadata(meta)
	case "image":
		// Surface the image_key so the caller can download the parent
		// hop's image. Empty text is intentional — there is no readable
		// body, the placeholder is rendered at the manager layer.
		if key := jsonMapString(decoded, "image_key"); key != "" {
			return "", []string{key}
		}
		return "", nil
	case "interactive":
		// Emit the raw card body as-is. Truncation is preferable to a
		// re-marshal: avoids re-ordering keys (LLMs read source order
		// as semantic) and avoids losing schema-version markers we
		// don't model.
		if len(bodyContent) > feishuInteractiveRawFallbackMaxBytes {
			return bodyContent[:feishuInteractiveRawFallbackMaxBytes] + "…[card truncated]", nil
		}
		return bodyContent, nil
	default:
		// merge_forward / share_chat / sticker: the parent body is the
		// literal placeholder string; the manager-layer expander handles
		// merge_forward explicitly, the others have no useful payload.
		return "", nil
	}
}

// liftImageKeysFromMetadata pulls parseFeishuPostContent's image_keys
// out of the side-effect metadata map. parseFeishuPostContent's signature
// is shared with the inbound parser, so we keep it returning metadata
// and lift here rather than fork the parser.
func liftImageKeysFromMetadata(meta map[string]any) []string {
	raw, ok := meta["image_keys"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		out := make([]string, 0, len(v))
		for _, k := range v {
			if k = strings.TrimSpace(k); k != "" {
				out = append(out, k)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, raw := range v {
			if s, ok := raw.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}
