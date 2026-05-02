# Security policy

## Supported versions

`v0.x` is pre-release ; the latest minor is the only supported line.
`v1.0` and later will follow a documented LTS schedule.

## Reporting a vulnerability

**Do not open a public issue for security reports.**

Email the maintainers — addresses listed in
[`.github/CODEOWNERS`](.github/CODEOWNERS) — with :

- A description of the vulnerability and the affected versions
- A minimal reproducer if possible
- Your assessment of impact and exploitability

You will receive an acknowledgement within 72 hours and a remediation timeline
within 7 days.

For protocol-level vulnerabilities (LSDP/1, LSML 1.0), report to the
`lumencast-protocol` repo. We coordinate fixes across SDKs before public
disclosure.

## Threat model — server kit

The `server/` kit is **token-agnostic**. The user provides an `Authenticator`
implementation. We make no claims about credential handling beyond :

- Tokens are passed in the `subscribe` frame, encrypted in transit by the
  WebSocket TLS layer (operators MUST deploy `wss://` in production).
- The default in-memory `StaticTokens` authenticator is **for development
  and tests only**. The `lumencast init` template flags it as a TODO.
- Role enforcement (`viewer` / `operator` / `service` / `test`) and path
  scoping are enforced server-side. A misbehaving client cannot bypass them.

Out of scope for this module : credential storage, JWT validation logic,
mTLS, secret rotation. Operators are expected to integrate their own
infrastructure.

## Disclosure

We follow [coordinated disclosure](https://en.wikipedia.org/wiki/Coordinated_vulnerability_disclosure).
Public advisory + patched release on the same day.
