package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

type SpanKind int

const (
	SpanKindInternal SpanKind = 1
	SpanKindServer   SpanKind = 2
	SpanKindClient   SpanKind = 3
	SpanKindProducer SpanKind = 4
	SpanKindConsumer SpanKind = 5
)

type SpanContext struct {
	TraceID    string
	SpanID     string
	TraceFlags string
	TraceState string
	Remote     bool
}

func (value SpanContext) Valid() bool {
	return traceIDPattern.MatchString(value.TraceID) && value.TraceID != zeroTraceID && spanIDPattern.MatchString(value.SpanID) && value.SpanID != zeroSpanID
}

func (value SpanContext) Traceparent() string {
	if !value.Valid() {
		return ""
	}
	flags := value.TraceFlags
	if !flagsPattern.MatchString(flags) {
		flags = "01"
	}
	return fmt.Sprintf("00-%s-%s-%s", value.TraceID, value.SpanID, flags)
}

type SpanData struct {
	Service      string
	Name         string
	Kind         SpanKind
	TraceID      string
	SpanID       string
	ParentSpanID string
	TraceState   string
	StartTime    time.Time
	EndTime      time.Time
	Attributes   map[string]any
	StatusCode   string
	StatusText   string
}

type Exporter interface {
	Export(context.Context, []SpanData) error
	Shutdown(context.Context) error
}

type Tracer struct {
	service  string
	exporter Exporter
	now      func() time.Time
}

type spanState struct {
	tracer  *Tracer
	context SpanContext
	span    *Span
}

type spanStateKey struct{}

type Span struct {
	mu         sync.Mutex
	tracer     *Tracer
	data       SpanData
	context    SpanContext
	ended      bool
	statusCode string
	statusText string
}

var (
	traceparentPattern = regexp.MustCompile(`^00-([0-9a-f]{32})-([0-9a-f]{16})-([0-9a-f]{2})$`)
	traceIDPattern     = regexp.MustCompile(`^[0-9a-f]{32}$`)
	spanIDPattern      = regexp.MustCompile(`^[0-9a-f]{16}$`)
	flagsPattern       = regexp.MustCompile(`^[0-9a-f]{2}$`)
	zeroTraceID        = strings.Repeat("0", 32)
	zeroSpanID         = strings.Repeat("0", 16)
)

func NewTracer(service string, exporter Exporter) *Tracer {
	if exporter == nil {
		exporter = NopExporter{}
	}
	return &Tracer{service: service, exporter: exporter, now: time.Now}
}

func ParseTraceparent(value, tracestate string) (SpanContext, bool) {
	match := traceparentPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 4 || match[1] == zeroTraceID || match[2] == zeroSpanID {
		return SpanContext{}, false
	}
	return SpanContext{TraceID: match[1], SpanID: match[2], TraceFlags: match[3], TraceState: sanitizeTracestate(tracestate), Remote: true}, true
}

func (tracer *Tracer) ExtractHTTP(ctx context.Context, header http.Header) context.Context {
	parent, ok := ParseTraceparent(header.Get("traceparent"), header.Get("tracestate"))
	if !ok {
		return context.WithValue(ctx, spanStateKey{}, spanState{tracer: tracer})
	}
	return context.WithValue(ctx, spanStateKey{}, spanState{tracer: tracer, context: parent})
}

func (tracer *Tracer) Start(ctx context.Context, name string, kind SpanKind, attributes map[string]any) (context.Context, *Span) {
	state, _ := ctx.Value(spanStateKey{}).(spanState)
	parent := state.context
	traceID := parent.TraceID
	if traceID == "" {
		traceID = randomID(16)
	}
	flags := parent.TraceFlags
	if flags == "" {
		flags = "01"
	}
	spanContext := SpanContext{TraceID: traceID, SpanID: randomID(8), TraceFlags: flags, TraceState: parent.TraceState}
	span := &Span{
		tracer:  tracer,
		context: spanContext,
		data: SpanData{
			Service:      tracer.service,
			Name:         name,
			Kind:         kind,
			TraceID:      traceID,
			SpanID:       spanContext.SpanID,
			ParentSpanID: parent.SpanID,
			TraceState:   parent.TraceState,
			StartTime:    tracer.now().UTC(),
			Attributes:   cloneAttributes(attributes),
		},
	}
	return context.WithValue(ctx, spanStateKey{}, spanState{tracer: tracer, context: spanContext, span: span}), span
}

func Start(ctx context.Context, name string, kind SpanKind, attributes map[string]any) (context.Context, *Span) {
	state, _ := ctx.Value(spanStateKey{}).(spanState)
	if state.tracer == nil {
		return ctx, &Span{}
	}
	return state.tracer.Start(ctx, name, kind, attributes)
}

func ContextWithRemoteParent(ctx context.Context, tracer *Tracer, traceparent, tracestate string) context.Context {
	if tracer == nil {
		return ctx
	}
	parent, ok := ParseTraceparent(traceparent, tracestate)
	if !ok {
		return context.WithValue(ctx, spanStateKey{}, spanState{tracer: tracer})
	}
	return context.WithValue(ctx, spanStateKey{}, spanState{tracer: tracer, context: parent})
}

func InjectHTTP(ctx context.Context, header http.Header) {
	value := SpanContextFromContext(ctx)
	if traceparent := value.Traceparent(); traceparent != "" {
		header.Set("traceparent", traceparent)
	}
	if value.TraceState != "" {
		header.Set("tracestate", value.TraceState)
	}
}

func SpanContextFromContext(ctx context.Context) SpanContext {
	state, _ := ctx.Value(spanStateKey{}).(spanState)
	return state.context
}

func SpanFromContext(ctx context.Context) *Span {
	state, _ := ctx.Value(spanStateKey{}).(spanState)
	return state.span
}

func TraceID(ctx context.Context) string {
	return SpanContextFromContext(ctx).TraceID
}

func Traceparent(ctx context.Context) string {
	return SpanContextFromContext(ctx).Traceparent()
}

func (span *Span) SetAttributes(attributes map[string]any) {
	if span == nil || span.tracer == nil {
		return
	}
	span.mu.Lock()
	defer span.mu.Unlock()
	if span.ended {
		return
	}
	if span.data.Attributes == nil {
		span.data.Attributes = map[string]any{}
	}
	for key, value := range attributes {
		span.data.Attributes[key] = value
	}
}

func (span *Span) SetStatus(code, text string) {
	if span == nil || span.tracer == nil {
		return
	}
	span.mu.Lock()
	defer span.mu.Unlock()
	if span.ended {
		return
	}
	span.statusCode = code
	span.statusText = text
}

func (span *Span) End() {
	if span == nil || span.tracer == nil {
		return
	}
	span.mu.Lock()
	if span.ended {
		span.mu.Unlock()
		return
	}
	span.ended = true
	span.data.EndTime = span.tracer.now().UTC()
	span.data.StatusCode = span.statusCode
	span.data.StatusText = span.statusText
	data := span.data
	span.mu.Unlock()
	_ = span.tracer.exporter.Export(context.Background(), []SpanData{data})
}

func randomID(bytes int) string {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		panic("secure trace identifier generation failed")
	}
	return hex.EncodeToString(buffer)
}

func cloneAttributes(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func sanitizeTracestate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 512 || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}
