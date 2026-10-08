# Streaming HTTP server

Run `bork run examples/http_stream_server`.

A local server writes two Server-Sent Events with `http.Write` and `http.Flush`.
The client reads the first event before releasing the handler to write the second.
The example prints raw event bytes as hex; HTTP chunk boundaries are not event
boundaries, so a real SSE client buffers and parses the SSE framing.

The request scope owns its incoming `BodyReader` and outgoing `BodyWriter`.
Returning from the handler joins request tasks and closes both resources.
See [the HTTP API](../../docs/std/http.md#streaming-server-bodies).
