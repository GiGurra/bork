# bork/crypto

`bork/crypto` hashes bytes, authenticates messages, generates secrets, and stores password hashes.

```bork
import "bork/crypto"
import "bork/encoding"

fn main() {
  println(crypto.SHA256Hex(encoding.Utf8("hello")))
  match (crypto.VerifyPassword("password", "not-a-hash")) {
    error: crypto.Error => println(error.message)
    verified: Bool => println(verified)
  }
}
```

```text
2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
expected an Argon2id v=19 PHC string
```

## API

| Signature | Meaning |
| --- | --- |
| `SHA256(data: Bytes): Bytes` | Compute a SHA-256 digest. |
| `SHA512(data: Bytes): Bytes` | Compute a SHA-512 digest. |
| `SHA256Hex(data: Bytes): String` | Compute lowercase SHA-256 hex. |
| `SHA512Hex(data: Bytes): String` | Compute lowercase SHA-512 hex. |
| `HMAC256(key: Bytes, data: Bytes): Bytes` | Authenticate bytes with HMAC-SHA256. |
| `HMAC512(key: Bytes, data: Bytes): Bytes` | Authenticate bytes with HMAC-SHA512. |
| `Equal(first: Bytes, second: Bytes): Bool` | Compare equal-length bytes in constant time. |
| `VerifyHMAC256(key: Bytes, data: Bytes, signature: Bytes): Bool` | Check an HMAC-SHA256 signature. |
| `VerifyHMAC512(key: Bytes, data: Bytes, signature: Bytes): Bool` | Check an HMAC-SHA512 signature. |
| `RandomBytes(size: Size) uses random: Bytes \| Error` | Draw cryptographic entropy bytes. |
| `Token(size: TokenSize = 32) uses random: String \| Error` | Draw entropy and encode URL-safe base64. |
| `DefaultPasswordParams(): PasswordParams` | Return the default Argon2id work factors. |
| `HashPassword(password: String, parameters: PasswordParams = PasswordParams { memoryKiB: 19456, iterations: 2, parallelism: 1 }) uses random: String \| Error` | Encode a salted Argon2id PHC hash. |
| `VerifyPassword(password: String, encoded: String): Bool \| Error` | Check an Argon2id or supported bcrypt hash. |
| `NeedsRehash(encoded: String, parameters: PasswordParams = PasswordParams { memoryKiB: 19456, iterations: 2, parallelism: 1 }): Bool` | Test whether a stored hash differs from the target policy. |

| Predicate signature | Meaning |
| --- | --- |
| `ValidSize(size: Int): Bool` | Prove a random-byte count is 0–16777216. |
| `ValidTokenSize(size: Int): Bool` | Prove an entropy count is 16–4096. |
| `ValidMemory(memory: Int): Bool` | Prove memory is 1024–262144 KiB. |
| `ValidIterations(iterations: Int): Bool` | Prove iterations are 1–16. |
| `ValidParallelism(parallelism: Int): Bool` | Prove parallelism is 1–16. |

`Error` is `{ message: String }`. `Size` is an Int from 0 through 16777216;
`TokenSize` is an Int from 16 through 4096. Guard dynamic sizes with
`ValidSize(size)` and `ValidTokenSize(size)`.

`PasswordParams` is a checked record:

| Field | Fact | Default |
| --- | --- | --- |
| `memoryKiB: Int` | `ValidMemory`: 1024–262144 | 19456 |
| `iterations: Int` | `ValidIterations`: 1–16 | 2 |
| `parallelism: Int` | `ValidParallelism`: 1–16 | 1 |

## Hash bytes and verify a MAC

```bork
import "bork/crypto"
import "bork/encoding"

fn main() {
  key = encoding.Utf8("secret")
  data = encoding.Utf8("message")
  mac = crypto.HMAC256(key, data)
  println(crypto.VerifyHMAC256(key, data, mac))
  println(crypto.VerifyHMAC256(key, encoding.Utf8("changed"), mac))
}
```

```text
true
false
```

SHA256 and SHA512 have Bytes and hex forms. HMAC verification uses
constant-time comparison for equal-length byte strings; differing lengths
return false. Unkeyed hashes do not authenticate a message.

## Generate secrets

```bork
import "bork/crypto"

fn main() {
  match (crypto.Token()) {
    token: String => println(token.byteLength())
    error: crypto.Error => eprintln(error.message)
  }
}
```

```text
43
```

`RandomBytes` and `Token` use the system cryptographic random source and return
`Error` on entropy failure. Tokens use unpadded URL-safe base64; their size
argument counts entropy bytes, not output characters.

## Store and verify passwords

```bork
import "bork/crypto"

fn demo() uses io + random: Ok | crypto.Error {
  hash = crypto.HashPassword("correct horse battery staple")?
  println(crypto.VerifyPassword("correct horse battery staple", hash))
  println(crypto.VerifyPassword("wrong", hash))
  println(crypto.NeedsRehash(hash))
}

fn main() {
  println(demo())
}
```

```text
true
false
false
Ok
```

`HashPassword` uses Argon2id with a fresh 16-byte salt and 32-byte key, encoded
as an Argon2id v19 PHC string. Defaults are 19456 KiB (19 MiB), two iterations
and one lane, following the [OWASP password storage recommendation](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).
`PasswordParams` is a checked record: memoryKiB 1024–262144, iterations 1–16,
parallelism 1–16. These bounds also limit work when verifying untrusted hashes.
Overrides below the defaults are supported for testing or an explicit app
policy; defaults are recommended for password storage. Argon2id has no bcrypt
72-byte password limit. 

`VerifyPassword` returns false for a wrong password and Error for a malformed,
unsupported or out-of-bounds hash. It also accepts legacy bcrypt 2/2a/2b/2y
hashes with cost at most 16; passwords longer than 72 bytes return false for
these legacy hashes. Argon2id salt/key lengths of 8–64 and 16–64 bytes are
accepted for imports. Padded base64, whitespace, unknown/duplicate parameters
and Argon2 versions other than v19 are rejected.

Call `NeedsRehash(hash, parameters = default)` after successful verification.
It returns true for bcrypt, invalid hashes, parameter differences, or salt/key
lengths other than 16/32. It compares against the exact application target,
including when a stored hash uses higher work factors; pass the application's
chosen parameters consistently to hashing and rehash checks.


See the [crypto example](../../examples/crypto/main.bork).

[All standard packages](README.md)
