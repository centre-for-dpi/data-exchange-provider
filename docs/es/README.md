# Kit de APIs instantáneas — DEN

> English version (official): [README.md](../../README.md)

Nodo que se instala en cada entidad proveedora de la Red de Intercambio de Datos. Expone vistas de su base de datos como APIs REST, a través de un túnel cifrado y con mTLS, solo hacia el gateway de la red.

Estado: el modo `canal` (canal seguro para las APIs propias de la entidad) y el modo `completo` (APIs instantáneas sobre SQL Server, PostgreSQL, MySQL y Oracle) están construidos y probados de punta a punta contra el portal (hitos K0–K3; el endurecimiento de K4 sigue en curso, ver `docs/BACKLOG.md`). Diseño: `docs/es/ARCHITECTURE.md`.

Para las entidades:

- [docs/es/guides/providers.md](guides/providers.md) — qué designa, dispone y hace una entidad proveedora (versión para proyectar: `providers.html`)
- [docs/es/INSTALL.md](INSTALL.md) — instalación y operación del nodo
- [docs/es/INSTANT-APIS.md](INSTANT-APIS.md) — APIs instantáneas: preparar la base, el archivo del servicio, tipos, respuestas y cambios (versión para proyectar: `INSTANT-APIS.html`)

El portal y el gateway están en otro repositorio. La frontera es el contrato de nodos v1 que publica el portal.

## Licencia

Apache-2.0, © 2026 Centre for Digital Public Infrastructure (CDPI). Ver [LICENSE](../../LICENSE), [NOTICE](../../NOTICE) y [ADR-0003](../adr/0003-licencia-apache-2.md).
