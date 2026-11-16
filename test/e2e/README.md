# Punta a punta: modo `canal` contra el portal real

`canal-e2e.mjs` prueba el entregable del modo `canal` (criterio de cierre de K1) contra el stack de desarrollo del portal (`../data-exchange`):

1. `elena.admin` registra un nodo y recibe el token.
2. El nodo arranca con la imagen `rid-node:<v>-canal` (`compose.yaml`). Corre sin capacidades, con sistema de archivos de solo lectura, sin root y sin puertos publicados.
3. El nodo se enrola, levanta el túnel con el hub real, envía heartbeats y queda `online`.
4. El nodo sirve el proveedor de prueba del portal (`mock-salud`) como si fuera la API propia de la entidad (`config/node.yaml`).
5. Se publica una versión con `upstream: node`, que pasa la revisión.
6. Un consumidor se suscribe y llama por el gateway.
7. Se comprueba el log del nodo: el gateway aparece por su nombre, la ruta como plantilla y no aparece el número de documento.
8. Se corren `status` y `doctor` dentro del contenedor.

```
# en ../data-exchange: pnpm infra:up y el API (pnpm dev)
make e2e
```

En tiempo de ejecución el script escribe el token en `.run/` y copia el OpenAPI del portal en `config/`. Los dos están en `.gitignore`.

Cada corrida deja en la base del portal un nodo `canal-e2e-*` y una API "Afiliación por el canal (e2e …)", con datos sintéticos.
