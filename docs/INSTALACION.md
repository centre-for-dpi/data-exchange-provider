# Guía de instalación del nodo (modo canal)

Para el **equipo de infraestructura** de una entidad proveedora que ya tiene sus propias APIs y quiere publicarlas en la red de intercambio de datos.

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
| **canal** | La entidad ya tiene APIs propias. El nodo solo da el canal seguro. Es el de esta guía |
| **completo** | Además, APIs instantáneas sobre vistas de bases de datos. Llega en una versión posterior del kit |

## 2. Antes de empezar

**Servidor**

- Ubuntu Server **22.04 o 24.04 LTS**, físico o virtual, dedicado al nodo. Si la entidad solo tiene Windows Server, una máquina virtual con Ubuntu.
- 1 vCPU, 1 GB de RAM y 5 GB de disco.
- Acceso como `root` o con `sudo`.
- La hora sincronizada (NTP). Con el reloj desfasado los certificados fallan.

**Red (solo salida)**

| Destino | Puerto | Para qué |
| :-- | :-- | :-- |
| El hub de la red | **UDP 51820** | El túnel. Es indispensable: no pasa por un proxy HTTP |
| El portal y el gateway | **TCP 443** | Enrolamiento, heartbeat y renovación. Puede pasar por el proxy corporativo |
| La API interna de la entidad | El de la API | Lo que el nodo publica |

No se abre ningún puerto de entrada. El operador de la red indica los nombres del portal y del gateway.

**En el portal**

- El **administrador de la entidad** (`entity_admin`) entra a **Mi entidad → Nodos del kit**, registra el nodo y copia el **token de enrolamiento**. El token se muestra una sola vez, sirve para un solo nodo y vence en **72 horas**.
- El **publicador** (`api_publisher`) tiene a mano el **contrato OpenAPI 3** de la API que va a publicar.

## 3. Instalar

El operador entrega el paquete `rid-node-kit-<versión>.tar.gz`. Trae el instalador, las imágenes del nodo (no hace falta salida a internet para descargarlas) y esta guía.

```bash
tar -xzf rid-node-kit-<versión>.tar.gz
cd rid-node-kit-<versión>
sudo ./install.sh
```

El instalador:

1. **Revisa el servidor:** la versión de Ubuntu, la arquitectura y la hora.
2. **Instala Docker Engine** desde el repositorio oficial de Docker, si falta. Antes pide confirmación.
3. **Pregunta el modo.** Responde `canal`.
4. **Crea `/opt/rid-node`** con permisos estrictos. Los secretos quedan en `/opt/rid-node/secrets`, y solo el nodo puede leerlos.
5. **Pide la URL del portal y el token,** que no se muestra al pegarlo.
6. **Carga la imagen, arranca el nodo y espera** a que quede **en línea**.

Al terminar, en el portal (**Mi entidad → Nodos del kit**) el nodo aparece **En línea**.

Si la entidad usa un **proxy corporativo** para salir a internet, agrega `HTTPS_PROXY=http://proxy:puerto` a `/opt/rid-node/.env` y ejecuta `sudo ./install.sh start`. El túnel (UDP 51820) no usa el proxy.

Volver a ejecutar `sudo ./install.sh` es seguro: no cambia la identidad del nodo ni sus secretos.

## 4. Agregar la API propia

```bash
sudo ./install.sh servicio
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
- **Credencial:** su valor no se muestra al escribirlo y se guarda en `/opt/rid-node/secrets`.
- **OpenAPI:** el nodo lo usa para registrar en sus logs el nombre de la operación en lugar de la ruta real. Además, la huella (SHA-256) del archivo viaja en cada heartbeat, para que el portal sepa qué contrato sirve el nodo.

El instalador valida todo antes de aplicarlo. Si algo está mal, como una ruta repetida o un secreto ilegible, no cambia nada y dice qué corregir. Si todo está bien, reinicia el nodo y muestra los datos para el portal.

## 5. Publicar la versión en el portal

El publicador, en el portal:

1. Crea la API, o abre una existente, y crea una **nueva versión**.
2. Sube el **mismo OpenAPI** que se dio en el paso anterior.
3. En **¿Dónde vive el servicio?** elige **Nodo del kit de APIs instantáneas**. El modo canal usa la misma opción.
4. En **Nodo del kit** elige el nodo instalado. En **Ruta del servicio en el nodo** escribe la ruta del servicio, por ejemplo `/certificados`.
5. Completa la clasificación, los datos expuestos, la base legal y las finalidades, y **envía a revisión**.

El portal prueba la conectividad a través del gateway y del nodo. Si la validación falla justo después de instalar el nodo, espera un minuto y pulsa **Repetir validaciones**: la red tarda unos segundos en conocer un nodo nuevo.

## 6. Verificar y operar

| Comando | Para qué |
| :-- | :-- |
| `sudo ./install.sh status` | Estado: portal, túnel, certificado, salud de cada servicio y avisos |
| `sudo ./install.sh doctor` | Revisa la configuración, los secretos, el DNS, la conexión con el portal, el reloj, las APIs internas y el túnel |
| `sudo ./install.sh logs` | Los últimos logs del nodo (solo metadatos). Con `-f` los sigue en vivo |
| `sudo ./install.sh stop` / `start` | Detiene o arranca el nodo, sin perder nada |

Si el nodo deja de enviar señales por 5 minutos, el portal lo marca **Fuera de línea** y avisa a los administradores.

## 7. Respaldo y actualización

**Respaldo**

```bash
sudo ./install.sh backup
```

El respaldo queda en `/opt/rid-node/backups/` e incluye las **claves del nodo**: guárdalo como un secreto. Sin él, si el servidor se pierde, hay que volver a enrolar el nodo y asociar de nuevo las versiones en el portal.

**Actualización**

Con el paquete de la versión nueva, desde su carpeta:

```bash
sudo ./install.sh update
```

Primero hace un respaldo, luego cambia a la versión nueva y espera a que el nodo quede en línea.

**Volver atrás**

Si la versión nueva no queda en línea, el instalador lo dice. Para volver:

```bash
sudo ./install.sh update <versión anterior>
```

Si el portal exige una versión mínima del kit, `status` lo muestra como aviso.

## 8. Re-enrolar

Hace falta en tres casos:

- el estado del nodo se perdió;
- su certificado venció;
- el nodo fue dado de baja en el portal.

Primero pide un token nuevo en **Mi entidad → Nodos del kit** y luego:

```bash
sudo ./install.sh reenrolar
```

El nodo nuevo tiene otra identidad: en el portal hay que elegirlo en las versiones que usaban el anterior.

## 9. Problemas comunes

| Síntoma | Causa probable | Qué hacer |
| :-- | :-- | :-- |
| `doctor`: "el túnel no tiene handshake reciente" | Salida UDP 51820 bloqueada | Permitir la salida UDP 51820 hacia el hub |
| `doctor`: "reloj desfasado" | Sin NTP | `timedatectl set-ntp true` |
| Logs: "el portal rechazó el token" | Token vencido, ya usado o mal copiado | Pedir uno nuevo y `sudo ./install.sh reenrolar` |
| Logs: "el token … existe pero el usuario del contenedor (uid 65532) no puede leerlo" | Permisos cambiados a mano | `sudo chown 65532:65532 /opt/rid-node/secrets/enrollment_token && sudo chmod 400 /opt/rid-node/secrets/enrollment_token` |
| `doctor`: "no hay conexión TCP con el portal" | Salida 443 bloqueada, o hace falta el proxy corporativo | Abrir la salida, o configurar `HTTPS_PROXY` (sección 3) |
| `status`: un servicio con ✘ | La API interna no responde | Revisar la API y su URL interna |
| Validación del portal falla con 502 justo después de instalar | La red aún no conoce el nodo | Esperar un minuto y **Repetir validaciones** |
| Llamadas con 504 | La API interna tarda más de 30 s | Revisar la API interna |

Para reportar un problema al operador de la red, adjunta la salida de `sudo ./install.sh status` y `sudo ./install.sh doctor`. No contienen secretos ni datos de personas.
