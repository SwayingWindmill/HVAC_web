package telemetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

const maxHistoryBatchBytes = 8 << 20

// ErrHistoryBatchPermanent marks a deterministic validation or schema error that retries cannot fix.
var ErrHistoryBatchPermanent = errors.New("ClickHouse history batch rejected permanently")

// IsPermanentHistoryError reports whether err is a permanent ClickHouse history failure.
func IsPermanentHistoryError(err error) bool {
	return errors.Is(err, ErrHistoryBatchPermanent)
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
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &ClickHouseHistorySink{
		baseURL: parsed, database: config.Database, table: config.Table,
		username: config.Username, password: config.Password, httpClient: client,
	}, nil
}

func (sink *ClickHouseHistorySink) InsertObservations(ctx context.Context, sequence uint64, observations []HistoryObservation) error {
	if sink == nil || sink.baseURL == nil || sink.httpClient == nil {
		return errors.New("ClickHouse history sink is closed")
	}
	if len(observations) == 0 {
		return nil
	}
	if len(observations) > 4096 {
		return errors.New("ClickHouse history insert batch exceeds 4096 observations")
	}
	if sequence == 0 {
		return errors.New("ClickHouse history insert requires the batch History Sequence")
	}
	// PostgreSQL UPDATE RETURNING has no order guarantee. Retries must produce
	// identical rows, order and block boundaries, including after a worker restart.
	observations = slices.Clone(observations)
	slices.SortFunc(observations, func(a, b HistoryObservation) int { return strings.Compare(a.ObservationID, b.ObservationID) })
	var encoded bytes.Buffer
	for _, observation := range observations {
		observation.HistorySequence = sequence
		if err := validateHistoryObservation(observation); err != nil {
			return err
		}
		body, err := json.Marshal(observation)
		if err != nil {
			return fmt.Errorf("%w: encode ClickHouse history observation %s: %w", ErrHistoryBatchPermanent, observation.ObservationID, err)
		}
		if encoded.Len()+len(body)+1 > maxHistoryBatchBytes {
			return fmt.Errorf("%w: ClickHouse history insert exceeds 8 MiB", ErrHistoryBatchPermanent)
		}
		encoded.Write(body)
		encoded.WriteByte('\n')
	}
	return sink.insertBatch(ctx, encoded.Bytes())
}

func validateHistoryObservation(observation HistoryObservation) error {
	if !uuidV7Pattern.MatchString(observation.ObservationID) {
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
		!uuidV7Pattern.MatchString(*observation.TenantID) || !uuidV7Pattern.MatchString(*observation.SiteID) ||
		!uuidV7Pattern.MatchString(*observation.DeviceID) || !uuidV7Pattern.MatchString(*observation.PointID) ||
		(observation.SensorID != nil && !uuidV7Pattern.MatchString(*observation.SensorID)) {
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

func (sink *ClickHouseHistorySink) insertBatch(ctx context.Context, body []byte) error {
	token := fmt.Sprintf("%x", sha256.Sum256(body))
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
	query.Set("max_insert_block_size", "4096")
	query.Set("min_insert_block_size_rows", "4096")
	query.Set("min_insert_block_size_bytes", "8388608")

	query.Set("wait_end_of_query", "1")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ClickHouse history request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-ndjson")
	if sink.username != "" {
		request.SetBasicAuth(sink.username, sink.password)
	}
	response, err := sink.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("insert ClickHouse history batch %s: %w", token, err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<10))
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
		if response.StatusCode == http.StatusBadRequest {
			return fmt.Errorf("%w: ClickHouse history insert returned %d for %s: %s", ErrHistoryBatchPermanent, response.StatusCode, token, message)
		}
		return fmt.Errorf("ClickHouse history insert returned %d for %s: %s", response.StatusCode, token, message)
	}
	return nil
}

var _ HistorySink = (*ClickHouseHistorySink)(nil)
