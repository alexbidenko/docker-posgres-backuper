package s3client

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Config struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
	UseTLS          bool
	Timeout         time.Duration
}

type Client struct {
	httpClient      *http.Client
	endpoint        *url.URL
	region          string
	accessKeyID     string
	secretAccessKey string
	forcePathStyle  bool
}

type ListObject struct {
	Key          string
	LastModified time.Time
}

type ListObjectsV2Output struct {
	Objects               []ListObject
	IsTruncated           bool
	NextContinuationToken string
}

func New(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("endpoint is required")
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("region is required")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("access key credentials are required")
	}
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	if endpoint.Scheme == "" {
		if cfg.UseTLS {
			endpoint.Scheme = "https"
		} else {
			endpoint.Scheme = "http"
		}
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		httpClient:      &http.Client{Timeout: timeout},
		endpoint:        endpoint,
		region:          cfg.Region,
		accessKeyID:     cfg.AccessKeyID,
		secretAccessKey: cfg.SecretAccessKey,
		forcePathStyle:  cfg.ForcePathStyle,
	}, nil
}

func (c *Client) PutObject(ctx context.Context, bucket, key string, body io.ReadSeeker) error {
	if body == nil {
		return fmt.Errorf("body is required")
	}
	payloadHash, length, err := hashAndLength(body)
	if err != nil {
		return err
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodPut, bucket, key, nil, payloadHash, body)
	if err != nil {
		return err
	}
	req.ContentLength = length
	req.Header.Set("Content-Length", fmt.Sprintf("%d", length))
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return httpError(resp)
	}
	return nil
}

func (c *Client) GetObject(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	req, err := c.newRequest(ctx, http.MethodGet, bucket, key, nil, emptyHash(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, httpError(resp)
	}
	return resp.Body, nil
}

func (c *Client) DeleteObject(ctx context.Context, bucket, key string) error {
	req, err := c.newRequest(ctx, http.MethodDelete, bucket, key, nil, emptyHash(), nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return httpError(resp)
	}
	return nil
}

func (c *Client) ListObjectsV2(ctx context.Context, bucket, prefix, continuationToken string) (ListObjectsV2Output, error) {
	query := url.Values{}
	query.Set("list-type", "2")
	if prefix != "" {
		query.Set("prefix", prefix)
	}
	if continuationToken != "" {
		query.Set("continuation-token", continuationToken)
	}
	req, err := c.newRequest(ctx, http.MethodGet, bucket, "", query, emptyHash(), nil)
	if err != nil {
		return ListObjectsV2Output{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ListObjectsV2Output{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ListObjectsV2Output{}, httpError(resp)
	}
	var result listBucketResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ListObjectsV2Output{}, fmt.Errorf("decode list response: %w", err)
	}
	objects := make([]ListObject, 0, len(result.Contents))
	for _, item := range result.Contents {
		t, err := time.Parse(time.RFC3339, item.LastModified)
		if err != nil {
			t = time.Time{}
		}
		objects = append(objects, ListObject{Key: item.Key, LastModified: t})
	}
	return ListObjectsV2Output{
		Objects:               objects,
		IsTruncated:           result.IsTruncated == "true",
		NextContinuationToken: result.NextContinuationToken,
	}, nil
}

type listBucketResult struct {
	XMLName               xml.Name      `xml:"ListBucketResult"`
	Contents              []objectEntry `xml:"Contents"`
	IsTruncated           string        `xml:"IsTruncated"`
	NextContinuationToken string        `xml:"NextContinuationToken"`
}

type objectEntry struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
}

func (c *Client) newRequest(ctx context.Context, method, bucket, key string, query url.Values, payloadHash string, body io.Reader) (*http.Request, error) {
	endpoint := *c.endpoint
	host := endpoint.Host
	canonicalKey := strings.TrimPrefix(key, "/")
	path := "/"
	if c.forcePathStyle {
		path += bucket
		if canonicalKey != "" {
			path += "/" + canonicalKey
		}
	} else {
		if bucket == "" {
			return nil, fmt.Errorf("bucket is required")
		}
		host = hostWithBucket(bucket, host)
		if canonicalKey != "" {
			path += canonicalKey
		}
	}
	canonicalURI := buildCanonicalPath(c.forcePathStyle, bucket, canonicalKey)
	unescapedPath, err := url.PathUnescape(canonicalURI)
	if err != nil {
		return nil, fmt.Errorf("unescape path: %w", err)
	}
	endpoint.Host = host
	endpoint.Path = unescapedPath
	endpoint.RawPath = canonicalURI
	if query != nil {
		endpoint.RawQuery = buildCanonicalQuery(query)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	req.Host = host
	timestamp := time.Now().UTC()
	amzDate := timestamp.Format("20060102T150405Z")
	dateStamp := timestamp.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	canonicalHeaders := buildCanonicalHeaders(host, payloadHash, amzDate)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, c.region)
	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI,
		canonicalQueryString(query),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		hex.EncodeToString(sha256Sum([]byte(canonicalRequest))),
	}, "\n")
	signingKey := c.signingKey(dateStamp)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	authorization := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", c.accessKeyID, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authorization)
	return req, nil
}

func buildCanonicalHeaders(host, payloadHash, amzDate string) string {
	return fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n", strings.ToLower(host), payloadHash, amzDate)
}

func canonicalQueryString(query url.Values) string {
	if query == nil {
		return ""
	}
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		values := append([]string(nil), query[key]...)
		sort.Strings(values)
		for _, value := range values {
			parts = append(parts, uriEncode(key, true)+"="+uriEncode(value, true))
		}
	}
	return strings.Join(parts, "&")
}

func buildCanonicalQuery(query url.Values) string {
	if query == nil {
		return ""
	}
	// Canonical query string is already encoded
	canonical := canonicalQueryString(query)
	return canonical
}

func buildCanonicalPath(forcePathStyle bool, bucket, key string) string {
	var builder strings.Builder
	builder.WriteString("/")
	if forcePathStyle {
		builder.WriteString(uriEncode(bucket, false))
		if key != "" {
			builder.WriteString("/")
		}
	}
	if key != "" {
		segments := strings.Split(key, "/")
		for i, segment := range segments {
			builder.WriteString(uriEncode(segment, false))
			if i < len(segments)-1 {
				builder.WriteString("/")
			}
		}
	}
	return builder.String()
}

func uriEncode(input string, encodeSlash bool) string {
	var buf strings.Builder
	for i := 0; i < len(input); i++ {
		b := input[i]
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-' || b == '_' || b == '.' || b == '~' {
			buf.WriteByte(b)
		} else if b == '/' && !encodeSlash {
			buf.WriteByte('/')
		} else {
			buf.WriteString(fmt.Sprintf("%%%02X", b))
		}
	}
	return buf.String()
}

func hashAndLength(body io.ReadSeeker) (string, int64, error) {
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	n, err := io.Copy(hash, body)
	if err != nil {
		return "", 0, err
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), n, nil
}

func emptyHash() string {
	h := sha256.Sum256([]byte{})
	return hex.EncodeToString(h[:])
}

func sha256Sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func hmacSHA256(key []byte, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func (c *Client) signingKey(date string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+c.secretAccessKey), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(c.region))
	kService := hmacSHA256(kRegion, []byte("s3"))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hostWithBucket(bucket, endpointHost string) string {
	if strings.Contains(endpointHost, ":") {
		parts := strings.Split(endpointHost, ":")
		return fmt.Sprintf("%s.%s:%s", bucket, parts[0], parts[1])
	}
	return fmt.Sprintf("%s.%s", bucket, endpointHost)
}

func httpError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("s3 request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(bytes.TrimSpace(data))))
}
