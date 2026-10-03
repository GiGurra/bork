# HTTP deadline and ambient propagation

Approved design for bork-gqxe4s. Deadline-budget propagation is implemented;
Ambient/trace forwarding follows the bork-avr3ns marker helpers. Typed ambient
values and the scope deadline bridge are already implemented.

## Deadline budget

HTTP uses a Bork-specific `Bork-Timeout-Ns` header: one unsigned decimal count of
remaining nanoseconds, in 0..9223372036854775807. Zero means exhausted. This is
a bork-to-bork protocol, not a gRPC timeout header. A grpc-timeout bridge can
be added separately later. Nanoseconds avoid rounding a small positive budget
upwards or losing sub-millisecond budgets. The limit fits Go's duration without
multiplication or saturation. Missing means no caller-supplied limit; malformed,
negative, oversized, comma-joined, or repeated values receive 400 before the
body is read or application work is admitted.

Listen, ListenRoutes and ListenTLS gain a final named option
`requestTimeoutMs: TimeoutMs = 0`. A positive value bounds each request from its
arrival at Bork's outer handler. Admission, body reads, handler work and
cooperative cleanup observe cancellation at that deadline. Zero adds no server policy deadline. The effective
request deadline is the earliest of this policy, the incoming budget, and the
existing listener/request context deadline. The listener's own lifetime remains
independent. A caller can only shorten the server's configured policy.

The deadline wrapper runs outside admission. It derives a cancellable Go context
before entering the admission queue, and the request Scope uses that context.
An already exhausted budget receives 504 without invoking the handler. These
early 400/504 responses use the existing HTTP/1 unread-body rejection strategy
(`Connection: close`) so a stalled upload cannot delay the rejection. Expiry
while queued also receives 504 rather than the admission policy's 429/503.
Queue timeout remains an overload. Body buffering must stop on the same
cancellation/deadline; merely replacing r.Context does not interrupt a blocked
server body read. Use the native response controller's read deadline and a
body-read-scoped cancellation hook. When buffering finishes, stop and join any
running hook, then clear an unexpired read deadline before invoking the handler
so it cannot affect handler work or a later keep-alive request. Recheck
cancellation before invoking the handler; request context cancellation remains
active independently. Never use the controller after ServeHTTP returns. Current Bork listeners serve HTTP/1; verify its body cancellation and
outgoing negotiated HTTP/2 budget propagation. Server HTTP/2 controller tests
follow when that transport is exposed; an unsupported deadline
controller receives 500 with the same HTTP/1 unread-body rejection strategy
before reading a deadline-bound body, rather than
introducing detached read goroutines. Expiry
while reading the body receives 504 if a response is still writable. A handler
that observes cancellation may return its own response; cancellation does not
forcibly stop arbitrary handler code. Request scopes keep their existing default of unbounded task/finalizer waits;
they do not inherit the listener's cleanup policies. Applications requiring
bounded waits put their work in explicit scopes with taskTimeout/cleanupTimeout.
Those policies bound individual waits and may orphan uncooperative work; they
do not promise a total request wall-clock bound.

Send takes its scope context and optional timeout as today. Immediately before
sending, it checks cancellation and the effective absolute deadline, refuses an
exhausted budget with typed DeadlineExceeded, and serializes the positive
remaining duration. Retry recomputes the header for every attempt, and a
per-call copy of http.DefaultClient refreshes it in CheckRedirect before each
redirect hop, preserving the configured redirect policy and shared transport.
Header changes happen before transport ownership. This avoids requiring a native
RoundTripper adapter type in the generated runtime. Exhausted redirects stop
with typed DeadlineExceeded. The hook refreshes only the budget; normal Go
redirect handling retains its existing sensitive-header rules. Native internal
transport replays happen inside RoundTrip and may retain its initial budget;
like wire transit, their elapsed time is still bounded by the caller's local
context but cannot be deducted exactly at the receiver. The header
is reserved: explicit or propagated fields with that name are removed and the
client writes exactly its effective scope/timeout budget; without a deadline it
sends no budget header; declarations must not use this reserved name. Applications express a tighter bound through scopes or
the existing timeoutMs argument.

Relative budgets avoid synchronized wall clocks. They start at each receiver,
so network transit is not deducted from the remote timer; the caller still
retains its original local deadline and cancels the HTTP call. This protocol
does not promise an exact shared absolute deadline across machines. Server
policy also bounds work if disconnect detection is delayed. Existing HTTP/1
unread-body disconnect limits still apply while queued.

## Ambient boundary

Outgoing Send writes only `_borkPropagated()` declarations, plus the reserved
deadline header. Marked values use their declared codec and header name; there
is no automatic principal, baggage, or unmarked-value propagation. An explicitly
bound propagated value replaces a manually supplied value of the same name,
case insensitively, using one header field. Other manual fields remain intact.

The outer server boundary calls `_borkBindPropagated` and defers restore around
admission, body handling and the application handler. This clears inherited
server labels before decoding incoming values. Missing/invalid values stay
unbound; invalid decodes warn without logging the raw header. Generic singleton
fields reject repeated values as invalid rather than choosing one. Since the
marker getter has only found/not-found, the adapter warns once per duplicate
header name, without values, then reports it as absent to the bridge. No marked
declarations means the generated helpers do no work.

These labels provide log attributes and downstream forwarding. Handler function
values retain the typed needs they captured when constructed. Incoming labels
never replace those arguments. Application code needing an incoming value calls
an ordinary checked decoder, then explicitly binds it with `with`. The compiler
does not gain a typed boundary callback or ambientHeader builtin for this task.

Invalid incoming values warn through bork/log without adding an effect: logging
is deliberately untracked. Listen/start retain their existing effects, and Send
keeps net + clock + state; forwarding labels adds no random or io effect. The
std boundary reads generated labels; pure application code cannot inspect those
labels to change its computed result.

## W3C trace context

Provide `http.TraceParent`, a String alias with checked validity facts, and a
pure `TraceParentOf(headers): Option[TraceParent] | DecodeError` helper. Missing
returns None; duplicates or invalid values return DecodeError. Decoding uses
ordinary Decode on the string boundary, so the resulting value carries its
facts and can be bound to an application declaration such as
`propagated("traceparent") logged ambient trace: http.TraceParent`.

Version 00 validates the 55-character lowercase-hex structure, separators,
nonzero trace/parent ids and flags. Version ff is invalid. Higher versions
validate the known prefix and extension boundary without interpreting unknown
fields. Forwarding preserves a valid incoming value; this phase does not create
spans, change sampling decisions, or generate trace ids. Applications may bind
their own valid root value. The example uses a fixed valid root for repeatable
output.

A companion `http.TraceState` fact alias and checked `TraceStateOf` helper support
W3C member/key/value limits and duplicate-key rejection. Repeated tracestate
fields combine in received order as the standard requires. Tracestate is only
bound/forwarded when traceparent is valid; invalid state does not invalidate the
parent. TraceStateOf applies the same parent check, returning None without a
valid parent. Empty state and empty/whitespace list members follow W3C acceptance
rules; malformed nonempty members, duplicate keys and size-limit violations
are invalid. The HTTP adapter supplies this combined representation to the marker
boundary and suppresses state without a valid parent. Outgoing selection first overlays marked fields on manual fields, then
validates the selected traceparent and combined tracestate. An invalid or
duplicate selected parent removes both headers; a valid parent plus invalid
state keeps only the parent. A marked parent replaces a manual parent. Marked
state can accompany a valid manual parent even without a parent declaration;
state alone is dropped. Missing incoming parent clears both inbound labels.
This order also applies when header names use different capitalization. Unmodified forwarding preserves member
order and contents; no vendor entries are added or rewritten.

The forwarding and validation rules follow the
[W3C Trace Context recommendation](https://www.w3.org/TR/trace-context/),
especially sections 3.2, 3.3 and 3.4. This is context forwarding, not a span
collector or exporter. Only declarations explicitly marked propagated cross
the boundary; std/http does not implicitly declare or bind application values.

## Scope and validation

Keep SQL unchanged in this phase: its existing explicit scope already supplies
local cancellation/deadlines, and SQL has no common HTTP-style ambient header
carrier. Do not invent a database trace protocol.

Regression coverage includes shrinking budget across two services and retries,
server policy caps, absent deadlines, zero/expired/malformed/duplicate/extreme
headers, stalled unread uploads during early rejection, queue versus deadline
expiry, body-read interruption, cancellation hooks racing response completion
and subsequent keep-alive requests, delayed/exhausted redirects, ancestor deadline
updates, caller cancellation, no socket activity for exhausted budgets, and
uncooperative cleanup demonstrating the documented limits.
Trace cases cover valid/invalid/future parents, zero ids, repeated state fields,
invalid/empty state without poisoning a valid parent, empty list members,
unknown flag bits, manual/marked precedence and state gating, missing/invalid inbound clearing
server labels, isolation between concurrent requests, lexical needs remaining
captured, and explicit checked rebind for application code. Race tests exercise
boundary restoration and server shutdown with admitted/queued requests.

A two-service example binds a valid root trace explicitly, forwards a request
through a middle service, displays a shrinking budget, and shows the same trace
id at the downstream service. It also demonstrates checked extraction plus
with for a helper with typed needs. Output asserts bounds and identity rather
than exact elapsed times.

Alternatives: absolute Unix deadlines need clock-skew policy; millisecond
budgets either round up and extend limits or discard a positive fractional
budget; grpc-timeout would imply support for a different protocol. Automatic
new span ids would add random effects and a span model; a dedicated collector
can introduce that later. AmbientHeader would add compiler surface when the
ordinary Decode boundary is already sufficient.
