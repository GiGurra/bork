# bork/http

## HTTP resources

`bork/http`, a server whose lifetime is a scope (`http.Listen(addr, s, handler)` serves until `s` closes, each request on its own goroutine, with a scope of its own that the handler gets), a scope-cancellable client (`http.Get(url, s, timeoutMs: 0)`, `http.Post(url, contentType, body, s, timeoutMs: 0)`, `http.Send(method, url, headers, body, s, timeoutMs: 0)`), whose optional millisecond timeout covers both the request and reading the response body; headers are maps of names to value lists, preserving repeated values, and transport/cancellation/timeout failures return `IoError`, and helpers (`http.Text`, `http.JsonReply`, `http.Segments` for matching paths with list patterns). Status codes are facts: `http.Text(42, "x")` does not compile. See [examples/signup_api](../../examples/signup_api/main.bork).

## Routes and shutdown

HTTP servers support `http.ListenRoutes(addr, s, routes, drainTimeoutMs: 0)` with immutable `http.Route { pattern: "GET /users/{id}", handler: ... }` records. Patterns follow Go 1.22 ServeMux: method matching, HEAD for GET, redirects, `{name}` and `{name...}` path captures (in `request.params`), and 404/405 responses. Invalid/conflicting patterns return `IoError` before listening. `http.Handler` permits all five effects, so routed server functions declare `uses io + net + clock + random + state`; ordinary functions `(http.Handler) => http.Handler` implement middleware. The original `Listen` retains open handler effects.

`http.Body[T: Decode](request)` decodes JSON with field facts. `Query` parses query values; `QueryAs[T]` and `PathAs[T]` load derived records, using literal strings and JSON syntax for other fields. Missing Option fields become None; repeated values for record fields are errors. `Form` parses URL-encoded body values separately from the query. `Multipart` returns value lists and immutable upload Bytes; multipart and incoming server bodies default to 16 MiB. Listen, ListenRoutes, ListenTLS and Multipart accept a nonnegative maxBodyBytes override; zero rejects nonempty bodies. A server rejects oversized bodies with HTTP 413. `Static(request, root, prefix: "")` serves a directory through Go's file server, buffering the response; it supports directory listings, symlinks, ranges and conditional requests. `ListenTLS(addr, s, routes, certFile, keyFile, drainTimeoutMs: 0)` loads PEM certificate/key files and requires TLS 1.2 or newer.

Closing the server scope cancels request scopes and stops accepting new connections, then waits for active handlers through Go's graceful shutdown. A zero drain timeout inherits `cleanupTimeout`; without that policy shutdown waits indefinitely. A positive per-server timeout overrides the default and cannot exceed a positive scope cleanup timeout (`IoError` otherwise). When draining times out, connections are forcibly closed; cooperative handlers may still be finishing. `Wait` waits for the listener to stop, while scope cleanup drains active requests. See [http_routes](../../examples/http_routes/main.bork).

## HTTP API

HTTP clients take an explicit `Scope` and optional nonnegative millisecond timeout: `http.Get(url, s, timeoutMs: 0)`, `http.Post(url, contentType, body, s, timeoutMs: 0)`, or `http.Send(method, url, headers, body, s, timeoutMs: 0)`. Zero uses only scope cancellation; the maximum is 9223372036854 milliseconds. Use `http.ValidTimeout(value)` as a guard for a dynamic timeout. `http.Headers` is `Map[String, List[String]]`; use `{:}` for no headers. `http.HeaderOf` finds the first value without regard to case. Cancellation, timeout, and transport failures are `IoError`; HTTP error status codes remain responses.

HTTP servers support `http.ListenRoutes(addr, s, routes, drainTimeoutMs: 0)` with immutable `http.Route { pattern: "GET /users/{id}", handler: ... }` records. Patterns follow Go 1.22 ServeMux: method matching, HEAD for GET, redirects, `{name}` and `{name...}` path captures (in `request.params`), and 404/405 responses. Invalid/conflicting patterns return `IoError` before listening. `http.Handler` permits all five effects, so routed server functions declare `uses io + net + clock + random + state`; ordinary functions `(http.Handler) => http.Handler` implement middleware. The original `Listen` retains open handler effects.

`http.Body[T: Decode](request)` decodes JSON with field facts. `Query` parses query values; `QueryAs[T]` and `PathAs[T]` load derived records, using literal strings and JSON syntax for other fields. Missing Option fields become None; repeated values for record fields are errors. `Form` parses URL-encoded body values separately from the query. `Multipart` returns value lists and immutable upload Bytes; multipart and incoming server bodies default to 16 MiB. Listen, ListenRoutes, ListenTLS and Multipart accept a nonnegative maxBodyBytes override; zero rejects nonempty bodies. A server rejects oversized bodies with HTTP 413. `Static(request, root, prefix: "")` serves a directory through Go's file server, buffering the response; it supports directory listings, symlinks, ranges and conditional requests. `ListenTLS(addr, s, routes, certFile, keyFile, drainTimeoutMs: 0)` loads PEM certificate/key files and requires TLS 1.2 or newer.

Closing the server scope cancels request scopes and stops accepting new connections, then waits for active handlers through Go's graceful shutdown. A zero drain timeout inherits `cleanupTimeout`; without that policy shutdown waits indefinitely. A positive per-server timeout overrides the default and cannot exceed a positive scope cleanup timeout (`IoError` otherwise). When draining times out, connections are forcibly closed; cooperative handlers may still be finishing. `Wait` waits for the listener to stop, while scope cleanup drains active requests. See [http_routes](../../examples/http_routes/main.bork).

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
