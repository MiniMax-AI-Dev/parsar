package imhistory

import (
	"strconv"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// keySep is an ASCII unit separator: it cannot appear in a platform id, chat
// id, thread id, or cursor, so joined key fields never collide across
// boundaries (e.g. chat "a" + thread "bc" vs chat "ab" + thread "c").
const keySep = "\x1f"

// chatKey scopes serialization to one (platform, chat). Thread and cursor are
// deliberately excluded: the underlying platform list call is per-chat, so all
// requests for a chat must queue together to avoid a burst.
func chatKey(platform channel.Platform, req channel.FetchHistoryRequest) string {
	return string(platform) + keySep + strings.TrimSpace(req.ExternalChatID)
}

// cacheKey identifies an exact repeat request. Thread, limit, and cursor are
// part of the identity because each yields a different page.
func cacheKey(platform channel.Platform, req channel.FetchHistoryRequest) string {
	return strings.Join([]string{
		string(platform),
		strings.TrimSpace(req.ExternalChatID),
		strings.TrimSpace(req.ExternalThreadID),
		strconv.Itoa(req.Limit),
		strings.TrimSpace(req.Cursor),
	}, keySep)
}
