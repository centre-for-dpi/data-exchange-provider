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

Cada corrida deja en el portal un nodo `prueba-kit-canal-*`, una API "Afiliación por el canal (prueba del kit …)", una aplicación y una suscripción, todos con datos sintéticos. La prueba queda como terminó, con el nodo `online`. Con `RID_E2E_CLEANUP=1` revoca la suscripción, da de baja el nodo y borra los contenedores locales.

## Contra el portal desplegado (VPS)

Con `RID_E2E_TARGET=vps` el nodo corre en esta máquina, como lo haría una entidad. Sale por internet hacia el portal, el gateway y el hub públicos (`compose.vps.yaml`, `config-vps/node.yaml`), y la API propia es el proveedor de prueba, corriendo a su lado.

Variables, tomadas del `.env` del despliegue:
- `RID_API_URL`, `KEYCLOAK_URL` (`…/auth`), `RID_GATEWAY_PUBLIC_URL`, `RID_GATEWAY_HTTP_URL` (`…/gw`) y `RID_WEB_URL`;
- los secretos `RID_PORTAL_WEB_CLIENT_SECRET`, `RID_DEMO_USER_PASSWORD` y `RID_DEMO_TOTP_SECRET`.

Los secretos no se guardan en el repositorio.
