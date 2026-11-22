package simulator

import (
	"encoding/json"
	"net/http"
	"time"
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
	RegisterAcceptanceFaultEndpoints(mux, plant)
}

// RegisterAcceptanceFaultEndpoints registers HTTP fault-injection endpoints for thermal lag,
// LOCAL/REMOTE cabinet mode, read-only locked registers, and hydraulic interlock switches.
func RegisterAcceptanceFaultEndpoints(mux *http.ServeMux, plant *Plant) {
	mux.HandleFunc("PUT /acceptance/plant/thermal-lag", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Seconds int `json:"seconds"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(writer, "invalid thermal-lag payload", http.StatusBadRequest)
			return
		}
		if err := plant.SetThermalLag(time.Duration(body.Seconds) * time.Second); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("PUT /acceptance/plant/cabinet-mode", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			DeviceID string `json:"deviceId"`
			Mode     string `json:"mode"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(writer, "invalid cabinet-mode payload", http.StatusBadRequest)
			return
		}
		if !plant.SetControlCabinetMode(body.DeviceID, body.Mode) {
			http.Error(writer, "unknown deviceId or unsupported cabinet mode", http.StatusBadRequest)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("PUT /acceptance/plant/register-readonly-lock", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			DeviceID string `json:"deviceId"`
			Active   bool   `json:"active"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(writer, "invalid register-readonly-lock payload", http.StatusBadRequest)
			return
		}
		if !plant.SetRegisterReadOnlyLock(body.DeviceID, body.Active) {
			http.Error(writer, "unknown deviceId", http.StatusBadRequest)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("PUT /acceptance/plant/hydraulic-interlock", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			CHWValveOpen         bool `json:"chwValveOpen"`
			CWValveOpen          bool `json:"cwValveOpen"`
			CHWFlowSwitchBlocked bool `json:"chwFlowSwitchBlocked"`
			CWFlowSwitchBlocked  bool `json:"cwFlowSwitchBlocked"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			http.Error(writer, "invalid hydraulic-interlock payload", http.StatusBadRequest)
			return
		}
		plant.SetHydraulicInterlockFault(body.CHWValveOpen, body.CWValveOpen, body.CHWFlowSwitchBlocked, body.CWFlowSwitchBlocked)
		writer.WriteHeader(http.StatusNoContent)
	})
}
