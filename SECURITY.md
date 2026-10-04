# Security

psp-sandbox is a test tool. It is not meant to handle real card data or to be exposed
to the internet: the control API has no authentication by design.

If you find a problem that matters anyway (for example, a way to make the container
execute arbitrary code), do not open a public issue. Report it privately through
[GitHub](https://github.com/IanFoxDev/psp-sandbox/security/advisories/new), or write to
ianfoxdeveloper@gmail.com.

## Supported versions

Fixes go into the latest release only. Until 1.0 that is the latest `0.x` tag; the
`0.4` image tag always points to the newest `0.4.x`.
