package iamserver

import (
	"net/http"

	"github.com/quanlaihe/hvac-web/services/iam-service/internal/iam"
)

const CurrentPrincipalPath = iam.CurrentPrincipalPath

type Config = iam.Config

func NewHandler(config Config) http.Handler {
	return iam.NewHandler(config)
}
