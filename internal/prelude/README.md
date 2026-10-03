# Embedded prelude

Every Bork package sees these files as one prelude package. The compiler
embeds all `*.bork` files here and parses them in filename order. Positions
use `prelude/<filename>` in diagnostics and `bork describe`.

| File | Definitions |
| --- | --- |
| [prelude.bork](prelude.bork) | Common errors and the Go representation of Bork values |
| [options.bork](options.bork) | `Option` and its methods |
| [lists.bork](lists.bork) | List constructors, `notEmpty`, and list methods |
| [strings.bork](strings.bork) | Number and Boolean parsing, and string methods |
| [runes.bork](runes.bork) | Unicode rune helpers |
| [maps.bork](maps.bork) | `Entry` and persistent map methods |
| [bytes.bork](bytes.bork) | Immutable bytes and UTF-8 conversions |
| [classes.bork](classes.bork) | `Eq`, `Show`, `Ord`, and primitive ordering instances |
| [json.bork](json.bork) | JSON values, parsing, rendering, `Decode`, `Encode`, and their instances |
| [concurrency.bork](concurrency.bork) | Tasks, cancellation, atoms, channels, and sleep |
| [scopes.bork](scopes.bork) | Resource attachment, scope policies, and finalizers |
| [environment.bork](environment.bork) | `IoError`, arguments, exit, and standard error output |
