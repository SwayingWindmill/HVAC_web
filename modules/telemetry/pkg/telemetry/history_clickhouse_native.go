package telemetry

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/quanlaihe/hvac-web/libs/uuidv7"
)

const (
	defaultClickHouseNativeDialTimeout     = 5 * time.Second
	defaultClickHouseNativeReadTimeout     = 30 * time.Second
	defaultClickHouseNativeMaxOpenConns    = 4
	defaultClickHouseNativeMaxIdleConns    = 2
	defaultClickHouseNativeConnMaxLifetime = 10 * time.Minute
)

// ClickHouseNativeHistoryConfig configures Native TCP (:9000) PrepareBatch writes to ClickHouse.
type ClickHouseNativeHistoryConfig struct {
	Addr            string
	Database        string
	Table           string
	Username        string
	Password        string
	TLS             *tls.Config
	DialTimeout     time.Duration
	ReadTimeout     time.Duration
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// ClickHouseNativeHistorySink streams columnar binary microbatches to ClickHouse over Native TCP.
type ClickHouseNativeHistorySink struct {
	conn      driver.Conn
	database  string
	table     string
	insertSQL string
	closeOnce sync.Once
	closed    atomic.Bool
}

// NewClickHouseNativeHistorySink validates configuration and opens a pooled Native TCP ClickHouse sink.
func NewClickHouseNativeHistorySink(config ClickHouseNativeHistoryConfig) (*ClickHouseNativeHistorySink, error) {
	addr := strings.TrimSpace(config.Addr)
	host, port, err := net.SplitHostPort(addr)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return nil, errors.New("ClickHouse native history address must be a host:port endpoint")
	}
	if !clickHouseIdentifierPattern.MatchString(config.Database) || !clickHouseIdentifierPattern.MatchString(config.Table) {
		return nil, errors.New("ClickHouse history database and table identifiers are invalid")
	}

	dialTimeout := config.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = defaultClickHouseNativeDialTimeout
	}
	readTimeout := config.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = defaultClickHouseNativeReadTimeout
	}
	maxOpenConns := config.MaxOpenConns
	if maxOpenConns <= 0 {
		maxOpenConns = defaultClickHouseNativeMaxOpenConns
	}
	maxIdleConns := config.MaxIdleConns
	if maxIdleConns <= 0 {
		maxIdleConns = defaultClickHouseNativeMaxIdleConns
	}
	connMaxLifetime := config.ConnMaxLifetime
	if connMaxLifetime <= 0 {
		connMaxLifetime = defaultClickHouseNativeConnMaxLifetime
	}

	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{addr},
		Auth: clickhouse.Auth{
			Database: config.Database,
			Username: config.Username,
			Password: config.Password,
		},
		TLS:             config.TLS,
		DialTimeout:     dialTimeout,
		ReadTimeout:     readTimeout,
		MaxOpenConns:    maxOpenConns,
		MaxIdleConns:    maxIdleConns,
		ConnMaxLifetime: connMaxLifetime,
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open ClickHouse native history connection: %w", err)
	}

	return &ClickHouseNativeHistorySink{
		conn:     conn,
		database: config.Database,
		table:    config.Table,
		insertSQL: fmt.Sprintf(
			"INSERT INTO %s.%s (observation_id, tenant_id, site_id, device_id, point_id, sensor_id, source_id, source_event_id, source_partition, source_offset, source_path, telemetry_key, point_type, point_revision, counter_decrease_mode, counter_rollover_modulus, value_type, unit, value_json, value_number, value_string, value_boolean, sampled_at, received_at, acceptance_status, quality, quality_reasons, payload_sha256, history_sequence)",
			config.Database,
			config.Table,
		),
	}, nil
}

// Ping verifies Native TCP connectivity to ClickHouse.
func (sink *ClickHouseNativeHistorySink) Ping(ctx context.Context) error {
	if sink == nil || sink.closed.Load() || sink.conn == nil {
		return errors.New("ClickHouse native history sink is closed")
	}
	return sink.conn.Ping(ctx)
}

// Close tears down the underlying Native TCP connection pool idempotently.
func (sink *ClickHouseNativeHistorySink) Close() error {
	if sink == nil {
		return nil
	}
	var closeErr error
	sink.closeOnce.Do(func() {
		sink.closed.Store(true)
		if sink.conn != nil {
			closeErr = sink.conn.Close()
		}
	})
	return closeErr
}

// InsertObservations validates, orders, deduplicates, and writes one History batch using PrepareBatch.
func (sink *ClickHouseNativeHistorySink) InsertObservations(ctx context.Context, sequence uint64, observations []HistoryObservation) error {
	if sink == nil || sink.closed.Load() || sink.conn == nil {
		return errors.New("ClickHouse native history sink is closed")
	}
	if len(observations) == 0 {
		return nil
	}
	ordered, err := prepareOrderedHistoryObservations(sequence, observations)
	if err != nil {
		return err
	}
	if len(ordered) == 0 {
		return nil
	}
	for i := range ordered {
		if ordered[i].SourceOffset < 0 {
			return fmt.Errorf("%w: ClickHouse history source offset cannot be negative", ErrHistoryBatchPermanent)
		}
		if ordered[i].PointRevision != nil && *ordered[i].PointRevision < 0 {
			return fmt.Errorf("%w: ClickHouse history point revision cannot be negative", ErrHistoryBatchPermanent)
		}
		if !uuidv7.IsValid(ordered[i].SourceEventID) {
			return fmt.Errorf("%w: ClickHouse history source event ID must be UUIDv7", ErrHistoryBatchPermanent)
		}
	}

	token := deterministicNativeHistoryBatchToken(sequence, ordered)
	batchCtx := clickhouse.Context(ctx, clickhouse.WithSettings(clickhouse.Settings{
		"insert_deduplication_token": token,
		"async_insert":               0,
		"insert_deduplicate":         1,
		"deduplicate_blocks_in_dependent_materialized_views": 1,
		"materialized_views_ignore_errors":                   0,
		"max_insert_block_size":                              maxHistoryBatchRows,
		"min_insert_block_size_rows":                         maxHistoryBatchRows,
		"min_insert_block_size_bytes":                        maxHistoryBatchBytes,
	}))

	batch, err := sink.conn.PrepareBatch(batchCtx, sink.insertSQL)
	if err != nil {
		return classifyClickHouseNativeError(err, token)
	}
	for i := range ordered {
		obs := ordered[i]
		var pointRevision *uint64
		if obs.PointRevision != nil {
			rev := uint64(*obs.PointRevision)
			pointRevision = &rev
		}
		reasons := obs.QualityReasons
		if reasons == nil {
			reasons = []string{}
		}
		if err := batch.Append(
			obs.ObservationID,
			obs.TenantID,
			obs.SiteID,
			obs.DeviceID,
			obs.PointID,
			obs.SensorID,
			obs.SourceID,
			obs.SourceEventID,
			obs.SourcePartition,
			uint64(obs.SourceOffset),
			obs.SourcePath,
			obs.TelemetryKey,
			obs.PointType,
			pointRevision,
			obs.CounterDecreaseMode,
			obs.CounterRolloverModulus,
			obs.ValueType,
			obs.Unit,
			obs.ValueJSON,
			obs.ValueNumber,
			obs.ValueString,
			obs.ValueBoolean,
			obs.SampledAt.UTC(),
			obs.ReceivedAt.UTC(),
			obs.AcceptanceStatus,
			obs.Quality,
			reasons,
			obs.PayloadSHA256,
			obs.HistorySequence,
		); err != nil {
			_ = batch.Abort()
			return fmt.Errorf("%w: append ClickHouse native history row %s: %w", ErrHistoryBatchPermanent, obs.ObservationID, err)
		}
	}
	if err := batch.Send(); err != nil {
		return classifyClickHouseNativeError(err, token)
	}
	return nil
}

func deterministicNativeHistoryBatchToken(sequence uint64, ordered []HistoryObservation) string {
	hasher := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], sequence)
	hasher.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], uint64(len(ordered)))
	hasher.Write(buf[:])

	writeLengthPrefixed := func(value string) {
		binary.BigEndian.PutUint64(buf[:], uint64(len(value)))
		hasher.Write(buf[:])
		hasher.Write([]byte(value))
	}

	for i := range ordered {
		obs := ordered[i]
		writeLengthPrefixed(obs.ObservationID)
		writeLengthPrefixed(obs.PayloadSHA256)
		binary.BigEndian.PutUint64(buf[:], uint64(obs.SampledAt.UTC().UnixNano()))
		hasher.Write(buf[:])
		binary.BigEndian.PutUint64(buf[:], uint64(obs.ReceivedAt.UTC().UnixNano()))
		hasher.Write(buf[:])
		writeLengthPrefixed(obs.AcceptanceStatus)
		writeLengthPrefixed(obs.Quality)
	}
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

func classifyClickHouseNativeError(err error, token string) error {
	if err == nil {
		return nil
	}
	var chErr *clickhouse.Exception
	if errors.As(err, &chErr) {
		if chErr.Code == 252 || isClickHouseTooManyPartsMessage(chErr.Message) {
			return fmt.Errorf("%w: ClickHouse native history insert returned code %d for %s: %w", ErrClickHouseTooManyParts, chErr.Code, token, err)
		}
		switch chErr.Code {
		case 6, 27, 32, 36, 37, 53, 62, 72, 117, 469:
			return fmt.Errorf("%w: ClickHouse native history insert returned code %d for %s: %w", ErrHistoryBatchPermanent, chErr.Code, token, err)
		}
	}
	if isClickHouseTooManyPartsMessage(err.Error()) {
		return fmt.Errorf("%w: ClickHouse native history insert for %s: %w", ErrClickHouseTooManyParts, token, err)
	}
	return fmt.Errorf("insert ClickHouse native history batch %s: %w", token, err)
}

// HistorySinkCloser extends HistorySink with readiness checks and resource cleanup.
type HistorySinkCloser interface {
	HistorySink
	Ping(context.Context) error
	Close() error
}

// OpenClickHouseHistorySinkFromLookup selects Native TCP PrepareBatch when TELEMETRY_CLICKHOUSE_NATIVE_ADDR
// is configured, and falls back to compressed HTTP JSONEachRow otherwise.
func OpenClickHouseHistorySinkFromLookup(lookup func(string) string, defaultUsername string) (HistorySinkCloser, error) {
	if lookup == nil {
		return nil, errors.New("ClickHouse history environment lookup is required")
	}
	envOrLookup := func(key, fallback string) string {
		if value := strings.TrimSpace(lookup(key)); value != "" {
			return value
		}
		return fallback
	}
	database := envOrLookup("TELEMETRY_CLICKHOUSE_DATABASE", "telemetry_history")
	table := envOrLookup("TELEMETRY_CLICKHOUSE_TABLE", "observations")
	username := envOrLookup("TELEMETRY_CLICKHOUSE_USERNAME", defaultUsername)
	password := lookup("TELEMETRY_CLICKHOUSE_PASSWORD")

	if nativeAddr := strings.TrimSpace(lookup("TELEMETRY_CLICKHOUSE_NATIVE_ADDR")); nativeAddr != "" {
		tlsConfig, err := loadClickHouseNativeTLSConfig(strings.TrimSpace(lookup("TELEMETRY_CLICKHOUSE_CA")))
		if err != nil {
			return nil, err
		}
		return NewClickHouseNativeHistorySink(ClickHouseNativeHistoryConfig{
			Addr:     nativeAddr,
			Database: database,
			Table:    table,
			Username: username,
			Password: password,
			TLS:      tlsConfig,
		})
	}

	httpURL := strings.TrimSpace(lookup("TELEMETRY_CLICKHOUSE_HTTP_URL"))
	if httpURL == "" {
		return nil, errors.New("TELEMETRY_CLICKHOUSE_HTTP_URL is required")
	}
	return NewClickHouseHistorySink(ClickHouseHistoryConfig{
		BaseURL:  httpURL,
		Database: database,
		Table:    table,
		Username: username,
		Password: password,
	})
}

func loadClickHouseNativeTLSConfig(caPath string) (*tls.Config, error) {
	if caPath == "" {
		return nil, nil
	}
	pemBytes, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read TELEMETRY_CLICKHOUSE_CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, errors.New("TELEMETRY_CLICKHOUSE_CA contains no valid PEM certificates")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    pool,
	}, nil
}

var (
	_ HistorySink       = (*ClickHouseNativeHistorySink)(nil)
	_ HistorySinkCloser = (*ClickHouseNativeHistorySink)(nil)
	_ HistorySinkCloser = (*ClickHouseHistorySink)(nil)
)
