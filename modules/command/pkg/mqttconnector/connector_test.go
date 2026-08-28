package mqttconnector

import (
	"testing"

	"github.com/quanlaihe/hvac-web/libs/commandmodel"
)

func TestRecoveredCorrelationNeverRepublishesAfterCommitPoint(t *testing.T) {
	tests := []struct {
		name  string
		state commandmodel.CorrelationState
		want  resumeAction
	}{
		{name: "crash before commit point may publish", state: commandmodel.CorrelationPrepared, want: resumePublish},
		{name: "crash after commit point is outcome unknown", state: commandmodel.CorrelationMayCommit, want: resumeOutcomeUnknown},
		{name: "durable reply is reused", state: commandmodel.CorrelationReplied, want: resumeUseReply},
		{name: "resolved reply is reused", state: commandmodel.CorrelationResolved, want: resumeUseReply},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resumeActionForCorrelation(test.state); got != test.want {
				t.Fatalf("resume action=%d want=%d", got, test.want)
			}
		})
	}
}
