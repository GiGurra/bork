# bork/math

`bork/math` provides float functions and immutable exact integers, rationals, and decimals.

```bork
import "bork/math"

fn demo() uses io: Ok | ParseError | OutOfRange {
  amount = math.ParseDecimal("12.345")?
  println(amount.Round(2, math.Rounding.HalfEven)?.ToString())
  match (math.Integer(1).Div(math.Integer(0))) {
    error: math.MathError => println(error.message)
    value: math.BigInt => println(value)
  }
}

fn main() {
  println(demo())
}
```

```text
12.34
division by zero
Ok
```

## API

| Signature | Meaning |
| --- | --- |
| `(value: Decimal) Coefficient(): BigInt` | Read the integer coefficient. |
| `(value: Decimal) Scale(): Int where DecimalScale` | Read the decimal scale. |
| `DecimalFrom(coefficient: BigInt, scale: Int): Decimal \| OutOfRange` | Construct a decimal with a checked scale. |
| `ParseDecimal(text: String): Decimal \| ParseError` | Parse fixed-point text, preserving scale. |
| `(value: Decimal) ToString(): String` | Format canonical text. |
| `(value: Decimal) Neg(): Decimal` | Negate without mutating the input. |
| `(value: Decimal) Abs(): Decimal` | Return the absolute value. |
| `(value: Decimal) Add(other: Decimal): Decimal` | Add exact values. |
| `(value: Decimal) Sub(other: Decimal): Decimal` | Subtract exact values. |
| `(value: Decimal) Compare(other: Decimal): Int` | Compare numerically: negative, zero, positive. |
| `(value: Decimal) Mul(other: Decimal): Decimal \| OutOfRange` | Multiply exact values. |
| `(value: Decimal) Round(scale: Int, rounding: Rounding): Decimal \| OutOfRange` | Choose a scale and rounding rule explicitly. |
| `(value: Decimal) Div(other: Decimal, scale: Int, rounding: Rounding): Decimal \| MathError \| OutOfRange` | Divide, rejecting a zero divisor. |
| `(value: Decimal) ToRational(): BigRat` | Convert a decimal exactly to a rational. |
| `(value: BigRat) ToDecimal(scale: Int, rounding: Rounding): Decimal \| OutOfRange` | Convert a rational with explicit scale and rounding. |
| `(value: Decimal) SameValue(other: Decimal): Bool` | Compare decimal numeric values without scale. |
| `Integer(value: Int): BigInt` | Construct a BigInt from an Int. |
| `ParseInteger(text: String): BigInt \| ParseError` | Parse signed base-10 digits. |
| `(value: BigInt) ToString(): String` | Format canonical text. |
| `(value: BigInt) ToInt(): Int \| OutOfRange` | Narrow to Int with overflow checking. |
| `(value: BigInt) Compare(other: BigInt): Int` | Compare numerically: negative, zero, positive. |
| `(value: BigInt) Add(other: BigInt): BigInt` | Add exact values. |
| `(value: BigInt) Sub(other: BigInt): BigInt` | Subtract exact values. |
| `(value: BigInt) Mul(other: BigInt): BigInt` | Multiply exact values. |
| `(value: BigInt) Div(other: BigInt): BigInt \| MathError` | Divide, rejecting a zero divisor. |
| `(value: BigInt) Rem(other: BigInt): BigInt \| MathError` | Return the truncating remainder. |
| `(value: BigInt) Mod(other: BigInt): BigInt \| MathError` | Return the nonnegative Euclidean modulus. |
| `(value: BigInt) Abs(): BigInt` | Return the absolute value. |
| `(value: BigInt) Neg(): BigInt` | Negate without mutating the input. |
| `(value: BigInt) Pow(exponent: Int): BigInt \| MathError` | Raise to a nonnegative integer exponent. |
| `(value: BigInt) Sqrt(): BigInt \| MathError` | Return the square root, rounded down for BigInt. |
| `Abs(value: Float): Float` | Return the absolute value. |
| `Sqrt(value: Float): Float` | Compute the floating-point square root. |
| `Floor(value: Float): Float` | Round toward negative infinity. |
| `Ceil(value: Float): Float` | Round toward positive infinity. |
| `Round(value: Float): Float` | Round to an integer float, ties away from zero. |
| `RoundToEven(value: Float): Float` | Round float ties to even. |
| `Sin(value: Float): Float` | Compute sine in radians. |
| `Cos(value: Float): Float` | Compute cosine in radians. |
| `Tan(value: Float): Float` | Compute tangent in radians. |
| `Asin(value: Float): Float` | Compute inverse sine. |
| `Acos(value: Float): Float` | Compute inverse cosine. |
| `Atan(value: Float): Float` | Compute inverse tangent. |
| `Log(value: Float): Float` | Compute the natural logarithm. |
| `Log2(value: Float): Float` | Compute the base-2 logarithm. |
| `Log10(value: Float): Float` | Compute the base-10 logarithm. |
| `Exp(value: Float): Float` | Compute e to the value. |
| `Exp2(value: Float): Float` | Compute 2 to the value. |
| `Min(a: Float, b: Float): Float` | Return the smaller float. |
| `Max(a: Float, b: Float): Float` | Return the larger float. |
| `Pow(a: Float, b: Float): Float` | Compute a raised to b with IEEE results. |
| `Atan2(a: Float, b: Float): Float` | Compute inverse tangent using y and x. |
| `Hypot(a: Float, b: Float): Float` | Compute the Euclidean norm. |
| `Pi(): Float` | Return pi. |
| `E(): Float` | Return Euler’s number. |
| `NaN(): Float` | Return IEEE NaN. |
| `Inf(sign: Int = 1): Float` | Return infinity with the requested sign. |
| `IsNaN(value: Float): Bool` | Detect NaN. |
| `IsInf(value: Float): Bool` | Detect either infinity. |
| `ParseRational(text: String): BigRat \| ParseError` | Parse rational text. |
| `Rational(numerator: BigInt, denominator: BigInt): BigRat \| MathError` | Construct a reduced rational; reject a zero denominator. |
| `(value: BigRat) ToString(): String` | Format canonical text. |
| `(value: BigRat) Numerator(): BigInt` | Read the reduced numerator. |
| `(value: BigRat) Denominator(): BigInt` | Read the positive denominator. |
| `(value: BigRat) Compare(other: BigRat): Int` | Compare numerically: negative, zero, positive. |
| `(value: BigRat) Add(other: BigRat): BigRat` | Add exact values. |
| `(value: BigRat) Sub(other: BigRat): BigRat` | Subtract exact values. |
| `(value: BigRat) Mul(other: BigRat): BigRat` | Multiply exact values. |
| `(value: BigRat) Div(other: BigRat): BigRat \| MathError` | Divide, rejecting a zero divisor. |
| `(value: BigRat) Abs(): BigRat` | Return the absolute value. |
| `(value: BigRat) Neg(): BigRat` | Negate without mutating the input. |

Every operation is pure. `MathError` is `{ message: String }`. Exact number
representations are private: construct them through the checked functions.
`DecimalScale(value: Int): Bool` proves 0 through 10000 inclusive.

## Float arithmetic

```bork
import "bork/math"

fn main() {
  println(math.Round(2.5), math.RoundToEven(2.5))
  println(math.IsNaN(math.Sqrt(-1.0)))
  println(math.IsInf(math.Inf()))
}
```

```text
3.0 2.0
true
true
```

Float functions follow IEEE behavior: invalid domains can yield NaN or infinity;
use `IsNaN` and `IsInf` to detect them. `Round` ties away from zero;
`RoundToEven` ties to even. Constants are functions; a negative `Inf` sign
selects negative infinity. Trigonometric functions take radians.

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
The [benchmark fixture](../../testdata/benchmarks/math/README.md) measures
parsing, formatting and allocation overhead.

## JSON

`use math.Codecs` imports string codecs for all three exact types. Individual
instances are `EncodeBigInt`, `DecodeBigInt`, `EncodeBigRat`, `DecodeBigRat`,
`EncodeDecimal`, and `DecodeDecimal`. JSON strings preserve precision across
consumers that would otherwise parse a JSON number as a binary float. Decimal
encoding preserves scale; decoding invokes the same checked parsers. JSON number
values and malformed strings return `codec.DecodeError`.

See [examples/math](../../examples/math/main.bork) for invoice arithmetic.



Run `bork doc bork/math` for the generated reference.

[All standard packages](README.md)
