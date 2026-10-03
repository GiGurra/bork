# bork/net

## TCP and UDP sockets

`bork/net` uses Bytes for binary TCP and UDP data and scope-owned Connection,
Server and Socket resources. Dial opens TCP with scope cancellation and an
optional opening timeout. Listen serves each connection on a task in its own
scope; server cleanup cancels active connections and waits for handlers.
Handler errors/panics are logged and isolated. Connections support bounded byte
reads, validated UTF-8 lines, writes, address inspection and per-call deadlines.
Line timeouts preserve consumed prefixes for retry or byte reads. Eof marks
clean stream closure; cancellation is Cancelled and transport/timeouts IoError.
Failed writes can be partial. One reader and one writer may run concurrently.

UDP Bind and Send use numeric IP:port addresses; Receive returns a complete
datagram with its sender, including zero-length data. Resources follow their
owner's cancellation, with attachment rebinding the cancellation source. Pure
host/port helpers parse and join addresses; scope-aware Resolve returns sorted
IP strings. Timeouts and buffer sizes have checked facts; there is no new syntax.
TLS can be added later alongside HTTP.

## Network API

`bork/net` provides TCP `Dial(address, scope, timeoutMs = 0)` and
`Listen(address, scope, handler)`. Listen returns a Server; `Address(server)`
reports its selected address and `Wait(server)` waits for shutdown. Each handler
receives a Connection and its own scope and returns `Unit | IoError | Cancelled`.
Handler errors and panics are logged; other connections continue. Server cleanup
cancels connections and waits for their handlers. `LocalAddress`/`RemoteAddress`
inspect a Connection. `Read(conn, size = 4096, timeoutMs = 0)` reads up to size
Bytes, returning `net.Eof` for a clean closed stream. Read sizes must be 1 through
16777216. `ReadLine` validates UTF-8, removes LF/CRLF, preserves final bare CR,
and returns a final unterminated line before Eof. A timed-out line prefix is
retained for a subsequent ReadLine or Read. `Write`/`WriteLine` return the number
of bytes sent. A failed write may have sent a prefix; do not blindly retry it.

UDP `Bind(address, scope)` requires a numeric IP:port (port 0 selects a free
port), and `SocketAddress(socket)` reports it. `Send(socket, numericAddress,
bytes, timeoutMs = 0)` sends a datagram; `Receive(socket, timeoutMs = 0)` returns
Packet with Bytes data and sender address, including empty datagrams. All socket
resources close on owner cancellation; attachment changes their cancellation
source. At most one reader and one writer may operate concurrently on a resource.
Timeouts are nonnegative milliseconds up to 9223372036854; zero clears the call's
deadline and relies on owner cancellation. Socket operations declare net/state
and calls with timeout support also declare clock. Cancellation is Cancelled;
transport and timeout errors are IoError, and invalid UTF-8 is ParseError.
`SplitAddress`/`JoinAddress` are pure host/port helpers; `Resolve(host, scope)`
resolves IP addresses with scope cancellation and sorts them. TLS is future work.
See [the network example](../../examples/net/main.bork).

## Examples

Import `bork/net` for scope-owned TCP/UDP sockets, binary data and UTF-8 lines,
per-call timeouts, and address helpers. See [examples/net](../../examples/net/main.bork).
