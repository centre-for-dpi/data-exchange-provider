# Desarrollo del kit

## Requisitos

- Go 1.27.
- Docker Engine para las imágenes y las pruebas de punta a punta.
- `make` en Linux. En Windows, los mismos comandos del `Makefile` se pueden correr a mano.
- Para la prueba de punta a punta: el repositorio del portal en `../data-exchange`, con su stack arriba (`pnpm infra:up`) y su API en `:4000`. También hace falta Node.js para el script.

## Estructura

| Ruta | Qué hay |
| :-- | :-- |
| `cmd/rid-node/` | CLI: `run`, `status`, `doctor` y `version` |
| `internal/config/` | `node.yaml`, validación y secretos en archivos |
| `internal/state/` | Estado persistente (escritura atómica, 0600) y `status.json` |
| `internal/contract/` | Cliente del contrato de nodos v1, probado contra una copia del OpenAPI del portal (`testdata/`) |
| `internal/tunnel/` | WireGuard en espacio de usuario. `tunneltest/` es un hub para pruebas |
| `internal/server/` | TLS que solo admite al gateway, enrutamiento por `servicePath` y access log sin contenido |
| `internal/proxy/` | Servicios `proxy`, hacia la API propia de la entidad |
| `internal/agent/` | Enrolamiento, heartbeat, salud, renovación, `/config` y estado. `fakeportal/` es un portal para pruebas |
| `internal/testpki/` | PKI en memoria para pruebas |
| `tools/licensecheck/` | Verificación de licencias de los módulos Go (regla 1) |
| `deploy/` | Dockerfile de las dos variantes |
| `test/e2e/` | Prueba de punta a punta contra el portal real |
| `spikes/` | Código desechable del ADR-0001. No forma parte del kit |

## Comandos

```
make build build-canal     # binarios en bin/
make test                  # pruebas de las dos variantes (completo y canal)
make check                 # gofmt, vet, go mod verify, pruebas, licencias, vulnerabilidades y guardia de canal
make image image-canal     # imágenes rid-node:<versión> y rid-node:<versión>-canal
make e2e                   # imagen canal + punta a punta contra el portal real
make test-engines          # APIs instantáneas contra SQL Server, PostgreSQL, MySQL y Oracle reales
```

### Motores de base de datos para las APIs instantáneas

`make test-engines` levanta `test/engines/compose.yaml` y corre las pruebas de integración. Son cuatro contenedores con datos sintéticos, en `127.0.0.1:55432` (PostgreSQL), `53306` (MySQL), `51433` (SQL Server) y `51521` (Oracle). Después pasa el lint del portal sobre el OpenAPI que generó cada motor. El lint necesita el repositorio del portal en `../data-exchange`, con sus dependencias instaladas.

Las contraseñas van en un archivo de variables fuera del repositorio. Por defecto es `~/.config/rid-node/engines.env`, y se puede cambiar con `RID_IT_ENV`. Para crearlo con contraseñas aleatorias:

```
mkdir -p ~/.config/rid-node && umask 077
r() { od -An -N12 -tx1 /dev/urandom | tr -d ' \n'; }
printf "RID_IT_ADMIN_PASSWORD='Rid-%s-Aa1'\nRID_IT_READER_PASSWORD='%s@:/?x'\nRID_IT_WRITER_PASSWORD='%sWr1'\n" "$(r)" "$(r)" "$(r)" \
  > ~/.config/rid-node/engines.env
```

La contraseña de `rid_reader` lleva `@:/?` a propósito, para probar que el DSN la escapa. `make engines-down` borra los contenedores y sus datos.

Las imágenes de los motores (incluidas SQL Server Developer y Oracle Free) son solo infraestructura de prueba: nunca se empaquetan en el kit.

## Variantes

- La variante `canal` se compila con `-tags canal`. No incluye las APIs instantáneas ni drivers de bases de datos.
- `make check-canal-deps` falla si alguno entra en su grafo de dependencias.
- Las pruebas corren en las dos variantes.

## Contrato con el portal

`internal/contract/testdata/node-contract-v1.openapi.json` es una copia del contrato del portal; su README dice de qué commit sale. Si el portal cambia el contrato:
1. vuelve a copiar el archivo;
2. corre `make test`.

Las pruebas de `contract` validan las peticiones y respuestas del kit contra esa copia.
