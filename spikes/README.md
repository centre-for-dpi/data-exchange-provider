# Spikes (desechables)

Código de prueba para decidir el ADR-0001. **No es el kit**: no tiene pruebas ni manejo de errores serio, y no se reutiliza. Lo que se aprende va a `docs/spikes/`.

| Spike | Pregunta |
| :-- | :-- |
| `dab/` | ¿Cuánto hay que construir alrededor de Data API builder para que su API y su OpenAPI cumplan el contrato de la red? |
| `go-node/` | ¿Un solo binario en Go puede enrolarse, levantar WireGuard sin privilegios, validar el nombre del gateway en la conexión TLS y servir un GET por clave sobre una vista? |
| `tools/rid-lint.mts` | Corre el lint real del portal (`../data-exchange`) sobre un OpenAPI. |

Ejecutar el lint (desde la raíz de este repositorio):

```
../data-exchange/node_modules/.bin/tsx spikes/tools/rid-lint.mts <openapi.json|yaml>
```
