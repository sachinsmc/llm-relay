# Security Policy

## Supported versions

`llm-relay` is pre-1.0. Security fixes are applied to the latest `v0.x` release.

| Version | Supported |
|---------|-----------|
| latest `v0.x` | ✅ |
| older | ❌ |

## Reporting a vulnerability

Please report security issues **privately**, not via public issues or pull
requests.

- Preferred: open a private advisory via GitHub Security Advisories
  ("Report a vulnerability" under the repository's **Security** tab).
- Or email **hey@sachinsmc.me** with details and a way to reproduce.

You'll get an acknowledgement as soon as possible. Once a fix is available and
released, the advisory will be published with credit (unless you prefer to
remain anonymous).

## Scope notes

`llm-relay` is a thin proxy that holds upstream provider API keys server-side
and forwards OpenAI-format requests. When reporting, useful areas include:

- ways the upstream key/model could leak to a client,
- quota/limiter bypasses,
- request smuggling or header injection into the upstream call,
- denial-of-service via the streaming path.
