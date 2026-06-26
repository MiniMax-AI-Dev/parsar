package oss

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	aliyun "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

// DefaultPresignTTL is the default validity for a presigned URL.
// Aliyun V4 rejects expirations > 7 days; we cap well below that.
const DefaultPresignTTL = time.Hour

// MaxPresignTTL caps presign duration at 24h. Anything longer
// enlarges the leaked-URL blast radius without a legitimate use.
const MaxPresignTTL = 24 * time.Hour

// Client wraps the Aliyun OSS SDK with the narrow surface parsar
// uses: presigned PUT, presigned GET, and in-process download.
// Safe for concurrent use.
//
// A nil *Client is the documented "OSS not configured" sentinel.
// Callers MUST check `if c == nil` before invoking methods.
type Client struct {
	sdk    *aliyun.Client
	bucket string
	region string

	// baseURL is the operator-supplied public-facing URL. Only
	// used by ObjectURL() — the SDK builds presigned URLs from
	// the internal endpoint, which may differ in VPC deployments.
	baseURL string
}

// New constructs a Client from cfg. Network reachability is NOT
// checked here (SDK is lazy); HealthCheck does a startup HeadBucket
// so credential failures surface clearly without blocking boot.
func New(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	provider := credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.AccessKeySecret)
	sdkCfg := aliyun.LoadDefaultConfig().
		WithRegion(cfg.Region).
		WithCredentialsProvider(provider)
	if endpoint := strings.TrimSpace(cfg.Endpoint); endpoint != "" {
		sdkCfg = sdkCfg.WithEndpoint(endpoint)
	}

	sdk := aliyun.NewClient(sdkCfg)
	return &Client{
		sdk:     sdk,
		bucket:  strings.TrimSpace(cfg.Bucket),
		region:  strings.TrimSpace(cfg.Region),
		baseURL: strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
	}, nil
}

// Bucket returns the bucket name this client is bound to.
func (c *Client) Bucket() string {
	if c == nil {
		return ""
	}
	return c.bucket
}

func (c *Client) Region() string {
	if c == nil {
		return ""
	}
	return c.region
}

// HealthCheck performs a HeadBucket to confirm credentials reach
// the bucket. cmd/server calls this at startup with a short timeout
// and degrades gracefully — OSS being down should not block the
// rest of parsar.
func (c *Client) HealthCheck(ctx context.Context) error {
	if c == nil {
		return ErrNotConfigured
	}
	_, err := c.sdk.GetBucketInfo(ctx, &aliyun.GetBucketInfoRequest{Bucket: aliyun.Ptr(c.bucket)})
	if err != nil {
		return fmt.Errorf("oss: head bucket %q: %w", c.bucket, err)
	}
	return nil
}

// PresignPutContentType is the Content-Type signed into every
// presigned PUT URL. Browsers issuing fetch(url, {body: file})
// auto-fill the request Content-Type from File.type, so the URL
// must be signed with the SAME literal value the browser sends or
// Aliyun's V4 signer rejects with SignatureDoesNotMatch
// (Content-Type is in the default signed-headers set; see
// signer/v4.go isDefaultSignedHeader). We pin to
// application/octet-stream and require the frontend to override
// fetch's auto-detection by sending this header verbatim.
const PresignPutContentType = "application/octet-stream"

// PresignPut returns a presigned URL the caller can use to upload
// via HTTP PUT. ttl is clamped to [1m, MaxPresignTTL]; ttl<=0
// picks DefaultPresignTTL.
//
// The URL is unauthenticated within its TTL window — callers MUST
// treat it as a short-lived capability and avoid logging it. The
// uploader MUST send Content-Type=PresignPutContentType verbatim
// (see the constant docstring).
func (c *Client) PresignPut(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error) {
	if c == nil {
		return "", time.Time{}, ErrNotConfigured
	}
	if strings.TrimSpace(key) == "" {
		return "", time.Time{}, ErrInvalidKey
	}
	ttl = normalizeTTL(ttl)
	req := &aliyun.PutObjectRequest{
		Bucket:      aliyun.Ptr(c.bucket),
		Key:         aliyun.Ptr(key),
		ContentType: aliyun.Ptr(PresignPutContentType),
	}
	res, err := c.sdk.Presign(ctx, req, aliyun.PresignExpires(ttl))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("oss: presign put %q: %w", key, err)
	}
	return res.URL, res.Expiration, nil
}

// PresignGet returns a presigned URL for HTTP GET. Same TTL
// contract as PresignPut.
func (c *Client) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error) {
	if c == nil {
		return "", time.Time{}, ErrNotConfigured
	}
	if strings.TrimSpace(key) == "" {
		return "", time.Time{}, ErrInvalidKey
	}
	ttl = normalizeTTL(ttl)
	req := &aliyun.GetObjectRequest{
		Bucket: aliyun.Ptr(c.bucket),
		Key:    aliyun.Ptr(key),
	}
	res, err := c.sdk.Presign(ctx, req, aliyun.PresignExpires(ttl))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("oss: presign get %q: %w", key, err)
	}
	return res.URL, res.Expiration, nil
}

// Download streams the object identified by key into memory. The
// MaxDownloadBytes cap protects against OOM on a giant blob.
func (c *Client) Download(ctx context.Context, key string) ([]byte, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(key) == "" {
		return nil, ErrInvalidKey
	}
	req := &aliyun.GetObjectRequest{
		Bucket: aliyun.Ptr(c.bucket),
		Key:    aliyun.Ptr(key),
	}
	out, err := c.sdk.GetObject(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("oss: get %q: %w", key, err)
	}
	defer out.Body.Close()

	limited := io.LimitReader(out.Body, MaxDownloadBytes+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("oss: read body %q: %w", key, err)
	}
	if int64(len(buf)) > MaxDownloadBytes {
		return nil, fmt.Errorf("%w: object %q exceeds %d bytes", ErrObjectTooLarge, key, MaxDownloadBytes)
	}
	return buf, nil
}

// ObjectURL returns the canonical public-style URL for an object.
// NOT a presigned URL — the bucket is private and a GET will be
// denied. Stored on capability_version rows for human debugging.
// Falls back to a region-derived URL when BaseURL is unset.
func (c *Client) ObjectURL(key string) string {
	if c == nil {
		return ""
	}
	key = strings.TrimLeft(strings.TrimSpace(key), "/")
	if key == "" {
		return ""
	}
	if c.baseURL != "" {
		return c.baseURL + "/" + key
	}
	return fmt.Sprintf("https://%s.oss-%s.aliyuncs.com/%s", c.bucket, c.region, key)
}

// MaxDownloadBytes caps the in-memory download size at 64 MiB.
// Operators needing bigger objects should switch to streaming reads
// rather than bumping this constant.
const MaxDownloadBytes int64 = 64 * 1024 * 1024

// ErrNotConfigured is returned when a Client method is called on a
// nil receiver (the documented "OSS not enabled" mode).
var ErrNotConfigured = errors.New("oss: client not configured")

var ErrInvalidKey = errors.New("oss: object key must be non-empty")

// ErrObjectTooLarge is returned when Download() would buffer more
// than MaxDownloadBytes.
var ErrObjectTooLarge = errors.New("oss: object exceeds in-memory download cap")

// normalizeTTL clamps caller TTLs to [1m, MaxPresignTTL]; ttl<=0
// picks DefaultPresignTTL. Sub-minute is almost certainly a bug —
// the URL needs to survive at least one round trip.
func normalizeTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return DefaultPresignTTL
	}
	if ttl < time.Minute {
		return time.Minute
	}
	if ttl > MaxPresignTTL {
		return MaxPresignTTL
	}
	return ttl
}
