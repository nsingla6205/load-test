package utils

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// fakeNotifyListener is a notifyListener the test drives directly, so the wake
// path can be exercised without a Postgres server.
type fakeNotifyListener struct {
	signals chan struct{}

	mu     sync.Mutex
	closed bool
}

func newFakeNotifyListener() *fakeNotifyListener {
	return &fakeNotifyListener{signals: make(chan struct{}, 8)}
}

func (f *fakeNotifyListener) Notifications() <-chan struct{} { return f.signals }

func (f *fakeNotifyListener) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeNotifyListener) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// notify simulates a NOTIFY arriving on the queue's channel.
func (f *fakeNotifyListener) notify() { f.signals <- struct{}{} }

// stopListening simulates the listener shutting down permanently.
func (f *fakeNotifyListener) stopListening() { close(f.signals) }

func TestNotifyChannel(t *testing.T) {
	tests := []struct {
		name  string
		queue string
		want  string
	}{
		{name: "plain name", queue: "performance", want: "pgjobs_performance"},
		{name: "underscores and digits survive", queue: "queue_2", want: "pgjobs_queue_2"},
		{name: "billing-retry hyphen is replaced with underscore in channel", queue: "billing-retry", want: "pgjobs_billing_retry"},
		{name: "empty queue still yields a valid channel", queue: "", want: "pgjobs_"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, notifyChannel(tt.queue))
		})
	}
}

// TestNotifyChannel_InvalidNamePanics guards the collision rule: a queue name
// with characters outside [a-z0-9_-] would produce an invalid Postgres
// identifier. Panic catches this at startup rather than in production.
// Hyphens are allowed and are replaced with underscores in the channel name.
func TestNotifyChannel_InvalidNamePanics(t *testing.T) {
	tests := []struct {
		name  string
		queue string
	}{
		{name: "space is not a valid identifier character", queue: "my queue"},
		{name: "semicolon could escape the SQL identifier", queue: "a;drop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Panics(t, func() { notifyChannel(tt.queue) })
		})
	}
}

// TestNotifyChannel_EmitterAndListenerAgree guards the invariant that both sides
// of a notification derive the channel name the same way. If they diverged,
// notifications would silently never be delivered and every queue would quietly
// degrade to fallback polling.
func TestNotifyChannel_EmitterAndListenerAgree(t *testing.T) {
	for _, queue := range []string{PerformanceQueue, UsageQueue, CollectionQueue, BizOpsReportQueue, BillingRetryQueue} {
		listenerChannel := newQueueWaker(queue, time.Hour, nil).channel
		assert.Equal(t, notifyChannel(queue), listenerChannel, "queue %s", queue)
		assert.Regexp(t, `^pgjobs_[a-z0-9_]*$`, listenerChannel, "queue %s must map to a bare identifier", queue)
	}
}

// TestNewQueueWaker_NonPositiveFallbackUsesDefault guards the ticker on the waker
// goroutine: time.NewTicker panics on a non-positive interval, and that panic
// would take down the fallback poll for the whole queue.
func TestNewQueueWaker_NonPositiveFallbackUsesDefault(t *testing.T) {
	for _, fallback := range []time.Duration{0, -time.Second} {
		assert.Equal(t, DefaultFallbackPollInterval, newQueueWaker("q1", fallback, nil).fallback,
			"fallback %v must be replaced with the default", fallback)
	}
}

// TestPQNotifyListener_ForwardsNotifications covers the pq adapter without a
// database by writing to the public Notify channel the library would have written
// to itself.
func TestPQNotifyListener_ForwardsNotifications(t *testing.T) {
	n := newTestPQNotifyListener(t)

	// A nil notification is pq's reconnect marker. It has to wake workers too:
	// anything announced during the outage never reached us.
	n.listener.Notify <- nil

	select {
	case <-n.Notifications():
	case <-time.After(5 * time.Second):
		t.Fatal("a reconnect marker was not forwarded as a signal")
	}

	n.listener.Notify <- &pq.Notification{Channel: "pgjobs_q1"}

	select {
	case <-n.Notifications():
	case <-time.After(5 * time.Second):
		t.Fatal("a notification was not forwarded as a signal")
	}
}

// TestPQNotifyListener_CloseStopsForwarding pins the shutdown contract the waker
// relies on: Close releases the connection, is safe to repeat, and closes the
// signal channel so runListener sees the session end.
func TestPQNotifyListener_CloseStopsForwarding(t *testing.T) {
	n := newTestPQNotifyListener(t)

	require.NoError(t, n.Close())
	require.NoError(t, n.Close(), "Close must be safe to call more than once")

	assert.Eventually(t, func() bool {
		select {
		case _, ok := <-n.Notifications():
			return !ok
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond, "Close must stop the forwarder and close the signal channel")
}

// newTestPQNotifyListener builds a pqNotifyListener whose forwarder is running but
// whose *pq.Listener never opens a connection: the DSN cannot be parsed, so pq
// fails every attempt locally without any network access.
func newTestPQNotifyListener(t *testing.T) *pqNotifyListener {
	t.Helper()

	listener := pq.NewListener("not-a-dsn", pqListenerMinReconnectInterval, pqListenerMaxReconnectInterval, nil)
	n := &pqNotifyListener{
		listener: listener,
		signals:  make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	go n.forward()
	t.Cleanup(func() { _ = n.Close() })

	return n
}

// TestNewPQNotifyListener_DoesNotBlockOnUnreachableDatabase is the regression guard
// for the whole notification path. pq.Listener.Listen parks until the server
// acknowledges LISTEN, so constructing the session inline used to hang the waker
// goroutine whenever the database was down: its retry ladder never ran and
// Shutdown could not reach it. The DSN here can never be parsed, so a construction
// that returns proves the attach was handed off.
func TestNewPQNotifyListener_DoesNotBlockOnUnreachableDatabase(t *testing.T) {
	built := make(chan notifyListener, 1)
	go func() {
		listener, err := newPQNotifyListener(context.Background(), "not-a-dsn", "pgjobs_q1", nil)
		if err == nil {
			built <- listener
		}
	}()

	select {
	case listener := <-built:
		require.NotNil(t, listener)
		t.Cleanup(func() { _ = listener.Close() })
	case <-time.After(5 * time.Second):
		t.Fatal("newPQNotifyListener blocked waiting for a database that will never answer")
	}
}

// TestPQNotifyListener_FailedAttachClosesSignals covers the path that carries an
// attach failure back to the waker: a closed signal channel is what runListener
// reads as a dead session, which is what triggers the reconnect backoff. Closing
// the listener up front makes pq reject the LISTEN immediately, which is also how
// a session parked in Listen gets released during Shutdown.
func TestPQNotifyListener_FailedAttachClosesSignals(t *testing.T) {
	listener := pq.NewListener("not-a-dsn", pqListenerMinReconnectInterval, pqListenerMaxReconnectInterval, nil)
	require.NoError(t, listener.Close())

	n := &pqNotifyListener{
		listener: listener,
		signals:  make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	go n.attachAndForward("pgjobs_q1", log.NewLogger())

	select {
	case _, ok := <-n.Notifications():
		assert.False(t, ok, "a failed attach must close the signal channel, not signal work")
	case <-time.After(5 * time.Second):
		t.Fatal("a failed attach left the signal channel open, so the session would never be retried")
	}
}

// TestPQEventCallback_ReportsEveryEvent pins the callback that replaced the nil pq
// was given before. Without it a session that never attaches produces no log lines
// and no metrics, so each pq event must map to exactly one reported event name.
func TestPQEventCallback_ReportsEveryEvent(t *testing.T) {
	tests := []struct {
		name  string
		event pq.ListenerEventType
		want  string
	}{
		{name: "connected", event: pq.ListenerEventConnected, want: monitoring.ListenerEventConnected},
		{name: "reconnected", event: pq.ListenerEventReconnected, want: monitoring.ListenerEventReconnected},
		{name: "disconnected", event: pq.ListenerEventDisconnected, want: monitoring.ListenerEventDisconnected},
		{name: "connection attempt failed", event: pq.ListenerEventConnectionAttemptFailed, want: monitoring.ListenerEventConnectFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			callback := pqEventCallback(log.NewLogger(), "pgjobs_q1", func(event string, _ error) {
				got = append(got, event)
			})
			require.NotNil(t, callback, "a nil callback is what made connection failures silent")

			callback(tt.event, errors.New("connection refused"))
			assert.Equal(t, []string{tt.want}, got)
		})
	}
}

// TestPQEventCallback_ToleratesNilReporter covers construction without a metrics
// reporter: logging must still happen rather than panicking on the pq goroutine.
func TestPQEventCallback_ToleratesNilReporter(t *testing.T) {
	callback := pqEventCallback(log.NewLogger(), "pgjobs_q1", nil)

	assert.NotPanics(t, func() {
		callback(pq.ListenerEventConnectionAttemptFailed, errors.New("connection refused"))
	})
}

// TestQueueWaker_RecordListenerEvent_ReportsQueueAndEvent covers the hop from the
// pq callback to the recorder, which is what makes a queue whose notifications
// never arrive visible instead of silently degrading to the fallback poll.
func TestQueueWaker_RecordListenerEvent_ReportsQueueAndEvent(t *testing.T) {
	recorder := &monitoring.MockMetricsRecorder{}
	recorder.EXPECT().
		RecordJobListenerEvent(&monitoring.MetricRecorderParams{
			QueueName:     BillingRetryQueue,
			ListenerEvent: monitoring.ListenerEventConnectFailed,
		}).
		Return().
		Once()

	waker := newQueueWaker(BillingRetryQueue, time.Hour, recorder)
	waker.recordListenerEvent(monitoring.ListenerEventConnectFailed, errors.New("connection refused"))

	recorder.AssertExpectations(t)
}

// TestQueueWaker_RecordListenerEvent_WithoutRecorder guards the nil recorder the
// waker tests rely on: listener health is optional telemetry, never a crash.
func TestQueueWaker_RecordListenerEvent_WithoutRecorder(t *testing.T) {
	waker := newQueueWaker("q1", time.Hour, nil)

	assert.NotPanics(t, func() {
		waker.recordListenerEvent(monitoring.ListenerEventDisconnected, nil)
	})
}

// TestQueueWaker_Listen_FactoryFailureIsRecorded covers the one failure the pq
// event callback cannot report: when the factory returns no listener at all, there
// is nothing for pq to report through, so the waker must record it itself.
func TestQueueWaker_Listen_FactoryFailureIsRecorded(t *testing.T) {
	recorder := &monitoring.MockMetricsRecorder{}
	recorded := make(chan string, 4)
	recorder.EXPECT().
		RecordJobListenerEvent(mock.Anything).
		Run(func(params *monitoring.MetricRecorderParams) {
			select {
			case recorded <- params.ListenerEvent:
			default:
			}
		}).
		Return()

	waker := newQueueWaker("q1", time.Hour, recorder)
	factory := func(_ context.Context, dsn, channel string, _ listenerEventFunc) (notifyListener, error) {
		return nil, errors.New("listen refused")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go waker.listen(ctx, "postgres://example/db", factory)

	select {
	case event := <-recorded:
		assert.Equal(t, monitoring.ListenerEventConnectFailed, event)
	case <-time.After(5 * time.Second):
		t.Fatal("a failed LISTEN session was never recorded, so the outage would be invisible")
	}
}

func TestQueueWaker_WakeCoalesces(t *testing.T) {
	waker := newQueueWaker("q1", time.Hour, nil)

	waker.wake()
	waker.wake()
	waker.wake()

	// Only one worker is released: the wake that is already pending will drain
	// whatever arrived, so signals are never counted.
	select {
	case <-waker.waitCh():
	default:
		t.Fatal("expected a pending wake")
	}

	select {
	case <-waker.waitCh():
		t.Fatal("expected repeated wakes to coalesce into one")
	default:
	}
}

func TestQueueWaker_WakeDoesNotBlock(t *testing.T) {
	waker := newQueueWaker("q1", time.Hour, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			waker.wake()
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("wake blocked when a signal was already pending")
	}
}

func TestQueueWaker_Run_FallbackTickerWakes(t *testing.T) {
	waker := newQueueWaker("q1", 5*time.Millisecond, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go waker.run(ctx, "", nil)

	for i := 0; i < 3; i++ {
		select {
		case <-waker.waitCh():
		case <-time.After(5 * time.Second):
			t.Fatalf("fallback ticker did not produce wake %d", i+1)
		}
	}
}

// TestQueueWaker_Run_NoListenerWithoutDSN documents that an empty DSN keeps the
// queue on the polling-only path, which is what every mock-driven test relies on.
func TestQueueWaker_Run_NoListenerWithoutDSN(t *testing.T) {
	waker := newQueueWaker("q1", 5*time.Millisecond, nil)

	var built atomic.Int32
	factory := func(_ context.Context, dsn, channel string, _ listenerEventFunc) (notifyListener, error) {
		built.Add(1)
		return newFakeNotifyListener(), nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go waker.run(ctx, "", factory)

	// The ticker still runs, so a wake proves run() got going without a listener.
	select {
	case <-waker.waitCh():
	case <-time.After(5 * time.Second):
		t.Fatal("fallback ticker did not wake")
	}
	assert.Zero(t, built.Load(), "no listener may be built without a DSN")
}

func TestQueueWaker_Listen_ForwardsNotifications(t *testing.T) {
	waker := newQueueWaker(BillingRetryQueue, time.Hour, nil)

	listener := newFakeNotifyListener()
	var gotDSN, gotChannel string
	factory := func(_ context.Context, dsn, channel string, _ listenerEventFunc) (notifyListener, error) {
		gotDSN, gotChannel = dsn, channel
		return listener, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go waker.run(ctx, "postgres://example/db", factory)

	// A fresh session wakes once, since jobs may have been enqueued before it
	// came up.
	select {
	case <-waker.waitCh():
	case <-time.After(5 * time.Second):
		t.Fatal("a newly attached listener must trigger one wake")
	}

	listener.notify()
	select {
	case <-waker.waitCh():
	case <-time.After(5 * time.Second):
		t.Fatal("notification was not forwarded as a wake")
	}

	assert.Equal(t, "postgres://example/db", gotDSN)
	assert.Equal(t, "pgjobs_billing_retry", gotChannel)

	cancel()
	assert.Eventually(t, listener.isClosed, 5*time.Second, 10*time.Millisecond,
		"the listening session must be released when the context is done")
}

// TestQueueWaker_Listen_FactoryFailureFallsBackToTicker proves a database that
// refuses the LISTEN cannot stall a queue: the fallback ticker still fires.
func TestQueueWaker_Listen_FactoryFailureFallsBackToTicker(t *testing.T) {
	waker := newQueueWaker("q1", 5*time.Millisecond, nil)

	factory := func(_ context.Context, dsn, channel string, _ listenerEventFunc) (notifyListener, error) {
		return nil, errors.New("listen refused")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go waker.run(ctx, "postgres://example/db", factory)

	for i := 0; i < 2; i++ {
		select {
		case <-waker.waitCh():
		case <-time.After(5 * time.Second):
			t.Fatalf("fallback ticker did not produce wake %d after listener setup failed", i+1)
		}
	}
}

// TestQueueWaker_Listen_BackoffGrowsBetweenRetries verifies a database that keeps
// refusing the LISTEN is retried with a widening delay rather than being hammered
// once per second forever. Only lower bounds are asserted, because a timer never
// fires early but a loaded machine can always be late.
func TestQueueWaker_Listen_BackoffGrowsBetweenRetries(t *testing.T) {
	waker := newQueueWaker("q1", time.Hour, nil)

	attempts := make(chan time.Time, 3)
	factory := func(_ context.Context, dsn, channel string, _ listenerEventFunc) (notifyListener, error) {
		select {
		case attempts <- time.Now():
		default:
		}
		return nil, errors.New("listen refused")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go waker.listen(ctx, "postgres://example/db", factory)

	timestamps := make([]time.Time, 0, 3)
	for i := 0; i < 3; i++ {
		select {
		case at := <-attempts:
			timestamps = append(timestamps, at)
		case <-time.After(30 * time.Second):
			t.Fatalf("only %d LISTEN attempts were made", i)
		}
	}

	assert.GreaterOrEqual(t, timestamps[1].Sub(timestamps[0]), pqListenerMinReconnectInterval,
		"the first retry must wait at least the minimum reconnect interval")
	assert.GreaterOrEqual(t, timestamps[2].Sub(timestamps[1]), 2*pqListenerMinReconnectInterval,
		"the delay must double between retries instead of staying flat")
}

// TestQueueWaker_Listen_StoppedListenerFallsBackToTicker covers a listener that
// dies for good: the queue must keep polling rather than go silent.
func TestQueueWaker_Listen_StoppedListenerFallsBackToTicker(t *testing.T) {
	waker := newQueueWaker("q1", 5*time.Millisecond, nil)

	listener := newFakeNotifyListener()
	factory := func(_ context.Context, dsn, channel string, _ listenerEventFunc) (notifyListener, error) {
		return listener, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go waker.run(ctx, "postgres://example/db", factory)

	select {
	case <-waker.waitCh():
	case <-time.After(5 * time.Second):
		t.Fatal("a newly attached listener must trigger one wake")
	}

	listener.stopListening()
	assert.Eventually(t, listener.isClosed, 5*time.Second, 10*time.Millisecond,
		"a permanently stopped listener must be released")

	for i := 0; i < 2; i++ {
		select {
		case <-waker.waitCh():
		case <-time.After(5 * time.Second):
			t.Fatalf("fallback ticker did not produce wake %d after the listener stopped", i+1)
		}
	}
}

// TestJobQueue_WakerFor_SharedPerQueue verifies every worker on a queue waits on
// the same signal, which is what makes one wake release exactly one worker.
func TestJobQueue_WakerFor_SharedPerQueue(t *testing.T) {
	queue, _, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
	defer cleanup()
	defer queue.Shutdown()

	first := queue.wakerFor([]string{"q1"})
	second := queue.wakerFor([]string{"q1"})
	other := queue.wakerFor([]string{"q2"})

	assert.Same(t, first, second, "workers on the same queue must share a waker")
	assert.NotSame(t, first, other, "different queues must not share a waker")
	assert.Equal(t, "pgjobs_q1", first.channel)
}

// TestJobQueue_WakerFor_RequiresExactlyOneQueue guards the constraint that wakers
// are one-to-one with queues. Passing zero or multiple queues would silently
// assign the wrong (or no) LISTEN session, so wakerFor panics instead.
func TestJobQueue_WakerFor_RequiresExactlyOneQueue(t *testing.T) {
	queue, _, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
	defer cleanup()
	defer queue.Shutdown()

	assert.Panics(t, func() { queue.wakerFor(nil) }, "nil queues must panic")
	assert.Panics(t, func() { queue.wakerFor([]string{}) }, "empty queues must panic")
	assert.Panics(t, func() { queue.wakerFor([]string{"q1", "q2"}) }, "multi-queue must panic")
}

// TestJobQueue_DequeueWithHook_FiresOnClaim checks the hand-off hook runs for a
// claimed job and, critically, before the job starts running: hooking after
// Perform would queue a backlog behind whichever job is slowest.
func TestJobQueue_DequeueWithHook_FiresOnClaim(t *testing.T) {
	recorder := &monitoring.MockMetricsRecorder{}
	recorder.On("RecordJobProcessed", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	recorder.On("RecordJobDequeued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	gate := &performGate{started: make(chan struct{}), unblock: make(chan struct{})}
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	queue := NewQueue(db, gate, recorder)
	queue.registerType(&GatedPerformJob{})

	payload, err := json.Marshal(GatedPerformJob{ID: "gated"})
	require.NoError(t, err)
	expectClaim(sqlMock, 1, "utils.GatedPerformJob", string(payload), "q1")
	sqlMock.ExpectBegin()
	sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
		WithArgs(JOB_STATUS_FINISHED, "", int64(1), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	sqlMock.ExpectCommit()

	hookCalls := make(chan struct{}, 4)
	go func() {
		<-gate.started
		// The hook must already have run by the time the job is executing.
		assert.Len(t, hookCalls, 1)
		close(gate.unblock)
	}()

	ctx := context.Background()
	claimed, err := queue.dequeueWithHook(ctx, ctx, []string{"q1"}, func() { hookCalls <- struct{}{} })
	require.NoError(t, err)
	require.True(t, claimed)
	assert.Len(t, hookCalls, 1, "the hand-off must fire exactly once per claim")
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_DequeueWithHook_SilentOnEmptyClaim is the other half of the
// hand-off contract: the chain has to stop as soon as the queue runs dry,
// otherwise a single wake would spin every worker forever.
func TestJobQueue_DequeueWithHook_SilentOnEmptyClaim(t *testing.T) {
	queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
	defer cleanup()
	queue.registerType(&MockJob{})

	sqlMock.ExpectQuery("UPDATE.*jobs.*RETURNING").WillReturnError(sql.ErrNoRows)

	hookCalls := 0
	ctx := context.Background()
	claimed, err := queue.dequeueWithHook(ctx, ctx, []string{"q1"}, func() { hookCalls++ })
	require.NoError(t, err)
	assert.False(t, claimed)
	assert.Zero(t, hookCalls, "an empty claim must not hand off to a peer")
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

func TestJobQueue_DequeueWithHook_NilHookIsSafe(t *testing.T) {
	queue, sqlMock, cleanup := newRealQueueMock(t, &monitoring.MockMetricsRecorder{})
	defer cleanup()
	queue.registerType(&MockJob{})

	sqlMock.ExpectQuery("UPDATE.*jobs.*RETURNING").WillReturnError(sql.ErrNoRows)

	ctx := context.Background()
	claimed, err := queue.dequeue(ctx, ctx, []string{"q1"})
	require.NoError(t, err)
	assert.False(t, claimed)
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_Worker_NotificationWakesWorkerIntoClaim is the core of the
// redesign: a worker that is not on any poll timer still picks up a job as soon
// as the database says one is available.
//
// The fallback interval is set beyond the test's lifetime, so the only thing that
// can move the worker is a notification.
func TestJobQueue_Worker_NotificationWakesWorkerIntoClaim(t *testing.T) {
	recorder := &monitoring.MockMetricsRecorder{}
	recorder.On("RecordJobProcessed", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	recorder.On("RecordJobDequeued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	gate := &performGate{started: make(chan struct{}), unblock: make(chan struct{})}
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cfg := DefaultJobQueueConfig()
	cfg.FallbackPollInterval = time.Hour
	cfg.NotifyEnabled = true
	cfg.NotifyDSN = "postgres://example/db"
	queue := NewQueueWithConfig(db, gate, recorder, cfg)
	defer queue.Shutdown()
	queue.registerType(&GatedPerformJob{})

	listener := newFakeNotifyListener()
	queue.newListener = func(_ context.Context, dsn, channel string, _ listenerEventFunc) (notifyListener, error) {
		return listener, nil
	}

	// One empty claim is expected before the notification: the worker's start-up
	// drain.
	sqlMock.ExpectQuery("UPDATE.*jobs.*RETURNING").WillReturnError(sql.ErrNoRows)

	payload, err := json.Marshal(GatedPerformJob{ID: "gated"})
	require.NoError(t, err)
	expectClaim(sqlMock, 7, "utils.GatedPerformJob", string(payload), "q1")
	sqlMock.ExpectBegin()
	sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
		WithArgs(JOB_STATUS_FINISHED, "", int64(7), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	sqlMock.ExpectCommit()

	pollCtx, cancelPoll := context.WithCancel(context.Background())
	defer cancelPoll()
	jobCtx := context.Background()

	// Start the waker before the worker and consume the wake that attaching the
	// listener produces. Wakes coalesce by design, so without this the attach wake
	// and the notification below could collapse into a single one.
	waker := queue.wakerFor([]string{"q1"})
	select {
	case <-waker.waitCh():
	case <-time.After(5 * time.Second):
		t.Fatal("attaching the listener must produce one wake")
	}

	workerDone := make(chan error, 1)
	go func() { workerDone <- queue.Worker(pollCtx, jobCtx, []string{"q1"}, &GatedPerformJob{}) }()

	listener.notify()

	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("notification did not wake the worker into a claim")
	}

	// Cancelling while the job runs makes the following drain a no-op, so the
	// claim expectations above are the complete set.
	cancelPoll()
	close(gate.unblock)

	select {
	case err := <-workerDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not exit after cancellation")
	}
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// notifyingQueueMock builds a queue with notifications fully enabled, so the
// pg_notify statement is expected to reach the database.
func notifyingQueueMock(t *testing.T, recorder *monitoring.MockMetricsRecorder) (*JobQueue, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)

	cfg := DefaultJobQueueConfig()
	cfg.NotifyEnabled = true
	cfg.NotifyDSN = "postgres://example/db"

	return NewQueueWithConfig(db, nil, recorder, cfg), sqlMock, func() { _ = db.Close() }
}

func TestJobQueue_Enqueue_NotifiesQueue(t *testing.T) {
	recorder := &monitoring.MockMetricsRecorder{}
	recorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue, sqlMock, cleanup := notifyingQueueMock(t, recorder)
	defer cleanup()

	// INSERT and pg_notify are now a single CTE round trip; the SQL contains both.
	sqlMock.ExpectExec("pg_notify").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), BillingRetryQueue, sqlmock.AnyArg(), "pgjobs_billing_retry").
		WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, queue.Enqueue(context.Background(), MockJob{ID: "1"}, BillingRetryQueue))
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_Enqueue_NoNotifyWithoutDSN is what keeps the notification path out
// of the way of every queue built without one, including the mock-driven tests.
func TestJobQueue_Enqueue_NoNotifyWithoutDSN(t *testing.T) {
	recorder := &monitoring.MockMetricsRecorder{}
	recorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue, sqlMock, cleanup := newRealQueueMock(t, recorder)
	defer cleanup()

	sqlMock.ExpectExec("INSERT INTO jobs").WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, queue.Enqueue(context.Background(), MockJob{ID: "1"}, PerformanceQueue))
	// An unexpected pg_notify would leave the mock complaining about an
	// unfulfilled call ordering, so a clean result proves nothing extra was sent.
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_Enqueue_NoNotifyWhenGateDisabled covers the kill-switch: with the
// gate off the enqueue still succeeds and no notification is attempted.
func TestJobQueue_Enqueue_NoNotifyWhenGateDisabled(t *testing.T) {
	recorder := &monitoring.MockMetricsRecorder{}
	recorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cfg := DefaultJobQueueConfig()
	cfg.NotifyEnabled = false
	cfg.NotifyDSN = "postgres://example/db"
	queue := NewQueueWithConfig(db, nil, recorder, cfg)

	sqlMock.ExpectExec("INSERT INTO jobs").WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, queue.Enqueue(context.Background(), MockJob{ID: "1"}, PerformanceQueue))
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_Enqueue_CTEFailureFailsEnqueue documents the atomicity of the CTE
// path: INSERT and pg_notify are a single statement, so any DB-level failure
// propagates as an enqueue error (the insert is also rolled back). The separate
// RecordJobNotifyFailed counter is only used by the notifyQueue helper (EnqueueAt,
// EnqueueBatch), not by the Enqueue CTE path.
func TestJobQueue_Enqueue_CTEFailureFailsEnqueue(t *testing.T) {
	recorder := &monitoring.MockMetricsRecorder{}
	recorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue, sqlMock, cleanup := notifyingQueueMock(t, recorder)
	defer cleanup()

	sqlMock.ExpectExec("pg_notify").WillReturnError(errors.New("connection reset"))

	require.Error(t, queue.Enqueue(context.Background(), MockJob{ID: "1"}, PerformanceQueue))
	require.NoError(t, sqlMock.ExpectationsWereMet())
	recorder.AssertNotCalled(t, "RecordJobNotifyFailed", mock.Anything)
	recorder.AssertExpectations(t)
}

// TestJobQueue_Enqueue_NoNotifyFailureCountedOnSuccess keeps the counter honest: a
// successful CTE enqueue must not register as a notify failure, or the metric is
// useless for alerting.
func TestJobQueue_Enqueue_NoNotifyFailureCountedOnSuccess(t *testing.T) {
	recorder := &monitoring.MockMetricsRecorder{}
	recorder.On("RecordJobEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue, sqlMock, cleanup := notifyingQueueMock(t, recorder)
	defer cleanup()

	// Single CTE statement covers INSERT + pg_notify in one round trip.
	sqlMock.ExpectExec("pg_notify").WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, queue.Enqueue(context.Background(), MockJob{ID: "1"}, PerformanceQueue))
	require.NoError(t, sqlMock.ExpectationsWereMet())
	recorder.AssertNotCalled(t, "RecordJobNotifyFailed", mock.Anything)
}

func TestJobQueue_EnqueueAt_NotifiesWhenDue(t *testing.T) {
	queue, sqlMock, cleanup := notifyingQueueMock(t, &monitoring.MockMetricsRecorder{})
	defer cleanup()

	sqlMock.ExpectExec("INSERT INTO jobs").WillReturnResult(sqlmock.NewResult(1, 1))
	sqlMock.ExpectExec("pg_notify").WithArgs("pgjobs_performance").WillReturnResult(sqlmock.NewResult(0, 0))

	err := queue.EnqueueAt(context.Background(), MockJob{ID: "1"}, PerformanceQueue, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_EnqueueAt_NoNotifyWhenFutureDated documents the one gap that only
// the fallback poll can close: a future-dated job has nothing claimable yet, and
// nothing fires when its run time arrives.
func TestJobQueue_EnqueueAt_NoNotifyWhenFutureDated(t *testing.T) {
	queue, sqlMock, cleanup := notifyingQueueMock(t, &monitoring.MockMetricsRecorder{})
	defer cleanup()

	sqlMock.ExpectExec("INSERT INTO jobs").WillReturnResult(sqlmock.NewResult(1, 1))

	err := queue.EnqueueAt(context.Background(), MockJob{ID: "1"}, PerformanceQueue, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_EnqueueBatch_NotifiesOnceAfterCommit verifies a batch sends a
// single notification once its rows are committed. One is enough: the woken worker
// hands the signal on after each claim, so the cascade spreads across the batch.
func TestJobQueue_EnqueueBatch_NotifiesOnceAfterCommit(t *testing.T) {
	recorder := &monitoring.MockMetricsRecorder{}
	recorder.On("RecordJobBatchEnqueued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue, sqlMock, cleanup := notifyingQueueMock(t, recorder)
	defer cleanup()

	sqlMock.ExpectBegin()
	sqlMock.ExpectExec("INSERT INTO jobs").WillReturnResult(sqlmock.NewResult(1, 2))
	sqlMock.ExpectCommit()
	sqlMock.ExpectExec("pg_notify").WithArgs("pgjobs_performance").WillReturnResult(sqlmock.NewResult(0, 0))

	jobs := []Job{MockJob{ID: "1"}, MockJob{ID: "2"}}
	require.NoError(t, queue.EnqueueBatch(context.Background(), jobs, PerformanceQueue))
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_Sweep_NotifiesReclaimedQueues covers the sweeper hand-back: a
// reclaimed job is claimable again, and no enqueue happens to announce it.
func TestJobQueue_Sweep_NotifiesReclaimedQueues(t *testing.T) {
	recorder := &monitoring.MockMetricsRecorder{}
	recorder.On("RecordJobReclaimed", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	queue, sqlMock, cleanup := notifyingQueueMock(t, recorder)
	defer cleanup()

	sqlMock.ExpectQuery("UPDATE.*jobs.*SET.*status.*lease_expires_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "type_name", "data", "attempt", "queue"}).
			AddRow(int64(1), "utils.MockJob", "{}", int32(2), PerformanceQueue).
			AddRow(int64(2), "utils.MockJob", "{}", int32(2), PerformanceQueue))
	// Two rows from one queue collapse into a single notification.
	sqlMock.ExpectExec("pg_notify").WithArgs("pgjobs_performance").WillReturnResult(sqlmock.NewResult(0, 0))

	reclaimed, err := queue.Sweep(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), reclaimed)
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_Sweep_NoNotifyWhenNothingReclaimed keeps an idle sweeper silent,
// which matters because it runs every minute on every pod.
func TestJobQueue_Sweep_NoNotifyWhenNothingReclaimed(t *testing.T) {
	queue, sqlMock, cleanup := notifyingQueueMock(t, &monitoring.MockMetricsRecorder{})
	defer cleanup()

	sqlMock.ExpectQuery("UPDATE.*jobs.*SET.*status.*lease_expires_at").
		WillReturnRows(sqlmock.NewRows([]string{"id", "type_name", "data", "attempt", "queue"}))

	reclaimed, err := queue.Sweep(context.Background())
	require.NoError(t, err)
	assert.Zero(t, reclaimed)
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// TestJobQueue_Worker_HandsOffToPeerOnClaim proves the cascade: one wake plus one
// successful claim releases a second worker, which is how a batch drains without
// every worker polling.
func TestJobQueue_Worker_HandsOffToPeerOnClaim(t *testing.T) {
	recorder := &monitoring.MockMetricsRecorder{}
	recorder.On("RecordJobProcessed", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()
	recorder.On("RecordJobDequeued", mock.AnythingOfType("*monitoring.MetricRecorderParams")).Return()

	gate := &performGate{started: make(chan struct{}), unblock: make(chan struct{})}
	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	cfg := DefaultJobQueueConfig()
	cfg.FallbackPollInterval = time.Hour
	queue := NewQueueWithConfig(db, gate, recorder, cfg)
	defer queue.Shutdown()
	queue.registerType(&GatedPerformJob{})

	payload, err := json.Marshal(GatedPerformJob{ID: "gated"})
	require.NoError(t, err)
	expectClaim(sqlMock, 1, "utils.GatedPerformJob", string(payload), "q1")
	sqlMock.ExpectBegin()
	sqlMock.ExpectExec("UPDATE.*jobs.*SET.*status").
		WithArgs(JOB_STATUS_FINISHED, "", int64(1), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	sqlMock.ExpectCommit()

	pollCtx, cancelPoll := context.WithCancel(context.Background())
	defer cancelPoll()
	jobCtx := context.Background()

	// The waker is created up front so its wake channel can be observed; the worker
	// below joins the same one. With no DSN configured there is no listener, so
	// nothing signals it except the hand-off under test.
	waker := queue.wakerFor([]string{"q1"})
	require.Empty(t, waker.waitCh(), "no wake is expected before the worker claims")

	workerDone := make(chan error, 1)
	go func() { workerDone <- queue.Worker(pollCtx, jobCtx, []string{"q1"}, &GatedPerformJob{}) }()

	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not claim the job")
	}

	// The claiming worker has handed the signal on while it is still busy, so a
	// peer is already released.
	select {
	case <-waker.waitCh():
	case <-time.After(5 * time.Second):
		t.Fatal("a successful claim must hand off to a peer worker")
	}

	cancelPoll()
	close(gate.unblock)

	select {
	case err := <-workerDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not exit after cancellation")
	}
	require.NoError(t, sqlMock.ExpectationsWereMet())
}
