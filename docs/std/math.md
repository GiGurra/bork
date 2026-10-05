# Exact arithmetic and money

Import `bork/math`. Every operation is pure. Float functions follow Go's
[math package](https://pkg.go.dev/math): invalid domains return NaN or infinity.
Use `IsNaN` and `IsInf` to distinguish these results.

Float functions are `Abs`, `Min`, `Max`, `Pow`, `Sqrt`, `Floor`, `Ceil`, `Round`
(ties away from zero), `RoundToEven`, `Sin`, `Cos`, `Tan`, `Asin`, `Acos`, `Atan`,
`Atan2(y, x)`, `Hypot`, `Log`, `Log2`, `Log10`, `Exp`, and `Exp2`. Constants are
functions: `Pi()`, `E()`, `NaN()`, and `Inf(sign = 1)`; negative sign selects
negative infinity.

## Decimal equality includes scale

**`1.0 != 1.00` with `==`.** Decimal preserves the scale supplied at construction,
so equality and map keys include it. Use `a.SameValue(b)` for numeric equality,
or `a.Compare(b)` for numeric ordering (negative, zero, positive). There is no
Decimal `Ord` instance: its numeric comparison would disagree with structural
`==`. Normalize both values with an explicit `Round(scale, rounding)` before
using amounts as keys when scale should be ignored.

```bork
import "bork/math"

fn tax(subtotal: math.Decimal, rate: math.Decimal): math.Decimal | OutOfRange {
  subtotal.Mul(rate)?.Round(2, math.Rounding.HalfEven)
}
```

`Decimal` represents an arbitrary-size integer coefficient times `10^-scale`.
`ParseDecimal("123.4500")` preserves four fractional digits. `DecimalFrom(integer,
scale)` constructs from `BigInt`; `Coefficient()` and `Scale()` inspect it.
Construction uses a private variant, so callers cannot forge its representation.
Scale must be between 0 and 10000 inclusive; this bounds decimal expansion and
powers of ten, while integer coefficients have no fixed width.

`ParseDecimal` accepts an optional `+` or `-`, one or more ASCII decimal digits,
and optionally a decimal point followed by one or more digits. It accepts leading
zeroes, normalizes the coefficient, and preserves fractional zeroes. Negative
zero prints without a sign. Whitespace, separators, exponents, `.5`, `1.`, NaN,
and infinity return `ParseError`. Input with more than 10000 fractional digits
also returns `ParseError`. No binary-float conversion is supplied for money.

| Method | Result and precision |
|--------|----------------------|
| `Add(other)`, `Sub(other)` | Exact, with the larger input scale |
| `Mul(other)` | Exact, with the sum of input scales; `OutOfRange` if it exceeds 10000 |
| `Div(other, scale, rounding)` | Requested scale and rounding; `MathError` for zero divisor, `OutOfRange` for invalid scale |
| `Round(scale, rounding)` | Requested scale; increasing scale pads zeroes, decreasing scale explicitly rounds; `OutOfRange` for invalid scale |
| `Abs()`, `Neg()` | Exact, preserving scale |
| `ToString()` | Fixed-point text, including trailing fractional zeroes |
| `ToRational()` | Exact `BigRat`, without scale metadata |

`Round` and `Div` have no default rounding or scale. Choose when to round in your
business rule: for example, multiply tax exactly and round once to cents, rather
than rounding each intermediate value. Decimal records no currency; currency
identity and permitted scales belong in the application's types.

| `math.Rounding` | Rule | `2.5` to scale 0 | `-2.5` to scale 0 |
|-----------------|------|------------------|-------------------|
| `TowardZero` | Truncate fractional digits | 2 | -2 |
| `AwayFromZero` | Increase magnitude whenever digits are discarded | 3 | -3 |
| `Floor` | Toward negative infinity | 2 | -3 |
| `Ceiling` | Toward positive infinity | 3 | -2 |
| `HalfEven` | Nearest; ties to an even last retained digit | 2 | -2 |
| `HalfAwayFromZero` | Nearest; ties increase magnitude | 3 | -3 |

## Integers and rationals

`Integer(n)` constructs a `BigInt` from `Int`; `ParseInteger(text)` parses signed
base-10 digits, returning `ParseError` for malformed input. Leading zeroes and
`+` are accepted and normalized; separators and whitespace are rejected.
`ToInt()` checks the signed 64-bit range and returns `OutOfRange` on overflow.
Methods are `Add`, `Sub`, `Mul`, `Div`, `Mod`, `Abs`, `Neg`, `Compare`, `ToString`,
`Rem`, `Pow(exponent)`, and `Sqrt()`. Division truncates toward zero and `Rem`
is its remainder, with the dividend's sign. `Mod` is the nonnegative Euclidean
modulus, even for a negative divisor. Zero divisors, negative integer exponents,
and square roots of negative integers return `MathError`. Integer square root
is rounded down. Arithmetic never silently narrows to `Int`.

`Rational(numerator, denominator)` constructs a `BigRat` from `BigInt`s and returns
`MathError` for a zero denominator. `ParseRational(text)` accepts Go's
[big.Rat syntax](https://pkg.go.dev/math/big#Rat.SetString), including fractions,
decimal and exponent forms, and returns `ParseError` for invalid input. Results
are reduced, with positive denominators; integers print without `/1`. Methods
are `Add`, `Sub`, `Mul`, `Div`, `Abs`, `Neg`, `Compare`, `Numerator`, `Denominator`,
`ToString`, and `ToDecimal(scale, rounding)`. Division by zero returns `MathError`;
conversion to Decimal returns `OutOfRange` for invalid scale and always requires
explicit rounding.

BigInt and BigRat have private representations, canonical structural equality,
and numeric `Ord` instances (`use math.OrdBigInt`, `use math.OrdBigRat`). Their
operations allocate fresh Go big numbers rather than changing their inputs.
Canonical strings cross the bork boundary; arithmetic parses them into
`math/big` values and formats the result. This favors immutable value semantics
over arithmetic throughput: the [benchmark fixture](../../testdata/benchmarks/math/README.md)
measures the parsing, formatting and allocation overhead.

## JSON

`use math.Codecs` imports string codecs for all three exact types. Individual
instances are `EncodeBigInt`, `DecodeBigInt`, `EncodeBigRat`, `DecodeBigRat`,
`EncodeDecimal`, and `DecodeDecimal`. JSON strings preserve precision across
consumers that would otherwise parse a JSON number as a binary float. Decimal
encoding preserves scale; decoding invokes the same checked parsers. JSON number
values and malformed strings return `codec.DecodeError`.

See [examples/math](../../examples/math/main.bork) for invoice arithmetic.

## Package summary

- **Math (implemented):** `bork/math` has pure IEEE float functions and constants,
  immutable arbitrary-size `BigInt` and reduced `BigRat`, and fixed-point `Decimal`
  using `math/big.Int` coefficients. Private variants prevent forged values.
  Decimal preserves scale: **`1.0 != 1.00` with `==`**, including as map keys;
  `SameValue` and `Compare` compare numeric amounts. Division and rescaling require
  explicit scale and rounding (`TowardZero`, `AwayFromZero`, `Floor`, `Ceiling`,
  `HalfEven`, `HalfAwayFromZero`); scales are checked in 0..10000. Exact arithmetic
  errors return unions; floats retain IEEE NaN/infinity behavior. Exact JSON codecs
  use strings, preserving precision and Decimal scale. Canonical string storage
  keeps mutable Go numbers inside operations, with measurable parsing/allocation
  overhead. See [math](math.md), [examples/math](../../examples/math/main.bork), and the
  [benchmark](../../testdata/benchmarks/math/README.md).

## Math API

`bork/math` supplies pure float functions, immutable `BigInt` and `BigRat`, and
fixed-point `Decimal` with explicit output scale and rounding. Their private
variants prevent forged representations. Decimal equality includes scale:
`1.0 != 1.00`; use `SameValue` or `Compare` for numeric comparison. No new syntax.
See [exact arithmetic and money](math.md) and [examples/math](../../examples/math/main.bork).

## Examples

`bork/math` provides float functions, arbitrary integers and rationals, and exact
fixed-point Decimal arithmetic for money with explicit rounding. **Decimal `==`
includes scale (`1.0 != 1.00`); use `SameValue` for numeric equality.** Exact values
have private representations and JSON string codecs. See [math](math.md)
and the [invoice example](../../examples/math/main.bork).
