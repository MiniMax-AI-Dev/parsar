package v1

type TokenUsage struct {
	InputTokens         int64              `json:"input_tokens" binding:"required"`
	InputTokensDetails  InputTokenDetails  `json:"input_tokens_details" binding:"required"`
	OutputTokens        int64              `json:"output_tokens" binding:"required"`
	OutputTokensDetails OutputTokenDetails `json:"output_tokens_details" binding:"required"`
	TotalTokens         int64              `json:"total_tokens" binding:"required"`
}
type InputTokenDetails struct {
	CachedTokens int64 `json:"cached_tokens" binding:"required"`
}
type OutputTokenDetails struct {
	ReasoningTokens int64 `json:"reasoning_tokens" binding:"required"`
}
