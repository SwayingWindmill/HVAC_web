package mv

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

const maxMVRequestBytes = int64(64 << 10)

// The Gateway reaches mv-service on the private network after deciding the caller may read the
// Site (site.read) or manage its measures (savings.manage); it names the Tenant and the actor.
const (
	headerTenant = "X-Tenant-ID"
	headerActor  = "X-Actor-ID"
)

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("savings service is required")
	}
	handler := &HTTPHandler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /v1/sites/{siteId}/savings/measures", handler.list)
	mux.HandleFunc("POST /v1/sites/{siteId}/savings/measures", handler.register)
	mux.HandleFunc("GET /v1/sites/{siteId}/savings/measures/{measureId}", handler.detail)
	mux.HandleFunc("POST /v1/sites/{siteId}/savings/measures/{measureId}/implement", handler.implement)
	mux.HandleFunc("POST /v1/sites/{siteId}/savings/measures/{measureId}/refit", handler.refit)
	mux.HandleFunc("POST /v1/sites/{siteId}/savings/measures/{measureId}/end", handler.end)
	mux.HandleFunc("GET /v1/sites/{siteId}/savings/summary", handler.summary)
	return mux, nil
}

type baselineView struct {
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	InterceptKWh  float64   `json:"interceptKWh"`
	Slope         float64   `json:"slope"`
	Samples       int       `json:"samples"`
	ExpectedHours int       `json:"expectedHours"`
	CVRMSE        float64   `json:"cvRmse"`
	NMBE          float64   `json:"nmbe"`
	Trusted       bool      `json:"trusted"`
	Reason        string    `json:"reason,omitempty"`
}

type dayView struct {
	Day                 string  `json:"day"`
	ExpectedHours       int     `json:"expectedHours"`
	Hours               int     `json:"hours"`
	UnpricedHours       int     `json:"unpricedHours"`
	AdjustedBaselineKWh float64 `json:"adjustedBaselineKWh"`
	ActualKWh           float64 `json:"actualKWh"`
	AvoidedKWh          float64 `json:"avoidedKWh"`
	AvoidedCost         float64 `json:"avoidedCost"`
}

type totalsView struct {
	Days                int     `json:"days"`
	AdjustedBaselineKWh float64 `json:"adjustedBaselineKWh"`
	ActualKWh           float64 `json:"actualKWh"`
	AvoidedKWh          float64 `json:"avoidedKWh"`
	AvoidedCost         float64 `json:"avoidedCost"`
}

type measureView struct {
	ID                string        `json:"id"`
	SiteID            string        `json:"siteId"`
	Name              string        `json:"name"`
	Kind              Kind          `json:"kind"`
	Description       string        `json:"description"`
	Evidence          string        `json:"evidence"`
	Status            Status        `json:"status"`
	PlannedAt         time.Time     `json:"plannedAt"`
	ImplementedAt     *time.Time    `json:"implementedAt"`
	EndedAt           *time.Time    `json:"endedAt"`
	InvestmentCNY     *float64      `json:"investmentCny"`
	ExpectedAnnualKWh *float64      `json:"expectedAnnualKWh"`
	Baseline          *baselineView `json:"baseline"`
	Totals            totalsView    `json:"totals"`
	Days              []dayView     `json:"days,omitempty"`
	CreatedAt         time.Time     `json:"createdAt"`
	UpdatedAt         time.Time     `json:"updatedAt"`
}

func totalsOf(results []DailyResult) totalsView {
	totals := totalsView{Days: len(results)}
	for _, result := range results {
		totals.AdjustedBaselineKWh += result.AdjustedBaselineKWh
		totals.ActualKWh += result.ActualKWh
		totals.AvoidedKWh += result.AvoidedKWh
		totals.AvoidedCost += result.AvoidedCost
	}
	return totals
}

func viewOf(measure Measure, results []DailyResult, withDays bool) measureView {
	view := measureView{
		ID: measure.ID, SiteID: measure.SiteID, Name: measure.Name, Kind: measure.Kind, Description: measure.Description, Evidence: measure.Evidence,
		Status: measure.Status, PlannedAt: measure.PlannedAt, ImplementedAt: measure.ImplementedAt, EndedAt: measure.EndedAt,
		InvestmentCNY: measure.InvestmentCNY, ExpectedAnnualKWh: measure.ExpectedAnnualKWh, Totals: totalsOf(results),
		CreatedAt: measure.CreatedAt, UpdatedAt: measure.UpdatedAt,
	}
	if baseline := measure.Baseline; baseline != nil {
		view.Baseline = &baselineView{
			From: baseline.From, To: baseline.To, InterceptKWh: baseline.Intercept, Slope: baseline.Slope, Samples: baseline.Samples,
			ExpectedHours: baseline.ExpectedHours, CVRMSE: baseline.CVRMSE, NMBE: baseline.NMBE, Trusted: baseline.Trusted, Reason: baseline.Reason,
		}
	}
	if withDays {
		view.Days = make([]dayView, 0, len(results))
		for _, result := range results {
			view.Days = append(view.Days, dayView{
				Day: result.Day, ExpectedHours: result.ExpectedHours, Hours: result.Hours, UnpricedHours: result.UnpricedHours,
				AdjustedBaselineKWh: result.AdjustedBaselineKWh, ActualKWh: result.ActualKWh, AvoidedKWh: result.AvoidedKWh, AvoidedCost: result.AvoidedCost,
			})
		}
	}
	return view
}

func (handler *HTTPHandler) list(writer http.ResponseWriter, request *http.Request) {
	tenantID, siteID := request.Header.Get(headerTenant), request.PathValue("siteId")
	measures, err := handler.service.store.Measures(request.Context(), tenantID, siteID)
	if err != nil {
		writeMVError(writer, err)
		return
	}
	items := make([]measureView, 0, len(measures))
	for _, measure := range measures {
		results, err := handler.service.store.DailyResults(request.Context(), tenantID, siteID, measure.ID)
		if err != nil {
			writeMVError(writer, err)
			return
		}
		items = append(items, viewOf(measure, results, false))
	}
	writeMVJSON(writer, http.StatusOK, map[string]any{"items": items})
}

func (handler *HTTPHandler) detail(writer http.ResponseWriter, request *http.Request) {
	tenantID, siteID := request.Header.Get(headerTenant), request.PathValue("siteId")
	measure, err := handler.service.store.Measure(request.Context(), tenantID, siteID, request.PathValue("measureId"))
	if err != nil {
		writeMVError(writer, err)
		return
	}
	results, err := handler.service.store.DailyResults(request.Context(), tenantID, siteID, measure.ID)
	if err != nil {
		writeMVError(writer, err)
		return
	}
	writeMVJSON(writer, http.StatusOK, viewOf(measure, results, true))
}

func (handler *HTTPHandler) register(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Name              string    `json:"name"`
		Kind              Kind      `json:"kind"`
		Description       string    `json:"description"`
		PlannedAt         time.Time `json:"plannedAt"`
		InvestmentCNY     *float64  `json:"investmentCny"`
		ExpectedAnnualKWh *float64  `json:"expectedAnnualKWh"`
		Evidence          string    `json:"evidence"`
	}
	if err := decodeMVJSON(request.Body, &input); err != nil {
		writeMVError(writer, ErrInvalidMeasure)
		return
	}
	measure, err := handler.service.Register(request.Context(), MeasureInput{
		TenantID: request.Header.Get(headerTenant), SiteID: request.PathValue("siteId"), Name: input.Name, Kind: input.Kind,
		Description: input.Description, PlannedAt: input.PlannedAt, InvestmentCNY: input.InvestmentCNY,
		ExpectedAnnualKWh: input.ExpectedAnnualKWh, Evidence: input.Evidence, Actor: request.Header.Get(headerActor),
	})
	if err != nil {
		writeMVError(writer, err)
		return
	}
	writeMVJSON(writer, http.StatusCreated, viewOf(measure, nil, true))
}

// transition runs a lifecycle step that takes one body and answers the measure afterwards.
func (handler *HTTPHandler) transition(writer http.ResponseWriter, request *http.Request, body any, step func() (Measure, error)) {
	if err := decodeMVJSON(request.Body, body); err != nil {
		writeMVError(writer, ErrInvalidMeasure)
		return
	}
	measure, err := step()
	if err != nil {
		writeMVError(writer, err)
		return
	}
	results, err := handler.service.store.DailyResults(request.Context(), measure.TenantID, measure.SiteID, measure.ID)
	if err != nil {
		writeMVError(writer, err)
		return
	}
	writeMVJSON(writer, http.StatusOK, viewOf(measure, results, true))
}

func (handler *HTTPHandler) implement(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		ImplementedAt time.Time `json:"implementedAt"`
	}
	handler.transition(writer, request, &input, func() (Measure, error) {
		return handler.service.Implement(request.Context(), request.Header.Get(headerTenant), request.PathValue("siteId"), request.PathValue("measureId"), input.ImplementedAt, request.Header.Get(headerActor))
	})
}

func (handler *HTTPHandler) refit(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		From time.Time `json:"from"`
		To   time.Time `json:"to"`
	}
	handler.transition(writer, request, &input, func() (Measure, error) {
		return handler.service.Refit(request.Context(), request.Header.Get(headerTenant), request.PathValue("siteId"), request.PathValue("measureId"), input.From, input.To, request.Header.Get(headerActor))
	})
}

func (handler *HTTPHandler) end(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		EndedAt time.Time `json:"endedAt"`
	}
	handler.transition(writer, request, &input, func() (Measure, error) {
		return handler.service.End(request.Context(), request.Header.Get(headerTenant), request.PathValue("siteId"), request.PathValue("measureId"), input.EndedAt, request.Header.Get(headerActor))
	})
}

// summary totals the Site's verified days in [from, to], given as Site-local dates.
func (handler *HTTPHandler) summary(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	from, to := query.Get("from"), query.Get("to")
	if _, err := time.Parse(time.DateOnly, from); err != nil {
		writeMVError(writer, ErrInvalidMeasure)
		return
	}
	if _, err := time.Parse(time.DateOnly, to); err != nil || to < from {
		writeMVError(writer, ErrInvalidMeasure)
		return
	}
	tenantID, siteID := request.Header.Get(headerTenant), request.PathValue("siteId")
	measures, err := handler.service.store.Measures(request.Context(), tenantID, siteID)
	if err != nil {
		writeMVError(writer, err)
		return
	}
	var days []DailyResult
	reporting := 0
	for _, measure := range measures {
		if measure.Status == StatusImplemented {
			reporting++
		}
		results, err := handler.service.store.DailyResults(request.Context(), tenantID, siteID, measure.ID)
		if err != nil {
			writeMVError(writer, err)
			return
		}
		for _, result := range results {
			if result.Day >= from && result.Day <= to {
				days = append(days, result)
			}
		}
	}
	writeMVJSON(writer, http.StatusOK, map[string]any{"from": from, "to": to, "measures": len(measures), "reporting": reporting, "totals": totalsOf(days)})
}

func decodeMVJSON(body io.Reader, target any) error {
	raw, err := io.ReadAll(io.LimitReader(body, maxMVRequestBytes+1))
	if err != nil || len(raw) == 0 || int64(len(raw)) > maxMVRequestBytes {
		return errors.New("request body is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("request contains trailing JSON")
	}
	return nil
}

func writeMVJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}

func writeMVError(writer http.ResponseWriter, err error) {
	status, code := http.StatusBadGateway, "savings_unavailable"
	switch {
	case errors.Is(err, ErrMeasureNotFound):
		status, code = http.StatusNotFound, "savings_measure_not_found"
	case errors.Is(err, ErrInvalidMeasure):
		status, code = http.StatusBadRequest, "savings_request_invalid"
	case errors.Is(err, ErrWrongStatus):
		status, code = http.StatusConflict, "savings_measure_state_conflict"
	case errors.Is(err, ErrReportingOverlap):
		status, code = http.StatusConflict, "savings_reporting_overlap"
	case errors.Is(err, ErrBaselineLocked):
		status, code = http.StatusConflict, "savings_baseline_locked"
	}
	writeMVJSON(writer, status, map[string]any{"code": code, "status": status})
}
