package channel

import "context"

// DirectoryAdapter is an optional sub-interface for listing bots / groups /
// members. A channel implements it only if the platform exposes a directory;
// the driver probes with a type assertion and degrades when absent. WeCom /
// DingTalk may implement only ListBots.
type DirectoryAdapter interface {
	ListBots(ctx context.Context) ([]BotAccount, error)
	ListGroups(ctx context.Context, botID string) ([]Group, error)
	ListMembers(ctx context.Context, botID, groupID string) ([]Member, error)
}

// BotAccount is a platform bot/app account.
type BotAccount struct {
	ID   string
	Name string
}

// Group is a chat group / channel the bot belongs to.
type Group struct {
	ID   string
	Name string
}

// Member is a member of a group.
type Member struct {
	ID   string
	Name string
}
