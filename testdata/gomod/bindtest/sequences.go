package bindtest

import "iter"

func CollectStrings(values iter.Seq[string]) []string {
	out := []string{}
	for value := range values {
		out = append(out, value)
	}
	return out
}
func StringValues() (iter.Seq[string], error) {
	return func(yield func(string) bool) { yield("one") }, nil
}
