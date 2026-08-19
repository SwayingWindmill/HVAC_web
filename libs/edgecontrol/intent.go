package edgecontrol

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type ControlIntent struct {
	ID        string    `json:"id"`
	Address   string    `json:"address"`
	Requested Value     `json:"requested"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Source    string    `json:"source"`
}

type IntentStore struct {
	mu      sync.Mutex
	runtime *Runtime
	byID    map[string]ControlIntent
	active  map[string]string
}

func NewIntentStore(runtime *Runtime) (*IntentStore, error) {
	if runtime == nil {
		return nil, errors.New("channel runtime is required")
	}
	return &IntentStore{runtime: runtime, byID: map[string]ControlIntent{}, active: map[string]string{}}, nil
}

func (store *IntentStore) Put(intent ControlIntent) (bool, error) {
	if store == nil {
		return false, errors.New("intent store is nil")
	}
	intent.ID = strings.TrimSpace(intent.ID)
	intent.Address = strings.TrimSpace(intent.Address)
	intent.Source = strings.TrimSpace(intent.Source)
	if intent.ID == "" || intent.Address == "" || intent.Source == "" {
		return false, errors.New("intent ID, address and source are required")
	}
	if intent.IssuedAt.IsZero() || intent.ExpiresAt.IsZero() || !intent.ExpiresAt.After(intent.IssuedAt) {
		return false, errors.New("intent requires a positive lease interval")
	}
	descriptor, ok := store.runtime.Descriptor(intent.Address)
	if !ok {
		return false, fmt.Errorf("intent targets unknown channel %s", intent.Address)
	}
	if !writable(descriptor) {
		return false, fmt.Errorf("intent targets read-only channel %s", intent.Address)
	}
	if err := intent.Requested.validate(descriptor.DataType); err != nil {
		return false, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if existing, exists := store.byID[intent.ID]; exists {
		if sameIntent(existing, intent) {
			return false, nil
		}
		return false, fmt.Errorf("intent ID %s was reused with different content", intent.ID)
	}
	if activeID, exists := store.active[intent.Address]; exists {
		active := store.byID[activeID]
		if !intent.IssuedAt.After(active.IssuedAt) {
			return false, fmt.Errorf("intent %s is not newer than active intent %s", intent.ID, active.ID)
		}
	}
	store.byID[intent.ID] = intent
	store.active[intent.Address] = intent.ID
	return true, nil
}

func sameIntent(left, right ControlIntent) bool {
	return left.ID == right.ID && left.Address == right.Address && left.Requested == right.Requested &&
		left.IssuedAt.Equal(right.IssuedAt) && left.ExpiresAt.Equal(right.ExpiresAt) && left.Source == right.Source
}

func (store *IntentStore) Active(at time.Time) []ControlIntent {
	if store == nil {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	addresses := make([]string, 0, len(store.active))
	for address, id := range store.active {
		intent := store.byID[id]
		if !at.Before(intent.ExpiresAt) {
			delete(store.active, address)
			delete(store.byID, id)
			continue
		}
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	out := make([]ControlIntent, 0, len(addresses))
	for _, address := range addresses {
		out = append(out, store.byID[store.active[address]])
	}
	return out
}

func (store *IntentStore) Revoke(id string) bool {
	if store == nil {
		return false
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	intent, exists := store.byID[id]
	if !exists {
		return false
	}
	if store.active[intent.Address] == id {
		delete(store.active, intent.Address)
	}
	return true
}

type IntentController struct {
	id    string
	store *IntentStore
}

func NewIntentController(id string, store *IntentStore) (*IntentController, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("intent controller ID is required")
	}
	if store == nil {
		return nil, errors.New("intent store is required")
	}
	return &IntentController{id: id, store: store}, nil
}

func (controller *IntentController) ID() string { return controller.id }

func (controller *IntentController) Run(_ context.Context, image ProcessImage, plan *ControlPlan) error {
	for _, intent := range controller.store.Active(image.At()) {
		if _, _, err := plan.Request(intent.Address, controller.id, intent.Requested); err != nil {
			return fmt.Errorf("apply intent %s: %w", intent.ID, err)
		}
	}
	return nil
}
