package policy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/thescaffold/gox-packages/libs/ai/core"
	"github.com/thescaffold/gox-packages/libs/ai/llm"
)

// ErrUnavailable: every candidate was skipped by an open breaker or failed.
var ErrUnavailable = errors.New("policy: no model could serve the call")

// Attempt records what happened with one candidate, so a run can show why a
// fallback served it.
type Attempt struct {
	Ref ModelRef
	// Skipped is set when the circuit breaker refused the provider.
	Skipped bool
	// Err is the failure, or nil when the attempt produced a response.
	Err error
	// Refused: the model answered with a refusal and the chain moved on.
	Refused bool
}

// Result is a completed Chat.
type Result struct {
	Response *core.ChatResponse
	Served   Candidate
	Attempts []Attempt
}

// Router runs calls along a resolved chain.
type Router struct {
	Policy  Policy
	Lookup  Lookup
	Drivers map[string]llm.ProviderDriver
	Breaker *Breaker
	// FallbackOnRefusal tries the next model when one refuses (TRD §6.2). The
	// last model's refusal is returned as a normal outcome for the caller to
	// record as evidence.
	FallbackOnRefusal bool
}

func (r *Router) breaker() *Breaker {
	if r.Breaker == nil {
		r.Breaker = &Breaker{}
	}
	return r.Breaker
}

func (r *Router) driver(c Candidate) (llm.ProviderDriver, error) {
	d, ok := r.Drivers[c.Ref.Provider]
	if !ok {
		return nil, fmt.Errorf("policy: no driver registered for provider %q", c.Ref.Provider)
	}
	return d, nil
}

// countsAgainstProvider: only failures that say the provider is unhealthy trip
// the breaker. A bad request, bad credentials, a missing model or a cancelled
// context are not the provider's health.
func countsAgainstProvider(err error) bool {
	var pe *core.ProviderError
	if !errors.As(err, &pe) {
		return false
	}
	return pe.Kind != core.KindNotFound && (pe.Retryable() || pe.Failover())
}

func shouldFailover(err error) bool {
	var pe *core.ProviderError
	return errors.As(err, &pe) && pe.Failover()
}

// Chat resolves the tier and calls the first healthy model, failing over along
// the chain. It returns the first response obtained. A failure that would
// repeat on the next model (a rejected request, refused credentials) and a
// cancelled context are returned at once.
func (r *Router) Chat(ctx context.Context, t Tier, c Constraints, req llm.ChatRequest) (*Result, error) {
	cands, err := r.Policy.Resolve(t, c, r.Lookup)
	if err != nil {
		return nil, err
	}
	res := &Result{}
	var last error
	for i, cand := range cands {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if !r.breaker().Allow(cand.Ref.Provider) {
			res.Attempts = append(res.Attempts, Attempt{Ref: cand.Ref, Skipped: true})
			continue
		}
		drv, err := r.driver(cand)
		if err != nil {
			return res, err
		}
		q := req
		q.Model = cand.Ref.Model
		resp, err := drv.Chat(ctx, q)
		if err != nil {
			res.Attempts = append(res.Attempts, Attempt{Ref: cand.Ref, Err: err})
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			if countsAgainstProvider(err) {
				r.breaker().Failure(cand.Ref.Provider)
			}
			if !shouldFailover(err) {
				return res, err
			}
			last = err
			continue
		}
		r.breaker().Success(cand.Ref.Provider)
		if resp.StopReason == core.StopRefusal && r.FallbackOnRefusal && i < len(cands)-1 {
			res.Attempts = append(res.Attempts, Attempt{Ref: cand.Ref, Refused: true})
			continue
		}
		res.Attempts = append(res.Attempts, Attempt{Ref: cand.Ref})
		res.Response, res.Served = resp, cand
		return res, nil
	}
	return res, unavailable(res.Attempts, last)
}

func unavailable(at []Attempt, last error) error {
	var parts []string
	for _, a := range at {
		switch {
		case a.Skipped:
			parts = append(parts, a.Ref.String()+": circuit open")
		case a.Err != nil:
			parts = append(parts, a.Ref.String()+": "+a.Err.Error())
		case a.Refused:
			parts = append(parts, a.Ref.String()+": refused")
		}
	}
	err := fmt.Errorf("%w (%s)", ErrUnavailable, strings.Join(parts, "; "))
	if last != nil {
		return errors.Join(err, last)
	}
	return err
}

// Stream is Chat as events. Failover happens only before any output reaches the
// caller: a failure to open, or an error as the very first event. Once output
// has been forwarded, an error is passed through, because output already shown
// cannot be taken back. The breaker learns from the terminal event either way.
func (r *Router) Stream(ctx context.Context, t Tier, c Constraints, req llm.ChatRequest) (<-chan llm.StreamEvent, Candidate, error) {
	cands, err := r.Policy.Resolve(t, c, r.Lookup)
	if err != nil {
		return nil, Candidate{}, err
	}
	var attempts []Attempt
	var last error
	for _, cand := range cands {
		if ctx.Err() != nil {
			return nil, Candidate{}, ctx.Err()
		}
		if !r.breaker().Allow(cand.Ref.Provider) {
			attempts = append(attempts, Attempt{Ref: cand.Ref, Skipped: true})
			continue
		}
		drv, err := r.driver(cand)
		if err != nil {
			return nil, Candidate{}, err
		}
		q := req
		q.Model = cand.Ref.Model
		ch, err := drv.Stream(ctx, q)
		if err == nil {
			// peek the first event so an immediate failure can still fail over
			first, ok := <-ch
			if !ok {
				err = errors.New("policy: stream closed without any event")
			} else if first.Type == llm.EventError {
				err = first.Err
			} else {
				return r.forward(ctx, cand, first, ch), cand, nil
			}
		}
		attempts = append(attempts, Attempt{Ref: cand.Ref, Err: err})
		if ctx.Err() != nil {
			return nil, Candidate{}, ctx.Err()
		}
		if countsAgainstProvider(err) {
			r.breaker().Failure(cand.Ref.Provider)
		}
		if !shouldFailover(err) {
			return nil, Candidate{}, err
		}
		last = err
	}
	return nil, Candidate{}, unavailable(attempts, last)
}

func (r *Router) forward(ctx context.Context, c Candidate, first llm.StreamEvent, in <-chan llm.StreamEvent) <-chan llm.StreamEvent {
	out := make(chan llm.StreamEvent)
	go func() {
		defer close(out)
		ev, ok := first, true
		for ok {
			select {
			case out <- ev:
			case <-ctx.Done(): // the consumer left; the driver's own ctx ends its stream
				return
			}
			if ev.Terminal() {
				if ev.Type == llm.EventMessageStop {
					r.breaker().Success(c.Ref.Provider)
				} else if countsAgainstProvider(ev.Err) {
					r.breaker().Failure(c.Ref.Provider)
				}
				return
			}
			ev, ok = <-in
		}
	}()
	return out
}

// Cost prices a response by the model that served it (a fallback can differ
// from the requested one). An unpriced model costs an error, not zero: silently
// metering nothing would under-charge.
func (p Policy) Cost(resp *core.ChatResponse) (core.MicroUSD, error) {
	pr, ok := p.Price(resp.Model)
	if !ok {
		return 0, fmt.Errorf("policy: no price for model %q", resp.Model)
	}
	return core.Cost(resp.Usage, pr), nil
}
