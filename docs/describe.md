# Compiler code queries

`bork describe file.bork:line:column` asks the compiler for the selected value's type, definition, visible methods, and conservatively known facts. `--where` asks the main question: is a particular requirement proven for this value at this point?

```bork
fn firstOrZero(xs: List[Int]): Int {
  if (!notEmpty(xs)) { return 0 }
  xs.first()
}
```

For this file saved as `main.bork`, `bork describe main.bork:3:3 --where notEmpty` reports `List[Int]`, the parameter's definition, its methods, and `proven: notEmpty`. Before the guard, the same query is not proven; the answer suggests a guard or parameter requirement using the compiler's existing diagnostic hints.

Write the constraint as in a `where` clause, with the selected value implicit: `--where notEmpty`, `--where 'between(1, 10)'`, or `--where 'positive and (small or zero)'`. Additional arguments can be constants or the enclosing function's parameters, as in `--where 'notEqual(other)'`. The actual compiler prover checks guards, declarations, aliases, returned values, rule inference and constant predicates. Constant queries run predicates just as compilation does. An unproven fact is an answer, so the command still exits successfully. Invalid source, positions, predicates, or incompatible predicate types fail with status 1.

The file's entire directory is checked as a package, along with its imports. A main function is not required. Queries currently require a valid package; the command does not return partial semantic results for broken source. Paths in answers are absolute for disk sources. Embedded prelude and standard package definitions retain their compiler source paths, such as `prelude/lists.bork`.

## Positions and selections

Lines and columns are one-based. Columns count UTF-8 bytes; tabs count as one byte. A position anywhere in a name or literal selects its token. Operators select the expression they compute. Binding names and function/lambda parameter names select their declared values; match pattern bindings and scope names are also supported. Name references point to their actual parameter, binding, pattern, function, field or variant declaration.

A declared function or method name in a call selects its instantiated function signature and definition. Select the call's opening `(` to query its result instead, including facts about that result. Methods' signatures omit the receiver. Direct callable selections also report parameter names and defaults, identifying those names as public API for named arguments. Function values retain only their function type. Function values select their function type. Compiler builtins have no source definition; their call reports the result type.

The compiler folds constant arithmetic into a single value. A position in that arithmetic selects the complete folded expression, and `expression` in the JSON (or the first text line) gives its source spelling. For example, every numeric/operator token in `128 - 1` bound to an `Int8` describes the folded `Int8` value `127`; `--where positive` queries that value. Whitespace and comments outside expressions do not select neighboring code. Type annotations and declaration names other than local bindings/parameters are not expression queries.

## Methods and known facts

Methods follow exactly the compiler's visibility and precedence rules: local package, exported methods from the receiver's package and imports, then prelude. Ambiguous names are reported with their candidate packages. Record fields hide methods with the same name. Receiver type parameters are filled from the selected type; remaining generic parameters stay named. `requires` lists declared predicate and class requirements, which may already hold in the caller. Listing a method does not promise that every invocation meets those requirements.

`facts` lists known guard, trusted, declaration, and signature facts, including aliases of those values. A fact's optional `path` applies to a part of the value, such as `.[]` for every list element or `.value` for an optional value. Enumeration is conservative: it does not try every possible predicate, rule conclusion, constant argument or derived result. An empty list does not mean no fact is provable. Use `--where` for the requirement that matters.

## JSON format

`--json` writes one versioned description object to stdout. Failed queries write diagnostic JSON Lines to stderr, using [the diagnostic format](diagnostics.md).

```json
{"schema_version":1,"position":{"file":"/project/main.bork","line":3,"column":9},"type":"List[Int]","definition":{"file":"/project/main.bork","line":1,"column":16},"methods":[],"facts":[{"constraint":"notEmpty"}],"proof":{"where":"notEmpty","proven":true}}
```

This abbreviated example omits method entries. Every description has `schema_version` (currently `1`), the requested `position`, `type`, `methods`, and `facts`. `definition` is omitted when there is no source definition. Folded arithmetic includes `expression`. A method has `name`, `type`, `definition` and optional `requires`; an ambiguous entry instead has `name` and `ambiguity`. `proof` is present only with `--where`, and contains `where`, `proven`, and a `reason` when the requirement is not proven. `callable` accompanies direct callable selections and visible methods. It has `named_arguments: true`, `parameter_names_are_api: true`, and a `parameters` array of `name`, `type`, optional `default` (source syntax), and optional `receiver: true` (which cannot be named). Function-value selections omit `callable`. Consumers should accept additional fields.

The position adapter reads the typed compiler tree. The CLI, JSON result, and backward proof queries are separate from lookup, so a future language server can use the same queries.
