package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "devgateway failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: devgateway <send-inbound|drain-outbound>")
	}
	switch args[0] {
	case "send-inbound":
		return sendInbound(ctx, args[1:])
	case "drain-outbound":
		return drainOutbound(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func sendInbound(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("send-inbound", flag.ContinueOnError)
	apiURL := fs.String("api-url", defaultAPIURL(), "Parsar API base URL")
	gatewayName := fs.String("gateway", "dev", "gateway name")
	messageID := fs.String("message-id", fmt.Sprintf("dev-msg-%d", time.Now().UnixNano()), "external message id")
	text := fs.String("text", "", "message text")
	actorID := fs.String("actor-id", "dev-user", "external actor id")
	actorEmail := fs.String("actor-email", "admin@example.com", "actor email fallback")
	chatID := fs.String("chat-id", "", "external chat id")
	threadID := fs.String("thread-id", "", "external thread id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*text) == "" {
		return errors.New("--text is required")
	}
	if strings.TrimSpace(*chatID) == "" {
		return errors.New("--chat-id is required")
	}
	payload := map[string]any{
		"gateway":          *gatewayName,
		"message":          gateway.MessageRef{ID: *messageID, Text: *text},
		"actor":            gateway.ActorRef{ID: *actorID, Email: *actorEmail},
		"conversation_ref": gateway.ConversationRef{ID: *chatID, ThreadID: *threadID},
	}
	return postJSON(ctx, *apiURL+"/dev/gateway/inbound", payload, os.Stdout)
}

func drainOutbound(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("drain-outbound", flag.ContinueOnError)
	apiURL := fs.String("api-url", defaultAPIURL(), "Parsar API base URL")
	gatewayName := fs.String("gateway", "dev", "gateway name")
	limit := fs.Int("limit", 100, "maximum deliveries to fetch")
	ack := fs.Bool("ack", true, "mark successfully processed deliveries as delivered")
	mode := fs.String("mode", "log", "outbound delivery mode: log or feishu")
	feishuBaseURL := fs.String("feishu-base-url", defaultFeishuBaseURL(), "Feishu API base URL")
	feishuToken := fs.String("feishu-token", defaultFeishuToken(), "Feishu tenant access token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/dev/gateway/outbound?gateway=%s&limit=%d", strings.TrimRight(*apiURL, "/"), *gatewayName, *limit)
	var body struct {
		Gateway    string           `json:"gateway"`
		Deliveries []map[string]any `json:"deliveries"`
		Messages   []map[string]any `json:"messages"`
	}
	if err := getJSON(ctx, endpoint, &body); err != nil {
		return err
	}
	deliveries, err := decodeDeliveries(body.Deliveries)
	if err != nil {
		return err
	}
	modeValue := strings.TrimSpace(*mode)
	switch modeValue {
	case "", "log":
		if *ack {
			for _, delivery := range deliveries {
				deliveryID := fmt.Sprintf("dev-delivery-%s", delivery.MessageID)
				if err := ackDelivery(ctx, strings.TrimRight(*apiURL, "/"), delivery.MessageID, deliveryID); err != nil {
					return err
				}
			}
		}
	case "feishu":
		client, err := gateway.NewFeishuClient(gateway.FeishuClientOptions{BaseURL: *feishuBaseURL, Token: *feishuToken})
		if err != nil {
			return err
		}
		for _, delivery := range deliveries {
			response, err := client.SendDelivery(ctx, delivery)
			if err != nil {
				return fmt.Errorf("send delivery %s to feishu: %w", delivery.MessageID, err)
			}
			if *ack {
				if err := ackDelivery(ctx, strings.TrimRight(*apiURL, "/"), delivery.MessageID, response.MessageID); err != nil {
					return err
				}
			}
		}
	default:
		return fmt.Errorf("unsupported drain-outbound mode: %s", modeValue)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func defaultAPIURL() string {
	if value := strings.TrimSpace(os.Getenv("PARSAR_API_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return "http://127.0.0.1:8080"
}

func defaultFeishuBaseURL() string {
	if value := strings.TrimSpace(os.Getenv("PARSAR_FEISHU_BASE_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	return ""
}

func defaultFeishuToken() string {
	return strings.TrimSpace(os.Getenv("PARSAR_FEISHU_TOKEN"))
}

func decodeDeliveries(raw []map[string]any) ([]gateway.Delivery, error) {
	deliveries := make([]gateway.Delivery, 0, len(raw))
	for _, item := range raw {
		body, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		var delivery gateway.Delivery
		if err := json.Unmarshal(body, &delivery); err != nil {
			return nil, err
		}
		if strings.TrimSpace(delivery.MessageID) == "" {
			continue
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, nil
}

func ackDelivery(ctx context.Context, apiURL string, messageID string, deliveryID string) error {
	return postJSON(ctx, fmt.Sprintf("%s/dev/gateway/outbound/%s/delivered", apiURL, messageID), map[string]any{"delivery_id": deliveryID}, io.Discard)
}

func postJSON(ctx context.Context, url string, payload any, output io.Writer) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("POST %s failed: %d %s", url, res.StatusCode, strings.TrimSpace(string(body)))
	}
	if output != nil {
		_, err = io.Copy(output, res.Body)
		return err
	}
	return nil
}

func getJSON(ctx context.Context, url string, output any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("GET %s failed: %d %s", url, res.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(res.Body).Decode(output)
}
