package commanddispatcher

import (
	"context"
	"time"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type CommandStore interface {
	PrepareDispatch(commandID, leaseOwner string, leaseUntil time.Time) (commandmodel.DispatchEnvelope, error)
	ResolveDispatch(envelope commandmodel.DispatchEnvelope, result commandmodel.ConnectorResult) error
}

type Connector interface {
	Execute(ctx context.Context, envelope commandmodel.DispatchEnvelope) (commandmodel.ConnectorResult, error)
}

type Dispatcher struct {
	store     CommandStore
	connector Connector
	workerID  string
	now       func() time.Time
	leaseFor  time.Duration
}

func New(store CommandStore, connector Connector, workerID string, now func() time.Time) *Dispatcher {
	if now == nil {
		now = time.Now
	}
	return &Dispatcher{
		store:     store,
		connector: connector,
		workerID:  workerID,
		now:       now,
		leaseFor:  30 * time.Second,
	}
}

func (d *Dispatcher) Dispatch(ctx context.Context, commandID string) error {
	envelope, err := d.store.PrepareDispatch(commandID, d.workerID, d.now().Add(d.leaseFor))
	if err != nil {
		return err
	}
	result, err := d.connector.Execute(ctx, envelope)
	if err != nil {
		return err
	}
	return d.store.ResolveDispatch(envelope, result)
}
