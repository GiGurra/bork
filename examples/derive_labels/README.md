# A custom derived class

Run from the repository root:

```sh
bork run examples/derive_labels
```

The `Labels` class derives field names using a typed list comprehension over
`bork/shape` descriptors. `Item` requests its instance inline; `Box` uses a
standalone request. `Box[A]` needs no bound on `A`, because the template only
reads metadata. Each compiled method returns a list of literal names.

See [derivation templates](../../docs/language/derivation.md) for staging, field
projections, helpers, and the ownership rules.
