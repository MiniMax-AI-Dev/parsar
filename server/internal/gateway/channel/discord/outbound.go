// Package discord — outbound transport (PR #5b).
//
// Reply / Send / Edit turn neutral channel calls into Discord REST calls
// (ChannelMessageSendComplex / ChannelMessageEditComplex) via discordgo. To keep
// the transport unit-testable without the HTTP client, the adapter talks to a
// small discordSender interface expressed in explicit arguments (channel,
// content, embeds, components) rather than discordgo session calls; clientSender
// adapts *discordgo.Session to it, and tests inject a fake.
//
// The bot token is resolved per call through the CredentialResolver so a
// re-installed bot rotates without a restart — mirroring the Slack/Feishu
// adapters, which keep exactly one interactive-send path (Send) to avoid the
// double-send class of bug. A Discord thread is itself a channel, so Send/Edit
// post into the thread channel when the target carries one (postChannel); Edit
// pins (channel, message id) since Discord identifies a message by that pair.
//
// The renderers (embed.go) emit our own deMessage JSON; cardContent decodes it
// and MAPS it to discordgo's typed embed/component structs here (rather than
// leaning on discordgo's MessageComponent interface unmarshaling), so the wire
// shape stays fully under this package's control.
package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// discordSender is the minimal outbound surface the adapter needs, in explicit
// arguments so it is testable without discordgo's HTTP client. clientSender
// adapts *discordgo.Session; tests pass a fake. content/embeds/components are
// the decoded card; post returns the created message's snowflake id.
type discordSender interface {
	post(ctx context.Context, channelID, content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent) (msgID string, err error)
	update(ctx context.Context, channelID, msgID, content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent) error
}

// clientSender adapts *discordgo.Session to discordSender, translating explicit
// arguments into discordgo's Complex send/edit payloads.
type clientSender struct{ api *discordgo.Session }

func (s clientSender) post(ctx context.Context, channelID, content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent) (string, error) {
	msg, err := s.api.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:    content,
		Embeds:     embeds,
		Components: components,
	}, discordgo.WithContext(ctx))
	if err != nil {
		return "", err
	}
	return msg.ID, nil
}

func (s clientSender) update(ctx context.Context, channelID, msgID, content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent) error {
	edit := &discordgo.MessageEdit{
		ID:         msgID,
		Channel:    channelID,
		Content:    &content,
		Embeds:     &embeds,
		Components: &components,
	}
	_, err := s.api.ChannelMessageEditComplex(edit, discordgo.WithContext(ctx))
	return err
}

// defaultSenderFactory builds a real discordgo-backed sender from a bot token.
// discordgo wants the "Bot " prefix on the Authorization header; New adds it
// when the token lacks one, but we pass the bare token and let discordgo's
// "Bot "-prefix convention apply.
func defaultSenderFactory(token string) discordSender {
	// discordgo.New never errors for a static bot token (it only parses the
	// token shape), so the error is intentionally discarded; an invalid token
	// surfaces at the first REST call as a 401.
	api, _ := discordgo.New("Bot " + token)
	return clientSender{api: api}
}

// senderFor resolves the bot token and builds a sender via the injected
// factory. Per-call resolution keeps token rotation hot. The resolver botID
// is the workspace-bot's app_id when inbound captured one (SourceAppID) —
// the join key into workspace_im_connectors — falling back to the legacy
// TenantKey (Discord guild_id) and finally the channel's static appID. The
// static/env resolver ignores the key and returns its one token.
func (c *Channel) senderFor(ctx context.Context, target channel.ReplyTarget) (discordSender, error) {
	botID := strings.TrimSpace(target.SourceAppID)
	if botID == "" {
		botID = strings.TrimSpace(target.TenantKey)
	}
	if botID == "" {
		botID = c.appID
	}
	cred, err := c.creds.Resolve(ctx, botID)
	if err != nil {
		return nil, err
	}
	return c.newSender(cred.AppSecret), nil
}

// postChannel returns the channel id to post into: always ExternalChatID.
//
// A Discord thread is itself a channel, so a message sent in a thread already
// arrives with channel_id == the thread id — ExternalChatID therefore already
// names the right post target for both top-level and in-thread origins.
// ExternalThreadID is NOT a second post target: the neutral pipeline stores the
// conversation's grouping key (InboundEvent.ThreadKey, which falls back to the
// originating message id for a top-level message) into external_thread_id, and a
// message id is not a postable channel. Honouring it made the inflight worker
// POST result cards to a message-id-as-channel and Discord rejected them with
// "Unknown Channel" (code 10003). Edit reuses this so an edit lands on the same
// channel the send used.
func postChannel(target channel.ReplyTarget) string {
	return strings.TrimSpace(target.ExternalChatID)
}

// cardContent decodes a rendered Discord card into its content fallback and the
// typed discordgo embeds/components. An empty payload yields a content-only
// path (no embeds/components), so a plain ack still posts.
func cardContent(card channel.Card) (content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent, err error) {
	if len(card.Payload) == 0 {
		return "", nil, nil, nil
	}
	var msg deMessage
	if err := json.Unmarshal(card.Payload, &msg); err != nil {
		return "", nil, nil, fmt.Errorf("discord channel: decode card payload: %w", err)
	}
	embeds = toDiscordEmbeds(msg.Embeds)
	components, err = toDiscordComponents(msg.Components)
	if err != nil {
		return "", nil, nil, err
	}
	return msg.Content, embeds, components, nil
}

// toDiscordEmbeds maps our wire embeds to discordgo's typed embeds.
func toDiscordEmbeds(in []deEmbed) []*discordgo.MessageEmbed {
	if len(in) == 0 {
		return nil
	}
	out := make([]*discordgo.MessageEmbed, 0, len(in))
	for _, e := range in {
		em := &discordgo.MessageEmbed{
			Title:       e.Title,
			Description: e.Description,
			Color:       e.Color,
		}
		if e.Footer != nil {
			em.Footer = &discordgo.MessageEmbedFooter{Text: e.Footer.Text}
		}
		for _, f := range e.Fields {
			em.Fields = append(em.Fields, &discordgo.MessageEmbedField{
				Name:   f.Name,
				Value:  f.Value,
				Inline: f.Inline,
			})
		}
		out = append(out, em)
	}
	return out
}

// toDiscordComponents maps our wire action rows (each holding heterogeneous
// buttons / selects decoded as JSON objects) to discordgo's typed components.
// Each row component is re-marshaled and dispatched on its "type" discriminator
// — the same round-trip the golden tests use — so a deMessage decoded from JSON
// (where Components is []any of maps) maps without bespoke unmarshaling.
func toDiscordComponents(rows []deActionRow) ([]discordgo.MessageComponent, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]discordgo.MessageComponent, 0, len(rows))
	for _, row := range rows {
		inner := make([]discordgo.MessageComponent, 0, len(row.Components))
		for _, raw := range row.Components {
			comp, err := toDiscordComponent(raw)
			if err != nil {
				return nil, err
			}
			inner = append(inner, comp)
		}
		out = append(out, discordgo.ActionsRow{Components: inner})
	}
	return out, nil
}

// toDiscordComponent maps one wire component (button or string select) to its
// discordgo type by re-marshaling and peeking the "type" field.
func toDiscordComponent(raw any) (discordgo.MessageComponent, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("discord channel: remarshal component: %w", err)
	}
	var probe struct {
		Type int `json:"type"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, fmt.Errorf("discord channel: peek component type: %w", err)
	}
	switch probe.Type {
	case componentButton:
		var btn deButton
		if err := json.Unmarshal(b, &btn); err != nil {
			return nil, fmt.Errorf("discord channel: decode button: %w", err)
		}
		return discordgo.Button{
			Label:    btn.Label,
			Style:    discordgo.ButtonStyle(btn.Style),
			CustomID: btn.CustomID,
		}, nil
	case componentStringSelect:
		var sel deSelect
		if err := json.Unmarshal(b, &sel); err != nil {
			return nil, fmt.Errorf("discord channel: decode select: %w", err)
		}
		opts := make([]discordgo.SelectMenuOption, 0, len(sel.Options))
		for _, o := range sel.Options {
			opts = append(opts, discordgo.SelectMenuOption{Label: o.Label, Value: o.Value})
		}
		minValues := sel.MinValues
		return discordgo.SelectMenu{
			MenuType:    discordgo.StringSelectMenu,
			CustomID:    sel.CustomID,
			Placeholder: sel.Placeholder,
			MinValues:   &minValues,
			MaxValues:   sel.MaxValues,
			Options:     opts,
		}, nil
	default:
		return nil, fmt.Errorf("discord channel: unknown component type %d", probe.Type)
	}
}

// Reply posts a plain-text command acknowledgement into the target channel
// (the thread channel when the target carries one).
func (c *Channel) Reply(ctx context.Context, target channel.ReplyTarget, text string) error {
	sender, err := c.senderFor(ctx, target)
	if err != nil {
		return err
	}
	_, err = sender.post(ctx, postChannel(target), text, nil, nil)
	return err
}

// Send posts a new card and returns its message snowflake as the MessageRef id.
// This is the adapter's single interactive-send path.
func (c *Channel) Send(ctx context.Context, target channel.ReplyTarget, card channel.Card) (gateway.MessageRef, error) {
	sender, err := c.senderFor(ctx, target)
	if err != nil {
		return gateway.MessageRef{}, err
	}
	content, embeds, components, err := cardContent(card)
	if err != nil {
		return gateway.MessageRef{}, err
	}
	id, err := sender.post(ctx, postChannel(target), content, embeds, components)
	if err != nil {
		return gateway.MessageRef{}, err
	}
	return gateway.MessageRef{ID: id, Text: content}, nil
}

// Edit re-renders an existing message's embeds in place
// (ChannelMessageEditComplex) — the inflight "executing" → terminal transition
// pins one (channel, message id). The channel comes from postChannel(target);
// the message id from the MessageRef.
func (c *Channel) Edit(ctx context.Context, target channel.ReplyTarget, ref gateway.MessageRef, card channel.Card) error {
	sender, err := c.senderFor(ctx, target)
	if err != nil {
		return err
	}
	msgID := strings.TrimSpace(ref.ID)
	if msgID == "" {
		return fmt.Errorf("discord channel: edit requires a message id")
	}
	channelID := postChannel(target)
	if channelID == "" {
		return fmt.Errorf("discord channel: edit requires a channel id")
	}
	content, embeds, components, err := cardContent(card)
	if err != nil {
		return err
	}
	return sender.update(ctx, channelID, msgID, content, embeds, components)
}
