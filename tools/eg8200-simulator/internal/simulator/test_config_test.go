package simulator

func testConfig() Config {
	plant := testPlantConfig()
	areas := []AreaConfig{
		{ID: "building", Name: "Commercial Building", Type: "BUILDING"},
		{ID: "plant-room", ParentID: "building", Name: "Central Plant Room", Type: "PLANT_ROOM"},
		{ID: "rooftop", ParentID: "building", Name: "Rooftop", Type: "ROOFTOP"},
		{ID: "outdoor", ParentID: "building", Name: "Outdoor", Type: "OUTDOOR"},
	}
	equipment := []EquipmentAssetConfig{
		{ID: "equipment-chiller-01", AreaID: "plant-room", Name: "Chiller 01", Type: "CHILLER"},
		{ID: "equipment-chwp-01", AreaID: "plant-room", Name: "Chilled Water Pump 01", Type: "CHILLED_WATER_PUMP"},
		{ID: "equipment-cwp-01", AreaID: "plant-room", Name: "Condenser Water Pump 01", Type: "COOLING_WATER_PUMP"},
		{ID: "equipment-ct-01", AreaID: "rooftop", Name: "Cooling Tower 01", Type: "COOLING_TOWER"},
		{ID: "equipment-meter-01", AreaID: "plant-room", Name: "HVAC Power Meter", Type: "HVAC_POWER_METER"},
		{ID: "equipment-btu-01", AreaID: "plant-room", Name: "BTU Meter 01", Type: "BTU_METER"},
		{ID: "equipment-weather-01", AreaID: "outdoor", Name: "Weather Station 01", Type: "WEATHER_STATION"},
	}
	devices := []DeviceEndpointConfig{
		{ID: plant.Chiller.ID, AreaID: "plant-room", Name: "Chiller Controller 01", Type: "CHILLER_CONTROLLER", EquipmentIDs: []string{"equipment-chiller-01"}},
		{ID: plant.ChilledWaterPump.ID, AreaID: "plant-room", Name: "CHWP Drive 01", Type: "PUMP_CONTROLLER", EquipmentIDs: []string{"equipment-chwp-01"}},
		{ID: plant.CoolingWaterPump.ID, AreaID: "plant-room", Name: "CWP Drive 01", Type: "PUMP_CONTROLLER", EquipmentIDs: []string{"equipment-cwp-01"}},
		{ID: plant.CoolingTower.ID, AreaID: "rooftop", Name: "Cooling Tower Controller 01", Type: "COOLING_TOWER_CONTROLLER", EquipmentIDs: []string{"equipment-ct-01"}},
		{ID: plant.PowerMeterID, AreaID: "plant-room", Name: "HVAC Power Meter", Type: "POWER_METER", EquipmentIDs: []string{"equipment-meter-01"}},
		{ID: plant.BTUMeterID, AreaID: "plant-room", Name: "BTU Meter 01", Type: "BTU_METER", EquipmentIDs: []string{"equipment-btu-01"}},
		{ID: plant.WeatherStationID, AreaID: "outdoor", Name: "Weather Station 01", Type: "WEATHER_STATION", EquipmentIDs: []string{"equipment-weather-01"}},
	}
	sensors := []SensorConfig{
		{ID: "sensor-chws", DeviceID: plant.Chiller.ID, MountedAreaID: "plant-room", Name: "Leaving Chilled Water Temperature Probe", Type: "TEMPERATURE", SerialNumber: "TEST-PT1000-001", CalibrationDueAt: "2027-08-01T00:00:00Z"},
	}
	point := func(deviceID, sensorID, equipmentID, sourceKey, telemetryKey, pointCode, name, unit, sample, publish string) PointConfig {
		return PointConfig{
			DeviceID:        deviceID,
			SensorID:        sensorID,
			SubjectType:     "EQUIPMENT",
			SubjectID:       equipmentID,
			SourceKey:       sourceKey,
			TelemetryKey:    telemetryKey,
			PointCode:       pointCode,
			Name:            name,
			PointType:       "TELEMETRY",
			ValueType:       "NUMBER",
			Unit:            unit,
			SampleInterval:  sample,
			PublishInterval: publish,
			StaleAfter:      "15s",
			SourceProtocol:  "SIMULATED",
			SourceAddress:   deviceID + ":" + sourceKey,
		}
	}
	points := []PointConfig{
		point(plant.Chiller.ID, "sensor-chws", "equipment-chiller-01", "leavingChilledWaterTemperatureC", "chiller.leaving_chilled_water_temperature", "leaving_chilled_water_temperature", "Leaving Chilled Water Temperature", "Cel", "1s", "2s"),
		point(plant.Chiller.ID, "", "equipment-chiller-01", "powerKw", "chiller.power", "power", "Chiller Power", "kW", "2s", "5s"),
		point(plant.ChilledWaterPump.ID, "", "equipment-chwp-01", "powerKw", "chwp.power", "power", "CHWP Power", "kW", "1s", "2s"),
		point(plant.CoolingWaterPump.ID, "", "equipment-cwp-01", "powerKw", "cwp.power", "power", "CWP Power", "kW", "1s", "2s"),
		point(plant.CoolingTower.ID, "", "equipment-ct-01", "powerKw", "cooling_tower.power", "power", "Cooling Tower Power", "kW", "1s", "2s"),
		point(plant.PowerMeterID, "", "equipment-meter-01", "activePowerKw", "hvac_meter.active_power", "active_power", "HVAC Active Power", "kW", "1s", "2s"),
		point(plant.BTUMeterID, "", "equipment-btu-01", "instantCoolingCapacityKw", "btu_meter.instant_cooling_capacity", "instant_cooling_capacity", "Cooling Capacity", "kW", "1s", "2s"),
		{DeviceID: plant.WeatherStationID, SubjectType: "SITE", SourceKey: "ambientDryBulbTemperatureC", TelemetryKey: "weather.ambient_dry_bulb_temperature", PointCode: "ambient_dry_bulb_temperature", Name: "Outdoor Dry Bulb Temperature", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", SampleInterval: "2s", PublishInterval: "5s", StaleAfter: "15s", SourceProtocol: "SIMULATED", SourceAddress: plant.WeatherStationID + ":ambientDryBulbTemperatureC"},
		{DeviceID: plant.WeatherStationID, SubjectType: "SITE", SourceKey: "ambientWetBulbTemperatureC", TelemetryKey: "weather.ambient_wet_bulb_temperature", PointCode: "ambient_wet_bulb_temperature", Name: "Outdoor Wet Bulb Temperature", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "Cel", SampleInterval: "2s", PublishInterval: "5s", StaleAfter: "15s", SourceProtocol: "SIMULATED", SourceAddress: plant.WeatherStationID + ":ambientWetBulbTemperatureC"},
		{DeviceID: plant.WeatherStationID, SubjectType: "SITE", SourceKey: "relativeHumidityPct", TelemetryKey: "weather.relative_humidity", PointCode: "relative_humidity", Name: "Outdoor Relative Humidity", PointType: "TELEMETRY", ValueType: "NUMBER", Unit: "%RH", SampleInterval: "2s", PublishInterval: "5s", StaleAfter: "15s", SourceProtocol: "SIMULATED", SourceAddress: plant.WeatherStationID + ":relativeHumidityPct"},
	}
	return Config{
		SchemaVersion:   ConfigSchemaVersion,
		GatewayID:       "EG8200-VIRTUAL-001",
		PublishInterval: "5s",
		Plant:           plant,
		Areas:           areas,
		Equipment:       equipment,
		Devices:         devices,
		Sensors:         sensors,
		Points:          points,
	}
}
