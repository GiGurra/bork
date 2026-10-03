# Backpressure and retry budgets

Design for bork-l0kn5g. Bounded task pools, HTTP admission, and typed client
failures, scope deadlines, retry budgets, and the runnable
[slow-downstream example](../../examples/slow_downstream/main.bork) are implemented. This note makes
bounded admission, HTTP shedding, and retry budgets explicit operations with
checked failure unions. The later deadline/trace propagation work is
bork-gqxe4s; request criticality and adaptive concurrency remain follow-ups.

## Task admission

Keep ordinary spawn/launch unchanged. Add bork/tasks with a scope-owned Pool
resource, a positive capacity, and typed nonblocking submission:

```bork
pool = tasks.Open(app, maxTasks: 64)
tasks.TrySpawn(pool, request, () => work())
```

TrySpawn returns Task[T] | TaskLimitReached | Cancelled; TryLaunch returns
Ok | TaskLimitReached | Cancelled. Callback effects remain open and are
charged to callers together with state, even for a pure callback: capacity and
cancellation are observable shared state. The explicit Scope owns the resulting task and governs its
cancellation. Pool capacity is shared across every submitter and child scope
using that pool, rather than silently inherited by ordinary spawn. No new
ScopePolicy variant changes an existing call's result type.

A slot counts admitted work until its callback returns or panics; awaiting the
result does not release it early. Cancelling an uncooperative callback does not
release its slot. A failed admission never invokes the callback or registers a
task. Cancelled/closing task scopes or a closed pool reject with Cancelled;
saturation gives TaskLimitReached { limit: Int } without cancelling the scope.
If cancellation races admission, an admitted callback may start and then observe
cancellation. Permit acquisition, task registration and scope closing must share
a synchronization boundary so cleanup cannot miss an admitted task.

Tasks remain owned by their explicit scope, not by the pool: the pool limits
capacity, it does not move task ownership. The existing lifetime checks must
prove the pool and callback captures live as long as that task scope. Attachment
can extend the pool resource's ownership but cannot move already running tasks.
The pool closes/disables admissions when its last ownership scope closes.
Callback permit release must remain safe after pool close. Returning or using a
Task success member keeps the lifetime of its explicit task scope.

Submission never implicitly waits. A task holding the last slot and trying to
submit a child gets TaskLimitReached rather than deadlocking before it can await
that child. A producer needing waiting can use a bounded channel and an explicit
worker pool; workers consume task permits, queued values consume channel space.
The first version has no waiting submission variant. A later SpawnWait would be
explicitly cancellable and warn that recursive submission into the same exhausted
pool can deadlock. Recursive work handles rejection, deliberately uses another
pool, or computes the child directly. Pool limits cannot constrain arbitrary
unsafe Go goroutines or ordinary spawn calls that do not select a pool.

A small parallel helper in bork/tasks may take a Pool, Scope and a list callback,
reserve all requested worker slots atomically and return TaskLimitReached without
starting partial work. Existing pure and scoped parMap APIs retain their signatures
and worker-count bounds. They do not implicitly consume a pool. Defer this helper
until core submission and HTTP admission are implemented; do not grow every
existing failure union merely because some callers choose a pool.

Alternatives considered: changing spawn/launch globally to failure unions taxes
unlimited callers; implicit blocking changes submission semantics and creates
recursive deadlocks; silently queuing callbacks hides an unbounded resource.
A typed bounded scope could work, but a shared pool composes across distinct
request scopes without adding compiler scope types. ScopePolicy maxTasks would
require deciding what old spawn does at saturation and is deferred.

## HTTP server admission

Add an optional `admission: Option[Admission] = Option.None` to Listen,
ListenRoutes and ListenTLS. None preserves current server behavior. The record
has proven positive maxInFlight, nonnegative maxQueued, and queueTimeoutMs/
retryAfterMs in the existing TimeoutMs range (0 through 9223372036854), and a rejection status restricted to 429 or 503:

```bork
http.Admission {
  maxInFlight: 64,
  maxQueued: 32,
  queueTimeoutMs: 25,
  retryAfterMs: 100,
  status: 503,
}
```

Admission wraps the complete server mux, before reading any request body,
allocating its byte buffer, constructing the bork Request, or invoking handler
middleware. A permit covers body read, handler execution, request-scope tasks
and cleanup, and response write. Release happens once on every path, including
body errors, disconnects and panics. Configure one admission state per listener,
shared by all routes; TLS and plain listeners have the same behavior. Static
responses, redirects and unknown routes also consume listener capacity.

The queue is FIFO and bounded by maxQueued. An idle permit admits directly
unless existing queued requests must be served first. A full queue rejects at
once. maxQueued zero means no waiting; queueTimeoutMs zero also means no waiting.
Queued requests wait in their existing Go request goroutine, never an additional
worker. Request-context cancellation removes the waiter promptly and releases its
place. Go detects HTTP/1 client disconnects only after consuming a request
body; an unread-body waiter can remain until queue timeout, server cancellation,
or permit transfer. Admission never peeks at or drains transport bodies. Queue timeout rejects with the configured overload response.
If admission races timeout/cancellation, the waiter owns either a permit or a
removed queue entry, never both; it cannot execute the handler after rejecting.

Return 503 for service capacity exhaustion by default. Allow 429 when a caller
chooses a rate-limiting policy. Rejections carry a small text body and Retry-After
as whole delta-seconds, rounding positive milliseconds upward; zero gives zero.
Use division/remainder rounding without an overflowing ms + 999 addition.
These choices follow [HTTP Retry-After](https://www.rfc-editor.org/rfc/rfc9110.html#section-10.2.3)
and [HTTP 429](https://www.rfc-editor.org/rfc/rfc6585.html#section-4).
No request body is buffered on rejection. HTTP/1 rejections set Connection:
close so Go does not drain an incomplete body before writing the rejection.
This bounds application work and buffered bodies, not connection count or every
Go transport goroutine. Existing header/body limits still apply. Queue waits and
active requests stop on server shutdown, and existing graceful drain policies
bound shutdown. A handler panic releases admission during unwinding; no typed
HTTP response is promised for a panic.

AdmissionState returns an optional consistent snapshot of inFlight and queued.
Listen charges net + clock + state in addition to the open handler effects;
routed and TLS listeners retain their full effect bound.

## Typed client failures

Add public HTTP failure types:

```bork
http.Overloaded { retryAfter: Option[time.Duration], response: http.Response }
http.DeadlineExceeded { message: String }
```

Get/Post/Send return Result, an alias for Response | Overloaded |
DeadlineExceeded | Cancelled | IoError. The shared client implementation classifies completed 429 and 503
responses as Overloaded while preserving their complete status, headers and body
in response. Other HTTP statuses remain Response; transport errors remain
IoError. Scope cancellation becomes Cancelled. An expired explicit request
timeout or scope deadline becomes DeadlineExceeded. A deadline wins if already
exhausted before sending, otherwise classify the actual operation's error.
Construction errors in URL/method/headers remain IoError. Body read cancellation
and timeout follow the same classification as the request itself.

Retry-After accepts a nonnegative decimal delta-seconds or an HTTP date, as
specified by HTTP. Convert safely to time.Duration; an absent, malformed,
ambiguous repeated or out-of-range value gives None. Past dates give zero.
Parsing a date uses the injected/system clock and is approximate under clock
skew. A large valid retry delay is not silently shortened: if the caller cannot
wait that long, it returns the original Overloaded. Parsing Retry-After adds the
clock effect to Get/Post/Send; preserve net and add state for cancellation reads.
Thread these effects through client wrappers and their callers during migration.
The pure RetryAfter(headers, now: time.Instant) parser accepts an explicit clock
reading; Send supplies the system reading.

This is a deliberate source migration: exhaustive matches must decide what
HTTP overload, deadline and cancellation mean. Examples should recover the
original response from Overloaded when they need raw status handling. Do not
retry automatically inside Get/Post/Send.

## Shared retry budgets

Add a scope-owned retry budget resource in bork/http. Create it once per
upstream/destination group, and share it across operations/tasks instead of
creating a fresh allowance for every request. A budget has a proven positive
capacity and refill interval in the positive TimeoutMs range, begins full, and accrues one retry token per
interval up to capacity. Compute replenishment lazily from monotonic system time by default;
`clock: Option[time.Clock] = .None` permits an injected clock (including
FixedClock or SystemClock with native `mock time.Now()` in tests). Backward
injected time pauses refill until it catches up. Fractional intervals retain
their phase;
there is no background refill goroutine. Close disables the budget and waiting
operations see cancellation. Its owner/attach semantics use the existing
resource protocol; the retry operation checks both its caller scope and the
budget's closed signal. The budget must outlive the supplied scope under the
existing lifetime checks, including when shared with tasks. No token is spent on an initial attempt. Reserve a
token only just before the next attempt; concurrent retries cannot overspend.
Tokens are not refunded when an admitted attempt fails or is cancelled.

The helper takes an explicit operation and policy:

```bork
http.Retry(s, budget, operation, maxAttempts: 3,
           baseDelayMs: 20, maxDelayMs: 1000)
```

Operation returns Response | Overloaded | DeadlineExceeded | Cancelled |
IoError, and receives the attempt scope. The helper charges operation effects
plus clock, random and state. maxAttempts includes the initial attempt and is
positive. Delays are nonnegative and proven to fit Go durations; maxDelayMs is
at least baseDelayMs. There is no retry of IoError, DeadlineExceeded, Cancelled,
or ordinary HTTP status responses in the first version. Exhausting attempts or
the shared budget returns the last Overloaded, without another attempt.

Calling Retry asserts that replay is safe: it is explicit, rather than enabled
for every HTTP request. Examples use GET or an idempotent operation; business
code must decide whether a side effect is safely repeatable. Request body data
is immutable, but that does not make a POST safe to replay. A retry callback can
still perform effects; the helper cannot prove external idempotency.

Use capped exponential backoff with full jitter, using overflow-safe arithmetic.
Default draws use bork/rand.IntBetween. An optional jitter callback accepts the
nanosecond ceiling and its result is clamped to 0..ceiling, never reported as an
IoError. Clock injection controls refill; waits and scope deadlines use real time.
Wait for serverMinimum + uniform jitter in [0, cappedBackoff], with serverMinimum
zero when Retry-After is absent. Check the addition for overflow; an unrepresentable
wait returns the last Overloaded. The wait is at least any valid Retry-After; maxDelayMs caps local backoff, not
server-specified Retry-After. Waiting is scope-cancellable and consumes no retry
token. Check scope/budget cancellation, remaining deadline and maxAttempts
before waiting and immediately before token admission. If Retry-After or backoff
cannot fit the remaining deadline, return the last Overloaded. If that deadline
is already exhausted, return DeadlineExceeded. If no deadline exists, attempts
and the shared budget still bound retries; each operation retains its own
explicit timeout where needed.

The shared deadline bridge exposes cancelAfter through the scope context. It
records the earliest explicit/parent deadline, uses monotonic time to calculate
the remaining budget, and never extends it through another cancelAfter call. Descendants
opened before a later parent deadline must also observe that tighter budget;
query effective deadline through scope ancestry rather than a creation-time copy.
Preserve deadlines on incoming Go contexts and keep deadline causes typed.
Nonpositive millisecond delays cancel immediately; huge positive values saturate
without wrapping. Scope contexts expose DeadlineExceeded through Err and Cause. Keep
this bridge distinct from cross-service propagation and ambient values, which
remain bork-gqxe4s. Request criticality scheduling and adaptive per-destination
limits are deferred until basic admission and budgets have clear measurements.

## Implementation and validation

Implement in reviewable steps: bounded task pool; HTTP
admission; client failures and Retry-After; deadline bridge and retry budget;
then the runnable slow-downstream example. Coordinate the task registry/admission
boundary with the scope ownership worker. Each step keeps task lifetimes and effect
signatures checked, and updates the prelude/standard documentation.

Tests use gates/channels to control concurrency rather than relying on sleep
ordering. Verify exact shared-pool bounds across scopes, no callbacks on rejection, slots
released on panic, cancelled submission, and nested saturated submission; verify
pool close/attachment and Task lifetimes. Reject pure functions that use pool
admission without declaring state. Verify FIFO queueing, queue bounds/timeouts,
disconnects, shutdown, admission before body allocation, release after response
write/body errors/panic, and independent listener budgets. Client tests include
429/503, all Retry-After forms, invalid and huge values, cancellation/deadline
classification during body reads, and unchanged other statuses.

Inject a clock/random source into budget/backoff tests to verify refill,
concurrent token races, no retry when budget/attempt/deadline is exhausted,
Retry-After minimum waits, cancellation during delay, overflow boundaries and
one initial attempt without a token. Include upper-bound queue/refill durations
and Retry-After rounding, rejecting duration overflow before starting timers. The example starts a downstream with a
small admission limit and a caller with a shared budget, sends an explicit burst,
and reports successes, overloads and total attempts. It must show bounded
active handlers/queue size and bounded retries while ordinary accepted calls
finish; do not rely on a machine-specific throughput benchmark.
