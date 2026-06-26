package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

var (
	ErrFeishuNon2xx          = errors.New("feishu send returned non-2xx status")
	ErrFeishuInvalidResponse = errors.New("feishu send returned invalid response")
)

type FeishuClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type FeishuClientOptions struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

type FeishuSendResponse struct {
	MessageID string `json:"message_id"`
}

type feishuSendAPIResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		MessageID string `json:"message_id"`
	} `json:"data"`
}

func NewFeishuClient(opts FeishuClientOptions) (*FeishuClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("feishu base URL is required")
	}
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		return nil, errors.New("feishu token is required")
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &FeishuClient{baseURL: baseURL, token: token, httpClient: httpClient}, nil
}

func (c *FeishuClient) SendDelivery(ctx context.Context, delivery Delivery) (FeishuSendResponse, error) {
	payload := FeishuDeliveryPayload(delivery)
	requestBody, err := json.Marshal(payload)
	if err != nil {
		return FeishuSendResponse{}, err
	}
	endpoint := c.baseURL + "/open-apis/im/v1/messages?receive_id_type=" + url.QueryEscape(payload.ReceiveIDType)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return FeishuSendResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return FeishuSendResponse{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return FeishuSendResponse{}, fmt.Errorf("%w: status=%d", ErrFeishuNon2xx, res.StatusCode)
	}
	var apiResponse feishuSendAPIResponse
	if err := json.NewDecoder(res.Body).Decode(&apiResponse); err != nil {
		return FeishuSendResponse{}, fmt.Errorf("%w: %v", ErrFeishuInvalidResponse, err)
	}
	if apiResponse.Code != 0 {
		return FeishuSendResponse{}, fmt.Errorf("%w: code=%d msg=%s", ErrFeishuInvalidResponse, apiResponse.Code, apiResponse.Msg)
	}
	messageID := strings.TrimSpace(apiResponse.Data.MessageID)
	if messageID == "" {
		return FeishuSendResponse{}, fmt.Errorf("%w: missing message_id", ErrFeishuInvalidResponse)
	}
	return FeishuSendResponse{MessageID: messageID}, nil
}
