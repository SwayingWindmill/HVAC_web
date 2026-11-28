package telemetry

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const (
	maxHistoryBatchRows  = 32768
	maxHistoryBatchBytes = 32 << 20
)

// ErrHistoryBatchPermanent marks a deterministic validation or schema error that retries cannot fix.
var ErrHistoryBatchPermanent = errors.New("ClickHouse history batch rejected permanently")

// ErrClickHouseTooManyParts marks ClickHouse part-count throttling (Code 252) as transient backoff.
var ErrClickHouseTooManyParts = errors.New("ClickHouse history batch throttled due to too many parts")

// IsPermanentHistoryError reports whether err is a permanent ClickHouse history failure.
func IsPermanentHistoryError(err error) bool {
	return errors.Is(err, ErrHistoryBatchPermanent)
}

// IsClickHouseTooManyPartsError reports whether err was caused by ClickHouse TOO_MANY_PARTS throttling.
func IsClickHouseTooManyPartsError(err error) bool {
	return errors.Is(err, ErrClickHouseTooManyParts)
}

func isClickHouseTooManyPartsMessage(message string) bool {
	upper := strings.ToUpper(message)
	return strings.Contains(upper, "TOO_MANY_PARTS") ||
		strings.Contains(upper, "TOO MANY PARTS") ||
		strings.Contains(message, "Code: 252")
}

var (
	clickHouseIdentifierPattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	clickHousePayloadDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type ClickHouseHistoryConfig struct {
	BaseURL    string
	Database   string
	Table      string
	Username   string
	Password   string
	HTTPClient *http.Client
}

type ClickHouseHistorySink struct {
	baseURL    *url.URL
	database   string
	table      string
	username   string
	password   string
	httpClient *http.Client
}

func NewClickHouseHistorySink(config ClickHouseHistoryConfig) (*ClickHouseHistorySink, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("ClickHouse history base URL must be an HTTP(S) origin without user info")
	}
	if !clickHouseIdentifierPattern.MatchString(config.Database) || !clickHouseIdentifierPattern.MatchString(config.Table) {
		return nil, errors.New("ClickHouse history database and table identifiers are invalid")
	}
	client := config.HTTPClient
	if client == nil {
		client = newDefaultClickHouseHistoryHTTPClient(30 * time.Second)
	}
	return &ClickHouseHistorySink{
		baseURL: parsed, database: config.Database, table: config.Table,
		username: config.Username, password: config.Password, httpClient: client,
	}, nil
}

func newDefaultClickHouseHistoryHTTPClient(timeout time.Duration) *http.Client {
	var transport *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		transport = base.Clone()
	} else {
		transport = &http.Transport{}
	}
	transport.DialContext = (&net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.MaxConnsPerHost = 16
	transport.MaxIdleConns = 64
	transport.MaxIdleConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.ExpectContinueTimeout = 1 * time.Second
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func (sink *ClickHouseHistorySink) Ping(ctx context.Context) error {
	if sink == nil || sink.baseURL == nil || sink.httpClient == nil {
		return errors.New("ClickHouse history sink is closed")
	}
	return pingClickHouseHTTP(ctx, sink.baseURL, sink.username, sink.password, sink.httpClient, "ClickHouse history ping")
}

// Close satisfies closer interfaces used alongside the native TCP sink; HTTP transports are stateless here.
func (sink *ClickHouseHistorySink) Close() error {
	return nil
}

func pingClickHouseHTTP(ctx context.Context, baseURL *url.URL, username, password string, client *http.Client, label string) error {
	endpoint := *baseURL
	q := endpoint.Query()
	q.Set("query", "SELECT 1")
	q.Set("enable_http_compression", "1")
	endpoint.RawQuery = q.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create %s request: %w", label, err)
	}
	request.Header.Set("Accept-Encoding", "gzip")
	if username != "" {
		request.SetBasicAuth(username, password)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	body, readErr := readBoundedClickHouseResponse(response, 8<<10, label)
	closeErr := response.Body.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return fmt.Errorf("close %s response: %w", label, closeErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if len(message) > 512 {
			message = message[:512]
		}
		return fmt.Errorf("%s returned %d: %s", label, response.StatusCode, message)
	}
	return nil
}

func readBoundedClickHouseResponse(response *http.Response, maxBytes int64, label string) ([]byte, error) {
	const maxDrainBytes = 4096
	reader := io.Reader(io.LimitReader(response.Body, maxBytes))
	if strings.EqualFold(strings.TrimSpace(response.Header.Get("Content-Encoding")), "gzip") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxDrainBytes))
			return nil, fmt.Errorf("decode %s gzip response: %w", label, err)
		}
		defer gz.Close()
		reader = io.LimitReader(gz, maxBytes)
	}
	body, err := io.ReadAll(reader)
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxDrainBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", label, err)
	}
	return body, nil
}

var historyGzipWriterPool = sync.Pool{
	New: func() any {
		writer, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return writer
	},
}

func compressHistoryGzip(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	writer, _ := historyGzipWriterPool.Get().(*gzip.Writer)
	if writer == nil {
		writer, _ = gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
	}
	defer func() {
		writer.Reset(io.Discard)
		historyGzipWriterPool.Put(writer)
	}()
	writer.Reset(&buf)
	if _, err := writer.Write(raw); err != nil {
		return nil, fmt.Errorf("compress ClickHouse history batch: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finalize compressed ClickHouse history batch: %w", err)
	}
	return buf.Bytes(), nil
}

func prepareOrderedHistoryObservations(sequence uint64, observations []HistoryObservation) ([]HistoryObservation, error) {
	if len(observations) == 0 {
		return nil, nil
	}
	if len(observations) > maxHistoryBatchRows {
		return nil, fmt.Errorf("ClickHouse history insert batch exceeds %d observations", maxHistoryBatchRows)
	}
	if sequence == 0 {
		return nil, errors.New("ClickHouse history insert requires the batch History Sequence")
	}
	// PostgreSQL UPDATE RETURNING has no order guarantee. Retries must produce
	// identical rows, order and block boundaries, including after a worker restart.
	ordered := deduplicateResolvedBatchSamples(slices.Clone(observations))
	slices.SortFunc(ordered, func(a, b HistoryObservation) int { return strings.Compare(a.ObservationID, b.ObservationID) })
	for i := range ordered {
		ordered[i].HistorySequence = sequence
		if err := validateHistoryObservation(ordered[i]); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// PrepareObservations deterministically orders, deduplicates resolved (point_id, sampled_at)
// collisions, validates, encodes, hashes, and gzip-compresses one history batch.
func (sink *ClickHouseHistorySink) PrepareObservations(sequence uint64, observations []HistoryObservation) ([]byte, string, error) {
	if sink == nil {
		return nil, "", errors.New("ClickHouse history sink is closed")
	}
	ordered, err := prepareOrderedHistoryObservations(sequence, observations)
	if err != nil {
		return nil, "", err
	}
	if len(ordered) == 0 {
		return nil, "", nil
	}
	var encoded bytes.Buffer
	for _, observation := range ordered {
		body, err := json.Marshal(observation)
		if err != nil {
			return nil, "", fmt.Errorf("%w: encode ClickHouse history observation %s: %w", ErrHistoryBatchPermanent, observation.ObservationID, err)
		}
		if encoded.Len()+len(body)+1 > maxHistoryBatchBytes {
			return nil, "", fmt.Errorf("%w: ClickHouse history insert exceeds 32 MiB", ErrHistoryBatchPermanent)
		}
		encoded.Write(body)
		encoded.WriteByte('\n')
	}
	raw := encoded.Bytes()
	token := fmt.Sprintf("%x", sha256.Sum256(raw))
	compressed, err := compressHistoryGzip(raw)
	if err != nil {
		return nil, "", err
	}
	return compressed, token, nil
}

type historyPointSampleKey struct {
	pointID       string
	sampledAtUnix int64
}

func deduplicateResolvedBatchSamples(observations []HistoryObservation) []HistoryObservation {
	if len(observations) < 2 {
		return observations
	}
	bestBySample := make(map[historyPointSampleKey]HistoryObservation, len(observations))
	keep := make([]HistoryObservation, 0, len(observations))
	for _, obs := range observations {
		isResolvedSample := obs.PointID != nil && *obs.PointID != "" &&
			(obs.AcceptanceStatus == string(ObservationAccepted) || obs.AcceptanceStatus == string(ObservationOutOfOrder))
		if !isResolvedSample {
			keep = append(keep, obs)
			continue
		}
		key := historyPointSampleKey{
			pointID:       strings.ToLower(strings.TrimSpace(*obs.PointID)),
			sampledAtUnix: obs.SampledAt.UTC().Truncate(time.Millisecond).UnixMilli(),
		}
		existing, seen := bestBySample[key]
		if !seen || historySampleWins(obs, existing) {
			bestBySample[key] = obs
		}
	}
	for _, obs := range bestBySample {
		keep = append(keep, obs)
	}
	return keep
}

func historySampleWins(candidate, current HistoryObservation) bool {
	if candidate.ReceivedAt.After(current.ReceivedAt) {
		return true
	}
	if candidate.ReceivedAt.Before(current.ReceivedAt) {
		return false
	}
	if candidate.SourceOffset != current.SourceOffset {
		return candidate.SourceOffset > current.SourceOffset
	}
	return candidate.ObservationID < current.ObservationID
}

// InsertPrepared uploads a pre-encoded gzipped NDJSON batch produced by PrepareObservations.
func (sink *ClickHouseHistorySink) InsertPrepared(ctx context.Context, compressed []byte, token string) error {
	if sink == nil || sink.baseURL == nil || sink.httpClient == nil {
		return errors.New("ClickHouse history sink is closed")
	}
	if len(compressed) == 0 {
		return nil
	}
	if !clickHousePayloadDigestPattern.MatchString(token) {
		return errors.New("ClickHouse history deduplication token is invalid")
	}
	return sink.insertBatch(ctx, compressed, token)
}

func (sink *ClickHouseHistorySink) InsertObservations(ctx context.Context, sequence uint64, observations []HistoryObservation) error {
	if sink == nil || sink.baseURL == nil || sink.httpClient == nil {
		return errors.New("ClickHouse history sink is closed")
	}
	if len(observations) == 0 {
		return nil
	}
	compressed, token, err := sink.PrepareObservations(sequence, observations)
	if err != nil {
		return err
	}
	return sink.InsertPrepared(ctx, compressed, token)
}

func validateHistoryObservation(observation HistoryObservation) error {
	if !uuidv7.IsValid(observation.ObservationID) {
		return fmt.Errorf("%w: ClickHouse history observation ID must be UUIDv7", ErrHistoryBatchPermanent)
	}
	if !clickHousePayloadDigestPattern.MatchString(observation.PayloadSHA256) {
		return fmt.Errorf("%w: ClickHouse history payload digest must be lowercase SHA-256 hex", ErrHistoryBatchPermanent)
	}
	if observation.AcceptanceStatus != string(ObservationAccepted) &&
		(observation.AcceptanceStatus != string(ObservationOutOfOrder) || observation.PointID == nil) {
		return nil
	}
	if observation.TenantID == nil || observation.SiteID == nil || observation.DeviceID == nil || observation.PointID == nil ||
		!uuidv7.IsValid(*observation.TenantID) || !uuidv7.IsValid(*observation.SiteID) ||
		!uuidv7.IsValid(*observation.DeviceID) || !uuidv7.IsValid(*observation.PointID) ||
		(observation.SensorID != nil && !uuidv7.IsValid(*observation.SensorID)) {
		return fmt.Errorf("%w: resolved ClickHouse history observation requires UUIDv7 Tenant, Site, Device and Point scope", ErrHistoryBatchPermanent)
	}
	if observation.PointType == nil || observation.PointRevision == nil || *observation.PointRevision < 1 {
		return fmt.Errorf("%w: resolved ClickHouse history observation requires Point type and revision semantics", ErrHistoryBatchPermanent)
	}
	if *observation.PointType == "COUNTER" {
		if observation.ValueType == nil || *observation.ValueType != "NUMBER" || observation.CounterDecreaseMode == nil {
			return fmt.Errorf("%w: counter history observation requires numeric value type and decrease semantics", ErrHistoryBatchPermanent)
		}
		switch *observation.CounterDecreaseMode {
		case "RESET_TO_ZERO", "INVALID":
			if observation.CounterRolloverModulus != nil {
				return fmt.Errorf("%w: non-rollover Counter history observation cannot define a rollover modulus", ErrHistoryBatchPermanent)
			}
		case "ROLLOVER":
			if observation.CounterRolloverModulus == nil || *observation.CounterRolloverModulus <= 0 {
				return fmt.Errorf("%w: rollover Counter history observation requires a positive modulus", ErrHistoryBatchPermanent)
			}
		default:
			return fmt.Errorf("%w: counter history observation decrease semantics are invalid", ErrHistoryBatchPermanent)
		}
	} else if observation.CounterDecreaseMode != nil || observation.CounterRolloverModulus != nil {
		return fmt.Errorf("%w: non-Counter history observation cannot define Counter semantics", ErrHistoryBatchPermanent)
	}
	return nil
}

func (sink *ClickHouseHistorySink) insertBatch(ctx context.Context, compressed []byte, token string) error {
	endpoint := *sink.baseURL
	query := endpoint.Query()
	query.Set("query", "INSERT INTO "+sink.database+"."+sink.table+" FORMAT JSONEachRow")
	query.Set("date_time_input_format", "best_effort")
	query.Set("insert_deduplication_token", token)
	query.Set("async_insert", "0")
	query.Set("insert_deduplicate", "1")
	query.Set("deduplicate_blocks_in_dependent_materialized_views", "1")
	query.Set("materialized_views_ignore_errors", "0")
	query.Set("input_format_parallel_parsing", "0")
	query.Set("max_insert_block_size", strconv.Itoa(maxHistoryBatchRows))
	query.Set("min_insert_block_size_rows", strconv.Itoa(maxHistoryBatchRows))
	query.Set("min_insert_block_size_bytes", strconv.Itoa(maxHistoryBatchBytes))

	query.Set("wait_end_of_query", "1")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("create ClickHouse history request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-ndjson")
	request.Header.Set("Content-Encoding", "gzip")
	if sink.username != "" {
		request.SetBasicAuth(sink.username, sink.password)
	}
	response, err := sink.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("insert ClickHouse history batch %s: %w", token, err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	closeErr := response.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read ClickHouse history response for %s: %w", token, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close ClickHouse history response for %s: %w", token, closeErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(responseBody))
		if len(message) > 512 {
			message = message[:512]
		}
		if isClickHouseTooManyPartsMessage(message) {
			return fmt.Errorf("%w: ClickHouse history insert returned %d for %s: %s", ErrClickHouseTooManyParts, response.StatusCode, token, message)
		}
		if response.StatusCode == http.StatusBadRequest {
			return fmt.Errorf("%w: ClickHouse history insert returned %d for %s: %s", ErrHistoryBatchPermanent, response.StatusCode, token, message)
		}
		return fmt.Errorf("ClickHouse history insert returned %d for %s: %s", response.StatusCode, token, message)
	}
	return nil
}

var (
	_ HistorySink         = (*ClickHouseHistorySink)(nil)
	_ PreparedHistorySink = (*ClickHouseHistorySink)(nil)
)
