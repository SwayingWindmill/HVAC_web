package simulator

import (
	"encoding/json"
	"net/http"
)

// RegisterCHWPDisturbance applies acceptance faults to the telemetry-producing plant.
func RegisterCHWPDisturbance(mux *http.ServeMux, plant *Plant) {
	mux.HandleFunc("PUT /acceptance/chwp/stuck-high", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Active bool `json:"active"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(writer, "invalid disturbance payload", http.StatusBadRequest)
			return
		}
		plant.SetCHWPStuckHighDisturbance(body.Active)
		writer.WriteHeader(http.StatusNoContent)
	})
}
