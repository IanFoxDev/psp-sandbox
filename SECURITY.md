# Security

psp-sandbox is a test tool. It is not meant to handle real card data or to be exposed
to the internet: the control API has no authentication by design.

If you find a problem that matters anyway (for example, a way to make the container
execute arbitrary code), do not open a public issue. Report it privately through
[GitHub](https://github.com/IanFoxDev/psp-sandbox/security/advisories/new), or write to
ianfoxdeveloper@gmail.com.

## Supported versions

| Version | Fixes |
|---|---|
| Latest minor of the current major | Bug and security fixes |
| Last minor of the previous major | Security fixes for 6 months after the new major |
| Older, and every `0.x` once 1.0 is out | None |

Until 1.0 that means the latest `0.x` release only; the `0.4` image tag always points to
the newest `0.4.x`. Security fixes ship as patch releases, the GitHub advisory is
published together with the release, and the image tags (`:1`, `:1.2`) move to the fixed
build. What the version numbers promise: [docs/stability.md](docs/stability.md).
