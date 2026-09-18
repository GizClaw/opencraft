package execd

import (
	"context"
	"sync/atomic"
)

// processActor owns one sandbox session: every operation on that
// session runs in the actor's goroutine, in submission order, so a
// process has exactly one in-flight operation at a time. A blocking
// operation (a long-poll read, a slow write) can be interrupted from
// outside through processEntry.interrupt, which cancels the operation's
// context and lets the actor move on to the next request (terminate,
// release, a fresh read).
type processActor struct {
	entry   *processEntry
	mailbox chan *actorRequest
	done    chan struct{}
	stopped atomic.Bool
}

type actorRequest struct {
	ctx   context.Context
	run   func(context.Context) (any, *Response)
	reply chan actorReply
	stop  bool
}

type actorReply struct {
	value any
	err   *Response
}

func newProcessActor(entry *processEntry) *processActor {
	actor := &processActor{
		entry:   entry,
		mailbox: make(chan *actorRequest, 16),
		done:    make(chan struct{}),
	}
	go actor.run()
	return actor
}

func (a *processActor) run() {
	defer close(a.done)
	for req := range a.mailbox {
		if req.stop {
			return
		}
		if err := req.ctx.Err(); err != nil {
			req.reply <- actorReply{
				err: errorResponse(CodeCanceled, "%v", err),
			}
			continue
		}
		opCtx, cancel := context.WithCancel(req.ctx)
		a.entry.setCurrent(cancel)
		value, errResp := req.run(opCtx)
		a.entry.clearCurrent()
		cancel()
		req.reply <- actorReply{value: value, err: errResp}
	}
}

// submit queues one operation and waits for its result. The caller's
// context bounds the wait; a canceled caller does not cancel the
// operation itself (a queued terminate must still happen), but the
// actor is interrupted so it cannot stay stuck behind a read nobody is
// waiting for.
func (a *processActor) submit(
	ctx context.Context,
	run func(context.Context) (any, *Response),
) (any, *Response) {
	if a.stopped.Load() {
		return nil, errorResponse(CodeNotFound, "process is stopping")
	}
	req := &actorRequest{ctx: ctx, run: run, reply: make(chan actorReply, 1)}
	select {
	case a.mailbox <- req:
	case <-a.done:
		return nil, errorResponse(CodeNotFound, "process is stopping")
	case <-ctx.Done():
		return nil, errorResponse(CodeCanceled, "%v", ctx.Err())
	}
	select {
	case reply := <-req.reply:
		return reply.value, reply.err
	case <-ctx.Done():
		a.entry.interrupt()
		return nil, errorResponse(CodeCanceled, "%v", ctx.Err())
	}
}

// stop interrupts the in-flight operation, drains the queued ones, and
// waits (bounded by ctx) for the actor goroutine to exit.
func (a *processActor) stop(ctx context.Context) {
	if a.stopped.Swap(true) {
		return
	}
	a.entry.interrupt()
	select {
	case a.mailbox <- &actorRequest{stop: true}:
	case <-a.done:
		return
	case <-ctx.Done():
		return
	}
	select {
	case <-a.done:
	case <-ctx.Done():
	}
}

// interrupt cancels the operation currently running in the actor, if
// any. It is safe to call while holding no locks; the actor clears the
// registration as soon as the operation returns.
func (e *processEntry) interrupt() {
	e.mu.Lock()
	cancel := e.currentCancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *processEntry) setCurrent(cancel context.CancelFunc) {
	e.mu.Lock()
	e.currentCancel = cancel
	e.mu.Unlock()
}

func (e *processEntry) clearCurrent() {
	e.mu.Lock()
	e.currentCancel = nil
	e.mu.Unlock()
}
