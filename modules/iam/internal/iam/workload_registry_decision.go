package iam

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/quanlaihe/hvac-web/libs/registryauth"
)

// RegistryWorkloadDecisionPath lets an unattended workload obtain a Registry
// grant as its own Workload Principal: the mTLS peer identity is the subject and
// the presenter, and no user session is involved (ADR 0017).
const RegistryWorkloadDecisionPath = "/internal/v1/registry/workload-decision"

func (h *handler) handleRegistryWorkloadDecision(writer http.ResponseWriter, request *http.Request) int {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeProblem(writer, http.StatusMethodNotAllowed, "IAM_METHOD_NOT_ALLOWED", "This IAM route only supports POST.")
		return http.StatusMethodNotAllowed
	}
	if hasForgedIdentityHeader(request.Header) || strings.TrimSpace(request.Header.Get("X-Delegation-Grant")) != "" {
		writeProblem(writer, http.StatusBadRequest, "IAM_FORGED_IDENTITY_HEADER", "Caller-supplied identity headers are not accepted.")
		return http.StatusBadRequest
	}
	_, workload, ok := peerIdentity(request)
	if !ok {
		writeProblem(writer, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.")
		return http.StatusUnauthorized
	}
	trustDomain, err := url.Parse(workload)
	if err != nil || trustDomain.Host == "" {
		writeProblem(writer, http.StatusUnauthorized, "IAM_WORKLOAD_IDENTITY_INVALID", "The calling workload identity is not trusted.")
		return http.StatusUnauthorized
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maximumDecisionRequestSize)
	var decisionRequest registryauth.DecisionRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decisionRequest); err != nil || ensureJSONEOF(decoder) != nil || decisionRequest.Validate() != nil || decisionRequest.GrantPresenter != "" {
		writeProblem(writer, http.StatusBadRequest, "IAM_REGISTRY_DECISION_REQUEST_INVALID", "The Registry authorization request is invalid.")
		return http.StatusBadRequest
	}
	return h.issueRegistryDecision(writer, request, decisionRequest, registryGrantActor{
		subjectIssuer: "spiffe://" + trustDomain.Host, subject: workload, presenter: workload,
	})
}
