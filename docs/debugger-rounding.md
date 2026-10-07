# Debugger floating-point rounding

The Debug Console evaluates checked Float and Float32 scalar operators in the
compiler host. The generated program uses Go float64 and float32 operators; the
compiler evaluates the same typed Go operators on the selected frame's operand
bits. The relay only transports reads and presents the compiler's scalar result.
No target function executes and no target memory is written.

## Operand and result protocol

The normal bork checker supplies operand types and rejects mixed-width
arithmetic. The compiler reads each stored float's IEEE bits through a typed
memory read. It does not use Delve's float previews, which erase signed zero and
cannot preserve IEEE special values in arithmetic temporaries. Constant operands
use the checker's existing checked-width rounding.

After the operand reads, the compiler evaluates unary negation, basic
arithmetic and scalar comparisons over the checked tree. Each Float32 operation
has float32 inputs and an explicit float32 result before its parent runs. Float
uses float64. Exact widening of a float32 result for compiler storage does not
change its value or zero sign. Comparisons observe the rounded operands; widening
binary32 values to binary64 preserves their ordering and IEEE NaN behavior.

The final typed scalar is formatted directly for the editor, without passing it
through a Delve arithmetic temporary. Thus signed zero, infinities and NaN survive
nested arithmetic and comparisons. NaN payload bits are not an API guarantee.
Compiler-folded constant expressions retain ordinary compilation semantics.
Boolean short-circuit expressions involving staged float evaluation remain
unsupported until conditional staging can preserve their read order.

## Faithfulness

Ordinary bork lowering emits Go's unary and binary operators. The compiler host
uses those same operators on values of the same checked type, including IEEE
signed zero, subnormal, infinity and NaN cases. Go's floating-point operations
round to nearest with ties to even at their result width. Explicit conversions
to the checked result type prevent carrying excess precision into the next
operation or fusing operations across a rounding boundary. There is no
exact-to-float64-to-float32 approximation path for Float32 arithmetic: its
operators and result conversions are typed float32 throughout.

This argument applies only to the checked scalar operators implemented here.
It does not extend to function calls, transcendentals, aggregate construction,
or different floating-point modes in a target changed through unsafe code.
The compiler does not implement another language's arithmetic in the relay.

## Regression coverage

Compiler tests cover both widths, signed zero, cancellation, underflow, overflow,
zero division, infinity, NaN comparisons and malformed raw-bit responses.
Pinned-Delve Console/watch/hover tests cover the 2^24 and 2^53 addition boundaries,
nested operations, all four basic operators, widely separated exponents,
subnormal ties and IEEE special results. Relay tests cover interleaved plans,
backend failures and execution moving between reads. These tests guard the
protocol; reuse of the runtime's typed Go operations establishes the arithmetic
semantics beyond the sampled values.
