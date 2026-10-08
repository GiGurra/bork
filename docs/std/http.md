# bork/http

`bork/http` serves scoped HTTP handlers and sends requests whose deadlines and cancellation follow an explicit scope.

This complete program starts a local server on a free port, calls it, and stops it when the scope ends:

```bork
import "bork/http"

fn main() {
  scope app {
    match (http.Listen("127.0.0.1:0", app, (req, rs) => http.Text(200, "hello"))) {
      server: http.Server => {
        match (http.Get("http://" + http.Address(server), app)) {
          response: http.Response => println(response.status, response.body)
          error => eprintln(toString(error))
        }
      }
      error: IoError => eprintln(error.message)
    }
  }
}
```

Save as `main.bork` and run `bork run main.bork`. Output:

```text
200 hello
```

Each incoming request gets its own scope; returning from the handler closes it. The server runs until its owning scope closes. A client call covers sending the request and reading the complete response body.

## Client API

Names below belong to `http`. All client calls return `Result = Response | Overloaded | BodyTooLarge | DeadlineExceeded | Cancelled | IoError`.

| Signature | Meaning |
| --- | --- |
| `Get(url: String, s: Scope, timeoutMs: TimeoutMs = 0, maxBodyBytes: BodyLimit = 16777216) uses net + clock + state: Result` | GET and read the response. |
| `Post(url: String, contentType: String, body: String, s: Scope, timeoutMs: TimeoutMs = 0, maxBodyBytes: BodyLimit = 16777216) uses net + clock + state: Result` | POST a body with a Content-Type header. |
| `Send(method: String, url: String, headers: Headers, body: String, s: Scope, timeoutMs: TimeoutMs = 0, maxBodyBytes: BodyLimit = 16777216) uses net + clock + state: Result` | Send an arbitrary method and repeated headers. |
| `HeaderOf(headers: Headers, name: String): Option[String]` | Find the first header value without regard to case. |
| `RetryAfter(headers: Headers, now: time.Instant): Option[time.Duration]` | Parse an overload hint with an explicit clock reading. |

| Type | Fields or constraint |
| --- | --- |
| `Headers` | `Map[String, List[String]]`; preserves repeated values. Use `{:}` for no headers. |
| `Status` | Int in `100..599`; guard dynamic values with `http.ValidStatus`. |
| `BodyLimit` | Nonnegative Int; guard dynamic limits with `http.ValidBodyLimit`. |
| `TimeoutMs` | Int in `0..9223372036854`; guard with `http.ValidTimeout`. |
| `Response` | `{ status: Status, headers: Headers, body: String }` |
| `Overloaded` | `{ retryAfter: Option[time.Duration], response: Response }` |
| `BodyTooLarge` | `{ limit: BodyLimit }`; decoded response exceeds maxBodyBytes. |
| `DeadlineExceeded` | `{ message: String }` |

Eager calls read at most `maxBodyBytes` decoded response bytes plus one byte to detect overflow. The default is 16 MiB. The limit also applies after automatic gzip decompression and to 429/503 bodies. Exact-limit bodies succeed; zero accepts only empty bodies. Overflow returns `BodyTooLarge` and closes the body without returning a partial response. Content-Length is not trusted as a byte bound. Set a larger nonnegative limit explicitly when needed.

For example, bound a download to 1 MiB and handle overflow separately:

```bork
import "bork/http"

fn fetch(url: String, s: Scope) uses net + clock + state: String | http.BodyTooLarge | http.Overloaded | http.DeadlineExceeded | Cancelled | IoError {
  response = http.Get(url, s, maxBodyBytes: 1048576)?
  response.body
}
```

A zero timeout adds no per-call deadline; the scope and its ancestors still cancel the call. A positive timeout caps it, including response-body reads. Completed 429/503 responses become `Overloaded`, retaining the body and headers; other HTTP statuses are ordinary `Response` values. Transport or request-construction failures are `IoError`. Cancellation and deadline expiry have their own result types. The library adds no automatic overload retry; Go's transport can replay eligible requests after connection failures and follows its normal redirect policy. Use Retry only when repeating the operation is safe.

A malformed URL exercises the failure branch without an external service:

```bork
import "bork/http"

fn main() {
  scope app {
    match (http.Get(":", app)) {
      _: IoError => println("request construction failed")
      result => println(toString(result))
    }
  }
}
```

The invalid URL takes the IoError branch:

```text
request construction failed
```

## Streaming client bodies

Use `GetStream` or `SendStream` to receive headers before the complete body arrives. `StreamResponse` has `status: Status`, `headers: Headers`, and `body: BodyReader`. All HTTP statuses, including 429/503, remain StreamResponse values; inspect status and use RetryAfter explicitly. Streaming calls do not retry uploads or buffer overload bodies.

| Signature | Meaning |
| --- | --- |
| `GetStream(url: String, s: Scope, timeoutMs: TimeoutMs = 0, maxBodyBytes: Option[BodyLimit] = .None)` | GET; return `StreamResult`. |
| `SendStream(method: String, url: String, headers: Headers, s: Scope, body: Option[BodyReader] in s = .None, timeoutMs: TimeoutMs = 0, maxBodyBytes: Option[BodyLimit] = .None)` | Send an optional binary upload stream; return `StreamResult`. |
| `Read(body: BodyReader, size: ReadSize = 4096)` | Read up to size immutable bytes; return `ReadResult`. |
| `Close(body: BodyReader)` | Abandon a stream early; safe more than once. |
| `OpenBody(s: Scope, next: (Scope) => ReadResult in s)` | Create a producer-backed upload with no queued chunks. Adds the callback's effects. |

These functions use `net + clock + state`, except Close uses `net + state`. `ReadSize` is an Int in `1..16777216`, checked with ValidReadSize. `ReadResult = Bytes | Eof | BodyTooLarge | DeadlineExceeded | Cancelled | IoError`; `StreamResult = StreamResponse | BodyTooLarge | DeadlineExceeded | Cancelled | IoError`.

Read returns available bytes, which may be smaller than size. Concurrent reads serialize; chunk boundaries do not correspond to application messages. Eof and errors are terminal. Bytes are binary: use `encoding.Utf8` to encode text and `encoding.ParseUtf8` when a complete text value has been assembled. Individual chunks may split a UTF-8 character.

The stream closes on Eof, failure, explicit Close, or last-owner scope cleanup. Scope cancellation interrupts blocked reads. A timeout covers headers and all subsequent body reads; returning headers does not reset it. Attach/move follow ordinary resource ownership, while the original total timeout continues. The optional maxBodyBytes bounds total decoded response bytes; no total limit is imposed by default, so long-lived event streams can continue. Read still bounds each allocation by size. At the configured limit, the next Read probes for overflow: exact-limit EOF succeeds and extra bytes return BodyTooLarge.

OpenBody runs its callback sequentially in a child scope and blocks on consumer backpressure between chunks. Return Bytes for data, Eof to finish, or a typed failure. Closing the body cancels that child scope and waits for producer cleanup; callbacks must cooperate with that scope's cancellation. Producer work is anchored to s: ending s cancels and joins it before releasing captured resources, even if the body reader is attached to an outer scope. That attached producer body then reads Cancelled; attachment does not migrate its callback or captures. SendStream consumes and closes its supplied upload body, including on failure or abandonment, so a blocked source read cannot retain its forwarding worker. Before returning final response headers, SendStream finishes or cancels and joins the upload. An early server response stops unfinished production; the returned response reader has no active upload dependency and can be attached independently. There is no full-duplex client in this phase. If an early response arrives while socket writes are still blocked, SendStream cancels that transport request and returns the received status/headers promptly; reading its body can report a typed cancellation or transport failure if the response was truncated. Nonreplayable upload bodies follow Go's redirect rules: 307/308 cannot replay them; eligible 301/302/303 redirects may change the method to GET.

```bork
import "bork/http"
import "bork/encoding"

fn chunks(url: String, app: Scope) uses io + net + clock + state: Ok | http.BodyTooLarge | http.DeadlineExceeded | Cancelled | IoError {
  response = http.GetStream(url, app, maxBodyBytes: .Some(1048576))?
  for {
    match (http.Read(response.body)) {
      bytes: Bytes => println(encoding.Hex(bytes))
      _: http.Eof => break
      error: http.BodyTooLarge => return error
      error: http.DeadlineExceeded => return error
      error: Cancelled => return error
      error: IoError => return error
    }
  }
}
```

See [http_stream_client](../../examples/http_stream_client/main.bork) for a runnable local download into an fs.File and incremental upload. A producer can adapt other resources, such as net.Read, by mapping their EOF/error results to ReadResult; no shared fs/net stream trait is required.

## Server API

All listener functions return `Server | IoError`. Their final options share these types and defaults:

| Named option | Meaning |
| --- | --- |
| `drainTimeoutMs: TimeoutMs = 0` | Graceful shutdown limit; zero inherits the scope's cleanup timeout. |
| `maxBodyBytes: BodyLimit = 16777216` | Incoming buffered-body limit; `BodyLimit` is a nonnegative Int. |
| `admission: Option[Admission] = Option.None` | Optional bound on active and queued application requests. |
| `requestTimeoutMs: TimeoutMs = 0` | Cap each incoming request's deadline; zero adds no cap. |

| Signature before the shared options | Effects |
| --- | --- |
| `Listen(addr: String, s: Scope, handler: (Request, Scope) => Response): Server \| IoError` | `net + clock + state`, plus the handler's open effects. |
| `ListenRoutes(addr: String, s: Scope, routes: List[Route]): Server \| IoError` | `io + net + clock + random + state` |
| `ListenTLS(addr: String, s: Scope, routes: List[Route], certFile: String, keyFile: String): Server \| IoError` | `io + net + clock + random + state` |
| `Address(server: Server): String` | Pure; includes the selected port when opening with port 0. |
| `Wait(server: Server) uses net` | Wait for one listener to stop. |
| `WaitAny(servers: List[Server]) uses net: Ok \| IoError` | Wait until any listener stops or its scope is cancelled. |
| `WaitAll(servers: List[Server]) uses net: Ok \| IoError` | Wait for every listener to stop or its scope to be cancelled. |
| `Text(status: Status, body: String): Response` | Pure; plain-text Content-Type. |
| `JsonReply(status: Status, body: String): Response` | Pure; JSON Content-Type; body is already encoded text. |
| `Segments(path: String): List[String]` | Pure; split a path into nonempty components. |

| Type | Fields |
| --- | --- |
| `Request` | `method: String`, `path: String`, `query: String`, `params: Map[String, String]`, `headers: Headers`, `body: String` |
| `Handler` | `(Request, Scope) uses io + net + clock + random + state => Response` |
| `Route` | `{ pattern: String, handler: Handler }` |

Listen routes every request to one handler. ListenRoutes and ListenTLS accept method/path patterns: `"GET /users/{id}"` captures `id` in `request.params`. GET also accepts HEAD; patterns support redirects, `{name...}` captures, and automatic 404/405 responses. Invalid or conflicting patterns return `IoError` before opening a socket.

`http.Handler` permits all five effects. A function starting a routed listener therefore declares all five, even when its handlers are pure. Middleware is an ordinary `(http.Handler) => http.Handler` function.

## Routes and checked input

Decode body, query or path fields into derived records. Field facts are checked before the decoder returns a value. This example sends one valid path and one invalid path:

```bork
import "bork/codec"
import "bork/http"
use codec.Defaults

pred positive(n: Int) { n > 0 }
type UserPath = { id: Int where positive } derive (codec.Decode)

fn user(req: http.Request, rs: Scope): http.Response {
  match (http.PathAs[UserPath](req)) {
    path: UserPath => http.Text(200, s"user ${path.id}")
    error: codec.DecodeError => http.Text(422, error.path + ": " + error.message)
  }
}

fn main() {
  scope app {
    match (http.ListenRoutes("127.0.0.1:0", app, [.{ pattern: "GET /users/{id}", handler: user }])) {
      server: http.Server => {
        for (id in ["42", "0"]) {
          match (http.Get("http://" + http.Address(server) + "/users/" + id, app)) {
            response: http.Response => println(response.status, response.body)
            error => eprintln(toString(error))
          }
        }
      }
      error: IoError => eprintln(error.message)
    }
  }
}
```

The valid path succeeds and the fact violation returns 422:

```text
200 user 42
422 .id: must be positive
```

| Decoder/helper signature | Result |
| --- | --- |
| `Body[T: codec.Decode](request: Request)` | `T \| json.JsonError \| codec.DecodeError` |
| `Query(request: Request)` | `Headers \| IoError` |
| `QueryAs[T: codec.Decode](request: Request)` | `T \| IoError \| codec.DecodeError` |
| `PathAs[T: codec.Decode](request: Request)` | `T \| codec.DecodeError` |
| `Form(request: Request)` | `Headers \| IoError` |
| `Multipart(request: Request, maxBodyBytes: BodyLimit = 16777216)` | `MultipartForm \| IoError` |
| `Static(request: Request, root: String, prefix: String = "") uses io` | `Response` |

Except for Static, these helpers are pure. Body parses JSON; syntax errors come from `bork/json`, while type/fact failures come from `bork/codec`. QueryAs and PathAs require derived record decoders, use literal strings for String fields and JSON syntax for other fields, and make missing Option fields None or use declared defaults. Query and path keys use canonical codec wire names and accept aliases; errors identify the supplied key. Supplying both a canonical key and an alias, or repeated query values for one field, is an error. Unknown keys follow the selected decoder’s policy, including `codec.Unknown.Reject`.

Form parses a URL-encoded body independently of the query. Multipart returns `MultipartForm { values: Headers, files: List[Upload] }`, where `Upload` has `field: String`, `filename: String`, `contentType: String`, and immutable `data: Bytes`.

Incoming bodies and Multipart default to 16 MiB. Override `maxBodyBytes` with a nonnegative value; zero rejects nonempty bodies. An oversized server request receives 413. Static buffers its response and supports directory listings, symlinks, ranges and conditional requests; choose the root and prefix accordingly. See [http_routes](../../examples/http_routes/main.bork) for middleware, forms, TLS and static serving.

## Multiple listeners

Closing the server scope cancels request scopes, stops accepting new connections, and drains active handlers. A zero `drainTimeoutMs` inherits `cleanupTimeout`; without that policy draining is unbounded. A positive server timeout overrides the default and cannot exceed a positive scope cleanup timeout (`IoError` otherwise). Expiry forcibly closes connections; cooperative handlers can still be finishing.

Wait/WaitAny/WaitAll can finish before draining completes. Scope cleanup performs the drain. WaitAny reports an unexpected stop as `IoError { path: address, message }`; a completed failure remains an error after cancellation. WaitAll returns the first unexpected failure in list order. Both return Ok immediately for an empty list.

```bork
import "bork/http"

fn serve(app: Scope) uses io + net + clock + random + state: Ok | IoError {
  api = http.ListenRoutes("127.0.0.1:8080", app, [
    .{ pattern: "GET /health", handler: (req, rs) => http.Text(200, "ok") },
  ])?
  metrics = http.ListenRoutes("127.0.0.1:9090", app, [
    .{ pattern: "GET /metrics", handler: (req, rs) => http.Text(200, "requests 0") },
  ])?
  http.WaitAny([api, metrics])
}

fn main() {
  scope app with cleanupTimeout(5000) {
    match (serve(app)) {
      _: Ok => {}
      error: IoError => eprintln(error.path + ": " + error.message)
    }
  }
}
```

This server waits for scope cancellation (Ctrl+C) or listener failure. Each listener exposes only its own routes. See [http_multi](../../examples/http_multi/main.bork) for a runnable multi-listener demo and startup-failure handling.

## Deadlines between services

Get, Post and Send forward the scope's remaining deadline, including a tighter `timeoutMs`, in the reserved `Bork-Timeout-Ns` header. Each call and redirect recomputes the budget. An exhausted deadline returns `DeadlineExceeded` before sending; manual values of this header are replaced, or removed when there is no deadline.

Incoming requests use the earliest listener/context deadline, server `requestTimeoutMs` cap and caller budget. Missing budgets add no limit; zero returns 504. Invalid or repeated budgets return 400 before admission or body buffering. Deadline expiry while queued or reading a body returns 504 where possible; ordinary admission timeout remains 429/503.

A handler's scope carries this deadline into outgoing calls and local operations, including later changes to the listener deadline. Handlers must cooperate with cancellation and may return their own response after cancellation. Request scopes do not inherit listener cleanup policies: use explicit taskTimeout/cleanupTimeout scopes when bounded task/finalizer waits are needed. A deadline does not forcibly stop arbitrary code or bound total cleanup time.

Relative budgets start on arrival, so wire transit and internal transport replay time are not deducted exactly from the remote timer. The caller still enforces its local deadline; clocks need not be synchronized. Current listeners serve HTTP/1; outgoing clients can negotiate HTTP/2. Early HTTP/1 rejection closes the connection to avoid draining an unfinished upload. See the [boundary design](../design/http-propagation.md) for the header format and transport constraints, and [service_context](../../examples/service_context/main.bork) for a two-service example.

## Bound admission

An Admission limits application work and buffered bodies per listener. It does not bound transport connections or all runtime goroutines.

| `Admission` field | Constraint/default |
| --- | --- |
| `maxInFlight: InFlightLimit` | Positive Int; required. |
| `maxQueued: QueueLimit = 0` | Nonnegative Int. |
| `queueTimeoutMs: TimeoutMs = 0` | Zero disables queue waiting. |
| `retryAfterMs: TimeoutMs = 0` | Rejection hint, rounded upward to seconds. |
| `status: AdmissionStatus = 503` | 429 or 503. |

```bork
import "bork/http"

fn main() {
  scope app {
    limit = http.Admission { maxInFlight: 64, maxQueued: 32, queueTimeoutMs: 25, retryAfterMs: 100 }
    match (http.Listen("127.0.0.1:0", app, (req, rs) => http.Text(200, "ok"), admission: .Some(limit))) {
      server: http.Server => println(http.AdmissionState(server))
      error: IoError => eprintln(error.message)
    }
  }
}
```

Admission precedes body buffering and middleware and holds capacity through request tasks, cleanup and response writing. Waiters use a bounded FIFO queue; a full queue rejects immediately. Zero maxQueued or queueTimeoutMs disables waiting. Cancellation removes queued requests; body errors and handler panics release capacity. Shutdown cancels queued waits.

Go detects HTTP/1 disconnects only after consuming the body, so unread-body waiters can remain until timeout, cancellation or permit transfer. Admission does not peek at or drain bodies. `AdmissionState(server) uses state: Option[AdmissionLoad]` gives a consistent `{ inFlight: Int, queued: Int }` snapshot, or None when disabled. See the [admission fixture](../../testdata/cases/http_admission/main.bork) for concurrent requests.

## Shared retries

Retrying asserts that repeating the operation is safe. Only Overloaded is retried; responses, BodyTooLarge, IoError, Cancelled and DeadlineExceeded return directly. Share one scope-owned RetryBudget per destination across callers.

```bork
import "bork/http"

fn main() {
  scope app {
    budget = http.OpenRetryBudget(app, capacity: 4, refillMs: 1000)
    attempts = atom(0)
    result = http.Retry(app, budget, operation: attempt => {
      count = update(attempts, n => n + 1)
      if (count == 1) {
        http.Overloaded { response: http.Text(503, "busy"), retryAfter: .None }
      } else { http.Text(200, "ok") }
      }, baseDelayMs: 0, maxDelayMs: 0)
    match (result) {
      response: http.Response => println(response.status, response.body)
      error => eprintln(toString(error))
    }
  }
}
```

The retry reaches its second attempt:

```text
200 ok
```

| Signature | Meaning |
| --- | --- |
| `OpenRetryBudget(s: Scope, capacity: RetryCount, refillMs: RefillMs, clock: Option[time.Clock] in s = .None) uses clock + state: RetryBudget` | Positive capacity and refill interval (milliseconds within TimeoutMs); starts full, adds one token per interval up to capacity. |
| `Retry(s: Scope, budget: RetryBudget in s, operation: (Scope) => Result, maxAttempts: RetryCount = 3, baseDelayMs: TimeoutMs = 20, maxDelayMs: TimeoutMs where AtLeastBase(baseDelayMs) = 1000, jitter: Option[(Int) uses random + state => Int] = .None) uses clock + random + state: Result` | Retry with shared tokens and fresh attempt scopes; adds operation effects. |

The first attempt spends no token; maxAttempts includes it. Further attempts reserve shared tokens before work, and concurrent callers cannot overspend. Tokens are not refunded. Every attempt finishes its task/resource cleanup before the next attempt. Closing the budget cancels active attempts and waits; operations must cooperate. Attachment extends ownership; the budget and any captured injected clock must outlive their explicit scope.

Waits add the nonnegative server hint to full jitter from zero through capped exponential local backoff. maxDelayMs caps only local backoff and cannot be below baseDelayMs; a valid server hint is never shortened. Overflow, insufficient remaining deadline, exhausted attempts or an empty budget return the last Overloaded. Expired deadlines return DeadlineExceeded; cancellation returns Cancelled. Negative manually constructed hints count as zero.

Retry-After accepts exactly one nonnegative whole-seconds or HTTP-date value. Missing, malformed, repeated or overflowing values become None; past dates give zero. Raw headers remain available on `Overloaded.response`. Date hints use the client clock and are approximate under clock skew.

For deterministic refill, inject `time.FixedClock` or another Clock; the default uses monotonic system time and needs no refill goroutine. Backward readings pause refill until the clock catches up. Custom jitter receives a nanosecond ceiling and its result is clamped to `0..ceiling`; timed waits and scope deadlines still use real time. See [slow_downstream](../../examples/slow_downstream/main.bork) for shared retry/admission bounds.

## TLS certificates

ListenTLS loads PEM certificate/key files before opening the listener and requires TLS 1.2 or newer. `LoadCertificate(certFile: String, keyFile: String) uses io: Certificate | IoError` reads files; `ParseCertificate(certificatePem: Bytes, keyPem: Bytes): Certificate | IoError` validates supplied bytes. A Certificate stores immutable private bytes; Show prints only `http.Certificate`.

```bork
import "bork/http"

fn main() {
  empty: List[Byte] = []
  match (http.ParseCertificate(empty.toBytes(), empty.toBytes())) {
    _: http.Certificate => println("valid certificate")
    _: IoError => println("invalid certificate")
  }
}
```

## Trace context and marked values

Send forwards only ambient declarations marked `propagated("header")`. Bound marked values replace manual headers case insensitively; unmarked values do not automatically cross the boundary. The server clears inherited propagated labels per request, then restores them. Missing, repeated singleton or invalid values stay unbound; warnings identify the header/error without raw input. Logging adds no effect to Listen.

```bork
import "bork/http"
use http.TraceCodecs
propagated("traceparent") logged ambient trace: http.TraceParent
propagated("tracestate") ambient vendor: http.TraceState
```

| Signature/type | Meaning |
| --- | --- |
| `TraceParent = String where ValidTraceParent` | Checked W3C parent header. |
| `TraceState = String where ValidTraceState` | Checked W3C vendor state. |
| `TraceParentOf(headers: Headers): Option[TraceParent] \| codec.DecodeError` | Missing is None; repeated or invalid is an error. |
| `TraceStateOf(headers: Headers): Option[TraceState] \| codec.DecodeError` | Combine repeated fields in order; None without a valid parent. |

`use http.TraceCodecs` selects ordinary codec.Decode instances. Incoming labels supply logs and downstream forwarding; they do not replace a handler's captured typed `needs`. Application code extracts with TraceParentOf and explicitly binds a checked value with `with (trace: value)`.

Forwarding preserves valid values and does not create spans, IDs, sampling decisions or vendor entries. Invalid incoming state leaves a valid parent usable. Outgoing validation drops both fields for an invalid/repeated parent, drops only invalid state for a valid parent, and drops state without a parent. Redirects retain Go's header forwarding rules and revalidate selected fields; marked fields are not reintroduced. See [service_context](../../examples/service_context/main.bork) for checked binding and [the propagation design](../design/http-propagation.md) for full validation limits.

## Larger examples

- [signup_api](../../examples/signup_api/main.bork): request validation and JSON replies.
- [http_routes](../../examples/http_routes/main.bork): routing and middleware.
- [http_multi](../../examples/http_multi/main.bork): independent API, metrics and debug listeners.
- [slow_downstream](../../examples/slow_downstream/main.bork): admission and shared retry budgets.
- [service_context](../../examples/service_context/main.bork): trace forwarding and deadline budgets.
