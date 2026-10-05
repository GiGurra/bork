# bork/http

## HTTP resources

`bork/http`, a server whose lifetime is a scope (`http.Listen(addr, s, handler)` serves until `s` closes, each request on its own goroutine, with a scope of its own that the handler gets), a scope-cancellable client (`http.Get(url, s, timeoutMs: 0)`, `http.Post(url, contentType, body, s, timeoutMs: 0)`, `http.Send(method, url, headers, body, s, timeoutMs: 0)`), whose optional millisecond timeout covers both the request and reading the response body; headers are maps of names to value lists, preserving repeated values, and transport failures return `IoError`, cancellation returns `Cancelled`, expired deadlines return `http.DeadlineExceeded`, and 429/503 responses return `http.Overloaded`, and helpers (`http.Text`, `http.JsonReply`, `http.Segments` for matching paths with list patterns). Status codes are facts: `http.Text(42, "x")` does not compile. See [examples/signup_api](../../examples/signup_api/main.bork).

## Routes and shutdown

HTTP servers support `http.ListenRoutes(addr, s, routes, drainTimeoutMs: 0)` with immutable `http.Route { pattern: "GET /users/{id}", handler: ... }` records. Patterns follow Go 1.22 ServeMux: method matching, HEAD for GET, redirects, `{name}` and `{name...}` path captures (in `request.params`), and 404/405 responses. Invalid/conflicting patterns return `IoError` before listening. `http.Handler` permits all five effects, so routed server functions declare `uses io + net + clock + random + state`; ordinary functions `(http.Handler) => http.Handler` implement middleware. The original `Listen` retains open handler effects.

`http.Body[T: Decode](request)` decodes JSON with field facts. `Query` parses query values; `QueryAs[T]` and `PathAs[T]` load derived records, using literal strings and JSON syntax for other fields. Missing Option fields become None; repeated values for record fields are errors. `Form` parses URL-encoded body values separately from the query. `Multipart` returns value lists and immutable upload Bytes; multipart and incoming server bodies default to 16 MiB. Listen, ListenRoutes, ListenTLS and Multipart accept a nonnegative maxBodyBytes override; zero rejects nonempty bodies. A server rejects oversized bodies with HTTP 413. `Static(request, root, prefix: "")` serves a directory through Go's file server, buffering the response; it supports directory listings, symlinks, ranges and conditional requests. `ListenTLS(addr, s, routes, certFile, keyFile, drainTimeoutMs: 0)` loads PEM certificate/key files and requires TLS 1.2 or newer.

Closing the server scope cancels request scopes and stops accepting new connections, then waits for active handlers through Go's graceful shutdown. A zero drain timeout inherits `cleanupTimeout`; without that policy shutdown waits indefinitely. A positive per-server timeout overrides the default and cannot exceed a positive scope cleanup timeout (`IoError` otherwise). When draining times out, connections are forcibly closed; cooperative handlers may still be finishing. `Wait` waits for the listener to stop, while scope cleanup drains active requests. See [http_routes](../../examples/http_routes/main.bork).

## HTTP API

HTTP clients take an explicit `Scope` and optional nonnegative millisecond timeout: `http.Get(url, s, timeoutMs: 0)`, `http.Post(url, contentType, body, s, timeoutMs: 0)`, or `http.Send(method, url, headers, body, s, timeoutMs: 0)`. Zero uses scope cancellation and its effective deadline; the maximum is 9223372036854 milliseconds. Use `http.ValidTimeout(value)` as a guard for a dynamic timeout. `http.Headers` is `Map[String, List[String]]`; use `{:}` for no headers. `http.HeaderOf` finds the first value without regard to case. Clients use `net + clock + state` and return `http.Result`, an alias for `Response | Overloaded | DeadlineExceeded | Cancelled | IoError`. Transport and request-construction errors remain `IoError`; cancellation and deadline expiry have their own types. Completed 429/503 responses become `Overloaded { response, retryAfter }`, preserving the full body and repeated headers. Other HTTP statuses remain responses. Deadline and cancellation errors during body reads follow the same classification. Already exhausted deadlines stop before sending. `cancelAfter` exposes the earliest
scope/ancestor deadline, including updates to ancestors of existing scopes. There are no automatic retries.

HTTP servers support `http.ListenRoutes(addr, s, routes, drainTimeoutMs: 0)` with immutable `http.Route { pattern: "GET /users/{id}", handler: ... }` records. Patterns follow Go 1.22 ServeMux: method matching, HEAD for GET, redirects, `{name}` and `{name...}` path captures (in `request.params`), and 404/405 responses. Invalid/conflicting patterns return `IoError` before listening. `http.Handler` permits all five effects, so routed server functions declare `uses io + net + clock + random + state`; ordinary functions `(http.Handler) => http.Handler` implement middleware. The original `Listen` retains open handler effects.

`http.Body[T: Decode](request)` decodes JSON with field facts. `Query` parses query values; `QueryAs[T]` and `PathAs[T]` load derived records, using literal strings and JSON syntax for other fields. Missing Option fields become None; repeated values for record fields are errors. `Form` parses URL-encoded body values separately from the query. `Multipart` returns value lists and immutable upload Bytes; multipart and incoming server bodies default to 16 MiB. Listen, ListenRoutes, ListenTLS and Multipart accept a nonnegative maxBodyBytes override; zero rejects nonempty bodies. A server rejects oversized bodies with HTTP 413. `Static(request, root, prefix: "")` serves a directory through Go's file server, buffering the response; it supports directory listings, symlinks, ranges and conditional requests. `ListenTLS(addr, s, routes, certFile, keyFile, drainTimeoutMs: 0)` loads PEM certificate/key files and requires TLS 1.2 or newer.

Closing the server scope cancels request scopes and stops accepting new connections, then waits for active handlers through Go's graceful shutdown. A zero drain timeout inherits `cleanupTimeout`; without that policy shutdown waits indefinitely. A positive per-server timeout overrides the default and cannot exceed a positive scope cleanup timeout (`IoError` otherwise). When draining times out, connections are forcibly closed; cooperative handlers may still be finishing. `Wait` waits for the listener to stop, while scope cleanup drains active requests. See [http_routes](../../examples/http_routes/main.bork).

## Deadline budgets between Bork services

Get, Post and Send automatically send the calling scope's remaining deadline
as `Bork-Timeout-Ns`, including a tighter timeoutMs. They recompute it for each
call and redirect, and return typed DeadlineExceeded without sending when the
budget is exhausted. The client reserves this header: manual values are replaced
by the effective budget, or removed when the call has no deadline. Other headers
retain their existing behavior. Redirect policy and sensitive-header handling
remain Go's defaults (or an explicitly configured native client policy).

Listen, ListenRoutes and ListenTLS accept `requestTimeoutMs: TimeoutMs = 0`.
A positive value caps each request from entry to the server handler; zero adds no
server deadline. The request uses the earliest listener/context deadline, server
cap and incoming budget. The header is one unsigned decimal nanosecond count,
with at most 19 digits, in 0..9223372036854775807. Missing adds no caller limit;
zero returns 504. Malformed, repeated, negative or out-of-range fields return
400. Validation and deadline cancellation precede admission and body buffering.
Deadline expiry while queued returns 504; an ordinary admission timeout remains
429/503. Expiry interrupts blocked body reads and returns 504 where possible.
HTTP/1 early rejections close the connection to avoid draining unfinished uploads.
Current listeners serve HTTP/1; outgoing clients can negotiate HTTP/2. Native
server transports must support response-controller read deadlines to
buffer cancellable bodies; an unsupported transport returns 500 before reading.

A handler scope exposes its effective deadline, including later changes to the
listener scope, for outgoing calls and local operations. Cooperative handlers may
return their own response after cancellation. Request deadlines do not forcibly
stop tasks/finalizers or bound total cleanup time; request scopes keep their
existing unbounded cleanup defaults and do not inherit listener cleanup policies.
Use explicit taskTimeout/cleanupTimeout policies where bounded waits are needed.

This is a bork-to-bork relative-budget protocol; a grpc-timeout bridge could be
added later. Receiving timers start on arrival, so wire transit and internal Go
transport replay time are not deducted exactly from the remote budget. The
caller retains its own local deadline and cancels its HTTP call. A server cap
bounds cooperative work when disconnect detection is delayed; clocks need not be
synchronized. See the [boundary design](../design/http-propagation.md).

## Retry-After hints

`Overloaded.retryAfter` is `Option[time.Duration]`. Exactly one Retry-After
value is accepted, without regard to header name case: nonnegative whole
seconds or an HTTP date. Missing, malformed, repeated, or overflowing values
become None; past dates give zero. Obsolete two-digit years follow the supplied
clock and HTTP's 50-year rule, rather than a fixed century pivot. The raw response always keeps its headers.
A valid hint is never silently shortened.

`http.RetryAfter(headers, now: time.Instant)` exposes the pure parser for
explicit clock readings and deterministic tests. Client requests read the
system clock when parsing dates; date hints are approximate under clock skew.
See [HTTP date formats](https://www.rfc-editor.org/rfc/rfc9110.html#section-5.6.7).
Handle `Overloaded.response` when an application needs the original status or
body. A typed overload result alone does not authorize replaying side effects.
See [Shared retries](#shared-retries) for the explicit shared-budget API.

## Bounded admission

Pass `admission: Option.Some { value: http.Admission { maxInFlight: 64,
maxQueued: 32, queueTimeoutMs: 25, retryAfterMs: 100, status: 503 } }` to
`Listen`, `ListenRoutes`, or `ListenTLS` to bound application work on one
listener. Omit it to preserve unlimited admission. `maxInFlight` must be
positive; `maxQueued` defaults to zero. Queue and retry durations use the
checked `TimeoutMs` range. Status defaults to 503 and may also be 429.

Admission precedes request body buffering and handler middleware, and covers
body reads, request tasks and cleanup, and response writing. Waiters enter a
bounded FIFO queue. A full queue rejects immediately; zero `maxQueued` or
`queueTimeoutMs` disables waiting. Request-context cancellation removes queued requests; queue timeout rejects
them. Go detects HTTP/1 client disconnects only after consuming their body, so
unread-body waiters can remain until timeout, server cancellation, or permit
transfer. Admission never peeks at or drains those bodies. Body errors and handler panics release capacity.

Rejections include `Retry-After` rounded upward to whole seconds (zero stays
zero). HTTP/1 rejections close the connection so an unfinished body cannot
hold up the overload response. The limit bounds application work and body
buffers, rather than transport connections or all Go goroutines. Graceful
shutdown cancels queued waits and retains the existing drain policy.

`http.AdmissionState(server)` returns `Option[http.AdmissionLoad]`, a consistent
snapshot with `inFlight` and `queued`, or None when admission is disabled. It
uses `state`. `Listen` adds `clock + state` to its open handler effects because
admission observes shared capacity and timed waits. Routed and TLS listeners
already declare all five effects. See the admission fixture in
[testdata/cases/http_admission](../../testdata/cases/http_admission/main.bork).

## TLS certificates

HTTP's `Certificate` stores validated PEM certificate and key bytes in a private
variant, so its declaration needs no Go signature resolution. `LoadCertificate`
reads and validates files; `ParseCertificate` validates supplied Bytes. Both
return `Certificate | IoError`. Its Show instance prints only `http.Certificate`.
`ListenTLS` reconstructs the Go certificate from immutable bytes when opening the
listener; certificate or key file changes after loading cannot alter the value.

## Examples

HTTP clients take a scope (`http.Get(url, s, timeoutMs: 0)`); cancellation and optional timeouts cover the response body too. Request/response headers are `Map[String, List[String]]`, preserving repeated header values.

HTTP servers accept method/path routes, middleware functions, typed body/query/path decoding, TLS, static files and forms. Server body limits are configurable (16 MiB by default). Scope cleanup drains active requests, bounded by `cleanupTimeout` or a per-server timeout. See [http_routes](../../examples/http_routes/main.bork).

## Shared retries

Create one `http.OpenRetryBudget(s, capacity: 4, refillMs: 1000)` per destination
and share it across callers. The scope-owned resource starts full and lazily adds
one token per interval, up to capacity, without a refill goroutine. Capacity and
refill intervals must be positive; refill milliseconds must fit `TimeoutMs`.
Attachment extends ownership; the budget closes when its last owner closes.
The budget and any captured injected clock must outlive their explicit scope.

```bork
import "bork/http"

fn fetch(url: String, request: Scope, budget: http.RetryBudget in request) uses net + clock + random + state: http.Result {
  http.Retry(request, budget, operation: attempt => http.Get(url, attempt), maxAttempts: 3, baseDelayMs: 20, maxDelayMs: 1000)
}

fn main() uses io + net + clock + random + state {
  scope app {
    budget = http.OpenRetryBudget(app, capacity: 4, refillMs: 1000)
    scope first {
      println(fetch("https://example.com", first, budget))
    }
    scope second {
      println(fetch("https://example.com", second, budget))
    }
  }
}
```

`Retry` explicitly asserts that replay is safe. It retries only `Overloaded`;
responses, transport errors, cancellation and deadline failures return directly.
`maxAttempts` is positive and includes the initial attempt, which spends no
budget token. Further attempts reserve shared tokens immediately before work;
concurrent callers cannot overspend. Each attempt receives a fresh scope, whose
tasks and resources finish cleanup before returning or beginning another attempt.
Closing the budget cancels active attempt scopes and retry waits. An operation
must cooperate with cancellation to finish promptly.

Waits use the nonnegative server hint plus full jitter from zero through capped
exponential local backoff. `maxDelayMs` must be at least `baseDelayMs`; it caps
local backoff, and never shortens a server hint. Overflow, an insufficient
remaining scope deadline, exhausted attempts or an empty budget return the last
`Overloaded`. An already expired deadline gives `DeadlineExceeded`; cancellation
gives `Cancelled`. Tokens spent on failed or cancelled retries are not refunded.
Negative hints in a manually constructed `Overloaded` are treated as zero.
The helper charges operation effects plus `clock + random + state`.

For deterministic refill tests, pass `clock: .Some { value: time.FixedClock(...) }`
or another `time.Clock` to `OpenRetryBudget`. The default `.None` reads monotonic
system time. Backward injected readings pause refill until the clock catches up;
fractional intervals retain their phase. Native `mock time.Now()` is another test
option when the injected clock is `time.SystemClock()`; it does not replace the
budget's default monotonic Go clock. The optional `Retry` argument
`jitter: Option[(Int) uses random + state => Int]` receives a nanosecond ceiling.
Its result is clamped to `0..ceiling`. Default jitter uses `bork/rand.IntBetween`,
which can also be mocked in native tests. Timed waits and scope deadlines retain
real system timing.

[examples/slow_downstream](../../examples/slow_downstream/main.bork) runs a local
service with two active requests and two queued requests. Six GET callers share
four retry tokens. Gates hold accepted work while excess callers exhaust the
budget, then let the accepted work finish. It reports four successes, two
overloads, ten total attempts, and a peak of two handlers. The example uses a
fixed refill clock and zero local delay to show exact bounds without a throughput
benchmark.

## Trace context and marked values

`Send` forwards ambient declarations explicitly marked `propagated("header")`.
Bound marked values replace matching manual headers, case insensitively;
unmarked values never cross this boundary automatically. The server clears
inherited propagated labels before decoding each request and restores them
when it finishes. Missing, repeated singleton, or invalid values stay unbound.
Warnings identify the header and error, without including raw input. Logging
adds no effect to `Listen`.

```bork
import "bork/http"
use http.TraceCodecs
propagated("traceparent") logged ambient trace: http.TraceParent
propagated("tracestate") ambient vendor: http.TraceState
```

`TraceParent` and `TraceState` are checked String aliases. `TraceParentOf(headers)`
returns `Option[TraceParent] | DecodeError`: missing is None; repeated or invalid
is an error. `TraceStateOf(headers)` returns `Option[TraceState] | DecodeError`,
combining repeated fields in order. It returns None without a valid parent;
invalid state leaves a valid parent usable. Headers with multiple case variants
of the tracestate key are ambiguous in an unordered Map and return DecodeError;
incoming HTTP headers already have one canonical key. `use http.TraceCodecs`
provides ordinary `Decode` instances for both aliases.

Validation follows [W3C Trace Context](https://www.w3.org/TR/trace-context/).
Version 00 requires its exact lowercase hexadecimal format and nonzero ids.
Version ff is rejected; future versions check the known prefix and extension
boundary while preserving unknown fields. Unknown flag bits are accepted.
Tracestate limits are 32 members, 256 bytes per key/value, and 512 bytes overall,
with the standard tenant/system key limits and no duplicate keys. Empty state
and whitespace-only members are accepted. Forwarding preserves valid values;
it does not create spans, ids, sampling decisions, or vendor entries.

Selected outgoing W3C fields are validated after marked values replace manual
ones. An invalid/repeated parent removes both parent and state; invalid state
removes only state. A marked state may accompany a valid manual parent. State
alone is dropped. Redirects retain Go's header forwarding rules and revalidate
selected trace fields; marked fields are not reintroduced on redirects.

Request labels supply logs and downstream forwarding. A handler's typed `needs`
remain captured where the function was made. To use an incoming trace in typed
application code, extract it with `TraceParentOf` and explicitly bind it with
`with (trace: value)`. The [service_context example](../../examples/service_context/main.bork)
shows this checked binding, automatic forwarding through two services, and
shrinking deadline budgets with a server policy cap. The `Option.Some` payload
retains the checked `TraceParent` fact, so this binding needs no additional guard.
