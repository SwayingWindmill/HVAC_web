package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/clickhousehttp"
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
	ch         *clickhousehttp.Client
}

func NewClickHouseHistorySink(config ClickHouseHistoryConfig) (*ClickHouseHistorySink, error) {
	parsed, err := clickhousehttp.ParseOriginURL(config.BaseURL)
	if err != nil {
		return nil, errors.New("ClickHouse history base URL must be an HTTP(S) origin without user info")
	}
	if !clickHouseIdentifierPattern.MatchString(config.Database) || !clickHouseIdentifierPattern.MatchString(config.Table) {
		return nil, errors.New("ClickHouse history database and table identifiers are invalid")
	}
	client := config.HTTPClient
	if client == nil {
		client = clickhousehttp.NewPooledHTTPClient(clickhousehttp.DataPlaneTransportConfig(30 * time.Second))
	}
	creds := clickhousehttp.Credentials{Username: config.Username, Password: config.Password}
	return &ClickHouseHistorySink{
		baseURL: parsed, database: config.Database, table: config.Table,
		username: config.Username, password: config.Password, httpClient: client,
		ch: clickhousehttp.NewClient(parsed, client, creds),
	}, nil
}

func (sink *ClickHouseHistorySink) transportClient() *clickhousehttp.Client {
	if sink.ch != nil {
		return sink.ch
	}
	return clickhousehttp.NewClient(sink.baseURL, sink.httpClient, clickhousehttp.Credentials{
		Username: sink.username,
		Password: sink.password,
	})
}

func (sink *ClickHouseHistorySink) Ping(ctx context.Context) error {
	if sink == nil || sink.baseURL == nil || sink.httpClient == nil {
		return errors.New("ClickHouse history sink is closed")
	}
	if err := sink.transportClient().Ping(ctx, true); err != nil {
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			return fmt.Errorf("ClickHouse history ping returned %d: %s", statusErr.StatusCode, statusErr.TrimmedBody(512))
		}
		return fmt.Errorf("ClickHouse history ping: %w", err)
	}
	return nil
}

// Close satisfies closer interfaces used alongside the native TCP sink; HTTP transports are stateless here.
func (sink *ClickHouseHistorySink) Close() error {
	return nil
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
	token := clickhousehttp.SHA256Hex(raw)
	compressed, err := clickhousehttp.CompressGzipBestSpeed(raw)
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
	params := url.Values{
		"query":                      {"INSERT INTO " + sink.database + "." + sink.table + " FORMAT JSONEachRow"},
		"date_time_input_format":     {"best_effort"},
		"insert_deduplication_token": {token},
		"async_insert":               {"0"},
		"insert_deduplicate":         {"1"},
		"deduplicate_blocks_in_dependent_materialized_views": {"1"},
		"materialized_views_ignore_errors":                   {"0"},
		"input_format_parallel_parsing":                      {"0"},
		"max_insert_block_size":                              {strconv.Itoa(maxHistoryBatchRows)},
		"min_insert_block_size_rows":                         {strconv.Itoa(maxHistoryBatchRows)},
		"min_insert_block_size_bytes":                        {strconv.Itoa(maxHistoryBatchBytes)},
		"wait_end_of_query":                                  {"1"},
	}
	if err := sink.transportClient().Insert(ctx, clickhousehttp.InsertRequest{
		Params:         params,
		Body:           compressed,
		CompressedGzip: true,
		MaxErrorBytes:  8 << 10,
	}); err != nil {
		var statusErr *clickhousehttp.StatusError
		if errors.As(err, &statusErr) {
			message := statusErr.TrimmedBody(512)
			if isClickHouseTooManyPartsMessage(message) {
				return fmt.Errorf("%w: ClickHouse history insert returned %d for %s: %s", ErrClickHouseTooManyParts, statusErr.StatusCode, token, message)
			}
			if statusErr.StatusCode == http.StatusBadRequest {
				return fmt.Errorf("%w: ClickHouse history insert returned %d for %s: %s", ErrHistoryBatchPermanent, statusErr.StatusCode, token, message)
			}
			return fmt.Errorf("ClickHouse history insert returned %d for %s: %s", statusErr.StatusCode, token, message)
		}
		return fmt.Errorf("insert ClickHouse history batch %s: %w", token, err)
	}
	return nil
}

var (
	_ HistorySink         = (*ClickHouseHistorySink)(nil)
	_ PreparedHistorySink = (*ClickHouseHistorySink)(nil)
)
