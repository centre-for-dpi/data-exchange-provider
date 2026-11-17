# Guía de instalación del nodo

> English version (official): [docs/INSTALL.md](../INSTALL.md)

Para el **equipo de infraestructura** de una entidad proveedora que quiere publicar datos en la red de intercambio de datos: con sus propias APIs, o con APIs instantáneas sobre vistas de sus bases de datos.

## 1. Qué es el nodo

El nodo es un servicio pequeño que se instala **dentro de la red de la entidad**. Le da a la API de la entidad un canal seguro con la red:

- **Sale, no entra.** El nodo abre un túnel cifrado (WireGuard) **hacia afuera**. No hay que abrir ningún puerto de entrada en el firewall.
- **Solo habla con el gateway de la red.** Acepta únicamente el certificado del gateway emitido para la entidad. Cualquier otro cliente se rechaza, aunque tenga un certificado de la misma red.
- **Reenvía a la API propia,** que sigue siendo interna. Si la API pide una credencial (una cabecera, usuario y contraseña, o un certificado), el nodo la agrega. Esa credencial nunca sale de la entidad.
- **No guarda contenido.** Sus logs solo tienen metadatos: qué operación, cuándo, con qué resultado. No tienen rutas reales, números de documento ni cuerpos.
- **Se renueva solo.** Su certificado se renueva antes de vencer, y el portal ve en todo momento si el nodo está en línea.

Hay dos modos:

| Modo | Para quién |
| :-- | :-- |
| **canal** | La entidad ya tiene APIs propias. El nodo solo da el canal seguro |
| **completo** | Además, **APIs instantáneas**: el nodo publica una vista de una base de datos de la entidad (SQL Server, PostgreSQL, MySQL u Oracle) como una API de consulta por clave, con su OpenAPI (sección 5) |

El modo se elige al instalar y se cambia después con `sudo /opt/den-node/install.sh modo canal|completo`, sin perder la identidad del nodo.

## 2. Antes de empezar

**Servidor**

- Ubuntu Server **22.04 o 24.04 LTS**, físico o virtual, dedicado al nodo, en **amd64 o arm64**. Si la entidad solo tiene Windows Server, una máquina virtual con Ubuntu.
- **Docker Engine.** Si no está, el instalador lo instala desde el repositorio oficial de Docker; para eso el servidor necesita salida a `download.docker.com`. Si Docker está instalado como *snap*, hay que quitarlo antes: el snap no puede montar `/opt/den-node`.
- 1 vCPU, 1 GB de RAM y 5 GB de disco.
- Acceso como `root` o con `sudo`.
- La hora sincronizada (NTP). Con el reloj desfasado los certificados fallan.

**Red (solo salida)**

| Destino | Puerto | Para qué |
| :-- | :-- | :-- |
| El hub de la red | **UDP 51820** | El túnel. Es indispensable: no pasa por un proxy HTTP |
| El portal y el gateway | **TCP 443** | Enrolamiento, heartbeat y renovación. Puede pasar por el proxy corporativo |
| La API interna de la entidad | El de la API | Lo que el nodo publica (APIs propias) |
| La base de datos de la entidad | El de la base (1433, 5432, 3306, 1521) | Las APIs instantáneas (modo completo) |

No se abre ningún puerto de entrada. El operador de la red indica los nombres del portal y del gateway.

**En el portal**

- El **administrador de la entidad** (`entity_admin`) entra a **Mi entidad → Nodos del kit**, registra el nodo y copia el **token de enrolamiento**. El token se muestra una sola vez, sirve para un solo nodo y vence en **72 horas**.
- El **publicador** (`api_publisher`) tiene a mano el **contrato OpenAPI 3** de la API que va a publicar.

## 3. Instalar

El operador entrega el paquete `den-node-kit-<versión>.tar.gz`. Trae el instalador, las imágenes del nodo (no hace falta salida a internet para descargarlas) y esta guía.

**Primero, verifica el paquete.** El operador publica su huella SHA-256 por un canal distinto al del paquete (el portal o su canal oficial con la entidad). Compárala antes de abrirlo:

```bash
sha256sum den-node-kit-<versión>.tar.gz
# debe coincidir con la huella publicada por el operador; si no coincide, no lo instales
```

Si el operador firma el paquete, entrega también `den-node-kit-<versión>.tar.gz.sig` y publica su clave pública en un archivo `allowed_signers`. La firma se verifica con OpenSSH, que ya trae Ubuntu:

```bash
ssh-keygen -Y verify -f allowed_signers -I <identidad del operador> -n den-kit \
  -s den-node-kit-<versión>.tar.gz.sig < den-node-kit-<versión>.tar.gz
# debe decir: Good "den-kit" signature for <identidad del operador>
```

Dentro del paquete, el instalador verifica además cada archivo contra `SHA256SUMS`. El paquete trae también la lista de componentes de cada imagen (SBOM, `sbom-completo.cdx.json` y `sbom-canal.cdx.json`, en formato CycloneDX) y sus licencias (`LICENSES.txt`).

```bash
tar -xzf den-node-kit-<versión>.tar.gz
cd den-node-kit-<versión>
sudo DEN_LANG=es ./install.sh
```

El instalador y el nodo hablan inglés por defecto; `DEN_LANG=es` los pone en español. La instalación recuerda el idioma: lo usan los comandos siguientes, los mensajes del nodo, los títulos de error de sus APIs y el OpenAPI que genera para los servicios instant. Un nodo instalado antes de que existieran los idiomas sigue en español. Los comandos en español (`servicio`, `restaurar`, `modo`, `reenrolar`) siguen funcionando, igual que sus equivalentes en inglés (`service`, `restore`, `mode`, `reenroll`).

El instalador:

1. **Revisa el servidor:** la versión de Ubuntu, la arquitectura y la hora.
2. **Instala Docker Engine** desde el repositorio oficial de Docker, si falta. Antes pide confirmación.
3. **Pregunta el modo.** Responde `canal`.
4. **Crea `/opt/den-node`** con permisos estrictos. Los secretos quedan en `/opt/den-node/secrets`, y solo el nodo puede leerlos.
5. **Pregunta si la salida a internet pasa por un proxy corporativo.**
6. **Pide la URL del portal y el token,** que no se muestra al pegarlo. Si el token se pega partido en dos líneas, lo une.
7. **Carga la imagen de la arquitectura del servidor, arranca el nodo y espera** a que quede **en línea**.
8. **Queda en `/opt/den-node/install.sh`.** Desde ahí se opera el nodo, aunque se borre la carpeta del paquete.

Al terminar, en el portal (**Mi entidad → Nodos del kit**) el nodo aparece **En línea**.

**Proxy corporativo:**
- debe aceptar `CONNECT` hacia el portal y el gateway **sin usuario ni contraseña**, porque las credenciales quedarían en la configuración del contenedor;
- la autenticación NTLM no está soportada;
- el túnel (UDP 51820) no usa el proxy;
- si Docker se va a instalar a través del proxy, ejecuta el instalador con `sudo -E` y la variable `https_proxy` definida.

**Si algo falla en el primer intento** (token vencido o mal copiado, URL equivocada), vuelve a ejecutar `sudo ./install.sh`. Si el nodo no llegó a enrolarse, el instalador pregunta otra vez la URL y el token. Si ya está enrolado, no cambia su identidad ni sus secretos.

## 4. Agregar la API propia

```bash
sudo /opt/den-node/install.sh servicio
```

| Pregunta | Ejemplo |
| :-- | :-- |
| Nombre del servicio | `certificados` |
| Ruta en el nodo (es la **Ruta del servicio en el nodo** del portal) | `/certificados` |
| URL interna de la API | `https://api.interna.entidad/certificados/v1` |
| Credencial que pide la API interna | `ninguna`, `cabecera`, `basic` o `certificado` |
| OpenAPI de la API | La ruta del archivo que se subirá al portal |

Detalles de cada respuesta:

- **URL interna:** si la API interna usa `http://` sin cifrar, el instalador pide aceptarlo explícitamente. Solo es razonable dentro de la red de la entidad.
- **Credencial:** su valor no se muestra al escribirlo y se guarda en `/opt/den-node/secrets`.
- **OpenAPI:** el nodo lo usa para registrar en sus logs el nombre de la operación en lugar de la ruta real. Además, la huella (SHA-256) del archivo viaja en cada heartbeat, para que el portal sepa qué contrato sirve el nodo.

El instalador valida todo antes de aplicarlo. Si algo está mal, como una ruta repetida o un secreto ilegible, no cambia nada y dice qué corregir. Si todo está bien, reinicia el nodo y muestra los datos para el portal.

Para cambiar un servicio, quítalo y vuelve a agregarlo:

```bash
sudo /opt/den-node/install.sh servicio quitar certificados
```

## 5. Agregar una API instantánea (modo completo)

Para una entidad que no tiene API propia: el nodo lee una **vista** de su base de datos y la publica como `GET {ruta}/{recurso}/{clave}`, con un OpenAPI generado que pasa el lint del portal.

**Antes, en la base de datos (el DBA de la entidad):**

1. Crea una **vista** con solo las columnas que se van a compartir, y con nombres simples (letras, números y `_`). La vista es el contrato: lo que no está en la vista no sale.
2. Crea un **usuario de solo lectura** con permiso `SELECT` sobre esas vistas y nada más. `doctor` avisa con ✘ si el usuario puede escribir.
3. Permite la conexión desde el servidor del nodo hacia la base.

Después:

```bash
sudo /opt/den-node/install.sh servicio
```

| Pregunta | Ejemplo |
| :-- | :-- |
| Tipo | `instant` |
| Nombre del servicio | `afiliacion` |
| Ruta en el nodo | `/afiliacion` |
| Motor | `sqlserver`, `postgres`, `mysql` u `oracle` |
| Servidor, puerto y base de datos (en Oracle, el nombre del servicio) | `bd.interna.entidad`, `5432`, `entidad` |
| Usuario de solo lectura y su contraseña | `den_reader` |
| TLS hacia la base | `require` (cifrado), `verify` (cifrado y certificado verificado) o `disable` |
| CA de la base (solo con `verify`; en Oracle no se pregunta) | La ruta del PEM de la CA interna, o `-` para las CA del sistema |
| Vista a exponer | `salud.v_afiliados` |
| Columna clave | `numero_documento` |
| Nombre del recurso en la URL y de la entidad en el OpenAPI | `-` para derivarlos de la vista (`v_afiliados` da `afiliados` y `Afiliados`) |
| Tipo de documento, si la clave identifica a una persona | `CC`, o `ninguno` |
| ¿Una clave puede traer varias filas? | `n` (una fila) o `s` (una lista, hasta 100 filas) |

El instalador:

1. Guarda la conexión y prueba que el usuario puede leer el catálogo. Si no puede, no deja nada escrito.
2. Muestra las vistas y tablas que ese usuario puede leer, con sus columnas.
3. Crea el recurso con todas las columnas de la vista que se pueden exponer. Las que no se pueden (binarias, por ejemplo) quedan anotadas como comentario en el archivo del servicio.
4. Valida, reinicia el nodo y genera el OpenAPI en `/opt/den-node/config/services/<nombre>.openapi.json`. Ese es el archivo que se sube al portal.

**Cómo responde la API:**

- `GET /afiliacion/afiliados/1000000001` devuelve el registro, con los nombres de columna de la vista.
- Una clave que no existe da **404**. Una clave con un formato inválido da **400**, sin llegar a la base. Cualquier parámetro en la URL también da **400**.
- Si la base no responde, el nodo contesta **503** y el portal ve el servicio con problemas. Cuando la base vuelve, el servicio se recupera solo.
- Cada consulta corre en una **transacción de solo lectura** (en PostgreSQL, MySQL y Oracle). En SQL Server la garantía es el usuario de solo lectura.

**Ajustes en el archivo del servicio** (`/opt/den-node/config/services/<nombre>.yaml`):

- Para no exponer una columna, quítala de `columns`.
- Para cambiar cómo se publica una columna, agrégale `as`. Por ejemplo, `as: boolean` para un `NUMBER(1)` o un `TINYINT` que guarda sí o no, o `as: date` para un `DATE` de Oracle que no necesita la hora.
- Para un patrón de clave propio, usa `key.pattern`. Por defecto, una clave de texto acepta letras, números y guiones, sin espacios.
- Después de editar, valida y regenera el OpenAPI:

```bash
cd /opt/den-node && sudo docker compose exec den-node /den-node validate
sudo docker compose exec den-node /den-node openapi afiliacion | sudo tee config/services/afiliacion.openapi.json >/dev/null
sudo docker compose restart den-node
```

Si el OpenAPI cambió, hay que publicar una versión nueva en el portal.

Los comandos para preparar la base en cada motor, todos los campos del archivo del servicio, los tipos, las respuestas y cómo agregar un segundo recurso están en [APIs instantáneas: uso y configuración](INSTANT-APIS.md).

## 6. Publicar la versión en el portal

El publicador, en el portal:

1. Crea la API, o abre una existente, y crea una **nueva versión**.
2. Sube el **mismo OpenAPI** que se dio en el paso anterior. Para una API instantánea, es el archivo `config/services/<nombre>.openapi.json` que generó el instalador.
3. En **¿Dónde vive el servicio?** elige **Nodo del kit de APIs instantáneas**. El modo canal usa la misma opción.
4. En **Nodo del kit** elige el nodo instalado. En **Ruta del servicio en el nodo** escribe la ruta del servicio, por ejemplo `/certificados`.
5. Completa la clasificación, los datos expuestos, la base legal y las finalidades, y **envía a revisión**.

El portal prueba la conectividad a través del gateway y del nodo. Si la validación falla justo después de instalar el nodo, espera un minuto y pulsa **Repetir validaciones**: la red tarda unos segundos en conocer un nodo nuevo.

## 7. Verificar y operar

| Comando | Para qué |
| :-- | :-- |
| `sudo /opt/den-node/install.sh status` | Estado: portal, túnel, certificado, salud de cada servicio (con sus peticiones, errores y latencia desde el arranque) y avisos |
| `sudo /opt/den-node/install.sh doctor` | Revisa la configuración, los secretos, el DNS, la conexión con el portal, el reloj, las APIs internas y el túnel |
| `sudo /opt/den-node/install.sh logs` | Los últimos logs del nodo (solo metadatos). Con `-f` los sigue en vivo |
| `sudo /opt/den-node/install.sh stop` / `start` | Detiene o arranca el nodo, sin perder nada |

Si el nodo deja de enviar señales por 5 minutos, el portal lo marca **Fuera de línea** y avisa a los administradores.

**La aserción firmada del gateway.** Cuando la red la tiene activa, el gateway firma cada llamada (quién consulta, para qué y con qué suscripción) en la cabecera `X-DEN-Assertion`. El nodo la verifica con las claves públicas del portal y anota el resultado en su log, en el campo `assertion`: `verified`, `absent` o `invalid: <motivo>`. Por defecto nunca rechaza una llamada por esto. Para exigirla, en `/opt/den-node/config/node.yaml`:

```yaml
gatewayAssertion:
  mode: require        # verify (por defecto) | require | off
  # audience: salud    # el identificador corto de la entidad; por defecto sale del certificado del gateway
```

Con `require`, una llamada sin aserción válida recibe 401. Actívalo solo cuando el operador confirme que la red ya firma las llamadas.

## 8. Respaldo y actualización

**Respaldo**

```bash
sudo /opt/den-node/install.sh backup
```

El respaldo queda en `/opt/den-node/backups/` e incluye las **claves del nodo**. **Cópialo fuera del servidor** y guárdalo como un secreto: si el servidor se pierde, el respaldo se pierde con él. Sin respaldo, hay que volver a enrolar el nodo y elegirlo de nuevo en las versiones del portal.

**Restaurar** (en el mismo servidor o en uno nuevo, con el nodo ya instalado por el paquete de la misma versión):

```bash
sudo /opt/den-node/install.sh restaurar /ruta/den-node-<fecha>.tar.gz
```

El nodo vuelve con la misma identidad, y el portal no ve ningún cambio.

**Actualización**

Con el paquete de la versión nueva, desde su carpeta:

```bash
sudo ./install.sh update
```

Primero comprueba que la versión nueva esté en el paquete. Luego hace un respaldo, cambia a la versión nueva y espera a que el nodo quede en línea. Si la versión no está, no cambia nada.

**Volver atrás**

Si la versión nueva no queda en línea, el instalador lo dice. Para volver:

```bash
sudo /opt/den-node/install.sh update <versión anterior>
```

Si la imagen anterior ya no está en el servidor, ejecútalo desde la carpeta del paquete de esa versión.

Si el portal exige una versión mínima del kit, `status` lo muestra como aviso.

## 9. Re-enrolar

Hace falta en tres casos:

- el estado del nodo se perdió;
- su certificado venció;
- el nodo fue dado de baja en el portal.

Primero pide un token nuevo en **Mi entidad → Nodos del kit** y luego:

```bash
sudo /opt/den-node/install.sh reenrolar
```

El nodo nuevo tiene otra identidad: en el portal hay que elegirlo en las versiones que usaban el anterior.

## 10. Problemas comunes

| Síntoma | Causa probable | Qué hacer |
| :-- | :-- | :-- |
| `doctor`: "el túnel no tiene handshake reciente" | Salida UDP 51820 bloqueada | Permitir la salida UDP 51820 hacia el hub |
| `doctor`: "reloj desfasado" | Sin NTP | `timedatectl set-ntp true` |
| Logs: "el portal rechazó el token" | Token vencido, ya usado o mal copiado | Pedir uno nuevo y volver a ejecutar `sudo ./install.sh` |
| Logs: "el token … existe pero el usuario del contenedor (uid 65532) no puede leerlo" | Permisos cambiados a mano | `sudo chown 65532:65532 /opt/den-node/secrets/enrollment_token && sudo chmod 400 /opt/den-node/secrets/enrollment_token` |
| `doctor`: "no hay conexión TCP con el portal" | Salida 443 bloqueada, o hace falta el proxy corporativo | Abrir la salida, o configurar `HTTPS_PROXY` (sección 3) |
| `status`: un servicio con ✘ | La API interna no responde | Revisar la API y su URL interna |
| `doctor`: "no se pudo conectar con la base de datos" | Firewall, servidor o puerto errado, o contraseña equivocada | Probar la conexión desde el servidor del nodo y revisar el usuario. `doctor` muestra el mensaje de la base |
| `doctor`: "el usuario … puede escribir en …" | El usuario de la base tiene permisos de más | Dejarle solo `SELECT` sobre las vistas expuestas |
| `doctor` o `validate`: "la declaración no compila contra la base" | La vista cambió: una columna se renombró o se borró | Ajustar el archivo del servicio, o volver a crear el recurso |
| Llamadas con 503 en una API instantánea | La base no responde | Revisar la base. El nodo se recupera solo cuando vuelve |
| Validación del portal falla con 502 justo después de instalar | La red aún no conoce el nodo | Esperar un minuto y **Repetir validaciones** |
| Llamadas con 504 | La API interna tarda más de 30 s | Revisar la API interna |

Para reportar un problema al operador de la red, adjunta la salida de `sudo /opt/den-node/install.sh status` y `sudo /opt/den-node/install.sh doctor`. No contienen secretos ni datos de personas.
