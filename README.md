# rclone 123Pan backend

This repository builds a custom `rclone-123` binary containing an experimental
out-of-tree backend for 123Pan personal accounts.

> [!WARNING]
> This is an internal alpha. It uses a reverse-engineered web API, has not been
> validated with a dedicated live test account, and is not ready for public or
> production use.

## Pinned baselines

- rclone `v1.75.0` (`9ee9d0a0cafd5e5fe3b271d2280b090ab6e64048`)
- OpenList `v4.2.5` (`cc87e88f038a5a27c8782afc7b66a3c1a3cdcb77`)

Only protocol behavior from OpenList `drivers/123` is used as a reference.
`123_open`, `123_link`, and `123_share` are outside the project scope.

## Current milestone

| Capability | Status |
| --- | --- |
| Custom static rclone entry point | Implemented |
| `123pan` backend registration | Implemented |
| Protocol types, signing, strict envelopes, redaction | Implemented and unit tested |
| Password login, cached token, coordinated 401 refresh, logout | Implemented and unit tested |
| File operations | In progress |
| Mock and fault-injection suite | Planned |
| Dedicated-account live validation | Blocked: no account supplied |
| Public release | Blocked: license review and live validation |

## Build

Go 1.25.0 is required.

```console
make build
./bin/rclone-123 version
```

The `noselfupdate` build tag is mandatory. Self-update would otherwise replace
the custom binary with an official build that does not contain this backend.

See [backend documentation](docs/123pan.md), [security](docs/security.md), and
[testing](docs/testing.md) for the intended behavior and release gates.
