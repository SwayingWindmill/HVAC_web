package mqttclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
)

// TLSConfig configures mutual TLS (TLS 1.3 minimum) for MQTT broker connections.
type TLSConfig struct {
	CACertFile     string
	ClientCertFile string
	ClientKeyFile  string
	ServerName     string
}

// NewMTLSConfig builds a tls.Config enforcing TLS 1.3 with client certificate
// authentication and an explicit CA root pool.
func NewMTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	certFile := strings.TrimSpace(cfg.ClientCertFile)
	keyFile := strings.TrimSpace(cfg.ClientKeyFile)
	caFile := strings.TrimSpace(cfg.CACertFile)
	if certFile == "" {
		return nil, errors.New("mqtt client certificate file is required")
	}
	if keyFile == "" {
		return nil, errors.New("mqtt client key file is required")
	}
	if caFile == "" {
		return nil, errors.New("mqtt ca certificate file is required")
	}

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load mqtt client certificate: %w", err)
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read mqtt ca certificate: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("parse mqtt ca certificate")
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		ServerName:   strings.TrimSpace(cfg.ServerName),
	}, nil
}

// ValidateShareGroup verifies an MQTT 5.0 shared subscription group name.
// Empty string is valid and disables shared subscriptions.
func ValidateShareGroup(group string) error {
	if group == "" {
		return nil
	}
	if strings.TrimSpace(group) != group {
		return errors.New("mqtt shared subscription group must not have leading or trailing whitespace")
	}
	if strings.ContainsAny(group, "/+#\x00") {
		return errors.New("mqtt shared subscription group must not contain '/', '+', '#', or NUL")
	}
	return nil
}

// SharedTopicFilter wraps filter in an MQTT 5.0 $share/<group>/<filter> subscription
// when group is non-empty and filter does not already start with "$share/".
func SharedTopicFilter(group, filter string) string {
	group = strings.TrimSpace(group)
	if group == "" || filter == "" || strings.HasPrefix(filter, "$share/") {
		return filter
	}
	return "$share/" + group + "/" + filter
}

// SharedTopicFilters applies SharedTopicFilter across a slice of topic filters.
func SharedTopicFilters(group string, filters []string) []string {
	out := make([]string, len(filters))
	for i, f := range filters {
		out[i] = SharedTopicFilter(group, f)
	}
	return out
}

// StripSharePrefix removes the MQTT 5.0 "$share/<group>/" prefix from a filter if present.
func StripSharePrefix(filter string) string {
	const prefix = "$share/"
	if !strings.HasPrefix(filter, prefix) {
		return filter
	}
	rest := strings.TrimPrefix(filter, prefix)
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return filter
	}
	return rest[slash+1:]
}

// MatchTopicFilter reports whether an MQTT topic matches filter according to MQTT
// wildcard rules (+, #, $-prefix exclusion), after stripping any $share/<group>/ prefix.
func MatchTopicFilter(filter, topic string) bool {
	filter = StripSharePrefix(filter)
	if filter == "" || topic == "" {
		return false
	}
	fLevels := strings.Split(filter, "/")
	tLevels := strings.Split(topic, "/")

	// MQTT 5.0 §4.7.2: wildcards at the first level do not match topics beginning with '$'.
	if strings.HasPrefix(topic, "$") && (fLevels[0] == "+" || fLevels[0] == "#") {
		return false
	}

	for i, fl := range fLevels {
		if fl == "#" {
			return i == len(fLevels)-1
		}
		if i >= len(tLevels) {
			return false
		}
		if fl != "+" && fl != tLevels[i] {
			return false
		}
	}
	return len(fLevels) == len(tLevels)
}
