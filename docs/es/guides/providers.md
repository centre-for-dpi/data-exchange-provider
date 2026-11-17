# Guía para entidades proveedoras

> English version (official): [docs/guides/providers.md](../../guides/providers.md)

Versión 2.0 · noviembre 2026 · para la jornada de incorporación (bootcamp) con las entidades

Esta guía explica qué tiene que **designar, disponer y hacer** una entidad para publicar sus datos en la red de intercambio de datos DEN (Data Exchange Network) como **proveedora**. Lo que hace una entidad **consumidora** está en la guía de entidades del portal (`docs/es/guides/entities.md` del repositorio del portal). La plataforma central (portal y gateway) la opera el operador nacional: la entidad no instala nada de eso. Lo único que instala la proveedora es el **nodo**.

El paso a paso de la instalación del nodo está en la [guía de instalación del nodo](../INSTALL.md), y la preparación de la base y la configuración de las APIs instantáneas, en [APIs instantáneas: uso y configuración](../INSTANT-APIS.md).

## 1. Qué significa ser proveedora

- **Publica una API sobre sus datos.** Decide, por medio de su **responsable de datos**, quién accede, para qué finalidad, a qué operaciones y por cuánto tiempo.
- **Sabe en cada consulta quién la hizo y para qué.** El gateway le pasa la entidad, la aplicación, la autorización y la finalidad de cada llamada.
- **Los datos se quedan en la entidad.** Nunca se copian a la plataforma: cada consulta llega a la entidad y la respuesta pasa por el gateway sin guardarse.
- **El ciudadano ve qué entidad consultó sus datos y para qué.**

## 2. Personas que la entidad debe designar

| Rol en el portal | Quién suele ser | Qué hace | Segundo factor |
| :-- | :-- | :-- | :-- |
| `entity_admin` | Jefe de TI o de la oficina de datos | Pide el alta de la entidad, invita a su gente, asigna roles y registra el nodo | — |
| `data_steward` (responsable de datos) | Quien responde por los datos (designación formal) | Aprueba, rechaza, suspende y revoca accesos a las APIs de la entidad | **Obligatorio** |
| `api_publisher` | Responsable técnico | Registra las APIs y las envía a revisión | — |
| Equipo de infraestructura | Administrador de servidores y de red | Instala y opera el nodo. No necesita cuenta en el portal | — |

Cada persona con cuenta necesita un **correo institucional propio**: la cuenta se verifica por correo y las invitaciones llegan a esa dirección. El segundo factor se configura con una app autenticadora (FreeOTP, Aegis, Google Authenticator u otra TOTP) en el teléfono de la persona.

**Documento:** el acto de designación del responsable de datos y del responsable técnico, en PDF. Se adjunta en la solicitud de alta.

Si la entidad también va a **consumir**, necesita además un `app_developer` (ver la guía de entidades del portal).

## 3. Cómo se conecta el servicio: el nodo

Toda entidad proveedora se conecta por medio del **nodo**: un servicio pequeño que se instala **dentro de la red de la entidad**.

- **Sale, no entra.** El nodo abre un túnel cifrado (WireGuard) **hacia afuera**, hacia el hub de la red. La entidad **no abre puertos de entrada** en su firewall.
- **Solo atiende al gateway.** Acepta únicamente el certificado del gateway emitido para la entidad. Cualquier otro cliente se rechaza, aunque tenga un certificado de la misma red.
- **Entrega la consulta al servicio de la entidad,** que sigue siendo interno. Si ese servicio pide una credencial, el nodo la agrega, y la credencial nunca sale de la entidad.
- **No guarda contenido.** Sus logs solo tienen metadatos: qué operación, cuándo, con qué resultado.
- **Se renueva solo** y el portal ve en todo momento si está en línea.

Detrás del nodo puede haber dos cosas, y el flujo en el portal es el mismo:

| Modo del nodo | Cuándo | Qué hay detrás |
| :-- | :-- | :-- |
| **canal** | La entidad ya tiene una API REST, propia o hecha con cualquier herramienta | La API de la entidad, en su URL interna |
| **completo** | La entidad no tiene API, o tiene las dos cosas | Las **APIs instantáneas** del nodo: consultas por clave sobre vistas de su base de datos (SQL Server, PostgreSQL, MySQL u Oracle), con un usuario de solo lectura. También sirve APIs propias |

Con una API propia, la entidad responde por su API y por su contrato OpenAPI. Con una API instantánea, el nodo genera la API y el OpenAPI a partir de las vistas que la entidad elige: la entidad decide qué columnas salen, creando la vista. El modo se elige al instalar y se puede cambiar después sin perder la identidad del nodo.

**Otras conexiones**

- **X-Road.** Si la entidad ya publica servicios en X-Road, el gateway llega por el Security Server del operador. La entidad necesita su miembro X-Road, que registra el operador. No lleva nodo.
- **API expuesta directamente,** sin nodo. Es una excepción que se acuerda con el operador: la API debe tener HTTPS con un certificado público válido, permitir la entrada desde la IP del gateway y, de preferencia, exigir mTLS con el certificado del gateway emitido para la entidad (`CN=den-gateway.<slug>`). El nodo evita todo esto, por eso es la opción por defecto.

## 4. Lo que la entidad debe disponer

### 4.1 Servidor para el nodo

| Qué | Detalle |
| :-- | :-- |
| Sistema | Ubuntu Server **22.04 o 24.04 LTS**, físico o virtual, dedicado al nodo, en **amd64 o arm64**. Si la entidad solo tiene Windows Server, una máquina virtual con Ubuntu |
| Docker Engine | Si no está, el instalador lo instala desde el repositorio oficial de Docker (necesita salida a `download.docker.com`). El Docker instalado como *snap* no sirve |
| Tamaño | 1 vCPU, 1 GB de RAM y 5 GB de disco |
| Acceso | `root` o `sudo` para el equipo de infraestructura |
| Hora | Sincronizada (NTP). Con el reloj desfasado los certificados fallan |
| Alcance | Debe llegar a la API interna de la entidad (modo canal) o a su base de datos (modo completo) |

### 4.2 Red: solo salida

| Destino | Puerto | Para qué |
| :-- | :-- | :-- |
| El hub de la red (nombre del gateway) | **UDP 51820** | El túnel. Es indispensable y no pasa por un proxy HTTP |
| El portal y el gateway | **TCP 443** | Enrolamiento, heartbeat y renovación. Puede pasar por el proxy corporativo, si acepta `CONNECT` sin usuario ni contraseña |
| La API interna de la entidad | El de la API | Lo que el nodo publica (API propia) |
| La base de datos de la entidad | El de la base (1433, 5432, 3306, 1521) | Las APIs instantáneas (modo completo) |

No se abre ningún puerto de entrada. El operador indica los nombres del portal y del gateway.

### 4.3 El servicio que se publica

- **Un contrato OpenAPI 3** que pase las reglas de la red. El portal lo revisa al registrar la versión:
  - `operationId` en cada operación, sin repetir;
  - al menos una respuesta de éxito por operación;
  - solo referencias `$ref` locales (`#/...`);
  - **ningún identificador en el query string:** los números de documento van en la ruta (`/personas/{documento}`) o en el cuerpo de un POST, nunca en `?documento=...`;
  - en APIs con datos personales o sensibles, cada operación declara dónde está el titular de los datos (`x-den-data-subject`) o que no tiene (`none`).
- **Con el modo canal:** la URL interna de la API y, si la pide, su credencial (una cabecera, usuario y contraseña, o un certificado de cliente).
- **Con las APIs instantáneas:** una **vista** por cada consulta, con solo las columnas que se van a compartir, y un **usuario de solo lectura** con `SELECT` sobre esas vistas y nada más. El OpenAPI lo genera el nodo. Los comandos de cada motor están en [APIs instantáneas: uso y configuración](../INSTANT-APIS.md#2-preparar-la-base-de-datos-dba).
- **Un sandbox** (recomendado): el mismo servicio con **datos sintéticos**. Con él el portal prueba el contrato al enviar a revisión, y los consumidores integran sin tocar datos reales. Se publica por el mismo nodo, como un servicio más.

### 4.4 Lista de preparación

El portal tiene esta lista por entidad (Mi entidad → Preparación del bootcamp), y el operador ve el avance de todas:

- [ ] **Servidor para el nodo** disponible, con los requisitos de 4.1.
- [ ] **Usuario de solo lectura** en la base de datos, con la vista a exponer. Solo hace falta con las APIs instantáneas; con una API propia se marca cuando la API interna y su OpenAPI están listos.
- [ ] **Salida 443 y UDP 51820** hacia el gateway y su hub.
- [ ] **Responsables designados:** el portal lo marca solo cuando hay un `api_publisher` y un `data_steward` activos.
- [ ] **Caso de uso** definido: qué dato, para qué finalidad, con qué base legal y entre qué entidades.

## 5. Paso a paso de la jornada

Antes de empezar, el operador da a las entidades los dos nombres de la plataforma, el del **portal** (`https://<portal>`) y el del **gateway** (`<gateway>`), y el paquete del nodo con su huella SHA-256.

### 5.1 Verificar la red desde el servidor del nodo

Con el script `entity-check.sh`, que el operador entrega en la jornada:

```bash
chmod +x entity-check.sh
./entity-check.sh check <portal> <gateway>
```

Comprueba el DNS, la salida HTTPS al portal y al gateway, y que el certificado del gateway verifica contra la raíz de la red. Solo necesita `bash`, `curl` y `openssl`.

El script no puede probar el túnel (UDP 51820) sin un nodo. Eso lo comprueba el nodo mismo después de instalarlo, con `sudo /opt/den-node/install.sh doctor`.

### 5.2 Alta de la entidad y de su gente

1. El futuro `entity_admin` entra a `https://<portal>`, crea su cuenta y confirma el correo.
2. Pide el alta de la entidad: datos, responsables designados y documento de designación. El operador la revisa y la aprueba. Si la entidad ya está incorporada por un acto formal, el operador la da de alta directamente.
3. El `entity_admin` invita a cada persona a su correo y le asigna roles. Cada invitado crea su cuenta con **ese mismo correo** y acepta la invitación.
4. El `data_steward` configura su segundo factor la primera vez que el portal lo pide.

### 5.3 Instalar el nodo

1. El `entity_admin` va a **Mi entidad → Nodos del kit**, registra el nodo y copia el **token de enrolamiento**. Se muestra una sola vez, sirve para un solo nodo y vence en **72 horas**. Se lo pasa al equipo de infraestructura por un canal seguro.
2. El equipo de infraestructura verifica el paquete y lo instala:
   ```bash
   sha256sum den-node-kit-<versión>.tar.gz   # debe coincidir con la huella publicada por el operador
   tar -xzf den-node-kit-<versión>.tar.gz
   cd den-node-kit-<versión>
   sudo ./install.sh                          # pide el modo (canal o completo), la URL del portal y el token
   ```
3. Agrega el servicio (y su sandbox, si lo tiene):
   ```bash
   sudo /opt/den-node/install.sh servicio
   ```
   - **API propia** (tipo `proxy`): pide el nombre, la **ruta en el nodo** (por ejemplo `/certificados`), la URL interna, la credencial y el OpenAPI.
   - **API instantánea** (tipo `instant`, modo completo): pide el nombre, la ruta en el nodo, la conexión a la base con el usuario de solo lectura, la vista y la columna clave. Muestra las vistas que el usuario puede leer y genera el OpenAPI en `/opt/den-node/config/services/<nombre>.openapi.json`.
4. En el portal, el nodo aparece **En línea**.

El detalle de cada pregunta, el proxy corporativo, el respaldo y la actualización están en la [guía de instalación del nodo](../INSTALL.md).

### 5.4 Publicar una API

Estados de una API: **borrador → en revisión → aprobada por el operador → aprobada por el regulador** (solo si es sensible) **→ publicada**.

1. El `api_publisher` crea la API y su versión:
   - sube el OpenAPI del servicio: el mismo que se dio al nodo para una API propia, o el que generó el nodo para una API instantánea;
   - en **¿Dónde vive el servicio?** elige **Nodo del kit de APIs instantáneas** (el modo canal usa la misma opción), el nodo instalado y la **Ruta del servicio en el nodo**, por ejemplo `/certificados`;
   - en **Servicio de pruebas (sandbox)** elige el mismo nodo y la ruta del servicio de pruebas;
   - completa la clasificación (pública, interna, personal o sensible), los datos expuestos, la base legal y las finalidades permitidas.
2. **Envía a revisión.** El portal prueba la conectividad a través del gateway y del nodo, y el contrato contra el sandbox. Si la validación falla justo después de instalar el nodo, espera un minuto y pulsa **Repetir validaciones**: la red tarda unos segundos en conocer un nodo nuevo. Si falla por otra razón, el publicador corrige su servicio y repite.
3. El operador revisa y aprueba. Si es **sensible**, también el regulador. La API queda **publicada**, y la ruta del gateway existe en segundos, cerrada hasta que haya accesos aprobados.
4. El `data_steward` recibe las solicitudes de acceso de los consumidores y decide cada una: finalidad, operaciones y vigencia.
5. La ficha imprimible del servicio (Catálogo → la API → Ficha del servicio) sirve para el expediente.

## 6. Lo que recibe el servicio en cada consulta

El gateway pone estas cabeceras y borra las que intente mandar el consumidor. El nodo las entrega intactas a la API de la entidad. La credencial del consumidor (`Authorization`) nunca llega a la API: se queda en el gateway.

| Cabecera | Contenido |
| :-- | :-- |
| `X-DEN-Consumer-Entity` / `X-DEN-Consumer-App` | Entidad y aplicación consumidoras |
| `X-DEN-Subscription` | La autorización con que se hizo la consulta |
| `X-DEN-Purpose` | Finalidad declarada |
| `X-DEN-Operation` | Operación del contrato (`operationId`) |
| `X-DEN-End-User-Hash` | Hash del funcionario que consulta, si la API lo exige |
| `X-DEN-Subject-Ref` | Seudónimo del titular de los datos, si la operación lo declara |
| `X-Request-Id` | Identificador de correlación, el mismo en la bitácora de la red |

Más adelante el gateway firmará cada llamada en una cabecera `X-DEN-Assertion`, para que la entidad pueda probar quién le consultó y verificar que la consulta viene del gateway.

## 7. Recomendaciones para el servicio de la entidad

La red no puede verificar lo que pasa dentro de la entidad. Estas prácticas son **recomendaciones**, y el modo completo del nodo las cumple por construcción:

- **Minimización.** Responder solo los campos que la ficha declara.
- **Sin contenido en los logs.** Que los logs de la API no guarden cuerpos de petición ni de respuesta, y que enmascaren rutas y query strings, porque pueden llevar números de identificación.
- **Solo lectura.** Que la API lea los datos con un usuario de solo lectura.
- **Solo por el nodo.** Que la API no quede expuesta fuera de la red interna: el nodo es su única puerta hacia la red.
- **Guardar la trazabilidad.** Registrar `X-Request-Id`, `X-DEN-Consumer-Entity` y `X-DEN-Purpose` junto a cada consulta permite responder ante una auditoría.

## 8. Compromisos de la entidad

- **Decidir los accesos.** El `data_steward` aprueba solo las finalidades legítimas y con base legal, y revoca un acceso cuando deja de corresponder.
- **Mantener el nodo en línea.** Si el nodo deja de enviar señales por 5 minutos, el portal lo marca **Fuera de línea** y avisa a los administradores. Cuando el portal exige una versión mínima del kit, se actualiza.
- **Respaldar el nodo.** El respaldo (`sudo /opt/den-node/install.sh backup`) lleva las claves del nodo: se copia fuera del servidor y se guarda como un secreto.
- **Avisar los cambios.** Un cambio incompatible en una API publicada exige una versión mayor nueva. La anterior se deprecia con fecha de retiro.

## 9. Operación del nodo

| Comando | Para qué |
| :-- | :-- |
| `sudo /opt/den-node/install.sh status` | Estado: portal, túnel, certificado, salud de cada servicio y avisos |
| `sudo /opt/den-node/install.sh doctor` | Revisa la configuración, los secretos, el DNS, el portal, el reloj, las APIs internas y el túnel |
| `sudo /opt/den-node/install.sh logs` | Los últimos logs del nodo (solo metadatos). Con `-f` los sigue en vivo |

Los problemas comunes y cómo resolverlos están en la [guía de instalación del nodo](../INSTALL.md#10-problemas-comunes). Para reportar un problema al operador, adjunta la salida de `status` y `doctor`: no contienen secretos ni datos de personas.

## 10. Contactos y soporte

- **Operador (AND):** altas de entidades, revisión de APIs, el paquete del nodo y los problemas de conexión con el gateway.
- **Regulador (MinTIC):** aprobaciones de datos sensibles, marco normativo.
- **Dentro de la entidad:** el `entity_admin` gestiona su gente y su nodo; el `data_steward` decide los accesos; el equipo de infraestructura opera el nodo.
