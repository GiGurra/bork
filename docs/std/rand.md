# bork/rand

## Random sources

`bork/rand` uses Go's `math/rand/v2` default source for functions declaring
`uses random`. `Seed(first, second = 0)` creates an opaque immutable PCG
generator. `IntFrom`, `FloatFrom`, `ShuffleFrom`, and `PickFrom` return a
`Draw[T]` with `value` and `next`; the corresponding generator methods do the
same. Reusing the input replays a draw; using `next` advances it. Seeded draws
are pure and require no effects. Do not rely on identical bounded draws across
32-bit and 64-bit platforms or future runtime versions.

Integer ranges include `lo` and exclude `hi`, require `hi > lo`, and support
ranges spanning the signed Int boundary. Floats lie in `[0, 1)`. Shuffle copies
its input. Pick returns None for an empty list, preserving the generator state.
This package is for simulations and sampling; use `bork/crypto` for secrets.

## Examples

Import `bork/rand` for bounded integers, floats, shuffling and picking. A seeded
opaque `Generator` returns `{ value, next }` draws, so replay needs no mutable
state. See [examples/rand](../../examples/rand/main.bork).
