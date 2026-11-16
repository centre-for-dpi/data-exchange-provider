# CLAUDE.md — Kit de APIs instantáneas (nodo del proveedor)

Este archivo es el contexto permanente para Claude Code. Léelo completo al inicio de cada sesión, y después `docs/CONTEXTO.md`.

## Qué es este repositorio

El **kit de APIs instantáneas** es lo que se entrega e instala en cada **entidad proveedora** de la Red de Intercambio de Datos (RID). El kit sirve a las entidades que no tienen una API propia:

1. Se conecta a una base de datos de la entidad con un **usuario de solo lectura**.
2. Descubre las **vistas y tablas** a las que ese usuario tiene acceso, y la entidad elige cuáles exponer.
3. Genera una **API REST** sobre ellas, con su **OpenAPI**.
4. Se **enrola** en la red con un token del portal, abre un **túnel WireGuard hacia afuera** (la entidad no abre puertos de entrada) y sirve la API por **mTLS** solo al gateway.
5. Envía **heartbeats** con los servicios que expone y **renueva** su certificado.

El kit es el segundo de dos entregables (ADR-0011 del portal). El primero, **portal + gateway**, está en el repositorio hermano `../data-exchange` y ya implementa todo su lado del contrato. **La frontera entre los dos es el contrato de nodos v1**: `../data-exchange/docs/contracts/node-contract-v1.openapi.json`, descrito en `../data-exchange/docs/ARCHITECTURE.md §10`. Un cambio de contrato se hace y se versiona en el portal, no aquí.

El primer despliegue es Colombia (entidades públicas; el gateway lo opera la AND y lo gobierna MinTIC), pero el kit debe servir en **cualquier país**.

## Estado (2026-11-16)

**Herramienta decidida, sin código del kit todavía.**

- La discusión terminó en el **ADR-0001** (`docs/adr/0001-nodo-en-go-con-apis-instantaneas-propias.md`). El nodo es el canal seguro de **toda** entidad proveedora, tenga API propia o use las APIs instantáneas.
- Es un solo binario en **Go**: agente, WireGuard en espacio de usuario, mTLS validando el nombre del gateway y enrutador por `servicePath`.
- Las APIs instantáneas son un **módulo propio y angosto** (GET por clave sobre vistas, OpenAPI generado que pasa el lint) para SQL Server, PostgreSQL, MySQL y Oracle (go-ora).
- Data API builder y las demás herramientas quedaron fuera del kit.
- La evidencia está en `docs/spikes/S-1-dab.md` y `docs/spikes/S-2-nodo-go.md`.
- `spikes/` es código desechable: se aprende de él, no se reutiliza como base del kit.

`docs/CONTEXTO.md` reúne lo que el portal ya hace y lo que el kit debe cumplir.

## Reglas de oro (heredadas del portal)

1. **Solo open source con licencia aprobada por la OSI**, incluidos los drivers de bases de datos y todo lo que se empaquete. Permitidas: MIT, Apache-2.0, BSD-2/3, ISC, MPL-2.0, LGPL, 0BSD, BlueOak-1.0.0, Python-2.0, Zlib, Unlicense, MIT-0 y EPL-2.0. GPL/AGPL solo en servicios separados que corren en su propio contenedor, nunca enlazados. **Prohibidas:** BSL, SSPL, Elastic License, "source-available", "fair source" y Commons Clause. Hay que tener cuidado con las ediciones "community" que reservan conectores (Oracle, SQL Server) a ediciones pagas. Verifica la licencia antes de proponer cualquier dependencia. Detalle: ADR-0002, ADR-0008 y ADR-0012 del portal.
2. **No se guarda el contenido de las respuestas.** Ni el kit ni sus logs guardan cuerpos de petición o respuesta, ni siquiera en modo debug. Solo metadatos. Rutas y query strings van **enmascarados** en los logs, porque pueden llevar números de identificación.
3. **Los datos se quedan en la entidad.** El kit lee con un usuario de solo lectura y no copia ni replica datos fuera de la base de la entidad.
4. **Nada específico de un país en el código.**
5. **Código, identificadores, commits y comentarios en inglés.** Documentación y textos para las entidades, en español.
6. **Sin secretos en el repo.** Ni tokens de enrolamiento, ni claves, ni credenciales de bases de datos.
7. **Seguridad por defecto.** El kit corre dentro de la red de la entidad: no abre puertos de entrada a internet, sirve solo por el túnel y solo al certificado del gateway, y se ejecuta con el mínimo privilegio.
8. **Las decisiones de arquitectura van en ADRs** (`docs/adr/NNNN-titulo.md`, formato en `docs/adr/0000-template.md`). Los ADRs aceptados no se modifican: se reemplazan con uno nuevo.

## Cómo trabajar

- Commits pequeños, en inglés, con Conventional Commits.
- Usa como referencia `../data-exchange/infra/mock-node/mock-node.mjs`, el simulador del portal: enrolamiento con `openssl` y `wg`, túnel, servidor mTLS y heartbeat. Es utilería de pruebas, no una versión del kit, pero muestra el contrato funcionando de punta a punta.
- Para probar de punta a punta hace falta el stack del portal (`pnpm infra:up` en `../data-exchange`), que trae el hub WireGuard, step-ca y APISIX. El perfil `nodes` levanta el nodo simulado.
