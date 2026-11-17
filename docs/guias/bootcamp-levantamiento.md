# Levantamiento por entidad (bootcamp)

Para el **equipo que acompaña a las entidades** en el bootcamp, y para el equipo de infraestructura y el DBA de cada entidad. Cada respuesta decide algo concreto: el modo del nodo, si la versión de la base está probada, y si el servidor y la red sirven tal como están.

Llenar una hoja por entidad. Los datos técnicos los saca la entidad con los comandos de cada sección; no hay que adivinarlos.

## 1. La entidad y sus datos

| Pregunta | Para qué sirve |
| :-- | :-- |
| ¿Qué datos va a publicar, y quién los consulta? | El caso de uso y la base legal en el portal |
| ¿Ya tiene una API para esos datos? (sí, no, en parte) | **Sí:** modo `canal`, el nodo solo da el canal seguro. **No:** modo `completo`, con APIs instantáneas sobre vistas |
| Si tiene API: ¿REST con OpenAPI? ¿Cómo se autentica (cabecera, usuario y contraseña, certificado)? | El servicio `proxy` necesita la URL interna, la credencial y el OpenAPI |
| ¿Quién administra el servidor del nodo y quién es el DBA? | Las dos personas que instalan |

## 2. El servidor del nodo

| Pregunta | Cómo saberlo | Qué se espera |
| :-- | :-- | :-- |
| Sistema operativo y versión | `lsb_release -a` | Ubuntu Server 22.04 o 24.04 LTS (ADR-0002). Si solo hay Windows Server, una máquina virtual con Ubuntu |
| Arquitectura | `uname -m` | `x86_64` o `aarch64` |
| ¿Docker instalado? ¿De dónde? | `docker version`, `snap list docker` | Docker Engine, o nada: el instalador lo instala. **No** el Docker de snap |
| Recursos | `nproc`, `free -h`, `df -h /opt` | 1 vCPU, 1 GB de RAM, 5 GB libres |
| Hora sincronizada | `timedatectl` | `System clock synchronized: yes` |

## 3. La red (solo salida)

| Pregunta | Qué se espera |
| :-- | :-- |
| ¿El servidor puede salir por **UDP 51820** hacia internet? | Indispensable: es el túnel. No pasa por un proxy HTTP. Si el firewall lo bloquea, hay que pedir la excepción hacia el hub de la red |
| ¿Sale por **TCP 443**? ¿Directo o con proxy corporativo? | Si hay proxy, su URL (`http://proxy:3128`). El instalador la pide |
| ¿Puede llegar a `download.docker.com`? | Solo si hay que instalar Docker |
| ¿El servidor del nodo alcanza la API interna o la base de datos? ¿Qué puerto? | La API o la base deben ser accesibles desde el nodo, dentro de la red de la entidad |

No se abre ningún puerto de entrada.

## 4. La base de datos (modo `completo`)

**Motor, versión y edición.** Es lo más importante del levantamiento:

| Motor | Consulta |
| :-- | :-- |
| Oracle | `SELECT product, version FROM product_component_version;` |
| SQL Server | `SELECT @@VERSION;` |
| PostgreSQL | `SELECT version();` |
| MySQL | `SELECT VERSION();` |

Versiones probadas con el kit: Oracle 11g XE, 21c XE y 23ai; SQL Server 2019; PostgreSQL 17; MySQL 8.4. Cualquier otra versión de Oracle, o una de SQL Server anterior a 2016, hay que anotarla con su número exacto: se prueba antes de instalar.

| Pregunta | Para qué sirve |
| :-- | :-- |
| ¿La conexión exige TLS? ¿Con certificado de una CA interna? | `tls: require`, `verify` (con el archivo de la CA) o `disable` |
| En Oracle: ¿nombre del servicio (`ORCLPDB1`, `XE`)? ¿Puerto 1521 o TCPS (2484)? | La conexión del nodo |
| ¿El DBA puede crear una **vista** y un **usuario de solo lectura** con `SELECT` sobre ella? | Es la condición del modo `completo`. Sin esto no se puede instalar |
| ¿Hay una réplica de lectura? | Mejor apuntar el nodo a la réplica que a la base de producción |

**Los datos candidatos**, por cada consulta que la entidad quiera publicar:

| Pregunta | Ejemplo |
| :-- | :-- |
| ¿Qué tabla o vista tiene los datos? | `salud.afiliados` |
| ¿Por qué columna se busca (la clave)? ¿De qué tipo es? | `numero_documento`, `VARCHAR2(20)` |
| ¿La clave identifica a una persona? ¿Con qué tipo de documento? | `CC`, `CE`, `TI`, o ninguno |
| ¿Una clave trae una fila o varias? ¿Cuántas como máximo? | Una; o varias (un hogar), hasta 100 |
| ¿Qué columnas se comparten? ¿Alguna es binaria, XML, JSON o geográfica? | Esos tipos no se publican: se convierten en la vista |
| ¿Las claves tienen espacios, puntos, letras al final o ceros a la izquierda? | Define el patrón de la clave |
| ¿Cuántas consultas por día se esperan? | Dimensiona la base y el nodo |

## 5. Señales de alerta

Si aparece alguna de estas, avisar al equipo del kit antes de seguir:

- **Oracle anterior a 11g**, o una versión de Oracle que no esté en la lista de probadas.
- **Solo servidores Windows**, sin la posibilidad de una máquina virtual Ubuntu.
- **UDP 51820 bloqueado** sin posibilidad de excepción.
- **El DBA no puede crear vistas ni usuarios** (por ejemplo, la base es de un proveedor externo).
- La clave que se quiere usar **no es única** y no hay otra.

## 6. Resumen para el equipo del kit

| Entidad | Modo | Sistema y arquitectura | Motor y versión | UDP 51820 | Proxy | Vista y clave | Alertas |
| :-- | :-- | :-- | :-- | :-- | :-- | :-- | :-- |
| | | | | | | | |
