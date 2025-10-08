package s3

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
)

const (
	amzDateFormat    = "20060102T150405Z"
	shortDateFormat  = "20060102"
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

type S3 struct {
	cfg        clientConfig
	httpClient *http.Client
	initErr    error
}

type clientConfig struct {
	region         string
	endpoint       *url.URL
	credentials    *credentials.Credentials
	useTLS         bool
	forcePathStyle bool
	maxRetries     int
	httpClient     *http.Client
}

type ListObjectsV2Input struct {
	Bucket            *string
	Prefix            *string
	ContinuationToken *string
}

type ListObjectsV2Output struct {
	Contents              []*Object
	IsTruncated           *bool
	NextContinuationToken *string
}

type Object struct {
	Key          *string
	LastModified *time.Time
}

type DeleteObjectInput struct {
	Bucket *string
	Key    *string
}

type DeleteObjectOutput struct{}

type PutObjectInput struct {
	Bucket        *string
	Key           *string
	Body          io.ReadSeeker
	ContentLength *int64
	StorageClass  *string
}

type PutObjectOutput struct{}

type listBucketResult struct {
	XMLName               xml.Name       `xml:"ListBucketResult"`
	IsTruncated           string         `xml:"IsTruncated"`
	NextContinuationToken string         `xml:"NextContinuationToken"`
	Contents              []bucketObject `xml:"Contents"`
}

type bucketObject struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
}

type requestError struct {
	statusCode int
	message    string
}

func (e *requestError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("s3 request failed with status %d", e.statusCode)
	}
	return fmt.Sprintf("s3 request failed with status %d: %s", e.statusCode, e.message)
}

func (e *requestError) retryable() bool {
	if e.statusCode == http.StatusTooManyRequests || e.statusCode == http.StatusRequestTimeout {
		return true
	}
	return e.statusCode >= http.StatusInternalServerError && e.statusCode != http.StatusNotImplemented && e.statusCode != http.StatusHTTPVersionNotSupported
}

func New(sess *session.Session, cfgs ...*aws.Config) *S3 {
	base := sess.Config
	if len(cfgs) > 0 && cfgs[0] != nil {
		base = cfgs[0]
	}
	clientCfg, err := buildClientConfig(base)
	httpClient := clientCfg.httpClient
	if httpClient == nil {
		httpClient = defaultHTTPClient(clientCfg)
	}
	return &S3{cfg: clientCfg, httpClient: httpClient, initErr: err}
}

func buildClientConfig(cfg *aws.Config) (clientConfig, error) {
	result := clientConfig{}
	if cfg == nil {
		return result, fmt.Errorf("missing aws config")
	}
	region := aws.StringValue(cfg.Region)
	if region == "" {
		return result, fmt.Errorf("s3 region is required")
	}
	useTLS := true
	if cfg.DisableSSL != nil && *cfg.DisableSSL {
		useTLS = false
	}
	endpoint := aws.StringValue(cfg.Endpoint)
	if endpoint == "" {
		endpoint = defaultEndpoint(region, useTLS)
	} else {
		endpoint = ensureEndpointScheme(endpoint, useTLS)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return result, fmt.Errorf("parse s3 endpoint: %w", err)
	}
	if parsed.Host == "" {
		return result, fmt.Errorf("s3 endpoint must include host")
	}
	result.region = region
	result.endpoint = parsed
	result.credentials = cfg.Credentials
	result.useTLS = useTLS
	if cfg.S3ForcePathStyle != nil {
		result.forcePathStyle = *cfg.S3ForcePathStyle
	}
	if cfg.MaxRetries != nil {
		result.maxRetries = *cfg.MaxRetries
	}
	result.httpClient = cfg.HTTPClient
	return result, nil
}

func defaultHTTPClient(cfg clientConfig) *http.Client {
	transport := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		ForceAttemptHTTP2: false,
	}
	if cfg.useTLS {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	return &http.Client{Transport: transport}
}

func (s *S3) PutObjectWithContext(ctx context.Context, input *PutObjectInput) (*PutObjectOutput, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("put object input is required")
	}
	bucket := aws.StringValue(input.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("bucket is required")
	}
	key := aws.StringValue(input.Key)
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}
	body := input.Body
	var provider func() (io.ReadSeeker, int64, error)
	if body != nil {
		contentLength := int64(0)
		if input.ContentLength != nil {
			contentLength = *input.ContentLength
		} else {
			current, err := body.Seek(0, io.SeekCurrent)
			if err != nil {
				return nil, err
			}
			end, err := body.Seek(0, io.SeekEnd)
			if err != nil {
				return nil, err
			}
			if _, err := body.Seek(current, io.SeekStart); err != nil {
				return nil, err
			}
			contentLength = end
		}
		provider = func() (io.ReadSeeker, int64, error) {
			if _, err := body.Seek(0, io.SeekStart); err != nil {
				return nil, 0, err
			}
			return body, contentLength, nil
		}
	}
	headers := map[string]string{}
	if input.StorageClass != nil && *input.StorageClass != "" {
		headers["x-amz-storage-class"] = *input.StorageClass
	}
	resp, err := s.execute(ctx, bucket, "PUT", key, nil, provider, headers)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		resp.Body.Close()
	}
	return &PutObjectOutput{}, nil
}

func (s *S3) ListObjectsV2PagesWithContext(ctx context.Context, input *ListObjectsV2Input, fn func(*ListObjectsV2Output, bool) bool) error {
	if err := s.checkReady(); err != nil {
		return err
	}
	if input == nil {
		return fmt.Errorf("list input is required")
	}
	bucket := aws.StringValue(input.Bucket)
	if bucket == "" {
		return fmt.Errorf("bucket is required")
	}
	continuation := aws.StringValue(input.ContinuationToken)
	for {
		values := url.Values{}
		values.Set("list-type", "2")
		if prefix := aws.StringValue(input.Prefix); prefix != "" {
			values.Set("prefix", prefix)
		}
		if continuation != "" {
			values.Set("continuation-token", continuation)
		}
		resp, err := s.execute(ctx, bucket, "GET", "", values, nil, nil)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read s3 list response: %w", err)
		}
		var result listBucketResult
		if err := xml.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse s3 list response: %w", err)
		}
		page := &ListObjectsV2Output{}
		for _, object := range result.Contents {
			obj := &Object{}
			key := object.Key
			obj.Key = aws.String(key)
			if t, err := parseS3Time(object.LastModified); err == nil {
				obj.LastModified = &t
			}
			page.Contents = append(page.Contents, obj)
		}
		truncated := strings.EqualFold(strings.TrimSpace(result.IsTruncated), "true")
		page.IsTruncated = aws.Bool(truncated)
		token := strings.TrimSpace(result.NextContinuationToken)
		if token != "" {
			page.NextContinuationToken = aws.String(token)
		}
		if !fn(page, !truncated) {
			return nil
		}
		if !truncated {
			return nil
		}
		continuation = token
		if continuation == "" {
			return nil
		}
	}
}

func (s *S3) DeleteObjectWithContext(ctx context.Context, input *DeleteObjectInput) (*DeleteObjectOutput, error) {
	if err := s.checkReady(); err != nil {
		return nil, err
	}
	if input == nil {
		return nil, fmt.Errorf("delete input is required")
	}
	bucket := aws.StringValue(input.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("bucket is required")
	}
	key := aws.StringValue(input.Key)
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}
	resp, err := s.execute(ctx, bucket, "DELETE", key, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		resp.Body.Close()
	}
	return &DeleteObjectOutput{}, nil
}

func (s *S3) checkReady() error {
	if s.initErr != nil {
		return s.initErr
	}
	if s.cfg.credentials == nil {
		return fmt.Errorf("s3 credentials are required")
	}
	return nil
}

func (s *S3) execute(ctx context.Context, bucket, method, key string, query url.Values, provider func() (io.ReadSeeker, int64, error), headers map[string]string) (*http.Response, error) {
	attempts := s.cfg.maxRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		var body io.ReadSeeker
		var length int64
		var err error
		if provider != nil {
			body, length, err = provider()
			if err != nil {
				return nil, err
			}
		}
		req, payloadHash, err := s.buildRequest(bucket, method, key, query, body, length, headers)
		if err != nil {
			return nil, err
		}
		req = req.WithContext(ctx)
		resp, err := s.do(req, payloadHash)
		if body != nil {
			if seeker, ok := body.(io.ReadSeeker); ok {
				seeker.Seek(0, io.SeekStart)
			}
		}
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == attempts {
			break
		}
		time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
	}
	return nil, lastErr
}

func (s *S3) buildRequest(bucket, method, key string, query url.Values, body io.ReadSeeker, length int64, headers map[string]string) (*http.Request, string, error) {
	targetURL := s.buildURL(bucket, key, query)
	var payloadHash string
	var req *http.Request
	var err error

	if body != nil {
		if _, err := body.Seek(0, io.SeekStart); err != nil {
			return nil, "", err
		}
		hash, err := hashReader(body)
		if err != nil {
			return nil, "", err
		}
		if _, err := body.Seek(0, io.SeekStart); err != nil {
			return nil, "", err
		}
		req, err = http.NewRequest(method, targetURL.String(), body)
		if err != nil {
			return nil, "", err
		}
		req.ContentLength = length
		payloadHash = hash
	} else {
		req, err = http.NewRequest(method, targetURL.String(), nil)
		if err != nil {
			return nil, "", err
		}
		payloadHash = emptyPayloadHash
	}

	req.Host = req.URL.Host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, payloadHash, nil
}

func (s *S3) do(req *http.Request, payloadHash string) (*http.Response, error) {
	now := time.Now().UTC()
	amzDate := now.Format(amzDateFormat)
	scopeDate := now.Format(shortDateFormat)

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if s.cfg.credentials.SessionToken != "" {
		req.Header.Set("x-amz-security-token", s.cfg.credentials.SessionToken)
	}

	canonicalHeaders, signedHeaders := canonicalizeHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL.Query()),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	hashedCanonical := hashString(canonicalRequest)
	scope := fmt.Sprintf("%s/%s/s3/aws4_request", scopeDate, s.cfg.region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hashedCanonical,
	}, "\n")

	signingKey := deriveSigningKey(s.cfg.credentials.SecretAccessKey, scopeDate, s.cfg.region, "s3")
	signature := hmacHex(signingKey, stringToSign)

	credential := fmt.Sprintf("%s/%s", s.cfg.credentials.AccessKeyID, scope)
	authorization := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s, SignedHeaders=%s, Signature=%s", credential, signedHeaders, signature)
	req.Header.Set("Authorization", authorization)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}

	defer resp.Body.Close()
	message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return nil, &requestError{statusCode: resp.StatusCode, message: strings.TrimSpace(string(message))}
}

func (s *S3) buildURL(bucket, key string, query url.Values) *url.URL {
	base := *s.cfg.endpoint
	base.RawQuery = ""

	pathSegments := []string{}
	basePath := strings.Trim(base.Path, "/")
	if basePath != "" {
		pathSegments = append(pathSegments, basePath)
	}

	if s.cfg.forcePathStyle {
		pathSegments = append(pathSegments, bucket)
		if key != "" {
			pathSegments = append(pathSegments, key)
		}
	} else {
		if key != "" {
			pathSegments = append(pathSegments, key)
		}
		base.Host = hostWithBucket(base.Host, bucket)
	}

	if len(pathSegments) > 0 {
		base.Path = "/" + strings.TrimLeft(path.Join(pathSegments...), "/")
	} else {
		base.Path = "/"
	}

	if query != nil {
		base.RawQuery = query.Encode()
	}

	return &base
}

func ensureEndpointScheme(endpoint string, useTLS bool) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return trimmed
	}

	if u, err := url.Parse(trimmed); err == nil && u.Scheme != "" {
		return trimmed
	}

	scheme := "https"
	if !useTLS {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s", scheme, trimmed)
}

func defaultEndpoint(region string, useTLS bool) string {
	scheme := "https"
	if !useTLS {
		scheme = "http"
	}

	host := fmt.Sprintf("s3.%s.amazonaws.com", region)
	if region == "us-east-1" {
		host = "s3.amazonaws.com"
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

func canonicalURI(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func canonicalQuery(values url.Values) string {
	if len(values) == 0 {
		return ""
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var pairs []string
	for _, key := range keys {
		vals := values[key]
		sort.Strings(vals)
		for _, value := range vals {
			pairs = append(pairs, escapeQuery(key)+"="+escapeQuery(value))
		}
	}
	return strings.Join(pairs, "&")
}

func escapeQuery(value string) string {
	escaped := url.QueryEscape(value)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	escaped = strings.ReplaceAll(escaped, "*", "%2A")
	escaped = strings.ReplaceAll(escaped, "%7E", "~")
	return escaped
}

func canonicalizeHeaders(req *http.Request) (string, string) {
	headers := map[string][]string{}
	for key, values := range req.Header {
		lower := strings.ToLower(key)
		copied := make([]string, len(values))
		for i, value := range values {
			cleaned := strings.Join(strings.Fields(value), " ")
			copied[i] = strings.TrimSpace(cleaned)
		}
		sort.Strings(copied)
		headers[lower] = copied
	}

	host := strings.ToLower(req.Host)
	headers["host"] = []string{host}

	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	signed := make([]string, 0, len(keys))
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteString(":")
		builder.WriteString(strings.Join(headers[key], ","))
		builder.WriteString("\n")
		signed = append(signed, key)
	}

	return builder.String(), strings.Join(signed, ";")
}

func hashReader(reader io.Reader) (string, error) {
	hasher := sha256.New()
	if _, err := io.Copy(hasher, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func hashString(value string) string {
	hasher := sha256.New()
	hasher.Write([]byte(value))
	return hex.EncodeToString(hasher.Sum(nil))
}

func deriveSigningKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func hmacHex(key []byte, data string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

func hostWithBucket(baseHost, bucket string) string {
	if strings.HasPrefix(strings.ToLower(baseHost), strings.ToLower(bucket)+".") {
		return baseHost
	}
	return bucket + "." + baseHost
}

func parseS3Time(value string) (time.Time, error) {
	layouts := []string{time.RFC3339, "2006-01-02T15:04:05.000Z", "2006-01-02T15:04:05Z"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format: %s", value)
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	var reqErr *requestError
	if errors.As(err, &reqErr) {
		return reqErr.retryable()
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}

	return false
}
