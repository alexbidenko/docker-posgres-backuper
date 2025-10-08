package storage

import (
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
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

type S3Config struct {
	Bucket          string
	Prefix          string
	Region          string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	UseTLS          bool
	ForcePathStyle  bool
	StorageClass    string
	MaxRetries      int
}

type S3 struct {
	bucket     string
	prefix     string
	cfg        S3Config
	baseURL    *url.URL
	httpClient *http.Client
	name       string
}

const (
	amzDateFormat    = "20060102T150405Z"
	shortDateFormat  = "20060102"
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

type readSeekCloser interface {
	io.ReadSeeker
	io.Closer
}

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

func NewS3(cfg S3Config) (*S3, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3 bucket is required")
	}
	if cfg.Region == "" {
		return nil, fmt.Errorf("s3 region is required")
	}
	if cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("s3 access key id and secret access key are required")
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint(cfg.Region, cfg.UseTLS)
	} else {
		endpoint = ensureEndpointScheme(endpoint, cfg.UseTLS)
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse s3 endpoint: %w", err)
	}

	if parsed.Host == "" {
		return nil, fmt.Errorf("s3 endpoint must include host")
	}

	transport := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		ForceAttemptHTTP2: false,
	}
	if parsed.Scheme == "https" {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)

	client := &http.Client{Transport: transport}

	return &S3{
		bucket:     cfg.Bucket,
		prefix:     normalizePrefix(cfg.Prefix),
		cfg:        cfg,
		baseURL:    parsed,
		httpClient: client,
		name:       fmt.Sprintf("s3:%s", cfg.Bucket),
	}, nil
}

func (s *S3) Name() string {
	return s.name
}

func (s *S3) Prepare(_ []string) error {
	return nil
}

func (s *S3) Store(database, filename, sourcePath string) error {
	key := s.objectKey(database, filename)
	provider := func() (readSeekCloser, int64, error) {
		file, err := os.Open(sourcePath)
		if err != nil {
			return nil, 0, err
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, 0, err
		}
		return file, info.Size(), nil
	}

	headers := map[string]string{}
	if s.cfg.StorageClass != "" {
		headers["x-amz-storage-class"] = s.cfg.StorageClass
	}

	resp, err := s.execute("PUT", key, nil, provider, headers)
	if err != nil {
		return err
	}
	if resp != nil {
		resp.Body.Close()
	}
	return nil
}

func (s *S3) Cleanup(database string, now time.Time) error {
	prefix := s.objectKey(database, "")
	trimPrefix := prefix
	if trimPrefix != "" && !strings.HasSuffix(trimPrefix, "/") {
		trimPrefix += "/"
	}

	continuation := ""
	for {
		values := url.Values{}
		values.Set("list-type", "2")
		if prefix != "" {
			values.Set("prefix", prefix)
		}
		if continuation != "" {
			values.Set("continuation-token", continuation)
		}

		resp, err := s.execute("GET", "", values, nil, nil)
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

		for _, object := range result.Contents {
			key := object.Key
			name := key
			if trimPrefix != "" {
				name = strings.TrimPrefix(key, trimPrefix)
			}
			if name == "" {
				continue
			}

			backupType, ok := parseBackupType(name)
			if !ok {
				continue
			}

			modTime, err := parseS3Time(object.LastModified)
			if err != nil {
				continue
			}

			if shouldRemove(backupType, modTime, now) {
				if err := s.deleteObject(key); err != nil {
					return err
				}
			}
		}

		if !strings.EqualFold(strings.TrimSpace(result.IsTruncated), "true") {
			break
		}
		continuation = strings.TrimSpace(result.NextContinuationToken)
		if continuation == "" {
			break
		}
	}

	return nil
}

func (s *S3) deleteObject(key string) error {
	resp, err := s.execute("DELETE", key, nil, nil, nil)
	if err != nil {
		return err
	}
	if resp != nil {
		resp.Body.Close()
	}
	return nil
}

func (s *S3) execute(method, key string, query url.Values, provider func() (readSeekCloser, int64, error), headers map[string]string) (*http.Response, error) {
	attempts := s.cfg.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		var body readSeekCloser
		var length int64
		var err error
		if provider != nil {
			body, length, err = provider()
			if err != nil {
				return nil, err
			}
		}

		req, payloadHash, err := s.buildRequest(method, key, query, body, length, headers)
		if err != nil {
			if body != nil {
				body.Close()
			}
			return nil, err
		}

		resp, err := s.do(req, payloadHash)
		if body != nil {
			body.Close()
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

func (s *S3) buildRequest(method, key string, query url.Values, body readSeekCloser, length int64, headers map[string]string) (*http.Request, string, error) {
	targetURL := s.buildURL(key, query)

	var payloadHash string
	var req *http.Request
	var err error

	if body != nil {
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
	if s.cfg.SessionToken != "" {
		req.Header.Set("x-amz-security-token", s.cfg.SessionToken)
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
	scope := fmt.Sprintf("%s/%s/s3/aws4_request", scopeDate, s.cfg.Region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hashedCanonical,
	}, "\n")

	signingKey := deriveSigningKey(s.cfg.SecretAccessKey, scopeDate, s.cfg.Region, "s3")
	signature := hmacHex(signingKey, stringToSign)

	credential := fmt.Sprintf("%s/%s", s.cfg.AccessKeyID, scope)
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

func (s *S3) buildURL(key string, query url.Values) *url.URL {
	base := *s.baseURL
	base.RawQuery = ""

	pathSegments := []string{}
	basePath := strings.Trim(base.Path, "/")
	if basePath != "" {
		pathSegments = append(pathSegments, basePath)
	}

	if s.cfg.ForcePathStyle {
		pathSegments = append(pathSegments, s.bucket)
		if key != "" {
			pathSegments = append(pathSegments, key)
		}
	} else {
		if key != "" {
			pathSegments = append(pathSegments, key)
		}
		base.Host = hostWithBucket(base.Host, s.bucket)
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

func (s *S3) objectKey(database, filename string) string {
	parts := []string{}
	if s.prefix != "" {
		parts = append(parts, s.prefix)
	}
	parts = append(parts, database)
	if filename != "" {
		parts = append(parts, filename)
	}
	return path.Join(parts...)
}

func normalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	prefix = strings.Trim(prefix, "/")
	return prefix
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
