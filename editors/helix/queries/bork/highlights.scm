(comment) @comment
(shebang) @comment
[(bare_return) (bare_break) (bare_continue)] @keyword
(identifier) @variable
(number) @constant.numeric
(tuple_index) @variable.other.member
(rune) @constant.character
(string) @string
(interpolated_string) @string
(escape_sequence) @constant.character.escape
(interpolation) @embedded
["true" "false"] @constant.builtin.boolean
(type (qualified_name (identifier) @type))
(type_declaration name: (identifier) @type.definition)
(derive_declaration class: (qualified_name (identifier) @type))
(variant name: (identifier) @type.enum.variant)
(function_declaration name: (identifier) @function)
(method_signature name: (identifier) @function.method)
(predicate_declaration name: (identifier) @function)
(rule_declaration name: (identifier) @function)
(parameter name: (identifier) @variable.parameter)
(field_declaration name: (identifier) @variable.other.member)
(record_literal name: (identifier) @variable.other.member)
(selector_expression field: (identifier) @variable.other.member)
(call_expression function: (qualified_name (identifier) @function.call))
(interpolated_string prefix: (qualified_name (identifier) @function.call))
["fn" "pred" "type" "sealed" "unsafe" "where" "and" "or" "trust" "rule"
 "generate" "yield" "for" "break" "continue" "if" "else" "return" "match"
 "import" "use" "class" "instance" "instances" "test" "private"
 "derive" "uses" "needs" "ambient" "logged" "propagated" "lazy" "async"
 "scope" "with" "in" "resource" "go" "comptime" "mock" "select"] @keyword
["&" "^" "<<" ">>" "+" "-" "*" "/" "%" "!" "&&" "||" "==" "!=" "<" "<=" ">" ">=" "|>" "|" "=>" "=" "?"] @operator
["(" ")" "[" "]" "{" "}"] @punctuation.bracket
["," ";" ":" "."] @punctuation.delimiter

(context_pattern name: (identifier) @type.enum.variant)

(is_expression "is" @keyword)

(test_field_pattern name: (identifier) @variable.other.member)

(specialized_variant_pattern name: (identifier) @type.enum.variant)
(specialized_variant_pattern (qualified_name (identifier) @type))
(list_comprehension name: (identifier) @variable)
