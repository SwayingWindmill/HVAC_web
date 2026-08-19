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
		plant.chiller.revision++
	case plant.config.ChilledWaterPump.ID:
		plant.chilledWaterPump.faultCode = strings.TrimSpace(faultCode)
		plant.chilledWaterPump.revision++
	case plant.config.CoolingWaterPump.ID:
		plant.coolingWaterPump.faultCode = strings.TrimSpace(faultCode)
		plant.coolingWaterPump.revision++
	case plant.config.CoolingTower.ID:
		plant.coolingTower.faultCode = strings.TrimSpace(faultCode)
		plant.coolingTower.revision++
	default:
		return false
	}
	return true
}

func (plant *Plant) applyChillerCommand(method string, params map[string]float64) CommandResult {
	switch method {
	case "start":
		if len(params) != 0 {
			return invalidParameters(plant.chiller.revision)
		}
		if plant.chiller.faultCode != "" {
			return CommandResult{Success: false, Code: "FAULT_ACTIVE", BusinessRevision: plant.chiller.revision}
		}
		if !plant.chilledWaterPump.running || plant.chilledWaterPump.faultCode != "" ||
			!plant.coolingWaterPump.running || plant.coolingWaterPump.faultCode != "" ||
			!plant.coolingTower.running || plant.coolingTower.faultCode != "" {
			return CommandResult{Success: false, Code: "INTERLOCK_OPEN", BusinessRevision: plant.chiller.revision}
		}
		plant.chiller.running = true
		plant.chiller.revision++
		return CommandResult{Success: true, Code: "APPLIED", BusinessRevision: plant.chiller.revision}
	case "stop":
		if len(params) != 0 {
			return invalidParameters(plant.chiller.revision)
		}
		plant.chiller.running = false
		plant.chiller.revision++
		return CommandResult{Success: true, Code: "APPLIED", BusinessRevision: plant.chiller.revision}
	case "setChilledWaterTemperatureSetpoint":
		value, ok := singleCommandParam(params, "setpointC")
		if !ok {
			return invalidParameters(plant.chiller.revision)
		}
		if value < 5 || value > 12 {
			return CommandResult{Success: false, Code: "SETPOINT_OUT_OF_RANGE", BusinessRevision: plant.chiller.revision}
		}
		plant.chiller.setpointC = value
		plant.chiller.revision++
		return CommandResult{Success: true, Code: "APPLIED", AppliedValue: value, BusinessRevision: plant.chiller.revision}
	case "setLoadLimit":
		value, ok := singleCommandParam(params, "loadLimitPct")
		if !ok {
			return invalidParameters(plant.chiller.revision)
		}
		if value < 20 || value > 100 {
			return CommandResult{Success: false, Code: "LOAD_LIMIT_OUT_OF_RANGE", BusinessRevision: plant.chiller.revision}
		}
		plant.chiller.loadLimitPct = value
		plant.chiller.revision++
		return CommandResult{Success: true, Code: "APPLIED", AppliedValue: value, BusinessRevision: plant.chiller.revision}
	case "resetFault":
		if len(params) != 0 {
			return invalidParameters(plant.chiller.revision)
		}
		plant.chiller.faultCode = ""
		plant.chiller.revision++
		return CommandResult{Success: true, Code: "APPLIED", BusinessRevision: plant.chiller.revision}
	default:
		return CommandResult{Success: false, Code: "COMMAND_UNSUPPORTED", BusinessRevision: plant.chiller.revision}
	}
}

func applyPumpCommand(state *pumpState, method string, params map[string]float64) CommandResult {
	switch method {
	case "start":
		if len(params) != 0 {
			return invalidParameters(state.revision)
		}
		if state.faultCode != "" {
			return CommandResult{Success: false, Code: "FAULT_ACTIVE", BusinessRevision: state.revision}
		}
		state.running = true
		state.revision++
		return CommandResult{Success: true, Code: "APPLIED", BusinessRevision: state.revision}
	case "stop":
		if len(params) != 0 {
			return invalidParameters(state.revision)
		}
		state.running = false
		state.revision++
		return CommandResult{Success: true, Code: "APPLIED", BusinessRevision: state.revision}
	case "setFrequency":
		value, ok := singleCommandParam(params, "frequencyHz")
		if !ok {
			return invalidParameters(state.revision)
		}
		if value < 20 || value > 50 {
			return CommandResult{Success: false, Code: "FREQUENCY_OUT_OF_RANGE", BusinessRevision: state.revision}
		}
		state.frequencyHz = value
		state.revision++
		return CommandResult{Success: true, Code: "APPLIED", AppliedValue: value, BusinessRevision: state.revision}
	case "resetFault":
		if len(params) != 0 {
			return invalidParameters(state.revision)
		}
		state.faultCode = ""
		state.revision++
		return CommandResult{Success: true, Code: "APPLIED", BusinessRevision: state.revision}
	default:
		return CommandResult{Success: false, Code: "COMMAND_UNSUPPORTED", BusinessRevision: state.revision}
	}
}

func (plant *Plant) applyCoolingTowerCommand(method string, params map[string]float64) CommandResult {
	switch method {
	case "start":
		if len(params) != 0 {
			return invalidParameters(plant.coolingTower.revision)
		}
		if plant.coolingTower.faultCode != "" {
			return CommandResult{Success: false, Code: "FAULT_ACTIVE", BusinessRevision: plant.coolingTower.revision}
		}
		plant.coolingTower.running = true
		plant.coolingTower.revision++
		return CommandResult{Success: true, Code: "APPLIED", BusinessRevision: plant.coolingTower.revision}
	case "stop":
		if len(params) != 0 {
			return invalidParameters(plant.coolingTower.revision)
		}
		plant.coolingTower.running = false
		plant.coolingTower.revision++
		return CommandResult{Success: true, Code: "APPLIED", BusinessRevision: plant.coolingTower.revision}
	case "setFanSpeed":
		value, ok := singleCommandParam(params, "fanSpeedPct")
		if !ok {
			return invalidParameters(plant.coolingTower.revision)
		}
		if value < 20 || value > 100 {
			return CommandResult{Success: false, Code: "FAN_SPEED_OUT_OF_RANGE", BusinessRevision: plant.coolingTower.revision}
		}
		plant.coolingTower.fanSpeedPct = value
		plant.coolingTower.revision++
		return CommandResult{Success: true, Code: "APPLIED", AppliedValue: value, BusinessRevision: plant.coolingTower.revision}
	case "resetFault":
		if len(params) != 0 {
			return invalidParameters(plant.coolingTower.revision)
		}
		plant.coolingTower.faultCode = ""
		plant.coolingTower.revision++
		return CommandResult{Success: true, Code: "APPLIED", BusinessRevision: plant.coolingTower.revision}
	default:
		return CommandResult{Success: false, Code: "COMMAND_UNSUPPORTED", BusinessRevision: plant.coolingTower.revision}
	}
}

func singleCommandParam(params map[string]float64, key string) (float64, bool) {
	if len(params) != 1 {
		return 0, false
	}
	value, ok := params[key]
	return value, ok
}

func invalidParameters(revision uint64) CommandResult {
	return CommandResult{Success: false, Code: "INVALID_PARAMETERS", BusinessRevision: revision}
}
