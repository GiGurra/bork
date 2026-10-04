package bindtest

func RuneIdentity(value int32) int32 { return value }

func RuneSlice(values []rune) []rune { return values }

func RuneMap(values map[rune]rune) map[rune]rune { return values }

func InvalidRuneSlice() []rune { return []rune{'a', 0xd800} }
