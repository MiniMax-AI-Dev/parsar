// Package inflight — production binding of the neutral Feishu channel
// adapter's outbound Transport seam (PR #3b.1).
//
// The channel/feishu adapter (PR #3a) keeps its Reply/Edit/Send wrappers thin
// and injects the heavy machinery — the FeishuTenantClient pool, the
// tenant_access_token cache, and per-bot app_secret resolution — via a
// Transport interface. This file wires that interface to the Worker's existing
// resolveCredentials + clientFor, so the adapter reuses the worker's
// rotation-safe token cache instead of duplicating it.
//
// 3b.1 is additive plumbing: it constructs the binding but does NOT yet route
// the inflight driver through the Channel (that lands in 3b.2). Nothing on the
// production path consumes this transport until then, so behavior is unchanged.
package inflight

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	feishuchannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/feishu"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// workerTransport adapts a *Worker to the channel/feishu Transport seam. It is
// per-worker (not per-bot): the adapter passes the bot's app_id on each call,
// and the worker resolves credentials + a cached tenant client from it.
type workerTransport struct {
	w *Worker
}

// Compile-time assertion that workerTransport satisfies the adapter seam.
var _ feishuchannel.Transport = workerTransport{}

// OutboundTransport returns a Transport bound to this worker, suitable for
// feishu.WithTransport. The returned value resolves credentials and a cached
// tenant client per call so vault rotation stays hot.
func (w *Worker) OutboundTransport() feishuchannel.Transport {
	return workerTransport{w: w}
}

// feishuChannel builds a per-bot neutral Channel handle bound to this worker's
// outbound transport. Cheap to construct per conversation: the Channel is a
// thin value and the transport reuses the worker's cached tenant clients, so
// the driver can mint one per (conversation, app_id) without a pool.
func (w *Worker) feishuChannel(appID string) *feishuchannel.Channel {
	return feishuchannel.New(feishuchannel.Config{AppID: appID}, feishuchannel.WithTransport(w.OutboundTransport()))
}

// channelFor resolves the neutral outbound Channel for a conversation's
// platform. Feishu is built per-call (transport-injected token cache, keyed by
// the conversation's source app_id); every other platform is looked up in the
// registry populated at NewWorker. The bool is false when no channel is
// registered — the neutral driver treats that as a defensive skip (it should
// never happen, since claim only returns registered platforms).
func (w *Worker) channelFor(platform, sourceAppID string) (channel.Channel, bool) {
	if platform == "" || platform == string(channel.PlatformFeishu) {
		return w.feishuChannel(sourceAppID), true
	}
	ch, ok := w.channels[channel.Platform(platform)]
	return ch, ok
}

// HistoryFetcher resolves the live channel.HistoryFetcher for a conversation's
// platform + bound bot, satisfying the internal history endpoint's resolver
// seam. It reuses channelFor (Feishu built per-call from the transport, others
// from the registry) and type-asserts the adapter to the optional
// HistoryFetcher capability. found is false when the platform has no adapter or
// its adapter cannot fetch history, so the endpoint degrades to an empty page
// rather than failing.
func (w *Worker) HistoryFetcher(_ context.Context, ref store.ConversationIMRef) (channel.HistoryFetcher, channel.Platform, bool) {
	ch, ok := w.channelFor(ref.Platform, ref.SourceAppID)
	if !ok || ch == nil {
		return nil, "", false
	}
	hf, ok := ch.(channel.HistoryFetcher)
	if !ok {
		return nil, "", false
	}
	return hf, ch.Platform(), true
}

// OutboundSender resolves the per-bot credentials and a cached tenant client
// for botID (the Feishu app_id). resolveCredentials derives the owning
// workspace from the agent route internally, so botID alone is sufficient;
// the tenant client is keyed app-scoped here (empty workspace) because a
// Feishu app_id maps to exactly one agent route, hence one workspace.
func (t workerTransport) OutboundSender(ctx context.Context, botID string) (feishuchannel.CardSender, gateway.OutboundCredentials, error) {
	creds, err := t.w.resolveCredentials(ctx, gateway.PendingOutboundMessage{SourceAppID: botID})
	if err != nil {
		return nil, gateway.OutboundCredentials{}, err
	}
	client, err := t.w.clientFor("", creds.AppID)
	if err != nil {
		return nil, gateway.OutboundCredentials{}, err
	}
	return client, creds, nil
}
