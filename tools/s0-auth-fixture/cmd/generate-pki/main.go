package main

import (
	"fmt"
	"os"
	"time"

	"github.com/quanlaihe/hvac-web/libs/testpki"
)

func main() {
	if len(os.Args) != 2 {
		_, _ = os.Stderr.WriteString("usage: generate-pki <output-directory>\n")
		os.Exit(2)
	}
	bundle, err := testpki.Generate(
		"spiffe://hvac.local/iam-service",
		"spiffe://hvac.local/platform-gateway",
		time.Now().UTC(),
	)
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	if err := bundle.WriteFiles(os.Args[1]); err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	fmt.Println(os.Args[1])
}
