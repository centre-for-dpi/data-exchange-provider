# Instant API kit — RID

> Versión en español: [docs/es/README.md](docs/es/README.md)

A node installed at each provider entity of the Data Exchange Network. It exposes views of the entity's database as REST APIs, through an encrypted tunnel and over mTLS, to the network gateway only.

Status: in design. See `CLAUDE.md` and `docs/CONTEXT.md`.

For entities:

- [docs/guides/providers.md](docs/guides/providers.md) — what a provider entity designates, provides and does (presentation version: `providers.html`)
- [docs/INSTALL.md](docs/INSTALL.md) — installing and operating the node
- [docs/INSTANT-APIS.md](docs/INSTANT-APIS.md) — instant APIs: preparing the database, the service file, types, responses and changes (presentation version: `INSTANT-APIS.html`)

The portal and the gateway live in another repository. The boundary is the node contract v1 that the portal publishes.

## License

Apache-2.0, © 2026 Centre for Digital Public Infrastructure (CDPI). See [LICENSE](LICENSE), [NOTICE](NOTICE) and [ADR-0003](docs/adr/0003-licencia-apache-2.md).
