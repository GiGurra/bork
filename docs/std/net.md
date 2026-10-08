# bork/net

`bork/net` provides scope-owned TCP connections and UDP sockets with binary data and per-call deadlines.

```bork
import "bork/encoding"
import "bork/net"

fn demo(s: Scope) uses io + net + state + clock: Ok | IoError | Cancelled {
  receiver = net.Bind("127.0.0.1:0", s)?
  sender = net.Bind("127.0.0.1:0", s)?
  _ = net.Send(sender, net.SocketAddress(receiver), encoding.Utf8("hello"), 1.seconds())?
  packet = net.Receive(receiver, 1.seconds())?
  println(encoding.ParseUtf8(packet.data))
  match net.SplitAddress("missing-port") {
    _: ParseError => println("invalid address")
    endpoint: net.Endpoint => println(endpoint)
  }
}

fn main() {
  println(scope app { demo(app) })
}
```

```text
hello
invalid address
Ok
```

Port zero chooses an available local port. This example uses only loopback and
needs no external service.

## API

| Signature | Meaning |
| --- | --- |
| `Dial(address: String, s: Scope, timeout: Timeout = Duration { nanos: 0 }) uses net + state + clock: Connection \| IoError \| Cancelled` | Open TCP with scope cancellation and an opening timeout. |
| `Listen(address: String, s: Scope, handler: (Connection, Scope) => Ok \| IoError \| Cancelled) uses net + state: Server \| IoError \| Cancelled` | Serve TCP connections in separate handler scopes. |
| `Address(server: Server): String` | Read the server address, including its selected port. |
| `Wait(server: Server) uses net + state: Ok` | Wait for server shutdown. |
| `LocalAddress(conn: Connection): String` | Read a connection’s local address. |
| `RemoteAddress(conn: Connection): String` | Read a connection’s peer address. |
| `Read(conn: Connection, size: ReadSize = 4096, timeout: Timeout = Duration { nanos: 0 }) uses net + state + clock: Bytes \| Eof \| IoError \| Cancelled` | Read up to size bytes; Eof means clean stream closure. |
| `ReadLine(conn: Connection, timeout: Timeout = Duration { nanos: 0 }) uses net + state + clock: String \| Eof \| IoError \| Cancelled \| ParseError` | Read and validate a UTF-8 line. |
| `Write(conn: Connection, data: Bytes, timeout: Timeout = Duration { nanos: 0 }) uses net + state + clock: Int \| IoError \| Cancelled` | Write bytes; return the byte count. |
| `WriteLine(conn: Connection, line: String, timeout: Timeout = Duration { nanos: 0 }) uses net + state + clock: Int \| IoError \| Cancelled` | Write text followed by LF. |
| `Bind(address: String, s: Scope) uses net + state: Socket \| IoError \| Cancelled` | Open a UDP socket on a numeric IP:port. |
| `SocketAddress(socket: Socket): String` | Read the bound UDP address. |
| `Receive(socket: Socket, timeout: Timeout = Duration { nanos: 0 }) uses net + state + clock: Packet \| IoError \| Cancelled` | Receive a complete datagram and sender address. |
| `Send(socket: Socket, address: String, data: Bytes, timeout: Timeout = Duration { nanos: 0 }) uses net + state + clock: Int \| IoError \| Cancelled` | Send a datagram to a numeric IP:port. |
| `SplitAddress(address: String): Endpoint \| ParseError` | Parse host and port; reject a missing port. |
| `JoinAddress(host: String, port: String): String` | Join host and port, including IPv6 brackets. |
| `Resolve(host: String, s: Scope) uses net + state: List[String] \| IoError \| Cancelled` | Resolve a host to sorted IP strings. |

| Type or fact | Fields or constraint |
| --- | --- |
| `Connection`, `Server`, `Socket` | Scope-owned resources. |
| `Packet` | `data: Bytes`, `address: String` |
| `Endpoint` | `host: String`, `port: String` |
| `Eof` | Empty record marking clean TCP stream closure. |
| `Timeout = Duration where ValidTimeout` | Nonnegative signed nanosecond durations. |
| `ReadSize = Int where ValidReadSize` | 1 through 16777216 bytes. |
| `ValidTimeout(duration: Duration): Bool` | Prove a timeout fits its range. |
| `ValidReadSize(n: Int): Bool` | Prove a read buffer size fits its range. |

A zero timeout clears the call's deadline and relies on owner cancellation.
Transport and timeout failures are `IoError`; cancellation is `Cancelled`, and
invalid UTF-8 is `ParseError`. Socket operations charge `net + state`; timeout
operations also charge `clock`. A Listen handler also contributes its effects.

## Serve and read TCP

```bork
import "bork/net"

fn echo(conn: net.Connection, s: Scope) uses net + state + clock: Ok | IoError | Cancelled {
  match net.ReadLine(conn, 1.seconds()) {
    line: String => { _ = net.WriteLine(conn, line, 1.seconds())?; checkpoint(s) }
    _: net.Eof => checkpoint(s)
    error: IoError => error
    error: ParseError => IoError { path: net.RemoteAddress(conn), message: error.message }
    stopped: Cancelled => stopped
  }
}

fn demo(s: Scope) uses io + net + state + clock: Ok | IoError | Cancelled {
  server = net.Listen("127.0.0.1:0", s, echo)?
  client = net.Dial(net.Address(server), s, 1.seconds())?
  _ = net.WriteLine(client, "hello", 1.seconds())?
  println(net.ReadLine(client, 1.seconds()))
}

fn main() {
  println(scope app { demo(app) })
}
```

```text
hello
Ok
```

Each connection runs as a task in its own scope. Server cleanup cancels active
connections and waits for handlers. Handler errors and panics are logged and
isolated from other connections. `Wait` waits for shutdown; it does not request
shutdown itself.

`Read` returns up to its requested size, which can be fewer bytes than a complete
application message. `ReadLine` removes LF or CRLF, preserves a final bare CR,
and returns a final unterminated line before `Eof`. A line timeout retains the
consumed prefix for the next `ReadLine` or `Read`.

One reader and one writer can operate concurrently on a resource. Failed writes
can be partial; retrying the complete payload can duplicate a prefix. Resources
follow their owners' cancellation; attachment extends ownership, so cancellation
occurs once every owner is cancelled. See [scopes](../language/scopes.md).

## Address helpers and UDP

```bork
import "bork/net"

fn main() {
  address = net.JoinAddress("::1", "8080")
  println(address)
  println(net.SplitAddress(address))
}
```

```text
[::1]:8080
Endpoint { host: "::1", port: "8080" }
```

`Bind` and `Send` require numeric IP:port addresses; resolve names with
`Resolve(host, s)` when necessary. `Receive` returns a whole datagram, including
zero-length data. Datagram boundaries are retained, unlike TCP stream reads.

See the [network example](../../examples/net/main.bork).

Run `bork doc bork/net` for the generated reference.

[All standard packages](README.md)
