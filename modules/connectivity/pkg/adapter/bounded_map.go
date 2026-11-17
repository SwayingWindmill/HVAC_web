package adapter

type boundedTouchEntry[V any] struct {
	value          V
	lastTouchedSeq uint64
}

type boundedTouchMap[V any] struct {
	maxEntries int
	touchSeq   uint64
	entries    map[string]*boundedTouchEntry[V]
}

func newBoundedTouchMap[V any](maxEntries int) boundedTouchMap[V] {
	return boundedTouchMap[V]{
		maxEntries: maxEntries,
		entries:    make(map[string]*boundedTouchEntry[V]),
	}
}

func (m *boundedTouchMap[V]) get(key string) (V, bool) {
	entry, ok := m.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	return entry.value, true
}

func (m *boundedTouchMap[V]) getOrCreate(key string, create func() V) V {
	m.touchSeq++
	if m.entries == nil {
		m.entries = make(map[string]*boundedTouchEntry[V])
	}
	if entry, ok := m.entries[key]; ok {
		entry.lastTouchedSeq = m.touchSeq
		return entry.value
	}
	if m.maxEntries > 0 && len(m.entries) >= m.maxEntries {
		var oldestKey string
		var oldestSeq uint64
		for k, item := range m.entries {
			if oldestKey == "" || item.lastTouchedSeq < oldestSeq {
				oldestKey = k
				oldestSeq = item.lastTouchedSeq
			}
		}
		if oldestKey != "" {
			delete(m.entries, oldestKey)
		}
	}
	val := create()
	m.entries[key] = &boundedTouchEntry[V]{
		value:          val,
		lastTouchedSeq: m.touchSeq,
	}
	return val
}
