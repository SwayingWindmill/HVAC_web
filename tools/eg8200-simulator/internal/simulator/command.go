package simulator

import (
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/edgecontrol"
)

func (plant *Plant) ReadCommandTarget(deviceID, method string) (edgecontrol.Value, time.Time, bool) {
	plant.mu.RLock()
	defer plant.mu.RUnlock()
	readAt := plant.now
	if readAt.IsZero() {
		readAt = time.Now().UTC()
	}
	method = strings.TrimSpace(method)
	switch deviceID {
	case plant.config.Chiller.ID:
		switch method {
		case "start":
			return edgecontrol.BooleanValue(plant.chiller.runRequested), readAt, true
		case "stop":
			return edgecontrol.BooleanValue(!plant.chiller.runRequested), readAt, true
		case "startUnitSequence":
			allRequested := plant.chiller.runRequested && plant.chilledWaterPump.runRequested && plant.coolingWaterPump.runRequested && plant.coolingTower.runRequested
			return edgecontrol.BooleanValue(allRequested), readAt, true
		case "stopUnitSequence":
			allStopped := !plant.chiller.runRequested && !plant.chilledWaterPump.runRequested && !plant.coolingWaterPump.runRequested && !plant.coolingTower.runRequested
			return edgecontrol.BooleanValue(allStopped), readAt, true
		case "setChilledWaterTemperatureSetpoint":
			return edgecontrol.DoubleValue(plant.chiller.setpointC), readAt, true
		case "setLoadLimit":
			return edgecontrol.DoubleValue(plant.chiller.loadLimitPct), readAt, true
		case "resetFault":
			return edgecontrol.BooleanValue(plant.chiller.faultCode == ""), readAt, true
		}
	case plant.config.ChilledWaterPump.ID:
		return readPumpCommandTarget(plant.chilledWaterPump, method, readAt)
	case plant.config.CoolingWaterPump.ID:
		return readPumpCommandTarget(plant.coolingWaterPump, method, readAt)
	case plant.config.CoolingTower.ID:
		switch method {
		case "start":
			return edgecontrol.BooleanValue(plant.coolingTower.runRequested), readAt, true
		case "stop":
			return edgecontrol.BooleanValue(!plant.coolingTower.runRequested), readAt, true
		case "setFanSpeed":
			return edgecontrol.DoubleValue(plant.coolingTower.fanSpeedSetpointPct), readAt, true
		case "resetFault":
			return edgecontrol.BooleanValue(plant.coolingTower.faultCode == ""), readAt, true
		}
	}
	return edgecontrol.Value{}, time.Time{}, false
}

func readPumpCommandTarget(state pumpState, method string, readAt time.Time) (edgecontrol.Value, time.Time, bool) {
	switch method {
	case "start":
		return edgecontrol.BooleanValue(state.runRequested), readAt, true
	case "stop":
		return edgecontrol.BooleanValue(!state.runRequested), readAt, true
	case "setFrequency":
		return edgecontrol.DoubleValue(state.frequencySetpointHz), readAt, true
	case "resetFault":
		return edgecontrol.BooleanValue(state.faultCode == ""), readAt, true
	default:
		return edgecontrol.Value{}, time.Time{}, false
	}
}

func (plant *Plant) ApplyCommand(command Command) CommandResult {
	plant.mu.Lock()
	defer plant.mu.Unlock()
	method := strings.TrimSpace(command.Method)
	switch command.DeviceID {
	case plant.config.Chiller.ID:
		if plant.chiller.localRemoteMode == ControlCabinetModeLocal {
			return CommandResult{Success: false, Code: "CABINET_LOCAL_MANUAL"}
		}
		return plant.applyChillerCommand(method, command.Params)
	case plant.config.ChilledWaterPump.ID:
		if plant.chilledWaterPump.localRemoteMode == ControlCabinetModeLocal {
			return CommandResult{Success: false, Code: "CABINET_LOCAL_MANUAL"}
		}
		return applyPumpCommand(&plant.chilledWaterPump, method, command.Params)
	case plant.config.CoolingWaterPump.ID:
		if plant.coolingWaterPump.localRemoteMode == ControlCabinetModeLocal {
			return CommandResult{Success: false, Code: "CABINET_LOCAL_MANUAL"}
		}
		return applyPumpCommand(&plant.coolingWaterPump, method, command.Params)
	case plant.config.CoolingTower.ID:
		if plant.coolingTower.localRemoteMode == ControlCabinetModeLocal {
			return CommandResult{Success: false, Code: "CABINET_LOCAL_MANUAL"}
		}
		return plant.applyCoolingTowerCommand(method, command.Params)
	default:
		return CommandResult{Success: false, Code: "DEVICE_NOT_CONTROLLABLE"}
	}
}

func (plant *Plant) SetFault(deviceID, faultCode string) bool {
	plant.mu.Lock()
	defer plant.mu.Unlock()
	switch deviceID {
	case plant.config.Chiller.ID:
		plant.chiller.faultCode = strings.TrimSpace(faultCode)
	case plant.config.ChilledWaterPump.ID:
		plant.chilledWaterPump.faultCode = strings.TrimSpace(faultCode)
	case plant.config.CoolingWaterPump.ID:
		plant.coolingWaterPump.faultCode = strings.TrimSpace(faultCode)
	case plant.config.CoolingTower.ID:
		plant.coolingTower.faultCode = strings.TrimSpace(faultCode)
	default:
		return false
	}
	return true
}

func (plant *Plant) applyChillerCommand(method string, params map[string]float64) CommandResult {
	switch method {
	case "start":
		if len(params) != 0 {
			return invalidParameters()
		}
		if plant.chiller.faultCode != "" {
			return CommandResult{Success: false, Code: "FAULT_ACTIVE"}
		}
		if !plant.chilledWaterPump.runRequested || !plant.chilledWaterPump.running || plant.chilledWaterPump.faultCode != "" ||
			!plant.coolingWaterPump.runRequested || !plant.coolingWaterPump.running || plant.coolingWaterPump.faultCode != "" ||
			!plant.coolingTower.runRequested || !plant.coolingTower.running || plant.coolingTower.faultCode != "" {
			return CommandResult{Success: false, Code: "INTERLOCK_OPEN"}
		}
		if !plant.chwValveOpen || !plant.cwValveOpen || !plant.chwFlowSwitchProvenLocked() || !plant.cwFlowSwitchProvenLocked() {
			return CommandResult{Success: false, Code: "CHILLER_HYDRAULIC_INTERLOCK_NOT_PROVEN"}
		}
		plant.chiller.runRequested = true
		return CommandResult{Success: true, Code: "APPLIED"}
	case "stop":
		if len(params) != 0 {
			return invalidParameters()
		}
		plant.chiller.runRequested = false
		return CommandResult{Success: true, Code: "APPLIED"}
	case "startUnitSequence":
		if len(params) != 0 {
			return invalidParameters()
		}
		if plant.chiller.faultCode != "" || plant.chilledWaterPump.faultCode != "" ||
			plant.coolingWaterPump.faultCode != "" || plant.coolingTower.faultCode != "" {
			return CommandResult{Success: false, Code: "FAULT_ACTIVE"}
		}
		if !plant.chwValveOpen || !plant.cwValveOpen || plant.chwFlowSwitchBlocked || plant.cwFlowSwitchBlocked {
			return CommandResult{Success: false, Code: "CHILLER_HYDRAULIC_INTERLOCK_NOT_PROVEN"}
		}
		plant.chilledWaterPump.runRequested = true
		plant.coolingWaterPump.runRequested = true
		plant.coolingTower.runRequested = true
		plant.chiller.runRequested = true
		return CommandResult{Success: true, Code: "APPLIED"}
	case "stopUnitSequence":
		if len(params) != 0 {
			return invalidParameters()
		}
		plant.chiller.runRequested = false
		plant.coolingTower.runRequested = false
		plant.coolingWaterPump.runRequested = false
		plant.chilledWaterPump.runRequested = false
		return CommandResult{Success: true, Code: "APPLIED"}
	case "setChilledWaterTemperatureSetpoint":
		value, ok := singleCommandParam(params, "setpointC")
		if !ok {
			return invalidParameters()
		}
		if value < 5 || value > 12 {
			return CommandResult{Success: false, Code: "SETPOINT_OUT_OF_RANGE"}
		}
		plant.chiller.setpointC = value
		return CommandResult{Success: true, Code: "APPLIED", AppliedValue: value}
	case "setLoadLimit":
		value, ok := singleCommandParam(params, "loadLimitPct")
		if !ok {
			return invalidParameters()
		}
		if value < 20 || value > 100 {
			return CommandResult{Success: false, Code: "LOAD_LIMIT_OUT_OF_RANGE"}
		}
		plant.chiller.loadLimitPct = value
		return CommandResult{Success: true, Code: "APPLIED", AppliedValue: value}
	case "resetFault":
		if len(params) != 0 {
			return invalidParameters()
		}
		plant.chiller.faultCode = ""
		return CommandResult{Success: true, Code: "APPLIED"}
	default:
		return CommandResult{Success: false, Code: "COMMAND_UNSUPPORTED"}
	}
}

func applyPumpCommand(state *pumpState, method string, params map[string]float64) CommandResult {
	switch method {
	case "start":
		if len(params) != 0 {
			return invalidParameters()
		}
		if state.faultCode != "" {
			return CommandResult{Success: false, Code: "FAULT_ACTIVE"}
		}
		state.runRequested = true
		return CommandResult{Success: true, Code: "APPLIED"}
	case "stop":
		if len(params) != 0 {
			return invalidParameters()
		}
		state.runRequested = false
		return CommandResult{Success: true, Code: "APPLIED"}
	case "setFrequency":
		value, ok := singleCommandParam(params, "frequencyHz")
		if !ok {
			return invalidParameters()
		}
		if value < 20 || value > 50 {
			return CommandResult{Success: false, Code: "FREQUENCY_OUT_OF_RANGE"}
		}
		state.frequencySetpointHz = value
		return CommandResult{Success: true, Code: "APPLIED", AppliedValue: value}
	case "resetFault":
		if len(params) != 0 {
			return invalidParameters()
		}
		state.faultCode = ""
		return CommandResult{Success: true, Code: "APPLIED"}
	default:
		return CommandResult{Success: false, Code: "COMMAND_UNSUPPORTED"}
	}
}

func (plant *Plant) applyCoolingTowerCommand(method string, params map[string]float64) CommandResult {
	switch method {
	case "start":
		if len(params) != 0 {
			return invalidParameters()
		}
		if plant.coolingTower.faultCode != "" {
			return CommandResult{Success: false, Code: "FAULT_ACTIVE"}
		}
		plant.coolingTower.runRequested = true
		return CommandResult{Success: true, Code: "APPLIED"}
	case "stop":
		if len(params) != 0 {
			return invalidParameters()
		}
		plant.coolingTower.runRequested = false
		return CommandResult{Success: true, Code: "APPLIED"}
	case "setFanSpeed":
		value, ok := singleCommandParam(params, "fanSpeedPct")
		if !ok {
			return invalidParameters()
		}
		if value < 20 || value > 100 {
			return CommandResult{Success: false, Code: "FAN_SPEED_OUT_OF_RANGE"}
		}
		plant.coolingTower.fanSpeedSetpointPct = value
		return CommandResult{Success: true, Code: "APPLIED", AppliedValue: value}
	case "resetFault":
		if len(params) != 0 {
			return invalidParameters()
		}
		plant.coolingTower.faultCode = ""
		return CommandResult{Success: true, Code: "APPLIED"}
	default:
		return CommandResult{Success: false, Code: "COMMAND_UNSUPPORTED"}
	}
}

func singleCommandParam(params map[string]float64, key string) (float64, bool) {
	if len(params) != 1 {
		return 0, false
	}
	value, ok := params[key]
	return value, ok
}

func invalidParameters() CommandResult {
	return CommandResult{Success: false, Code: "INVALID_PARAMETERS"}
}
