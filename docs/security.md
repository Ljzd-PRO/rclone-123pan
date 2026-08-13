# Security

The 123Pan personal backend uses a reverse-engineered web API. Credentials must
only be sent to `login.123pan.com`; authenticated control requests must only be
sent to `yun.123pan.com`. Download requests must never forward API
authorization headers to storage hosts.

Passwords, tokens, signed URLs, authorization headers, cookies, and pre-signed
query parameters must be redacted from errors, logs, and CI artifacts.

The password uses rclone's obscured-password storage and is revealed only when
constructing the authenticated client. Tokens are sensitive and hidden from
the configurator and CLI; they are persisted only after successful login and
cleared on disconnect.

Concurrent 401 responses coordinate on the exact rejected token. One caller
refreshes while the others replay using the new value. Every logical request
is replayed at most once, so a second 401 cannot form a login loop.

Deletion means moving a verified object ID to the recycle bin. Permanent
deletion and recycle-bin cleanup are out of scope.
