// Package clickhousehttp provides the canonical HTTP transport, connection pooling,
// bounded gzip response handling, and BasicAuth request helpers for ClickHouse :8123 clients.
package clickhousehttp

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const defaultDrainBytes = int64(64 << 10)

// Credentials holds ClickHouse HTTP BasicAuth credentials.
type Credentials struct {
	Username string
	Password string
}

// TransportConfig parameterizes a pooled ClickHouse HTTP client.
type TransportConfig struct {
	Timeout               time.Duration
	DialTimeout           time.Duration
	MaxConnsPerHost       int
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
	IdleConnTimeout       time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	ExpectContinueTimeout time.Duration
	DisableRedirects      bool
}

// ServiceTransportConfig returns the pooled transport profile shared by analytics services.
func ServiceTransportConfig(timeout time.Duration) TransportConfig {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return TransportConfig{
		Timeout:               timeout,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		DisableRedirects:      true,
	}
}

// DataPlaneTransportConfig returns the pooled transport profile shared by telemetry/energy/metric stores.
func DataPlaneTransportConfig(timeout time.Duration) TransportConfig {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return TransportConfig{
		Timeout:               timeout,
		DialTimeout:           5 * time.Second,
		MaxConnsPerHost:       16,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// NewPooledHTTPClient constructs a pooled *http.Client using the given TransportConfig.
func NewPooledHTTPClient(cfg TransportConfig) *http.Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	if cfg.DialTimeout > 0 {
		transport.DialContext = (&net.Dialer{
			Timeout:   cfg.DialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext
	}
	if cfg.MaxConnsPerHost > 0 {
		transport.MaxConnsPerHost = cfg.MaxConnsPerHost
	}
	if cfg.MaxIdleConns > 0 {
		transport.MaxIdleConns = cfg.MaxIdleConns
	} else {
		transport.MaxIdleConns = 64
	}
	if cfg.MaxIdleConnsPerHost > 0 {
		transport.MaxIdleConnsPerHost = cfg.MaxIdleConnsPerHost
	} else {
		transport.MaxIdleConnsPerHost = 32
	}
	if cfg.IdleConnTimeout > 0 {
		transport.IdleConnTimeout = cfg.IdleConnTimeout
	} else {
		transport.IdleConnTimeout = 90 * time.Second
	}
	if cfg.TLSHandshakeTimeout > 0 {
		transport.TLSHandshakeTimeout = cfg.TLSHandshakeTimeout
	} else {
		transport.TLSHandshakeTimeout = 10 * time.Second
	}
	if cfg.ResponseHeaderTimeout > 0 {
		transport.ResponseHeaderTimeout = cfg.ResponseHeaderTimeout
	}
	if cfg.ExpectContinueTimeout > 0 {
		transport.ExpectContinueTimeout = cfg.ExpectContinueTimeout
	} else {
		transport.ExpectContinueTimeout = time.Second
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
	if cfg.DisableRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return client
}

// ParseOriginURL parses a strict ClickHouse HTTP(S) origin without user info, path, query, or fragment.
func ParseOriginURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("ClickHouse URL must be an HTTP(S) origin")
	}
	return parsed, nil
}

// ParseBaseURL parses a ClickHouse HTTP(S) base URL with host and optional userinfo restriction.
func ParseBaseURL(raw string, forbidUser bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("ClickHouse URL must be absolute http/https")
	}
	if forbidUser && parsed.User != nil {
		return nil, errors.New("ClickHouse URL must not embed user info")
	}
	return parsed, nil
}

// DrainAndClose drains up to 64 KiB of body so keep-alive connections can be reused, then closes it.
func DrainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, defaultDrainBytes))
	_ = body.Close()
}

// StatusError reports a non-success HTTP status returned by ClickHouse.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("ClickHouse returned status %d: %s", e.StatusCode, e.TrimmedBody(512))
}

// TrimmedBody returns the trimmed response snippet capped to maxLen bytes (0 = 512).
func (e *StatusError) TrimmedBody(maxLen int) string {
	if e == nil {
		return ""
	}
	if maxLen <= 0 {
		maxLen = 512
	}
	msg := strings.TrimSpace(e.Body)
	if len(msg) > maxLen {
		return msg[:maxLen]
	}
	return msg
}

// ResponseTooLargeError reports that a ClickHouse HTTP response exceeded MaxResponseBytes.
type ResponseTooLargeError struct {
	LimitBytes int64
}

func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("ClickHouse response exceeds %d bytes", e.LimitBytes)
}

// IsSelectQuery reports whether sql begins with SELECT or WITH (case-insensitive).
func IsSelectQuery(sql string) bool {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	return strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "WITH")
}

// QueryRequest describes a bounded ClickHouse HTTP query execution.
type QueryRequest struct {
	Method           string
	SQL              string
	Params           url.Values
	Accept           string
	CompressResponse bool
	MaxResponseBytes int64
	// TruncateAtLimit reads up to MaxResponseBytes without returning ResponseTooLargeError when exact-limit cap is desired.
	TruncateAtLimit bool
	// RequireStatusOK rejects non-200 2xx statuses when true; otherwise accepts any 2xx status.
	RequireStatusOK bool
	PrepareHeader   func(context.Context, http.Header)
}

// StreamRequest describes a streaming POST ClickHouse HTTP query.
type StreamRequest struct {
	SQL             string
	Params          url.Values
	Accept          string
	MaxErrorBytes   int64
	RequireStatusOK bool
	PrepareHeader   func(context.Context, http.Header)
}

// InsertRequest describes an HTTP JSONEachRow (optionally gzipped) ClickHouse INSERT request.
type InsertRequest struct {
	Params         url.Values
	Body           []byte
	CompressedGzip bool
	MaxErrorBytes  int64
	PrepareHeader  func(context.Context, http.Header)
}

// Client is the canonical ClickHouse HTTP client shared across Go backends.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	creds      Credentials
}

// NewClient constructs a ClickHouse HTTP client for a validated endpoint and pooled HTTP client.
func NewClient(baseURL *url.URL, httpClient *http.Client, creds Credentials) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: httpClient,
		creds:      creds,
	}
}

// BaseURL returns a copy of the configured endpoint URL, or nil if uninitialized.
func (c *Client) BaseURL() *url.URL {
	if c == nil || c.baseURL == nil {
		return nil
	}
	copyURL := *c.baseURL
	return &copyURL
}

// HTTPClient returns the underlying *http.Client.
func (c *Client) HTTPClient() *http.Client {
	if c == nil {
		return nil
	}
	return c.httpClient
}

// WithCredentials clones the client with overridden BasicAuth credentials while sharing the HTTP pool.
func (c *Client) WithCredentials(username, password string) *Client {
	if c == nil {
		return nil
	}
	return &Client{
		baseURL:    c.baseURL,
		httpClient: c.httpClient,
		creds: Credentials{
			Username: username,
			Password: password,
		},
	}
}

// CloseIdleConnections releases idle keep-alive connections held by the underlying transport.
func (c *Client) CloseIdleConnections() {
	if c != nil && c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
}

func (c *Client) initialized() error {
	if c == nil || c.baseURL == nil || c.httpClient == nil {
		return errors.New("ClickHouse HTTP client is not initialized")
	}
	return nil
}

// Ping verifies ClickHouse connectivity via a lightweight GET SELECT 1 query.
func (c *Client) Ping(ctx context.Context, compress bool) error {
	if err := c.initialized(); err != nil {
		return err
	}
	_, err := c.Query(ctx, QueryRequest{
		Method:           http.MethodGet,
		SQL:              "SELECT 1",
		CompressResponse: compress,
		MaxResponseBytes: 8 << 10,
		TruncateAtLimit:  true,
	})
	return err
}

// Query executes a ClickHouse SQL query over HTTP GET or POST, handling gzip decompression,
// response-size bounds, and clean keep-alive connection draining.
func (c *Client) Query(ctx context.Context, req QueryRequest) ([]byte, error) {
	if err := c.initialized(); err != nil {
		return nil, err
	}
	method := req.Method
	if method == "" {
		method = http.MethodPost
	}

	endpoint := *c.baseURL
	queryParams := url.Values{}
	for key, values := range endpoint.Query() {
		queryParams[key] = append([]string(nil), values...)
	}
	for key, values := range req.Params {
		queryParams[key] = append([]string(nil), values...)
	}
	var bodyReader io.Reader
	if method == http.MethodGet {
		queryParams.Set("query", req.SQL)
	} else {
		bodyReader = strings.NewReader(req.SQL)
	}
	if req.CompressResponse {
		queryParams.Set("enable_http_compression", "1")
	}
	if len(queryParams) > 0 {
		endpoint.RawQuery = queryParams.Encode()
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bodyReader)
	if err != nil {
		return nil, err
	}
	if method != http.MethodGet {
		httpReq.Header.Set("Content-Type", "text/plain; charset=utf-8")
	}
	if req.Accept != "" {
		httpReq.Header.Set("Accept", req.Accept)
	}
	if req.CompressResponse {
		httpReq.Header.Set("Accept-Encoding", "gzip")
	}
	if c.creds.Username != "" {
		httpReq.SetBasicAuth(c.creds.Username, c.creds.Password)
	}
	if req.PrepareHeader != nil {
		req.PrepareHeader(ctx, httpReq.Header)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { DrainAndClose(resp.Body) }()

	maxBytes := req.MaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = 8 << 20
	}
	limit := maxBytes
	if !req.TruncateAtLimit {
		limit = maxBytes + 1
	}

	payload, tooLarge, err := readResponseBody(resp, limit, maxBytes, !req.TruncateAtLimit)
	if err != nil {
		return nil, err
	}
	if !statusAcceptable(resp.StatusCode, req.RequireStatusOK) {
		return nil, &StatusError{StatusCode: resp.StatusCode, Body: string(payload)}
	}
	if tooLarge {
		return nil, &ResponseTooLargeError{LimitBytes: maxBytes}
	}
	return payload, nil
}

// OpenStream executes a POST query with HTTP gzip compression enabled and returns a streaming
// io.ReadCloser that transparently decodes gzip and drains/closes the underlying HTTP body on Close.
func (c *Client) OpenStream(ctx context.Context, req StreamRequest) (io.ReadCloser, error) {
	if err := c.initialized(); err != nil {
		return nil, err
	}
	endpoint := *c.baseURL
	queryParams := url.Values{}
	for key, values := range endpoint.Query() {
		queryParams[key] = append([]string(nil), values...)
	}
	for key, values := range req.Params {
		queryParams[key] = append([]string(nil), values...)
	}
	queryParams.Set("enable_http_compression", "1")
	endpoint.RawQuery = queryParams.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(req.SQL))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if req.Accept != "" {
		httpReq.Header.Set("Accept", req.Accept)
	} else {
		httpReq.Header.Set("Accept", "application/x-ndjson")
	}
	httpReq.Header.Set("Accept-Encoding", "gzip")
	if c.creds.Username != "" {
		httpReq.SetBasicAuth(c.creds.Username, c.creds.Password)
	}
	if req.PrepareHeader != nil {
		req.PrepareHeader(ctx, httpReq.Header)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if !statusAcceptable(resp.StatusCode, req.RequireStatusOK) {
		maxErr := req.MaxErrorBytes
		if maxErr <= 0 {
			maxErr = 8 << 10
		}
		payload, _, _ := readResponseBody(resp, maxErr, maxErr, false)
		DrainAndClose(resp.Body)
		return nil, &StatusError{StatusCode: resp.StatusCode, Body: string(payload)}
	}
	if strings.EqualFold(strings.TrimSpace(resp.Header.Get("Content-Encoding")), "gzip") {
		gz, gzErr := gzip.NewReader(resp.Body)
		if gzErr != nil {
			DrainAndClose(resp.Body)
			return nil, fmt.Errorf("decode ClickHouse gzip stream: %w", gzErr)
		}
		return &gzipStreamReadCloser{gz: gz, body: resp.Body}, nil
	}
	return resp.Body, nil
}

// Insert posts an NDJSON batch (plain or gzip-compressed) to ClickHouse with query settings.
func (c *Client) Insert(ctx context.Context, req InsertRequest) error {
	if err := c.initialized(); err != nil {
		return err
	}
	endpoint := *c.baseURL
	if len(req.Params) > 0 {
		queryParams := url.Values{}
		for key, values := range endpoint.Query() {
			queryParams[key] = append([]string(nil), values...)
		}
		for key, values := range req.Params {
			queryParams[key] = append([]string(nil), values...)
		}
		endpoint.RawQuery = queryParams.Encode()
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(req.Body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/x-ndjson")
	if req.CompressedGzip {
		httpReq.Header.Set("Content-Encoding", "gzip")
	}
	if c.creds.Username != "" {
		httpReq.SetBasicAuth(c.creds.Username, c.creds.Password)
	}
	if req.PrepareHeader != nil {
		req.PrepareHeader(ctx, httpReq.Header)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer func() { DrainAndClose(resp.Body) }()

	maxErr := req.MaxErrorBytes
	if maxErr <= 0 {
		maxErr = 8 << 10
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		payload, _, _ := readResponseBody(resp, maxErr, maxErr, false)
		return &StatusError{StatusCode: resp.StatusCode, Body: string(payload)}
	}
	return nil
}

func statusAcceptable(code int, requireOK bool) bool {
	if requireOK {
		return code == http.StatusOK
	}
	return code >= http.StatusOK && code < http.StatusMultipleChoices
}

func readResponseBody(resp *http.Response, limit, maxBytes int64, checkTooLarge bool) ([]byte, bool, error) {
	reader := io.Reader(io.LimitReader(resp.Body, limit))
	if strings.EqualFold(strings.TrimSpace(resp.Header.Get("Content-Encoding")), "gzip") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("decode ClickHouse gzip response: %w", err)
		}
		defer func() { _ = gz.Close() }()
		reader = io.LimitReader(gz, limit)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		return nil, false, err
	}
	if checkTooLarge && int64(len(payload)) > maxBytes {
		return payload, true, nil
	}
	return payload, false, nil
}

type gzipStreamReadCloser struct {
	gz   *gzip.Reader
	body io.ReadCloser
}

func (g *gzipStreamReadCloser) Read(p []byte) (int, error) {
	return g.gz.Read(p)
}

func (g *gzipStreamReadCloser) Close() error {
	gzErr := g.gz.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(g.body, 4096))
	bodyErr := g.body.Close()
	return errors.Join(gzErr, bodyErr)
}

var gzipWriterPool = sync.Pool{
	New: func() any {
		writer, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return writer
	},
}

// CompressGzipBestSpeed compresses raw using a pooled gzip.BestSpeed writer.
func CompressGzipBestSpeed(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer, _ := gzipWriterPool.Get().(*gzip.Writer)
	if writer == nil {
		writer, _ = gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	}
	defer func() {
		writer.Reset(io.Discard)
		gzipWriterPool.Put(writer)
	}()
	writer.Reset(&buf)
	if _, err := writer.Write(raw); err != nil {
		return nil, fmt.Errorf("compress ClickHouse batch: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finalize compressed ClickHouse batch: %w", err)
	}
	return buf.Bytes(), nil
}

// SHA256Hex returns the lowercase hexadecimal SHA-256 digest of raw.
func SHA256Hex(raw []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
