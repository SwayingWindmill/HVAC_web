package analyticsmodel

// ReportConditionAnchorsSummary summarizes outdoor weather, plant load ratio, and AI closed-loop
// control share averaged over a reporting window. Nil fields mean no finite anchor rows existed.
type ReportConditionAnchorsSummary struct {
	MeanWetBulbC      *float64 `json:"meanWetBulbC,omitempty"`
	MeanDryBulbC      *float64 `json:"meanDryBulbC,omitempty"`
	MeanLoadRatio     *float64 `json:"meanLoadRatio,omitempty"`
	AIClosedLoopShare *float64 `json:"aiClosedLoopShare,omitempty"`
}

// ReportEnrichmentResponse holds optional P2 monthly report diagnostics computed from ClickHouse
// layered-efficiency, meter-balance, and condition-anchor views.
type ReportEnrichmentResponse struct {
	LayeredEfficiency *LayeredEfficiency             `json:"layeredEfficiency,omitempty"`
	MeterBalance      *MeterBalanceSummary           `json:"meterBalance,omitempty"`
	ConditionAnchors  *ReportConditionAnchorsSummary `json:"conditionAnchors,omitempty"`
}

// HasMetrics reports whether any finite efficiency metric was derived for the period.
func (efficiency LayeredEfficiency) HasMetrics() bool {
	return isFinitePtr(efficiency.PlantCOP) ||
		isFinitePtr(efficiency.ChillerCOP) ||
		isFinitePtr(efficiency.ChilledPumpWTF) ||
		isFinitePtr(efficiency.CoolingPumpWTF) ||
		isFinitePtr(efficiency.TowerPowerShare)
}

// HasValues reports whether any finite operating-condition anchor was derived for the period.
func (anchors ReportConditionAnchorsSummary) HasValues() bool {
	return isFinitePtr(anchors.MeanWetBulbC) ||
		isFinitePtr(anchors.MeanDryBulbC) ||
		isFinitePtr(anchors.MeanLoadRatio) ||
		isFinitePtr(anchors.AIClosedLoopShare)
}

func isFinitePtr(value *float64) bool {
	return value != nil && isFinite(*value)
}
