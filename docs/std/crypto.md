# bork/crypto

## Hashing and passwords

`bork/crypto` provides SHA256/SHA512 (Bytes and hex forms), HMAC256/HMAC512,
constant-time `Equal` for equal-length byte strings, and HMAC verification.
`RandomBytes` accepts 0 through 16 MiB; `Token(size = 32)` accepts 16 through
4096 bytes of entropy and returns unpadded URL-safe base64. Both use the system
cryptographic random source and return Error on entropy failure.

`HashPassword` uses Argon2id with a fresh 16-byte salt and 32-byte key, encoded
as an Argon2id v19 PHC string. Defaults are 19456 KiB (19 MiB), two iterations
and one lane, following the [OWASP password storage recommendation](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).
`PasswordParams` is a checked record: memoryKiB 1024–262144, iterations 1–16,
parallelism 1–16. These bounds also limit work when verifying untrusted hashes.
Overrides below the defaults are supported for testing or an explicit app
policy; defaults are recommended for password storage. Argon2id has no bcrypt
72-byte password limit. The package pins golang.org/x/crypto v0.37.0.

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

## Examples

Import `bork/crypto` for SHA-256/512, HMAC, secure bytes/tokens and Argon2id
password hashes. Verification also accepts legacy bcrypt hashes; `NeedsRehash`
supports upgrading them on login. See [examples/crypto](../../examples/crypto/main.bork).
