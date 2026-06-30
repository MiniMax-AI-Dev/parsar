// Package discord — interaction-response rendering (PR #5c).
//
// The Gateway runner answers a component/modal interaction by sending an
// InteractionRespond. HandleAction returns the neutral ack as deMessage JSON
// (action.go renderDiscordAck); RenderInteractionUpdate maps those bytes into the
// discordgo response data the runner attaches to an UpdateMessage response,
// reusing the same cardContent decode the outbound Send/Edit path uses so the
// embed/component translation stays in one place. It lives in the adapter (not
// the runner) so all discordgo mapping is owned by this package.
package discord

import (
	"github.com/bwmarrin/discordgo"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// RenderInteractionUpdate maps a HandleAction ack (deMessage JSON, as produced by
// renderDiscordAck) into the discordgo message-update response data the runner
// sends via InteractionRespond with an UpdateMessage type. An empty ack yields a
// nil data (the runner then defers the update — a silent ack); a malformed ack
// surfaces the decode error.
func RenderInteractionUpdate(ack []byte) (*discordgo.InteractionResponseData, error) {
	if len(ack) == 0 {
		return nil, nil
	}
	content, embeds, components, err := cardContent(channel.Card{Payload: ack})
	if err != nil {
		return nil, err
	}
	return &discordgo.InteractionResponseData{
		Content:    content,
		Embeds:     embeds,
		Components: components,
	}, nil
}
