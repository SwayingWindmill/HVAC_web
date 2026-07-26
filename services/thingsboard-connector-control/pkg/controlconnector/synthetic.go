package controlconnector

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

var (
	ErrPayloadMismatch = errors.New("attempt payload changed")
	ErrOldFence        = errors.New("execution fence is older than the connector watermark")
)

type Mode string

const (
	ModeVerifiedSuccess      Mode = "VERIFIED_SUCCESS"
	ModePreSendRejected      Mode = "PRE_SEND_REJECTED"
	ModeCommittedThenTimeout Mode = "COMMITTED_THEN_TIMEOUT"
)

type Synthetic struct {
	mu sync.Mutex

	mode             Mode
	results          map[string]record
	maxFenceByDevice map[string]uint64
}

type record struct {
	PayloadHash string
	Result      commandmodel.ConnectorResult
}

func NewSynthetic(mode Mode) *Synthetic {
	return &Synthetic{
		mode:             mode,
		results:          make(map[string]record),
		maxFenceByDevice: make(map[string]uint64),
	}
}

func (c *Synthetic) Execute(_ context.Context, envelope commandmodel.DispatchEnvelope) (commandmodel.ConnectorResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := fmt.Sprintf("%s|%d", envelope.AttemptID, envelope.ExecutionFence)
	if existing, ok := c.results[key]; ok {
		if existing.PayloadHash != envelope.PayloadHash {
			return commandmodel.ConnectorResult{}, ErrPayloadMismatch
		}
		return existing.Result, nil
	}
	if envelope.ExecutionFence < c.maxFenceByDevice[envelope.DeviceID] {
		return commandmodel.ConnectorResult{}, ErrOldFence
	}
	if envelope.ExecutionFence > c.maxFenceByDevice[envelope.DeviceID] {
		c.maxFenceByDevice[envelope.DeviceID] = envelope.ExecutionFence
	}

	var result commandmodel.ConnectorResult
	switch c.mode {
	case ModeVerifiedSuccess:
		result = commandmodel.ConnectorResult{
			Phase:        commandmodel.ConnectorAcknowledged,
			Acknowledged: true,
			Verified:     false,
			EvidenceID:   "synthetic:provider-acknowledged",
		}
	case ModePreSendRejected:
		result = commandmodel.ConnectorResult{
			Phase:       commandmodel.ConnectorPreSendRejected,
			FailureCode: "SYNTHETIC_PRE_SEND_REJECTION",
			EvidenceID:  "synthetic:pre-send-rejected",
		}
	case ModeCommittedThenTimeout:
		result = commandmodel.ConnectorResult{
			Phase:       commandmodel.ConnectorRequestCommitted,
			FailureCode: "SYNTHETIC_ACK_TIMEOUT",
			EvidenceID:  "synthetic:request-committed-timeout",
		}
	default:
		return commandmodel.ConnectorResult{}, fmt.Errorf("unsupported synthetic mode %q", c.mode)
	}

	c.results[key] = record{PayloadHash: envelope.PayloadHash, Result: result}
	return result, nil
}
