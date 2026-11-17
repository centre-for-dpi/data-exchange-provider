# DEN provider node kit

> Versión en español: [es/README.md](es/README.md)

The node is installed at each provider entity of the Data Exchange Network (DEN). It gives the entity's own APIs a secure channel to the network gateway (`canal` mode) and can also publish views of the entity's database as REST APIs (`completo` mode, instant APIs over SQL Server, PostgreSQL, MySQL and Oracle). It reaches the network through an outbound encrypted tunnel and serves only the gateway, over mTLS.

## For entities

- [Provider guide](guides/providers.html): what a provider entity designates, provides and does ([full text](guides/providers.md))
- [Installing and operating the node](INSTALL.md)
- [Instant APIs](INSTANT-APIS.html): preparing the database, the service file, types, responses and changes ([full text](INSTANT-APIS.md))
- [Intake survey for the bootcamp](guides/bootcamp-survey.md)

## For developers and operators

- [Architecture](ARCHITECTURE.md)
- [Development](DEVELOPMENT.md): requirements, commands, tests and continuous integration
- [Architecture decisions](https://github.com/centre-for-dpi/data-exchange-provider/tree/main/docs/adr)
- [Source code](https://github.com/centre-for-dpi/data-exchange-provider)

The portal and the gateway live in another repository, [data-exchange-admin-portal](https://github.com/centre-for-dpi/data-exchange-admin-portal). The boundary between them is the node contract v1 that the portal publishes.

Apache-2.0, © 2026 Centre for Digital Public Infrastructure (CDPI).
