# Channels

A channel carries values from one task to another. Like a file or a task, a channel belongs to a [scope](scopes.md) and cannot outlive it. Operations that wait take the scope they wait in: a send or receive reports `Closed` or `Cancelled` as a value, and cancelling the scope always ends the wait.

## Making a channel

```bork
fn main() {
  scope s {
    handOff = channel[String](s)
    buffered = channel[Int](s, 16)
    queue = unboundedChannel[Int](s)
    println(handOff.capacity(), buffered.capacity(), queue.capacity())
  }
}
```

```text
Option.Some { value: 0 } Option.Some { value: 16 } Option.None
```

| Made with | Buffer | A send waits |
| --- | --- | --- |
| `channel[T](s)` | none | until a receiver takes the value |
| `channel[T](s, n)` | `n` values | while the buffer is full |
| `unboundedChannel[T](s)` | grows as needed | never |

The capacity must be proven non-negative (`validCapacity`): a literal is, and a computed number needs a check first. A fixed buffer is allocated as it fills, so a large capacity costs nothing until it is used. Prefer a fixed capacity: when the receiver falls behind, the sender waits instead of memory growing. An unbounded channel suits a sender that must never be held up, when the receiver keeps up on average.

When the scope ends, it closes the channel and drops anything still buffered.

## Sending and receiving

```bork
fn main() {
  scope s {
    numbers = channel[Int](s)
    launch(s, () => {
      _ = numbers.send(s, 1)
      _ = numbers.send(s, 2)
      numbers.close()
    })
    println(numbers.receive(s))
    println(numbers.receive(s))
    println(numbers.receive(s))
  }
}
```

```text
1
2
Closed {}
```

`ch.send(s, x)` gives `Ok | Closed | Cancelled`, and `ch.receive(s)` gives `T | Closed | Cancelled`. Both wait as long as they must, in the scope `s` they are given:

- `Closed` means the channel was closed: a send to it fails, and a receive has had every buffered value.
- `Cancelled` means `s` (or the channel's own scope) was cancelled, by a deadline, by a failed task, or by `cancel`. Cancellation wins: a cancelled scope completes no operation, even one that is ready.

In a function that returns these outcomes, `?` passes them on:

```bork
fn sendAll(s: Scope, out: Channel[Int], values: List[Int]) uses state: Ok | Closed | Cancelled {
  for (x in values) {
    out.send(s, x)?
  }
  Ok
}

fn main() {
  scope s {
    out = channel[Int](s, 3)
    println(sendAll(s, out, [1, 2, 3]))
    out.close()
    println(sendAll(s, out, [4]))
  }
}
```

```text
Ok
Closed {}
```

Values arrive in the order they were sent. Senders and receivers that wait are served first come, first served.

## Closing and for loops

`ch.close()` says no more values are coming. It is final, and closing twice does nothing. Afterwards sends give `Closed`, and receives give what is still buffered, then `Closed`.

`ch.values(s)` is a sequence that receives until the channel is closed, so a `for` loop reads a channel to its end:

```bork
fn main() {
  scope s {
    words = channel[String](s, 3)
    _ = words.send(s, "one")
    _ = words.send(s, "two")
    words.close()
    for (word in words.values(s)) {
      println(word)
    }
    println(words.toList(s))
  }
}
```

```text
one
two
[]
```

The loop also ends, quietly, if `s` is cancelled. Unlike most sequences, `values(s)` does not start again when it is read a second time: it goes on receiving where the last read stopped. `ch.toList(s)` receives everything into a list, or gives `Cancelled`.

The sender closes a channel, to tell its receivers it is done. A receiver can close it too, to tell the sender to stop: the sender's next send gives `Closed`.

## Operations that do not wait

`trySend(x)` gives `Ok`, `Full` or `Closed`, and `tryReceive()` gives `Option.Some` of a value, `Option.None` when no value is ready, or `Closed`. Neither waits, and neither checks for cancellation. `length()` is how many values are buffered, and `capacity()` the size of the buffer (`None` for an unbounded channel).

```bork
fn main() {
  scope s {
    slots = channel[Int](s, 1)
    println(slots.trySend(1), slots.trySend(2))
    println(slots.length())
    println(slots.tryReceive(), slots.tryReceive())
  }
}
```

```text
Ok Full {}
1
Option.Some { value: 1 } Option.None
```

## Producers and merge

`produce(s, capacity, work)` makes a channel, runs `work` with it as a task of `s`, and closes the channel when `work` returns. The receivers' loops then end, and no close can be forgotten. If `work` panics, the failed task cancels `s`, as any failed task does, and the panic resurfaces when `s` ends.

```bork
fn squares(s: Scope, out: Channel[Int], count: Int) uses state: Ok | Closed | Cancelled {
  for (n in Seq.range(1, count + 1)) {
    out.send(s, n * n)?
  }
  Ok
}

fn main() {
  scope s {
    for (n in produce[Int](s, 0, out => squares(s, out, 4)).values(s)) {
      println(n)
    }
  }
}
```

```text
1
4
9
16
```

`merge(s, channels)` passes the values of several channels on to one, which closes once all of them have. Values from one channel keep their order; values from different channels interleave as they arrive.

## Select

`select` waits until one of several channel operations can happen, completes only that one, and gives the value of its arm:

```bork
fn main() {
  scope s {
    numbers = channel[Int](s, 1)
    words = channel[String](s, 1)
    _ = words.send(s, "hello")
    println(select {
      n = numbers.receive(s) => s"number $n"
      w = words.receive(s) => s"word $w"
    })
  }
}
```

```text
word hello
```

An arm is `ch.receive(s) => body` or `ch.send(s, x) => body`, optionally naming the operation's result: `name = ch.receive(s)` binds `T | Closed`, and `name = ch.send(s, x)` binds `Ok | Closed`. When several operations are ready, one is chosen at random, so no channel is starved.

The body is ordinary code of the function around it: it can `return`, use `?`, or `break` and `continue` a loop around the `select`. The `select` gives the value of the arm that ran, or `Cancelled` if the scope it waits in is cancelled first, so its type is the arms' types and `Cancelled`. When arms give different types, say which type you want, for example as the function's result.

`select` is a keyword only where an expression starts and `{` follows, so `select` is still usable as a name.

### Timeouts and tickers

A timeout is a channel too. `time.After(s, d)` gives a channel that receives the time once, after `d`, and then closes. `time.Tick(s, every)` receives the time at that period until its scope ends, dropping ticks a slow receiver misses. Both belong to `s`, so their timers stop when it ends:

```bork
import "bork/time"

type TimedOut = {}

fn reply(s: Scope, replies: Channel[String]) uses clock + state: String | Closed | TimedOut | Cancelled {
  deadline = time.After(s, time.Nanoseconds(50_000_000))
  select {
    r = replies.receive(s) => r
    _ = deadline.receive(s) => TimedOut {}
  }
}

fn main() {
  scope s {
    println(reply(s, channel[String](s)))
  }
}
```

```text
TimedOut {}
```

To bound everything that happens in a block, rather than one wait, give the block a deadline instead: in `withTimeout(s, ms, c => ...)`, every operation waiting in `c` gives `Cancelled` when the time is up, `select` included.

### Not waiting

A `_ => body` arm runs when no operation is ready, so the `select` does not wait:

```bork
fn main() {
  scope s {
    mailbox = channel[String](s, 1)
    println(select {
      m = mailbox.receive(s) => m
      _ => "empty"
    })
  }
}
```

```text
empty
```

## Patterns

- **Pipeline**: stages joined by channels, each a task made with `produce` that receives from the stage before it. See [examples/pipeline](../../examples/pipeline/main.bork).
- **Fan out, fan in**: several workers receive from one channel of jobs, so each job goes to one of them, and `merge` collects their results. See [examples/fan_in_out](../../examples/fan_in_out/main.bork).
- **Timeouts and heartbeats**: `select` over a reply, `time.After`, and `time.Tick`. See [examples/select_timeout](../../examples/select_timeout/main.bork).
- **A queue that never blocks its producer**: [examples/unbounded_queue](../../examples/unbounded_queue/main.bork).
- **The basics in one program**: [examples/channels](../../examples/channels/main.bork).

## Coming from Go

| Go | bork |
| --- | --- |
| `make(chan T)`, `make(chan T, n)` | `channel[T](s)`, `channel[T](s, n)`: owned by scope `s` |
| no unbounded channel | `unboundedChannel[T](s)` |
| `ch <- x`, `<-ch` | `ch.send(s, x)`, `ch.receive(s)`, which give `Closed` or `Cancelled` as values |
| `v, ok := <-ch` | `match (ch.receive(s)) { ... }` |
| `for v := range ch` | `for (v in ch.values(s))` |
| send on a closed channel panics | gives `Closed` |
| closing twice panics | does nothing |
| `select` with `case <-ctx.Done()` | `select` gives `Cancelled` by itself |
| `default:` | `_ => ...` |
| `time.After(d)` | `time.After(s, d)`, stopped when `s` ends |
| a forgotten receiver leaks its goroutine | the scope cancels waiting tasks when it ends |

---

Previous: [Scopes and tasks](scopes.md) · Next: [Compile-time evaluation](comptime.md) · [All pages](../README.md#the-language)
