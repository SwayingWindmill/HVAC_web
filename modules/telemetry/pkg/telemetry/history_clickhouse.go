package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const maxConcurrentClickHouseInserts = 16

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

type encodedHistoryObservation struct {
	observationID string
	body          []byte
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

func (sink *ClickHouseHistorySink) InsertObservations(ctx context.Context, observations []HistoryObservation) error {
	if sink == nil || sink.baseURL == nil || sink.httpClient == nil {
		return errors.New("ClickHouse history sink is closed")
	}
	if len(observations) == 0 {
		return nil
	}
	if len(observations) > 4096 {
		return errors.New("ClickHouse history insert batch exceeds 4096 observations")
	}
	encoded := make([]encodedHistoryObservation, len(observations))
	for index, observation := range observations {
		if !uuidV7Pattern.MatchString(observation.ObservationID) {
			return errors.New("ClickHouse history observation ID must be UUIDv7")
		}
		if !clickHousePayloadDigestPattern.MatchString(observation.PayloadSHA256) {
			return errors.New("ClickHouse history payload digest must be lowercase SHA-256 hex")
		}
		if observation.AcceptanceStatus == string(ObservationAccepted) ||
			(observation.AcceptanceStatus == string(ObservationOutOfOrder) && observation.PointID != nil) {
			if observation.TenantID == nil || observation.SiteID == nil || observation.DeviceID == nil || observation.PointID == nil ||
				!uuidV7Pattern.MatchString(*observation.TenantID) || !uuidV7Pattern.MatchString(*observation.SiteID) ||
				!uuidV7Pattern.MatchString(*observation.DeviceID) || !uuidV7Pattern.MatchString(*observation.PointID) ||
				(observation.SensorID != nil && !uuidV7Pattern.MatchString(*observation.SensorID)) {
				return errors.New("resolved ClickHouse history observation requires UUIDv7 Tenant, Site, Device and Point scope")
			}
			if observation.PointType == nil || observation.PointRevision == nil || *observation.PointRevision < 1 {
				return errors.New("resolved ClickHouse history observation requires Point type and revision semantics")
			}
			if *observation.PointType == "COUNTER" {
				if observation.ValueType == nil || *observation.ValueType != "NUMBER" || observation.CounterDecreaseMode == nil {
					return errors.New("Counter history observation requires numeric value type and decrease semantics")
				}
				switch *observation.CounterDecreaseMode {
				case "RESET_TO_ZERO", "INVALID":
					if observation.CounterRolloverModulus != nil {
						return errors.New("non-rollover Counter history observation cannot define a rollover modulus")
					}
				case "ROLLOVER":
					if observation.CounterRolloverModulus == nil || *observation.CounterRolloverModulus <= 0 {
						return errors.New("rollover Counter history observation requires a positive modulus")
					}
				default:
					return errors.New("Counter history observation decrease semantics are invalid")
				}
			} else if observation.CounterDecreaseMode != nil || observation.CounterRolloverModulus != nil {
				return errors.New("non-Counter history observation cannot define Counter semantics")
			}
		}
		body, err := json.Marshal(observation)
		if err != nil {
			return fmt.Errorf("encode ClickHouse history observation %s: %w", observation.ObservationID, err)
		}
		encoded[index] = encodedHistoryObservation{observationID: observation.ObservationID, body: append(body, '\n')}
	}

	insertContext, cancel := context.WithCancel(ctx)
	defer cancel()
	semaphore := make(chan struct{}, maxConcurrentClickHouseInserts)
	var waitGroup sync.WaitGroup
	var firstError error
	var firstErrorOnce sync.Once
	for _, item := range encoded {
		item := item
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-insertContext.Done():
				return
			}
			if err := sink.insertObservation(insertContext, item); err != nil {
				firstErrorOnce.Do(func() {
					firstError = err
					cancel()
				})
			}
		}()
	}
	waitGroup.Wait()
	return firstError
}

func (sink *ClickHouseHistorySink) insertObservation(ctx context.Context, item encodedHistoryObservation) error {
	endpoint := *sink.baseURL
	query := endpoint.Query()
	query.Set("query", "INSERT INTO "+sink.database+"."+sink.table+" FORMAT JSONEachRow")
	query.Set("date_time_input_format", "best_effort")
	query.Set("insert_deduplication_token", item.observationID)
	query.Set("async_insert", "1")
	query.Set("wait_for_async_insert", "1")
	query.Set("async_insert_deduplicate", "1")
	query.Set("wait_end_of_query", "1")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(item.body))
	if err != nil {
		return fmt.Errorf("create ClickHouse history request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-ndjson")
	if sink.username != "" {
		request.SetBasicAuth(sink.username, sink.password)
	}
	response, err := sink.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("insert ClickHouse history observation %s: %w", item.observationID, err)
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	closeErr := response.Body.Close()
	if readErr != nil {
		return fmt.Errorf("read ClickHouse history response for %s: %w", item.observationID, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close ClickHouse history response for %s: %w", item.observationID, closeErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(responseBody))
		if len(message) > 512 {
			message = message[:512]
		}
		return fmt.Errorf("ClickHouse history insert returned %d for %s: %s", response.StatusCode, item.observationID, message)
	}
	return nil
}

var _ HistorySink = (*ClickHouseHistorySink)(nil)
