# bork/env

`bork/env` reads environment variables and loads derived records with checked field values.

Use an injected lookup to try configuration without changing your shell environment:

```bork
import "bork/codec"
import "bork/env"
use codec.Defaults

type Config = { host: String, httpPort: Int, token: Option[String] } derive (codec.Decode)

fn main() {
  values = { "APP_HOST": "localhost", "APP_HTTP_PORT": "8080" }
  match (env.LoadWith[Config]("APP", name => values.get(name))) {
    config: Config => println(config.host, config.httpPort, config.token)
    error: env.ConfigError => println(error.errors)
  }
}
```

The injected reader supplies two fields and leaves token absent:

```text
localhost 8080 Option.None
```

## API

| Signature | Meaning |
| --- | --- |
| `Get(name: String) uses io: Option[String]` | Some for a present variable, including an empty value; None for absent. |
| `Require(name: String) uses io: String \| Missing` | Require a variable to be present; empty is still present. |
| `All() uses io: Map[String, String]` | Snapshot every current environment variable. |
| `Load[T: codec.Decode](prefix: String) uses io: T \| ConfigError` | Load a derived record from one variable per field. |
| `LoadWith[T: codec.Decode](prefix: String, lookup: (String) => Option[String]): T \| ConfigError` | Load through an injected reader; includes its effects. |
| `LoadJson[T: codec.Decode](name: String) uses io: T \| Missing \| json.JsonError \| codec.DecodeError` | Decode one variable containing a JSON document. |

`env.Missing` has `{ variable: String }`. `env.ConfigError` has `{ errors: List[codec.DecodeError] }`; each error has a variable/field path and message. JSON syntax errors come from [bork/json](json.md).

## Load configuration

Field `httpPort` with prefix `APP` maps to `APP_HTTP_PORT`. String fields use literal text; other fields use JSON syntax. Absent fields with defaults use those defaults; missing Option fields without defaults become None. Derived field facts are checked, and all invalid or missing fields are collected rather than stopping at the first failure.

```bork
import "bork/codec"
import "bork/env"
use codec.Defaults

pred positive(n: Int) { n > 0 }
type Config = { host: String, httpPort: Int where positive } derive (codec.Decode)

fn main() {
  values = { "APP_HTTP_PORT": "0" }
  match (env.LoadWith[Config]("APP", name => values.get(name))) {
    config: Config => println(config)
    error: env.ConfigError => {
      for (problem in error.errors) { println(problem.path + ": " + problem.message) }
    }
  }
}
```

Both missing and invalid fields are reported:

```text
APP_HOST: is missing
APP_HTTP_PORT: must be positive
```

In an application, call `env.Load[Config]("APP")` with shell variables:

```sh
APP_HOST=localhost APP_HTTP_PORT=8080 bork run main.bork
```

Loading uses the selected decoder's record schema. A custom decoder can publish schema metadata; use LoadJson when configuration is naturally one structured document. See [codec metadata](codec.md) and [time_env](../../examples/time_env/main.bork).

## Missing and empty variables

Use Get for optional values and Require for presence. If an empty value is invalid too, check its length or derive a record field with a nonempty fact. All returns an immutable snapshot, so later environment changes do not alter it.

```bork
import "bork/env"

fn main() {
  match (env.Require("APP_TOKEN")) {
    token: String => println("token length", token.byteLength())
    missing: env.Missing => eprintln("missing " + missing.variable)
  }
}
```
