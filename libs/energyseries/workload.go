package energyseries

import (
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/quanlaihe/hvac-web/libs/workloadtls"
)

// WorkloadConfig is a workload's mutual-TLS identity and where IAM and the query service listen.
type WorkloadConfig struct {
	CertificatePath string
	PrivateKeyPath  string
	CAPath          string
	IAMURL          string
	QueryURL        string
}

// NewWorkload is a Client that presents the workload's own identity to IAM and to the query service.
func NewWorkload(config WorkloadConfig) (*Client, error) {
	certificate := &workloadtls.CertificateFiles{CertificatePath: config.CertificatePath, PrivateKeyPath: config.PrivateKeyPath}
	client := func(origin string) (*http.Client, error) {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Hostname() == "" {
			return nil, fmt.Errorf("invalid service URL %q", origin)
		}
		return workloadtls.NewHTTPClient(workloadtls.ClientConfig{
			CertificateFiles: certificate, ServerCAPath: config.CAPath, ServerName: parsed.Hostname(), Timeout: 30 * time.Second,
		})
	}
	iamClient, err := client(config.IAMURL)
	if err != nil {
		return nil, err
	}
	queryClient, err := client(config.QueryURL)
	if err != nil {
		return nil, err
	}
	return New(Config{IAMURL: config.IAMURL, QueryURL: config.QueryURL, IAMClient: iamClient, QueryClient: queryClient}), nil
}
