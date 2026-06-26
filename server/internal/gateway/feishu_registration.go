package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	feishuAppRegistrationPath = "/oauth/v1/app/registration"

	defaultFeishuAccountsBaseURL = "https://accounts.feishu.cn"
	defaultLarkAccountsBaseURL   = "https://accounts.larksuite.com"
	defaultFeishuOpenBaseURL     = "https://open.feishu.cn"
	defaultLarkOpenBaseURL       = "https://open.larksuite.com"

	defaultFeishuAppRegistrationExpiresIn = 300
	defaultFeishuAppRegistrationInterval  = 5
	maxFeishuAppRegistrationPollInterval  = 60
)

var (
	ErrFeishuAppRegistrationConfig = errors.New("feishu app registration client misconfigured")
	ErrFeishuAppRegistrationHTTP   = errors.New("feishu app registration http error")
	ErrFeishuAppRegistrationBegin  = errors.New("feishu app registration begin failed")
	ErrFeishuAppRegistrationPoll   = errors.New("feishu app registration poll failed")
)

type FeishuAppRegistrationClient struct {
	accountsBaseFeishu string
	accountsBaseLark   string
	openBaseFeishu     string
	openBaseLark       string
	httpClient         *http.Client
}

type FeishuAppRegistrationClientOptions struct {
	AccountsBaseURL string
	OpenBaseURL     string
	HTTPClient      *http.Client
}

type FeishuAppRegistrationBeginResult struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type FeishuAppRegistrationPollKind string

const (
	FeishuAppRegistrationPollPending FeishuAppRegistrationPollKind = "pending"
	FeishuAppRegistrationPollSuccess FeishuAppRegistrationPollKind = "success"
	FeishuAppRegistrationPollError   FeishuAppRegistrationPollKind = "error"
)

type FeishuAppRegistrationPollResult struct {
	Kind            FeishuAppRegistrationPollKind `json:"kind"`
	NextIntervalSec int                           `json:"next_interval_sec,omitempty"`
	ClientID        string                        `json:"client_id,omitempty"`
	ClientSecret    string                        `json:"client_secret,omitempty"`
	AdminOpenID     string                        `json:"admin_open_id,omitempty"`
	TenantBrand     string                        `json:"tenant_brand,omitempty"`
	Error           string                        `json:"error,omitempty"`
	Description     string                        `json:"description,omitempty"`
}

func NewFeishuAppRegistrationClient(opts FeishuAppRegistrationClientOptions) (*FeishuAppRegistrationClient, error) {
	accountsBase := strings.TrimRight(strings.TrimSpace(opts.AccountsBaseURL), "/")
	if accountsBase == "" {
		accountsBase = defaultFeishuAccountsBaseURL
	}
	openBase := strings.TrimRight(strings.TrimSpace(opts.OpenBaseURL), "/")
	if openBase == "" {
		openBase = defaultFeishuOpenBaseURL
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &FeishuAppRegistrationClient{
		accountsBaseFeishu: accountsBase,
		accountsBaseLark:   defaultLarkAccountsBaseURL,
		openBaseFeishu:     openBase,
		openBaseLark:       defaultLarkOpenBaseURL,
		httpClient:         httpClient,
	}, nil
}

func (c *FeishuAppRegistrationClient) Begin(ctx context.Context) (FeishuAppRegistrationBeginResult, error) {
	if c == nil {
		return FeishuAppRegistrationBeginResult{}, fmt.Errorf("%w: nil client", ErrFeishuAppRegistrationConfig)
	}
	if _, err := c.postForm(ctx, c.accountsBaseFeishu, url.Values{"action": {"init"}}); err != nil {
		return FeishuAppRegistrationBeginResult{}, fmt.Errorf("%w: init: %v", ErrFeishuAppRegistrationBegin, err)
	}
	body, err := c.postForm(ctx, c.accountsBaseFeishu, url.Values{
		"action":            {"begin"},
		"archetype":         {"PersonalAgent"},
		"auth_method":       {"client_secret"},
		"request_user_info": {"open_id tenant_brand"},
	})
	if err != nil {
		return FeishuAppRegistrationBeginResult{}, fmt.Errorf("%w: %v", ErrFeishuAppRegistrationBegin, err)
	}
	if errStr := stringField(body, "error"); errStr != "" {
		return FeishuAppRegistrationBeginResult{}, fmt.Errorf("%w: %s: %s", ErrFeishuAppRegistrationBegin, errStr, stringField(body, "error_description"))
	}
	deviceCode := stringField(body, "device_code")
	userCode := stringField(body, "user_code")
	if deviceCode == "" || userCode == "" {
		return FeishuAppRegistrationBeginResult{}, fmt.Errorf("%w: response missing device_code or user_code", ErrFeishuAppRegistrationBegin)
	}
	verificationURI := stringField(body, "verification_uri")
	verificationComplete := stringField(body, "verification_uri_complete")
	if verificationComplete == "" {
		verificationComplete = c.openBaseFeishu + "/page/cli?user_code=" + url.QueryEscape(userCode)
	}
	return FeishuAppRegistrationBeginResult{
		DeviceCode:              deviceCode,
		UserCode:                userCode,
		VerificationURI:         verificationURI,
		VerificationURIComplete: verificationComplete,
		ExpiresIn:               intField(body, "expires_in", defaultFeishuAppRegistrationExpiresIn),
		Interval:                intField(body, "interval", defaultFeishuAppRegistrationInterval),
	}, nil
}

func (c *FeishuAppRegistrationClient) Poll(ctx context.Context, deviceCode string, currentIntervalSec int, tenantBrand string) (FeishuAppRegistrationPollResult, error) {
	if c == nil {
		return FeishuAppRegistrationPollResult{}, fmt.Errorf("%w: nil client", ErrFeishuAppRegistrationConfig)
	}
	deviceCode = strings.TrimSpace(deviceCode)
	if deviceCode == "" {
		return FeishuAppRegistrationPollResult{}, fmt.Errorf("%w: device_code required", ErrFeishuAppRegistrationPoll)
	}
	if currentIntervalSec <= 0 {
		currentIntervalSec = defaultFeishuAppRegistrationInterval
	}
	base := c.accountsBaseFeishu
	if strings.EqualFold(strings.TrimSpace(tenantBrand), "lark") {
		base = c.accountsBaseLark
	}
	body, err := c.postForm(ctx, base, url.Values{
		"action":      {"poll"},
		"device_code": {deviceCode},
	})
	if err != nil {
		return FeishuAppRegistrationPollResult{}, fmt.Errorf("%w: %v", ErrFeishuAppRegistrationPoll, err)
	}
	if stringField(body, "client_id") != "" {
		userInfo := map[string]any{}
		if raw, ok := body["user_info"].(map[string]any); ok {
			userInfo = raw
		}
		return FeishuAppRegistrationPollResult{
			Kind:         FeishuAppRegistrationPollSuccess,
			ClientID:     stringField(body, "client_id"),
			ClientSecret: stringField(body, "client_secret"),
			AdminOpenID:  stringField(userInfo, "open_id"),
			TenantBrand:  stringField(userInfo, "tenant_brand"),
		}, nil
	}
	errStr := stringField(body, "error")
	switch errStr {
	case "authorization_pending", "":
		return FeishuAppRegistrationPollResult{Kind: FeishuAppRegistrationPollPending, NextIntervalSec: currentIntervalSec}, nil
	case "slow_down":
		return FeishuAppRegistrationPollResult{Kind: FeishuAppRegistrationPollPending, NextIntervalSec: min(currentIntervalSec+5, maxFeishuAppRegistrationPollInterval)}, nil
	case "access_denied":
		return FeishuAppRegistrationPollResult{Kind: FeishuAppRegistrationPollError, Error: "access_denied", Description: "app registration denied by user"}, nil
	case "expired_token", "invalid_grant":
		return FeishuAppRegistrationPollResult{Kind: FeishuAppRegistrationPollError, Error: "expired_token", Description: "device code expired, please try again"}, nil
	default:
		desc := stringField(body, "error_description")
		if desc == "" {
			desc = errStr
		}
		if desc == "" {
			desc = "unknown app registration error"
		}
		return FeishuAppRegistrationPollResult{Kind: FeishuAppRegistrationPollError, Error: coalesceString(errStr, "unknown"), Description: desc}, nil
	}
}

func (c *FeishuAppRegistrationClient) postForm(ctx context.Context, base string, form url.Values) (map[string]any, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return nil, fmt.Errorf("%w: accounts base URL required", ErrFeishuAppRegistrationConfig)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+feishuAppRegistrationPath, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFeishuAppRegistrationHTTP, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrFeishuAppRegistrationHTTP, err)
	}
	var body map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, fmt.Errorf("%w: non-json response http=%d body=%s", ErrFeishuAppRegistrationHTTP, res.StatusCode, truncateForError(raw))
		}
	} else {
		body = map[string]any{}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		errStr := stringField(body, "error")
		desc := stringField(body, "error_description")
		if errStr == "" {
			errStr = truncateForError(raw)
		}
		return nil, fmt.Errorf("%w: http=%d error=%s desc=%s", ErrFeishuAppRegistrationHTTP, res.StatusCode, errStr, desc)
	}
	return body, nil
}

func stringField(obj map[string]any, key string) string {
	v, _ := obj[key].(string)
	return strings.TrimSpace(v)
}

func intField(obj map[string]any, key string, fallback int) int {
	switch v := obj[key].(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	}
	return fallback
}

func coalesceString(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v != "" {
		return v
	}
	return fallback
}
