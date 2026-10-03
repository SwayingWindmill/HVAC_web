package mqttconnector

import (
	"context"
	"testing"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

type rejectingRoutes struct {
	TransportState
	err error
}

func (routes rejectingRoutes) ResolveCommandRoute(context.Context, commandmodel.DispatchEnvelope) (commandmodel.DeviceRoute, error) {
	return commandmodel.DeviceRoute{}, routes.err
}

// A command the Registry does not route is rejected before anything is prepared or
// published, with a reason the operator can act on.
func TestUnroutableCommandIsRejectedBeforeSend(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{err: commandmodel.ErrCommandRouteNotFound, code: "COMMAND_ROUTE_NOT_FOUND"},
		{err: commandmodel.ErrCommandControlDisabled, code: "COMMAND_CONTROL_DISABLED"},
		{err: commandmodel.ErrCommandGatewayCredentialInactive, code: "COMMAND_GATEWAY_CREDENTIAL_INACTIVE"},
	} {
		t.Run(test.code, func(t *testing.T) {
			// The rejecting store implements nothing else, so any prepare or publish would panic.
			connector := &Connector{config: Config{TransportState: rejectingRoutes{err: test.err}}, manager: &autopaho.ConnectionManager{}}
			result, err := connector.Execute(context.Background(), commandmodel.DispatchEnvelope{AttemptID: "attempt-1"})
			if err != nil || result.Phase != commandmodel.ConnectorPreSendRejected || result.FailureCode != test.code {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}
