package main

// RingQueue is a growable ring buffer FIFO queue for player IDs.
type RingQueue struct {
	data []string
	head int
	tail int
	size int
}

func NewRingQueue(capacity int) *RingQueue {
	if capacity < 1 {
		capacity = 1
	}
	return &RingQueue{data: make([]string, capacity)}
}

func (q *RingQueue) Len() int { return q.size }

func (q *RingQueue) grow() {
	newCap := len(q.data) * 2
	if newCap == 0 {
		newCap = 1
	}
	nd := make([]string, newCap)
	// Copy in FIFO order
	for i := 0; i < q.size; i++ {
		nd[i] = q.data[(q.head+i)%len(q.data)]
	}
	q.data = nd
	q.head = 0
	q.tail = q.size
}

func (q *RingQueue) Enqueue(v string) {
	if q.size == len(q.data) {
		q.grow()
	}
	q.data[q.tail] = v
	q.tail = (q.tail + 1) % len(q.data)
	q.size++
}

func (q *RingQueue) Dequeue() (string, bool) {
	if q.size == 0 {
		return "", false
	}
	v := q.data[q.head]
	q.data[q.head] = ""
	q.head = (q.head + 1) % len(q.data)
	q.size--
	return v, true
}

func (q *RingQueue) Snapshot() []string {
	out := make([]string, q.size)
	for i := 0; i < q.size; i++ {
		out[i] = q.data[(q.head+i)%len(q.data)]
	}
	return out
}
