# Instant API kit — DEN

> Versión en español: [docs/es/README.md](docs/es/README.md)

A node installed at each provider entity of the Data Exchange Network. It exposes views of the entity's database as REST APIs, through an encrypted tunnel and over mTLS, to the network gateway only.

Status: the `canal` mode (secure channel for the entity's own APIs) and the `completo` mode (instant APIs over SQL Server, PostgreSQL, MySQL and Oracle) are built and tested end to end against the portal (milestones K0–K3; K4 hardening in progress, see `docs/BACKLOG.md`). Design: `docs/ARCHITECTURE.md`.

For entities:

- [docs/guides/providers.md](docs/guides/providers.md) — what a provider entity designates, provides and does (presentation version: `providers.html`)
- [docs/INSTALL.md](docs/INSTALL.md) — installing and operating the node
- [docs/INSTANT-APIS.md](docs/INSTANT-APIS.md) — instant APIs: preparing the database, the service file, types, responses and changes (presentation version: `INSTANT-APIS.html`)

The portal and the gateway live in another repository. The boundary is the node contract v1 that the portal publishes.

## License

Apache-2.0, © 2026 Centre for Digital Public Infrastructure (CDPI). See [LICENSE](LICENSE), [NOTICE](NOTICE) and [ADR-0003](docs/adr/0003-licencia-apache-2.md).
