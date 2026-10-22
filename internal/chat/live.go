package chat

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"
)

// journalBytes bounds one run's replay buffer (PRD §6.3: bounded per-run
// buffers). When it is exceeded the oldest events are dropped; a client that
// needs them gets a run.resumed{replace:true} and the canonical transcript.
const journalBytes = 4 << 20

const retainFinished = 5 * time.Minute

type sseEvent struct {
	seq  int
	name string
	data []byte
}

// liveRun is the in-memory side of one run: the journal browsers replay from
// and the handle that stops the worker. The database row is the authority on
// status; this only carries events. It is lost on restart by design — a
// reconnect then reconciles with Hermes (service.reattach).
type liveRun struct {
	epoch  string // part of every event id, so an id from a previous process or worker never matches
	runID  string
	userID string
	chatID string

	mu       sync.Mutex
	events   []sseEvent
	nextSeq  int
	size     int
	done     bool // a terminal event is in the journal
	detached bool // the worker gave up; a reconnect must reattach
	changed  chan struct{}
	doneAt   time.Time

	cancelWorker func()
	cancelled    bool
}

func newLive(runID, userID, chatID string) *liveRun {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return &liveRun{epoch: hex.EncodeToString(b), runID: runID, userID: userID, chatID: chatID, nextSeq: 1, changed: make(chan struct{})}
}

// emit appends one event and wakes subscribers. After a terminal event the
// journal is closed: nothing can follow completion.
func (l *liveRun) emit(name string, payload any, terminal bool) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return
	}
	l.events = append(l.events, sseEvent{seq: l.nextSeq, name: name, data: data})
	l.nextSeq++
	l.size += len(data) + len(name)
	for l.size > journalBytes && len(l.events) > 1 {
		l.size -= len(l.events[0].data) + len(l.events[0].name)
		l.events = l.events[1:]
	}
	if terminal {
		l.done, l.doneAt = true, time.Now()
	}
	close(l.changed)
	l.changed = make(chan struct{})
}

func (l *liveRun) markDetached() {
	l.mu.Lock()
	l.detached = true
	close(l.changed)
	l.changed = make(chan struct{})
	l.mu.Unlock()
}

func (l *liveRun) isDetached() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.detached && !l.done
}

func (l *liveRun) markCancelled() func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cancelled = true
	return l.cancelWorker
}

func (l *liveRun) wasCancelled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cancelled
}

// since returns the events after lastID. lostOlder is true when the client
// asked for events the journal no longer holds. wait is closed on the next
// change; done means the journal is closed AND everything returned is the
// last of it.
func (l *liveRun) since(after int) (evs []sseEvent, lostOlder, done bool, wait <-chan struct{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) > 0 && after+1 < l.events[0].seq {
		lostOlder = true
		after = l.events[0].seq - 1
	}
	for _, e := range l.events {
		if e.seq > after {
			evs = append(evs, e)
		}
	}
	return evs, lostOlder, l.done || l.detached, l.changed
}

// parseEventID reads "<epoch>-<seq>". A foreign epoch (the process restarted,
// or the worker was replaced) means "start from the beginning of this
// journal", which always opens with run.started or run.resumed.
func (l *liveRun) parseEventID(id string) int {
	e, s, ok := strings.Cut(id, "-")
	if !ok || e != l.epoch {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (l *liveRun) eventID(seq int) string { return l.epoch + "-" + strconv.Itoa(seq) }
