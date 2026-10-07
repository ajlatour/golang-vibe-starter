package httpstore

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ajlatour/golang-vibe-starter/internal/jsoncall"
)

// WithRetries lets the store try a GET again, up to n times, when the
// upstream gave no usable answer fast: a refused or reset connection, or a
// 5xx, within retryFastFailure. A GET reads, so trying it again can't change
// anything; a write might already have happened, so it's never retried.
// Neither is a timeout or a slow failure, which already cost the client
// time a retry would double, nor a call whose client has gone, nor one whose
// request hasn't the time left for a whole attempt (the client's timeout)
// before its deadline. The default, 0, retries nothing.
//
// Retries share a budget: across all calls, at most about one retry for
// every ten requests (retryRatio). So when the upstream is down, not merely
// unlucky, retries stop adding load to it rather than doubling it.
func WithRetries(n int) Option {
	return func(s *Store) { s.retries = n }
}

const (
	// retryRatio is how much of a retry each request earns, and so the most
	// retries can add to the upstream's traffic: a tenth.
	retryRatio = 0.1
	// retryBurst is the most retries saved up: what a quiet spell earns
	// for a moment's trouble.
	retryBurst = 10
	// retryPause is the shortest pause before a retry; it's jittered up to
	// twice that, so retries after one blip don't all arrive at once.
	retryPause = 25 * time.Millisecond
	// retryFastFailure is the longest a failed attempt may have taken and
	// still be retried: a refused connection or an upstream's quick 5xx is
	// worth another try, while an upstream that took longer to fail is
	// struggling, and a retry would mostly add to the wait.
	retryFastFailure = time.Second
)

// retryBudget is a token bucket of retries: each request deposits
// retryRatio, each retry spends one. Safe for concurrent use.
type retryBudget struct {
	mu     sync.Mutex
	tokens float64
}

func newRetryBudget() *retryBudget { return &retryBudget{tokens: retryBurst} }

func (b *retryBudget) earn() {
	b.mu.Lock()
	b.tokens = min(retryBurst, b.tokens+retryRatio)
	b.mu.Unlock()
}

// spend takes a retry from the budget if there's one.
func (b *retryBudget) spend() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// retriable reports whether a GET that failed with err may be tried again:
// the upstream gave no usable answer, fast, and the client is still there.
func retriable(ctx context.Context, method string, err error) bool {
	if method != http.MethodGet || ctx.Err() != nil {
		return false
	}
	var ce *jsoncall.Error
	if !errors.As(err, &ce) || !ce.NoAnswer() {
		return false // the upstream answered: retrying gets the same answer
	}
	var ne net.Error
	return !errors.Is(err, context.DeadlineExceeded) && !(errors.As(err, &ne) && ne.Timeout())
}

// worthRetrying reports whether there's time for another attempt: the one
// that failed did so fast, and if the call has a deadline (the request's),
// a whole attempt still fits before it, pause included. Otherwise the retry
// would only turn the failure into a timeout later.
func (s *Store) worthRetrying(ctx context.Context, elapsed time.Duration) bool {
	if elapsed > retryFastFailure {
		return false
	}
	deadline, ok := ctx.Deadline()
	return !ok || s.client.Timeout == 0 || time.Until(deadline) > s.client.Timeout+2*retryPause
}

// pause waits before a retry, jittered, unless ctx ends first.
func pause(ctx context.Context) bool {
	t := time.NewTimer(retryPause + rand.N(retryPause)) // #nosec G404 -- jitter only spreads retries out; nothing depends on it being unpredictable
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
