package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/eclipse/paho.golang/paho"
	"github.com/quanlaihe/hvac-web/libs/edgefleet"
)

type mqttQoSPublisher interface {
	Publish(context.Context, *paho.Publish) (*paho.PublishResponse, error)
}

type mqttEvidenceRecord struct {
	Topic   string `json:"topic"`
	Payload []byte `json:"payload"`
}

type mqttEvidenceSpool struct {
	buffer *edgefleet.OfflineBuffer
	flush  sync.Mutex
}

func newMQTTEvidenceSpool(directory string, capacityBytes int64) (*mqttEvidenceSpool, error) {
	buffer, err := edgefleet.OpenOfflineBuffer(directory, capacityBytes)
	if err != nil {
		return nil, err
	}
	return &mqttEvidenceSpool{buffer: buffer}, nil
}

func (spool *mqttEvidenceSpool) Enqueue(id string, class edgefleet.EvidenceClass, topic string, payload []byte) (edgefleet.OfflineAdmission, error) {
	if spool == nil || spool.buffer == nil || strings.TrimSpace(id) == "" || strings.TrimSpace(topic) == "" || len(payload) == 0 {
		return edgefleet.OfflineAdmission{}, errors.New("MQTT evidence spool item is invalid")
	}
	record, err := json.Marshal(mqttEvidenceRecord{Topic: strings.TrimSpace(topic), Payload: append([]byte(nil), payload...)})
	if err != nil {
		return edgefleet.OfflineAdmission{}, fmt.Errorf("encode MQTT evidence spool record: %w", err)
	}
	return spool.buffer.Admit(edgefleet.OfflineItem{ID: strings.TrimSpace(id), Class: class, Payload: record})
}

func (spool *mqttEvidenceSpool) Flush(ctx context.Context, publisher mqttQoSPublisher) error {
	if spool == nil || spool.buffer == nil || publisher == nil {
		return errors.New("MQTT evidence spool is unavailable")
	}
	spool.flush.Lock()
	defer spool.flush.Unlock()
	for _, item := range spool.buffer.Pending() {
		var record mqttEvidenceRecord
		if err := json.Unmarshal(item.Payload, &record); err != nil || strings.TrimSpace(record.Topic) == "" || len(record.Payload) == 0 {
			return errors.New("MQTT evidence spool contains an invalid record")
		}
		if _, err := publisher.Publish(ctx, &paho.Publish{QoS: 1, Retain: false, Topic: record.Topic, Payload: record.Payload}); err != nil {
			return err
		}
		if _, err := spool.buffer.Remove(item.ID); err != nil {
			return fmt.Errorf("remove delivered MQTT evidence: %w", err)
		}
	}
	return nil
}

func (spool *mqttEvidenceSpool) State() edgefleet.CapacityState {
	if spool == nil || spool.buffer == nil {
		return edgefleet.CapacityReadOnlySafety
	}
	return spool.buffer.State()
}

func (spool *mqttEvidenceSpool) UsedBytes() int64 {
	if spool == nil || spool.buffer == nil {
		return 0
	}
	return spool.buffer.UsedBytes()
}
