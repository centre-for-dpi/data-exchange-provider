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
```

## Variantes

- La variante `canal` se compila con `-tags canal`. No incluye las APIs instantáneas ni drivers de bases de datos.
- `make check-canal-deps` falla si alguno entra en su grafo de dependencias.
- Las pruebas corren en las dos variantes.

## Contrato con el portal

`internal/contract/testdata/node-contract-v1.openapi.json` es una copia del contrato del portal; su README dice de qué commit sale. Si el portal cambia el contrato:
1. vuelve a copiar el archivo;
2. corre `make test`.

Las pruebas de `contract` validan las peticiones y respuestas del kit contra esa copia.
