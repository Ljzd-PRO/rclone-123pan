# Security

The 123Pan personal backend uses a reverse-engineered web API. Credentials must
only be sent to `login.123pan.com`; authenticated control requests must only be
sent to `yun.123pan.com`. Download requests must never forward API
authorization headers to storage hosts.

Passwords, tokens, signed URLs, authorization headers, cookies, and pre-signed
query parameters must be redacted from errors, logs, and CI artifacts.

Deletion means moving a verified object ID to the recycle bin. Permanent
deletion and recycle-bin cleanup are out of scope.

