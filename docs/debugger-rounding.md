# Debugger floating-point rounding

The Debug Console evaluates supported Float and Float32 arithmetic one operation
at a time. This note explains why each accepted operation has the same
round-to-nearest, ties-to-even result as runtime arithmetic. It applies to the
pinned Delve v1.27.2 evaluator, not arbitrary debugger versions.

## Evaluation protocol

The normal bork checker supplies operand types and the normal Go lowering
supplies each operator. Before arithmetic, the compiler reads a variable or
stored field's IEEE bits through a typed memory read. Scalar previews are
insufficient: Delve's `go/constant.MakeFloat64` representation erases negative
zero. The compiler rejects non-finite and negative-zero operands and spells
accepted values as exactly representable, typed hexadecimal literals.

Each computed float is evaluated separately. The compiler parses and rounds its
response to the checked width, then substitutes another exact hexadecimal
literal before evaluating its parent. Float literals use the compiler's existing
checked-width rounding. Operands therefore have their checked width at every
step; mixed-width arithmetic is rejected by the ordinary checker.

A read-only operation can still be unsupported. Delve refuses division by zero.
The compiler rejects non-finite results and all runtime-computed zeros, covering
signed-zero and underflow ambiguity conservatively. Compiler-folded constant
expressions retain their ordinary compilation semantics. Arithmetic inside
boolean short-circuit expressions is rejected to avoid eagerly reading an
operand that runtime would skip. No target function executes, and the DAP relay
contains no float operators or rounding logic.

## Float: exact result followed by one rounding

Delve's `pkg/proc/eval.go` uses `go/constant.BinaryOp` for arithmetic.
Finite binary64 operands, including subnormals, have exact rational
representations whose numerator and denominator each need at most 1,075 bits.
One addition, subtraction, multiplication or division of two such rationals
needs at most 2,151 bits per component. Go's `go/constant` retains fractions with
components below its 4,096-bit threshold, so this operation does not enter its
approximate big.Float path. Unary negation is exact as well.

In `service/api/conversions.go`, Delve's `convertFloatValue` applies
`constant.Float64Val` to the result. For an exact rational, Go's `big.Rat.Float64`
rounds to nearest binary64 with ties to even. Delve then uses
`strconv.FormatFloat(value, 'f', -1, 64)`, which produces a decimal that
round-trips to that binary64 value. Parsing this decimal at width 64 and emitting
its exact hexadecimal representation adds no rounding. Thus each accepted Float
operation is the nearest-even rounding of its exact result.

## Float32: why the Float64 intermediate cannot misround

Delve also formats Float32 through `constant.Float64Val`, followed by
`strconv.FormatFloat(..., 32)`. This is a double rounding path, so the Float
argument alone does not establish Float32 correctness. The restriction to one
basic operation on already-rounded binary32 operands is essential.

Binary32 has 24 significant bits; binary64 has 53. For multiplication, the exact
product needs at most 48 significant bits and is exactly representable in
binary64. The exponent range of binary64 also covers every product and quotient
of finite nonzero binary32 operands.

For addition/subtraction, let the operands' normalized exponents differ by d.
If d <= 28, their exact sum needs at most 25 + d <= 53 significant bits, and is
exactly representable in binary64. If d > 28, the smaller operand is far below
half a binary32 ulp of the larger operand, even across a power-of-two boundary.
The binary64 rounding error cannot move the result to a binary32 midpoint.
Subtraction with similar magnitudes is in the first, exact case.

For division, write the positive significands as integers A and B below 2^24;
signs and powers of two only rescale the result. Let j = floor(log2(A/B)), so the
normalized exact quotient is A/(B * 2^j) in [1, 2). A binary32 midpoint in this
interval has the form M/2^24 for an odd integer M.

If j >= 0, the normalized denominator B * 2^j <= A < 2^24. If j < 0, multiply
A by 2^(-j), leaving denominator B < 2^24. In either case, unless the quotient is
exactly the midpoint, subtracting M/2^24 gives a nonzero integer numerator over
a denominator smaller than 2^48. Its distance from the midpoint therefore
exceeds 2^-48. Binary64 rounding error in this normalized interval is at most
2^-53: it cannot reach or cross that midpoint. Exact midpoints are themselves
exactly representable in binary64, preserving the ties-to-even decision.
Subnormal binary32 grids are coarser, so their midpoint bound is no weaker.

Consequently the binary64 intermediate, followed by binary32 rounding, equals
direct nearest-even rounding for these four basic operations. This argument does
not cover arbitrary higher-precision operands, whole expressions evaluated
before rounding, or transcendental functions. The compiler never uses this
protocol for those cases.

## Regression coverage

Pinned-Delve tests cover the 2^24 and 2^53 addition boundaries, nested operations,
subtraction, multiplication, division, widely separated exponents and subnormal
ties. They also reject overflow, underflow to zero, cancellation to zero,
division by zero, non-finite inputs, negative-zero inputs and short-circuit
arithmetic. Compiler tests cover both widths and malformed intermediate
responses. These tests guard the implementation; the preceding argument
establishes the accepted subset beyond the sampled values.
