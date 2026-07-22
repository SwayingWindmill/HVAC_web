package main

import "testing"

func TestValidatePrivateServiceURLRequiresHTTPSOrigin(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "https origin", value: "https://core.example.test"},
		{name: "https origin trailing slash", value: "https://core.example.test/"},
		{name: "http rejected", value: "http://core.example.test", wantErr: true},
		{name: "credentials rejected", value: "https://user:password@core.example.test", wantErr: true},
		{name: "path rejected", value: "https://core.example.test/private", wantErr: true},
		{name: "query rejected", value: "https://core.example.test?target=legacy", wantErr: true},
		{name: "relative rejected", value: "core.example.test", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePrivateServiceURL(test.value, "CORE_URL")
			if (err != nil) != test.wantErr {
				t.Fatalf("validatePrivateServiceURL(%q) error=%v", test.value, err)
			}
		})
	}
}
