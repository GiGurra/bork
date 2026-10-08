[(failure_mapper) (block) (comprehension_expression) (record_literal) (record_type) (list_literal) (list_comprehension) (map_literal)
 (tuple_literal) (tuple_type) (tuple_pattern)
 (parameters) (type_arguments) (match_expression) (select_expression) (class_declaration)
 (instance_declaration) (instances_declaration)] @indent.begin
["}" ")" "]"] @indent.end @indent.branch
(comment) @indent.ignore
(go_content) @indent.ignore

(tag_group) @indent.begin
