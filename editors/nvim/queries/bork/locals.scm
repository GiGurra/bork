(block) @local.scope
(function_declaration) @local.scope
(parameter name: (identifier) @local.definition)
(binding name: (identifier) @local.definition)
(lambda_parameter (identifier) @local.definition)
(identifier) @local.reference
(lambda_expression) @local.scope
(for_expression) @local.scope
(scope_expression) @local.scope
(mock_expression) @local.scope
(match_arm) @local.scope
(receiver name: (identifier) @local.definition)
(lambda_expression parameter: (identifier) @local.definition)
(for_expression name: (identifier) @local.definition)
(scope_expression name: (identifier) @local.definition)
(mock_parameters name: (identifier) @local.definition)
(pattern name: (identifier) @local.definition)
(field_pattern name: (identifier) @local.definition !pattern)
((pattern . (qualified_name . (identifier) @local.definition .) .)
 (#match? @local.definition "^[a-z]"))
