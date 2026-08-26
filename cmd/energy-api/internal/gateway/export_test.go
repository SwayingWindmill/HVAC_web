package gateway

import (
	"context"
	"sync"
	"time"
)

type memoryLoginStateStoreForTest struct {
	mu     sync.Mutex
	states map[string]loginState
}

func NewMemoryLoginStateStoreForTest() LoginStateStore {
	return &memoryLoginStateStoreForTest{states: map[string]loginState{}}
}

func (store *memoryLoginStateStoreForTest) Put(_ context.Context, state string, value loginState, _ time.Duration) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.states[state] = value
	return nil
}

func (store *memoryLoginStateStoreForTest) Consume(_ context.Context, state string) (loginState, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	value, ok := store.states[state]
	if !ok {
		return loginState{}, ErrLoginStateNotFound
	}
	delete(store.states, state)
	return value, nil
}
