(block) @local.scope
(function_declaration) @local.scope
(lambda_expression) @local.scope
(parameter name: (identifier) @local.definition)
(binding name: (identifier) @local.definition)
(lambda_parameter (identifier) @local.definition)
(identifier) @local.reference
