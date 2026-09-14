package udb

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
)

// WritePipelineOptions controls the asynchronous write pipeline.
//
// The pipeline intentionally batches requests only at the transaction level.
// It does not coalesce, reorder, or otherwise rewrite operations. Requests
// therefore retain their submission order and are executed in that order.
type BackpressurePolicy int

const (
	// BackpressureBlock waits until queue space is available.
	BackpressureBlock BackpressurePolicy = iota
	// BackpressureReject returns ErrWritePipelineFull when the queue is full.
	BackpressureReject
	// BackpressureTimeout waits according to the caller context and returns its
	// error if admission does not complete before the deadline.
	BackpressureTimeout
)

type WritePipelineOptions struct {
	// MaxBatchSize is the maximum number of write requests committed by one
	// bbolt transaction. It must be greater than zero.
	MaxBatchSize int

	// MaxWait is the maximum time the writer waits for more requests after the
	// first request of a batch arrives. A non-positive value disables the timer
	// and makes the writer flush when MaxBatchSize is reached or the queue
	// becomes temporarily empty.
	MaxWait time.Duration

	// QueueSize is the bounded number of requests waiting to be processed.
	// It must be greater than zero.
	QueueSize int

	// Backpressure controls what happens when the bounded queue is full.
	// Block is the backwards-compatible default.
	Backpressure BackpressurePolicy

	// BatchPolicy optionally chooses the target batch size dynamically. When
	// nil, MaxBatchSize is used for every batch.
	BatchPolicy BatchPolicy
}

// DefaultWritePipelineOptions is deliberately conservative for a durable
// bbolt database. The values are tunable; they are not tied to the database's
// durability settings.
func DefaultWritePipelineOptions() WritePipelineOptions {
	return WritePipelineOptions{
		MaxBatchSize: 100,
		MaxWait:      5 * time.Millisecond,
		QueueSize:    1024,
		Backpressure: BackpressureBlock,
	}
}

func normalizeWritePipelineOptions(in WritePipelineOptions) (WritePipelineOptions, error) {
	if in.MaxBatchSize <= 0 {
		return WritePipelineOptions{}, errors.New("udb: write pipeline MaxBatchSize must be > 0")
	}
	if in.QueueSize <= 0 {
		return WritePipelineOptions{}, errors.New("udb: write pipeline QueueSize must be > 0")
	}
	if in.MaxWait < 0 {
		return WritePipelineOptions{}, errors.New("udb: write pipeline MaxWait must be >= 0")
	}
	if in.Backpressure < BackpressureBlock || in.Backpressure > BackpressureTimeout {
		return WritePipelineOptions{}, errors.New("udb: invalid write pipeline backpressure policy")
	}
	return in, nil
}

// PipelineStats is a point-in-time snapshot of pipeline activity.
type PipelineStats struct {
	Submitted       uint64
	Committed       uint64
	Failed          uint64
	Transactions    uint64
	RequestsInQueue int
	QueueCapacity   int

	TotalItems         uint64
	TotalCommitLatency time.Duration
	LastCommitLatency  time.Duration
	MinCommitLatency   time.Duration
	MaxCommitLatency   time.Duration
	CurrentBatchSize   uint64
	Batches            uint64
	AvgBatchSize       float64
	AvgCommitLatency   time.Duration
	Throughput         float64
}

type pipelineOp uint8

const (
	pipelineHSet pipelineOp = iota + 1
	pipelineZSet
	pipelineHDel
	pipelineZDel
	pipelineFlush
)

type pipelineRequest struct {
	op     pipelineOp
	name   string
	key    []byte
	value  []byte
	score  uint64
	result chan error
	seq    uint64
}

// WriteFuture represents one asynchronously submitted write. The future is
// completed exactly once by the pipeline writer. Waiting on a future does not
// change request ordering or transaction atomicity.
type WriteFuture struct {
	result <-chan error
	seq    uint64
}

func (f *WriteFuture) Wait() error {
	return f.WaitContext(context.Background())
}

func (f *WriteFuture) WaitContext(ctx context.Context) error {
	if f == nil || f.result == nil {
		return ErrWritePipelineClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case err := <-f.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *WriteFuture) Seq() uint64 {
	if f == nil {
		return 0
	}
	return f.seq
}

// WritePipeline turns many concurrent high-level writes into fewer managed
// bbolt write transactions:
//
//	producers -> bounded queue -> one writer -> one bbolt Update per batch
//
// The queue provides backpressure. A request is copied before it is queued so
// callers may safely reuse or mutate their input buffers after HSet/ZSet/etc.
// return. No operation coalescing is performed in V5.20.
type WritePipeline struct {
	db   *DB
	opts WritePipelineOptions
	q    chan *pipelineRequest

	// submitMu protects closed state, sequence assignment, and queue insertion.
	// Keeping sequence assignment and send in one critical section guarantees
	// that concurrent producers cannot overtake one another.
	submitMu sync.Mutex
	closed   bool
	seq      uint64

	closeOnce  sync.Once
	writerDone chan struct{}
	closeErr   error

	submitted       atomic.Uint64
	committed       atomic.Uint64
	failed          atomic.Uint64
	transactions    atomic.Uint64
	batches         atomic.Uint64
	totalItems      atomic.Uint64
	commitNanos     atomic.Uint64
	lastNanos       atomic.Int64
	minNanos        atomic.Uint64
	maxNanos        atomic.Uint64
	currentBatch    atomic.Uint64
	lastBatchTarget atomic.Uint64
	startNanos      int64
}

// NewWritePipeline starts one dedicated writer goroutine.
func (db *DB) NewWritePipeline(opts WritePipelineOptions) (*WritePipeline, error) {
	if db == nil {
		return nil, ErrDatabaseClosed
	}
	if db.opts.ReadOnly {
		return nil, bolt.ErrDatabaseReadOnly
	}
	if opts == (WritePipelineOptions{}) {
		opts = DefaultWritePipelineOptions()
	}
	norm, err := normalizeWritePipelineOptions(opts)
	if err != nil {
		return nil, err
	}

	p := &WritePipeline{
		db:         db,
		opts:       norm,
		q:          make(chan *pipelineRequest, norm.QueueSize),
		writerDone: make(chan struct{}),
		startNanos: time.Now().UnixNano(),
	}
	go p.run()
	return p, nil
}

// Options returns the immutable pipeline configuration.
func (p *WritePipeline) Options() WritePipelineOptions {
	if p == nil {
		return WritePipelineOptions{}
	}
	return p.opts
}

// Stats returns a point-in-time activity snapshot.
func (p *WritePipeline) Stats() PipelineStats {
	if p == nil {
		return PipelineStats{}
	}
	transactions := p.transactions.Load()
	items := p.totalItems.Load()
	totalNanos := p.commitNanos.Load()
	avgBatch := 0.0
	if transactions > 0 {
		avgBatch = float64(items) / float64(transactions)
	}
	avgLatency := time.Duration(0)
	if transactions > 0 {
		avgLatency = time.Duration(totalNanos / transactions)
	}
	throughput := 0.0
	elapsed := time.Since(time.Unix(0, p.startNanos)).Seconds()
	if elapsed > 0 {
		throughput = float64(p.committed.Load()) / elapsed
	}
	min := p.minNanos.Load()
	return PipelineStats{
		Submitted: p.submitted.Load(), Committed: p.committed.Load(), Failed: p.failed.Load(),
		Transactions: transactions, RequestsInQueue: len(p.q), QueueCapacity: cap(p.q),
		TotalItems: items, TotalCommitLatency: time.Duration(totalNanos),
		LastCommitLatency: time.Duration(p.lastNanos.Load()), MinCommitLatency: time.Duration(min),
		MaxCommitLatency: time.Duration(p.maxNanos.Load()), CurrentBatchSize: p.currentBatch.Load(),
		Batches: p.batches.Load(), AvgBatchSize: avgBatch, AvgCommitLatency: avgLatency, Throughput: throughput,
	}
}

func (p *WritePipeline) submit(ctx context.Context, req *pipelineRequest) error {
	if p == nil {
		return ErrWritePipelineClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}

	p.submitMu.Lock()
	defer p.submitMu.Unlock()
	if p.closed {
		return ErrWritePipelineClosed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	p.seq++
	req.seq = p.seq
	if p.opts.Backpressure == BackpressureReject {
		select {
		case p.q <- req:
		default:
			return ErrWritePipelineFull
		}
	} else {
		select {
		case p.q <- req:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// Flush is an ordering barrier, not a write request. Keep it out of
	// the request counters so Submitted/Committed/Failed describe actual
	// database mutations only.
	if req.op != pipelineFlush {
		p.submitted.Add(1)
	}
	return nil
}

func (p *WritePipeline) submitAsync(ctx context.Context, req *pipelineRequest) (*WriteFuture, error) {
	if err := p.submit(ctx, req); err != nil {
		return nil, err
	}
	return &WriteFuture{result: req.result, seq: req.seq}, nil
}

func (p *WritePipeline) request(ctx context.Context, req *pipelineRequest) error {
	f, err := p.submitAsync(ctx, req)
	if err != nil {
		return err
	}
	return f.WaitContext(ctx)
}

// HSetAsync validates and copies inputs, then returns after the request is
// accepted by the bounded queue. It does not wait for the transaction commit.
func (p *WritePipeline) HSetAsync(name string, key, value []byte) (*WriteFuture, error) {
	return p.HSetAsyncContext(context.Background(), name, key, value)
}

func (p *WritePipeline) HSetAsyncContext(ctx context.Context, name string, key, value []byte) (*WriteFuture, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	if err := validKey(key); err != nil {
		return nil, err
	}
	return p.submitAsync(ctx, &pipelineRequest{op: pipelineHSet, name: name, key: cloneBytes(key), value: cloneBytes(value), result: make(chan error, 1)})
}

func (p *WritePipeline) ZSetAsync(name string, key []byte, score uint64) (*WriteFuture, error) {
	return p.ZSetAsyncContext(context.Background(), name, key, score)
}

func (p *WritePipeline) ZSetAsyncContext(ctx context.Context, name string, key []byte, score uint64) (*WriteFuture, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	if err := validKey(key); err != nil {
		return nil, err
	}
	return p.submitAsync(ctx, &pipelineRequest{op: pipelineZSet, name: name, key: cloneBytes(key), score: score, result: make(chan error, 1)})
}

func (p *WritePipeline) HDelAsync(name string, key []byte) (*WriteFuture, error) {
	return p.HDelAsyncContext(context.Background(), name, key)
}

func (p *WritePipeline) HDelAsyncContext(ctx context.Context, name string, key []byte) (*WriteFuture, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	if err := validKey(key); err != nil {
		return nil, err
	}
	return p.submitAsync(ctx, &pipelineRequest{op: pipelineHDel, name: name, key: cloneBytes(key), result: make(chan error, 1)})
}

func (p *WritePipeline) ZDelAsync(name string, key []byte) (*WriteFuture, error) {
	return p.ZDelAsyncContext(context.Background(), name, key)
}

func (p *WritePipeline) ZDelAsyncContext(ctx context.Context, name string, key []byte) (*WriteFuture, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	if err := validKey(key); err != nil {
		return nil, err
	}
	return p.submitAsync(ctx, &pipelineRequest{op: pipelineZDel, name: name, key: cloneBytes(key), result: make(chan error, 1)})
}

func (p *WritePipeline) HSet(name string, key, value []byte) error {
	return p.HSetContext(context.Background(), name, key, value)
}

func (p *WritePipeline) HSetContext(ctx context.Context, name string, key, value []byte) error {
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	return p.request(ctx, &pipelineRequest{
		op:     pipelineHSet,
		name:   name,
		key:    cloneBytes(key),
		value:  cloneBytes(value),
		result: make(chan error, 1),
	})
}

func (p *WritePipeline) ZSet(name string, key []byte, score uint64) error {
	return p.ZSetContext(context.Background(), name, key, score)
}

func (p *WritePipeline) ZSetContext(ctx context.Context, name string, key []byte, score uint64) error {
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	return p.request(ctx, &pipelineRequest{
		op:     pipelineZSet,
		name:   name,
		key:    cloneBytes(key),
		score:  score,
		result: make(chan error, 1),
	})
}

func (p *WritePipeline) HDel(name string, key []byte) error {
	return p.HDelContext(context.Background(), name, key)
}

func (p *WritePipeline) HDelContext(ctx context.Context, name string, key []byte) error {
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	return p.request(ctx, &pipelineRequest{
		op:     pipelineHDel,
		name:   name,
		key:    cloneBytes(key),
		result: make(chan error, 1),
	})
}

func (p *WritePipeline) ZDel(name string, key []byte) error {
	return p.ZDelContext(context.Background(), name, key)
}

func (p *WritePipeline) ZDelContext(ctx context.Context, name string, key []byte) error {
	if err := validName(name); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	return p.request(ctx, &pipelineRequest{
		op:     pipelineZDel,
		name:   name,
		key:    cloneBytes(key),
		result: make(chan error, 1),
	})
}

// Flush waits until every request submitted before the Flush call has been
// executed. It is implemented as an ordered barrier rather than a sleep or a
// polling loop.
func (p *WritePipeline) Flush() error {
	return p.FlushContext(context.Background())
}

func (p *WritePipeline) FlushContext(ctx context.Context) error {
	if p == nil {
		return ErrWritePipelineClosed
	}
	req := &pipelineRequest{op: pipelineFlush, result: make(chan error, 1)}
	return p.request(ctx, req)
}

// Close stops accepting new requests, drains all accepted writes, and waits
// for the writer goroutine to exit. Close is idempotent. Accepted writes are
// never discarded merely because Close was called.
func (p *WritePipeline) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		p.submitMu.Lock()
		p.closed = true
		close(p.q)
		p.submitMu.Unlock()
		<-p.writerDone
	})
	return p.closeErr
}

func (p *WritePipeline) run() {
	defer close(p.writerDone)

	for {
		first, ok := <-p.q
		if !ok {
			return
		}
		if first.op == pipelineFlush {
			first.result <- nil
			continue
		}

		target := p.opts.MaxBatchSize
		if p.opts.BatchPolicy != nil {
			current := int(p.lastBatchTarget.Load())
			if current < 1 {
				current = 1
			}
			target = p.opts.BatchPolicy.NextBatchSize(len(p.q), cap(p.q), current, p.Stats())
			if target < 1 {
				target = 1
			}
			if target > p.opts.MaxBatchSize {
				target = p.opts.MaxBatchSize
			}
		}
		p.lastBatchTarget.Store(uint64(target))
		p.currentBatch.Store(1)
		batch := make([]*pipelineRequest, 0, target)
		batch = append(batch, first)

		var timer *time.Timer
		var timerC <-chan time.Time
		if p.opts.MaxWait > 0 {
			timer = time.NewTimer(p.opts.MaxWait)
			timerC = timer.C
		}

		collecting := true
		for collecting && len(batch) < target {
			if p.opts.MaxWait <= 0 {
				select {
				case req, ok := <-p.q:
					if !ok {
						collecting = false
						break
					}
					if req.op == pipelineFlush {
						p.executeBatch(batch)
						batch = batch[:0]
						req.result <- nil
						collecting = false
						continue
					}
					batch = append(batch, req)
					p.currentBatch.Store(uint64(len(batch)))
				default:
					collecting = false
				}
				continue
			}
			select {
			case req, ok := <-p.q:
				if !ok {
					collecting = false
					break
				}
				if req.op == pipelineFlush {
					// The barrier belongs after all requests already collected.
					p.executeBatch(batch)
					batch = batch[:0]
					req.result <- nil
					collecting = false
					continue
				}
				batch = append(batch, req)
				p.currentBatch.Store(uint64(len(batch)))
			case <-timerC:
				collecting = false
			}
		}
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		if len(batch) > 0 {
			p.executeBatch(batch)
		}
	}
}

func (p *WritePipeline) executeBatch(batch []*pipelineRequest) {
	if len(batch) == 0 {
		return
	}
	p.transactions.Add(1)
	started := time.Now()
	err := p.db.Update(func(tx *Tx) error {
		for _, req := range batch {
			var err error
			switch req.op {
			case pipelineHSet:
				err = p.db.Hset(tx, req.name, req.key, req.value)
			case pipelineZSet:
				err = p.db.Zset(tx, req.name, req.key, req.score)
			case pipelineHDel:
				err = p.db.Hdel(tx, req.name, req.key)
			case pipelineZDel:
				err = p.db.Zdel(tx, req.name, req.key)
			default:
				err = errors.New("udb: invalid write pipeline operation")
			}
			if err != nil {
				return err
			}
		}
		return nil
	})

	latency := time.Since(started)
	p.commitNanos.Add(uint64(latency))
	p.lastNanos.Store(int64(latency))
	p.totalItems.Add(uint64(len(batch)))
	p.batches.Add(1)
	for {
		old := p.minNanos.Load()
		if old != 0 && old <= uint64(latency) {
			break
		}
		if p.minNanos.CompareAndSwap(old, uint64(latency)) {
			break
		}
	}
	for {
		old := p.maxNanos.Load()
		if old >= uint64(latency) {
			break
		}
		if p.maxNanos.CompareAndSwap(old, uint64(latency)) {
			break
		}
	}
	p.currentBatch.Store(0)
	if err != nil {
		p.failed.Add(uint64(len(batch)))
		for _, req := range batch {
			req.result <- err
		}
		return
	}
	p.committed.Add(uint64(len(batch)))
	for _, req := range batch {
		req.result <- nil
	}
}
