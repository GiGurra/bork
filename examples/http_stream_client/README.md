# Streaming HTTP client

Run `bork run examples/http_stream_client` from the repository root.

The example starts a local server, downloads its response in binary chunks into a temporary file, then uploads two chunks with a producer body. Each stream belongs to the application scope and closes on EOF or scope exit. The server uses the existing eager convenience API; streamed server responses have a separate example.
