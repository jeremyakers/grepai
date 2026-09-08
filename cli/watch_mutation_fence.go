package cli

import (
	"context"
	"errors"
	"sync"
)

var errWatchMutationAdmissionClosed = errors.New("watch mutation admission closed")

// watchMutationFence orders event admission against watcher fatal state and
// delays readiness withdrawal until every admitted synchronous mutation exits.
// A mutex/counter/channel is used instead of WaitGroup so admission cannot race
// with the transition into a waiting state.
type watchMutationFence struct {
	mu            sync.Mutex
	watchers      []watchSource
	closed        bool
	cause         error
	active        int
	idle          chan struct{}
	failed        chan struct{}
	nextID        uint64
	cancellations map[uint64]context.CancelFunc
}

type watchMutationWorker struct {
	started <-chan struct{}
	done    <-chan struct{}
}

func completedWatchMutationWorker() watchMutationWorker {
	started := make(chan struct{})
	done := make(chan struct{})
	close(started)
	close(done)
	return watchMutationWorker{started: started, done: done}
}

func startWatchMutationWorker(ctx context.Context, fence *watchMutationFence, run func(context.Context)) watchMutationWorker {
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var startedOnce sync.Once
		markStarted := func() { startedOnce.Do(func() { close(started) }) }
		_ = fence.handle(ctx, func(workerCtx context.Context) {
			markStarted()
			run(workerCtx)
		})
		markStarted()
	}()
	return watchMutationWorker{started: started, done: done}
}

func newWatchMutationFence() *watchMutationFence {
	f := &watchMutationFence{}
	f.mu.Lock()
	f.initializeLocked()
	f.mu.Unlock()
	return f
}

func (f *watchMutationFence) initializeLocked() {
	if f.idle == nil {
		f.idle = make(chan struct{})
		close(f.idle)
	}
	if f.cancellations == nil {
		f.cancellations = make(map[uint64]context.CancelFunc)
	}
}

func (f *watchMutationFence) addWatcher(w watchSource) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initializeLocked()
	if f.closed {
		return false
	}
	f.watchers = append(f.watchers, w)
	return true
}

func (f *watchMutationFence) removeWatcher(w watchSource) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, candidate := range f.watchers {
		if candidate == w {
			f.watchers = append(f.watchers[:i], f.watchers[i+1:]...)
			return
		}
	}
}

func (f *watchMutationFence) ready(publish func() error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initializeLocked()
	if f.closed {
		return f.closedErrorLocked()
	}
	return withWatchSourcesReady(f.watchers, publish)
}

// publishReadiness linearizes readiness publication with admission closure.
// Once closed, a queued readiness notification is stale and is ignored.
func (f *watchMutationFence) publishReadiness(publish func() error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initializeLocked()
	if f.closed {
		return nil
	}
	return withWatchSourcesReady(f.watchers, publish)
}

func (f *watchMutationFence) handle(parent context.Context, mutate func(context.Context)) error {
	f.mu.Lock()
	f.initializeLocked()
	if f.closed {
		err := f.closedErrorLocked()
		f.mu.Unlock()
		return err
	}
	if err := parent.Err(); err != nil {
		f.mu.Unlock()
		return err
	}

	var eventCtx context.Context
	var cancel context.CancelFunc
	var id uint64
	err := withWatchSourcesReady(f.watchers, func() error {
		eventCtx, cancel = context.WithCancel(parent)
		if f.active == 0 {
			f.idle = make(chan struct{})
		}
		f.active++
		f.nextID++
		id = f.nextID
		f.cancellations[id] = cancel
		return nil
	})
	f.mu.Unlock()
	if err != nil {
		return err
	}

	defer func() {
		cancel()
		f.mu.Lock()
		delete(f.cancellations, id)
		f.active--
		if f.active == 0 {
			close(f.idle)
		}
		f.mu.Unlock()
	}()
	mutate(eventCtx)
	return nil
}

func (f *watchMutationFence) fail(withdraw func()) {
	f.failWithCause(nil, nil, withdraw)
}

// failWithCause closes admission and cancels active contexts. The first caller
// owns afterClose and withdraw; duplicate callers wait for that sequence so
// external fatal callbacks run exactly once and only after quiescence.
func (f *watchMutationFence) failWithCause(cause error, afterClose, withdraw func()) {
	f.mu.Lock()
	f.initializeLocked()
	if f.closed {
		failed := f.failed
		f.mu.Unlock()
		if failed != nil {
			<-failed
		}
		return
	}
	f.closed = true
	if cause == nil {
		cause = errWatchMutationAdmissionClosed
	}
	f.cause = cause
	f.failed = make(chan struct{})
	failed := f.failed
	cancels := make([]context.CancelFunc, 0, len(f.cancellations))
	for _, cancel := range f.cancellations {
		cancels = append(cancels, cancel)
	}
	idle := f.idle
	f.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	if afterClose != nil {
		afterClose()
	}
	<-idle
	if withdraw != nil {
		withdraw()
	}
	close(failed)
}

func (f *watchMutationFence) closedErrorLocked() error {
	if f.cause != nil {
		return f.cause
	}
	return errWatchMutationAdmissionClosed
}
