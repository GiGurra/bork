(comment) @comment
(shebang) @comment
[(bare_return) (bare_break) (bare_continue)] @keyword
(identifier) @variable
(number) @number
(tuple_index) @property
(rune) @string
(string) @string
(interpolated_string) @string
(escape_sequence) @string.escape
(interpolation) @embedded
["true" "false"] @boolean
(type (qualified_name (identifier) @type))
(type_declaration name: (identifier) @type)
(derive_declaration class: (qualified_name (identifier) @type))
(variant name: (identifier) @variant)
(function_declaration name: (identifier) @function)
(method_signature name: (identifier) @function)
(predicate_declaration name: (identifier) @function)
(rule_declaration name: (identifier) @function)
(parameter name: (identifier) @variable.parameter)
(field_declaration name: (identifier) @property)
(record_literal name: (identifier) @property)
(selector_expression field: (identifier) @property)
(call_expression function: (qualified_name (identifier) @function))
(interpolated_string prefix: (qualified_name (identifier) @function))
["fn" "pred" "type" "sealed" "unsafe" "where" "and" "or" "trust" "rule"
 "generate" "yield" "for" "break" "continue" "if" "else" "return" "match"
 "import" "use" "class" "instance" "instances" "test" "private"
 "derive" "uses" "needs" "ambient" "logged" "propagated" "lazy" "async"
 "scope" "with" "in" "resource" "go" "comptime" "mock" "select"] @keyword
["&" "^" "<<" ">>" "+" "-" "*" "/" "%" "!" "&&" "||" "==" "!=" "<" "<=" ">" ">=" "|>" "|" "=>" "=" "?"] @operator
["(" ")" "[" "]" "{" "}"] @punctuation.bracket
["," ";" ":" "."] @punctuation.delimiter

(context_pattern name: (identifier) @variant)

(is_expression "is" @keyword)

(test_field_pattern name: (identifier) @property)

(specialized_variant_pattern name: (identifier) @variant)
(specialized_variant_pattern (qualified_name (identifier) @type))
(list_comprehension name: (identifier) @variable)
