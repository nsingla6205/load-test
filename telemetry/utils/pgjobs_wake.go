package utils

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// notifyChannelPrefix is prepended to every queue name to namespace the channels
// and avoid collisions with anything else listening on the same database.
const notifyChannelPrefix = "pgjobs_"

const (
	pqListenerMinReconnectInterval = 1 * time.Second
	pqListenerMaxReconnectInterval = 30 * time.Second
)

// notifyChannel maps a queue name to a Postgres channel name. Hyphens are
// replaced with underscores so that queue names like "billing-retry" produce a
// valid, unquoted Postgres identifier ("pgjobs_billing_retry"). Any other
// character outside [a-z0-9_-] is rejected with a panic because it would
// produce an identifier that Postgres silently rejects or misinterprets. Both
// the emitter (pg_notify) and the listener (LISTEN) call this function, so they
// always agree on the channel name.
func notifyChannel(queue string) string {
	lower := strings.ToLower(queue)
	for _, r := range lower {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-') {
			panic(fmt.Sprintf("queue: queue name %q contains character %q outside [a-z0-9_-]; use only the compile-time queue constants", queue, r))
		}
	}

	return notifyChannelPrefix + strings.ReplaceAll(lower, "-", "_")
}

// notifyListener delivers payload-free signals whenever the database sends a
// notification on a queue's channel. A signal means "check the queue now";
// dropping one is safe because the fallback ticker re-checks unconditionally.
type notifyListener interface {
	// Notifications returns one signal per NOTIFY received. Closed on shutdown.
	Notifications() <-chan struct{}
	// Close releases the underlying database connection.
	Close() error
}

// listenerEventFunc reports a LISTEN session transition, named by one of the
// monitoring.ListenerEvent* constants. Supplied by the caller so the pq adapter
// reports connection health without knowing how it is recorded.
type listenerEventFunc func(event string, err error)

// listenerFactory opens a LISTEN session for a single channel. Stored as a field
// on JobQueue so tests can inject a fake without a real database.
type listenerFactory func(ctx context.Context, dsn, channel string, onEvent listenerEventFunc) (notifyListener, error)

// pqNotifyListener wraps *pq.Listener (which handles reconnection automatically)
// and adapts it to the payload-free notifyListener interface.
type pqNotifyListener struct {
	listener  *pq.Listener
	signals   chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

// newPQNotifyListener opens a dedicated connection and issues LISTEN on channel.
// It returns without waiting for the session to attach: pq.Listener.Listen blocks
// until the server acknowledges LISTEN, which never happens while the database is
// unreachable. Blocking here would park the caller outside its own retry ladder
// and out of Shutdown's reach, so the attach runs on a goroutine instead. A
// session that fails to attach closes its signal channel, which the caller reads
// as a dead session.
func newPQNotifyListener(ctx context.Context, dsn, channel string, onEvent listenerEventFunc) (notifyListener, error) {
	logger := util.GetLogger(ctx)
	listener := pq.NewListener(dsn, pqListenerMinReconnectInterval, pqListenerMaxReconnectInterval,
		pqEventCallback(logger, channel, onEvent))

	n := &pqNotifyListener{
		listener: listener,
		signals:  make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	go n.attachAndForward(channel, logger)

	return n, nil
}

// pqEventCallback reports pq's own connection lifecycle to the log and to onEvent.
// Without it a session that never attaches is completely silent: the queue keeps
// draining on the fallback ticker and looks healthy while notifications are never
// delivered. Retry attempts are spaced by pq from pqListenerMinReconnectInterval
// up to pqListenerMaxReconnectInterval, which bounds how often a sustained outage
// repeats the warning.
func pqEventCallback(logger log.Logger, channel string, onEvent listenerEventFunc) pq.EventCallbackType {
	if onEvent == nil {
		onEvent = func(string, error) {}
	}

	return func(event pq.ListenerEventType, err error) {
		switch event {
		case pq.ListenerEventConnectionAttemptFailed:
			logger.Warnf("queue: LISTEN connection attempt failed on channel %q: %v", channel, err)
			onEvent(monitoring.ListenerEventConnectFailed, err)
		case pq.ListenerEventDisconnected:
			logger.Warnf("queue: LISTEN session lost on channel %q: %v", channel, err)
			onEvent(monitoring.ListenerEventDisconnected, err)
		case pq.ListenerEventConnected:
			logger.Infof("queue: LISTEN session connected on channel %q", channel)
			onEvent(monitoring.ListenerEventConnected, nil)
		case pq.ListenerEventReconnected:
			logger.Infof("queue: LISTEN session reconnected on channel %q", channel)
			onEvent(monitoring.ListenerEventReconnected, nil)
		}
	}
}

// attachAndForward issues LISTEN and then relays notifications for the life of the
// session. Exactly one path closes signals: a failed attach closes it here, and an
// attached session hands that duty to forward.
func (n *pqNotifyListener) attachAndForward(channel string, logger log.Logger) {
	// Blocks until the server acknowledges LISTEN, or until Close unblocks it.
	if err := n.listener.Listen(channel); err != nil {
		logger.Warnf("queue: failed to listen on channel %q: %v", channel, err)
		close(n.signals)
		return
	}

	// Attaching is the first moment a notification can reach us, so re-check once:
	// anything enqueued while we were connecting was announced to nobody.
	n.signal()
	n.forward()
}

// forward reads from pq.Listener.Notify and collapses each event into at most
// one pending signal. A nil notification signals a reconnect; it is forwarded
// so any notifications missed during the outage trigger a re-check.
func (n *pqNotifyListener) forward() {
	defer close(n.signals)

	for {
		select {
		case <-n.done:
			return
		case _, ok := <-n.listener.Notify:
			if !ok {
				return
			}
			n.signal()
		}
	}
}

// signal queues at most one pending notification and never blocks. A signal that
// is already pending will drain whatever arrived, so extra ones carry no
// information.
func (n *pqNotifyListener) signal() {
	select {
	case n.signals <- struct{}{}:
	default: // signal already pending; collapse into it
	}
}

// Notifications implements notifyListener.
func (n *pqNotifyListener) Notifications() <-chan struct{} {
	return n.signals
}

// Close implements notifyListener. It is safe to call more than once.
func (n *pqNotifyListener) Close() error {
	var err error
	n.closeOnce.Do(func() {
		close(n.done)
		err = n.listener.Close()
	})
	return err
}

// queueWaker drives wake signals for one queue. A fallback ticker and a single
// LISTEN session feed a shared channel; database load is fixed regardless of
// worker count. One signal releases one worker; that worker re-signals after each
// claim, cascading through waiting workers until the queue is empty.
type queueWaker struct {
	queue    string
	channel  string
	fallback time.Duration
	wakeCh   chan struct{}
	metrics  monitoring.MetricsRecorder
}

// newQueueWaker builds the waker for queue. fallback must be positive; a zero or
// negative value is replaced with DefaultFallbackPollInterval to prevent a ticker
// panic on the background goroutine. metrics may be nil, in which case listener
// health is only logged.
func newQueueWaker(queue string, fallback time.Duration, metrics monitoring.MetricsRecorder) *queueWaker {
	if fallback <= 0 {
		fallback = DefaultFallbackPollInterval
	}

	return &queueWaker{
		queue:    queue,
		channel:  notifyChannel(queue),
		fallback: fallback,
		wakeCh:   make(chan struct{}, 1),
		metrics:  metrics,
	}
}

// recordListenerEvent publishes a LISTEN session transition for this queue. The
// error is already logged by whoever observed it, so only the event name is
// recorded.
func (w *queueWaker) recordListenerEvent(event string, _ error) {
	if w.metrics == nil {
		return
	}

	w.metrics.RecordJobListenerEvent(&monitoring.MetricRecorderParams{
		QueueName:     w.queue,
		ListenerEvent: event,
	})
}

// wake unblocks one waiting worker. If a signal is already pending the call is a
// no-op — the pending signal will drain whatever is in the queue. Never blocks.
func (w *queueWaker) wake() {
	select {
	case w.wakeCh <- struct{}{}:
	default:
	}
}

// waitCh returns the channel workers block on while waiting for work.
func (w *queueWaker) waitCh() <-chan struct{} {
	return w.wakeCh
}

// run drives wake signals until ctx is done. With an empty DSN only the fallback
// ticker is used; the LISTEN goroutine is not started.
func (w *queueWaker) run(ctx context.Context, dsn string, newListener listenerFactory) {
	ticker := time.NewTicker(w.fallback)
	defer ticker.Stop()

	// Start LISTEN in a separate goroutine: connection setup can block if the
	// database is unreachable, but the ticker must keep firing regardless.
	if dsn != "" && newListener != nil {
		go w.listen(ctx, dsn, newListener)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.wake()
		}
	}
}

// listen opens a LISTEN session and forwards notifications as wake signals.
// Retries with exponential backoff on open or session failure; exits when ctx is done.
func (w *queueWaker) listen(ctx context.Context, dsn string, newListener listenerFactory) {
	logger := util.GetLogger(ctx)
	backoff := pqListenerMinReconnectInterval

	for ctx.Err() == nil {
		listener, err := newListener(ctx, dsn, w.channel, w.recordListenerEvent)
		if err != nil {
			logger.Warnf("queue: LISTEN session for queue %v unavailable, retrying in %v: %v", w.queue, backoff, err)
			// pq reports its own connection failures through the event callback,
			// but a factory that never produced a listener cannot, so record it here.
			w.recordListenerEvent(monitoring.ListenerEventConnectFailed, err)
		} else {
			backoff = pqListenerMinReconnectInterval
			// Wake once per session attempt; the session signals again once it is
			// actually attached, and repeated wakes coalesce.
			w.wake()

			if !w.runListener(ctx, listener) {
				return // ctx cancelled
			}
			logger.Warnf("queue: LISTEN session for queue %v stopped unexpectedly, reconnecting in %v", w.queue, backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if next := backoff * 2; next > pqListenerMaxReconnectInterval {
			backoff = pqListenerMaxReconnectInterval
		} else {
			backoff = next
		}
	}
}

// runListener forwards notifications from an open session until it closes or ctx
// is done. Returns false when ctx was cancelled (caller should stop), true when
// the session ended on its own (caller should reconnect).
func (w *queueWaker) runListener(ctx context.Context, listener notifyListener) (sessionDied bool) {
	defer func() { _ = listener.Close() }()

	for {
		select {
		case <-ctx.Done():
			return false
		case _, ok := <-listener.Notifications():
			if !ok {
				return true
			}
			w.wake()
		}
	}
}
