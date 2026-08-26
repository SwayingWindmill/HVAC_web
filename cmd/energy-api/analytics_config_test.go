package main

import "testing"

func TestLoadAnalyticsConfigIsOptional(t *testing.T) {
	t.Setenv("TELEMETRY_QUERY_URL", "")
	config, err := loadAnalyticsConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if config != nil {
		t.Fatalf("config=%#v", config)
	}
}

func TestLoadAnalyticsConfigRejectsInsecureOrPathBasedURL(t *testing.T) {
	for _, value := range []string{"http://query.example.test", "https://query.example.test/internal"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TELEMETRY_QUERY_URL", value)
			if _, err := loadAnalyticsConfig(nil); err == nil {
				t.Fatal("loadAnalyticsConfig() error = nil")
			}
		})
	}
}

func TestLoadAnalyticsConfigRequiresGatewayWorkloadCertificate(t *testing.T) {
	t.Setenv("TELEMETRY_QUERY_URL", "https://query.example.test")
	_, err := loadAnalyticsConfig(nil)
	if err == nil {
		t.Fatal("loadAnalyticsConfig() error = nil")
	}
}
