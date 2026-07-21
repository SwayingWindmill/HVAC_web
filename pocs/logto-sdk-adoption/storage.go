package logtopoc

import "sync"

type memoryStorage struct {
	mu    sync.RWMutex
	items map[string]string
	drop  bool
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{items: map[string]string{}}
}

func (storage *memoryStorage) GetItem(key string) string {
	storage.mu.RLock()
	defer storage.mu.RUnlock()
	return storage.items[key]
}

func (storage *memoryStorage) SetItem(key, value string) {
	if storage.drop {
		return
	}
	storage.mu.Lock()
	storage.items[key] = value
	storage.mu.Unlock()
}
