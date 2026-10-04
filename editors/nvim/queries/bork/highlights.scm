(comment) @comment
(shebang) @comment
[(bare_return) (bare_break) (bare_continue)] @keyword
(identifier) @variable
(number) @number
(rune) @character
(string) @string
(interpolated_string) @string
(escape_sequence) @string.escape
(interpolation) @embedded
["true" "false"] @boolean
(type (qualified_name (identifier) @type))
(type_declaration name: (identifier) @type.definition)
(variant name: (identifier) @type.enum.variant)
(function_declaration name: (identifier) @function)
(method_signature name: (identifier) @function.method)
(predicate_declaration name: (identifier) @function)
(rule_declaration name: (identifier) @function)
(parameter name: (identifier) @variable.parameter)
(field_declaration name: (identifier) @variable.member)
(record_literal name: (identifier) @variable.member)
(selector_expression field: (identifier) @variable.member)
(call_expression function: (qualified_name (identifier) @function.call))
(interpolated_string prefix: (qualified_name (identifier) @function.call))
["fn" "pred" "type" "sealed" "unsafe" "where" "and" "or" "trust" "rule"
 "generate" "yield" "for" "break" "continue" "if" "else" "return" "match"
 "import" "use" "class" "instance" "instances" "providers" "test" "private"
 "derive" "uses" "needs" "ambient" "logged" "propagated" "lazy" "async"
 "scope" "with" "in" "resource" "go" "comptime" "mock"] @keyword
["+" "-" "*" "/" "%" "!" "&&" "||" "==" "!=" "<" "<=" ">" ">=" "|>" "|" "=>" "=" "?"] @operator
["(" ")" "[" "]" "{" "}"] @punctuation.bracket
["," ";" ":" "."] @punctuation.delimiter
