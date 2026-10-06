package naming

import "go.yaml.in/yaml/v4"

// YAMLString uses bork/yaml's loader and core-schema resolver. A wire tag
// must be a string when written as a plain scalar, rather than a bool, null,
// number, collection, or YAML syntax.
func YAMLString(name string) bool {
	var node yaml.Node
	if err := yaml.Load([]byte(name), &node); err != nil {
		return false
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = *node.Content[0]
	}
	return node.Kind == yaml.ScalarNode && node.ShortTag() == "!!str" && node.Value == name
}
