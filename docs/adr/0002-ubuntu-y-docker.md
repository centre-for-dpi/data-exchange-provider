# ADR-0002 — El kit corre en Ubuntu con Docker

- Estado: Aceptado
- Fecha: 2026-11-16
- Precisa: ADR-0001, decisión 5 (empaquetado)

## Contexto

ADR-0001 dejó el empaquetado en dos pasos: primero un contenedor con Compose y después un servicio nativo de Windows y de systemd, por las entidades con Windows Server.

Soportar varios sistemas operativos multiplica el trabajo:
- el instalador;
- las pruebas;
- la manera de guardar el estado y los secretos;
- la forma de actualizar;
- el soporte en cada entidad.

El kit ya corre en un contenedor sin privilegios (S-2). Además, el servidor del nodo es una máquina nueva que la entidad dedica a la red: no comparte sistema con sus aplicaciones.

## Decisión

1. **Requisito para la entidad:** un servidor (físico o virtual) con **Ubuntu Server LTS** (22.04 o 24.04) y **Docker Engine** con el plugin Compose.
2. **Todo el kit se entrega en contenedores:**
   - una imagen del nodo;
   - un `compose.yaml`;
   - un instalador para Ubuntu que prepara Docker Engine si falta, configura y arranca.
3. **No hay servicio nativo** de Windows ni de systemd. Una entidad con Windows Server pone una máquina virtual con Ubuntu.
4. **Docker Engine** (Apache-2.0), **no Docker Desktop**, cuya licencia es comercial para organizaciones grandes.

## Alternativas consideradas

- **Binario nativo para Windows y Linux, además del contenedor.** Es lo que preveía ADR-0001. Duplica el instalador, las pruebas y el soporte, para un caso que una máquina virtual con Ubuntu resuelve.
- **Cualquier Linux con Docker.** Abre la matriz de soporte a distribuciones con otras versiones de Docker, firewall y systemd. Se puede ampliar después, si hace falta.
- **Podman en lugar de Docker.** Es compatible en lo esencial, pero el instalador y la guía se escriben para una sola herramienta.

## Consecuencias

- Hay una sola plataforma que probar y documentar, y el instalador puede asumir `apt` y systemd.
- Se pide más a las entidades con solo Windows Server: una máquina virtual con Ubuntu. Va en la lista de preparación del bootcamp (M4-07 del portal).
- El requisito de WireGuard en espacio de usuario de ADR-0001 se mantiene, aunque ya no lo exija Windows: el contenedor sigue sin `NET_ADMIN` ni `/dev/net/tun`, y no depende del kernel del servidor.
- La guía de entidades del portal (`docs/guias/entidades.md`) debe decir "Ubuntu Server LTS con Docker" en los requisitos del nodo.
