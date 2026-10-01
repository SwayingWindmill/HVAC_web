package simulator

import "strings"

func (plant *Plant) ApplyCommand(command Command) CommandResult {
	plant.mu.Lock()
	defer plant.mu.Unlock()
	method := strings.TrimSpace(command.Method)
	switch command.DeviceID {
	case plant.config.Chiller.ID:
		return plant.applyChillerCommand(method, command.Params)
	case plant.config.ChilledWaterPump.ID:
		return applyPumpCommand(&plant.chilledWaterPump, method, command.Params)
	case plant.config.CoolingWaterPump.ID:
		return applyPumpCommand(&plant.coolingWaterPump, method, command.Params)
	case plant.config.CoolingTower.ID:
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
		plant.chiller.runRequested = true
		return CommandResult{Success: true, Code: "APPLIED"}
	case "stop":
		if len(params) != 0 {
			return invalidParameters()
		}
		plant.chiller.runRequested = false
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
