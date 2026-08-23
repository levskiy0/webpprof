package webpprof

// entryOrder is a bounded-friendly FIFO implemented as a circular deque.
// Removing the oldest entry is O(1), which is the steady-state path once the
// profiler reaches its configured event or byte limit.
type entryOrder struct {
	values []string
	head   int
	count  int
}

func newEntryOrder(capacity int) entryOrder {
	if capacity < 0 {
		capacity = 0
	}
	return entryOrder{values: make([]string, capacity)}
}

func (d *entryOrder) len() int {
	return d.count
}

func (d *entryOrder) at(index int) string {
	if index < 0 || index >= d.count {
		return ""
	}
	return d.values[(d.head+index)%len(d.values)]
}

func (d *entryOrder) front() (string, bool) {
	if d.count == 0 {
		return "", false
	}
	return d.values[d.head], true
}

func (d *entryOrder) pushBack(id string) {
	if d.count == len(d.values) {
		d.grow()
	}
	index := (d.head + d.count) % len(d.values)
	d.values[index] = id
	d.count++
}

func (d *entryOrder) popFront() (string, bool) {
	if d.count == 0 {
		return "", false
	}
	id := d.values[d.head]
	d.values[d.head] = ""
	d.head = (d.head + 1) % len(d.values)
	d.count--
	if d.count == 0 {
		d.head = 0
	}
	return id, true
}

func (d *entryOrder) remove(id string) bool {
	for index := 0; index < d.count; index++ {
		if d.at(index) != id {
			continue
		}
		for current := index; current < d.count-1; current++ {
			d.values[(d.head+current)%len(d.values)] = d.at(current + 1)
		}
		last := (d.head + d.count - 1) % len(d.values)
		d.values[last] = ""
		d.count--
		if d.count == 0 {
			d.head = 0
		}
		return true
	}
	return false
}

func (d *entryOrder) reset() {
	clear(d.values)
	d.head = 0
	d.count = 0
}

func (d *entryOrder) grow() {
	capacity := max(4, len(d.values)*2)
	values := make([]string, capacity)
	for index := 0; index < d.count; index++ {
		values[index] = d.at(index)
	}
	d.values = values
	d.head = 0
}
