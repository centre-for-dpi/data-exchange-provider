# ADR-0003 — El kit se publica con licencia Apache-2.0

- Estado: Aceptado
- Fecha: 2026-11-17

## Contexto

El kit no tenía licencia. Sin ella, nadie puede usar, modificar ni redistribuir el código, aunque tenga acceso al repositorio.

El modelo de distribución pide una licencia clara. El operador de la red de cada país (en Colombia, la AND) debería poder hacer un fork del kit y del portal, construir el paquete desde su copia, firmarlo con su clave y entregarlo a las entidades. El portal se pensó como bien público digital (ADR-0001 del portal), y el kit debe servir en cualquier país.

## Decisión

El kit se publica con **Apache-2.0**. El titular de los derechos es el **Centre for Digital Public Infrastructure (CDPI)**, dueño del repositorio. El portal usa la misma licencia y el mismo titular.

- `LICENSE` lleva el texto completo de la licencia y `NOTICE` el titular.
- Los dos archivos van en el paquete de entrega y dentro de la imagen.
- Las dependencias conservan sus licencias, y `LICENSES.txt` y el SBOM de cada paquete las listan.

## Alternativas consideradas

- **MIT** — igual de simple, pero sin concesión de patentes. Para los equipos jurídicos de gobierno, esa concesión es la diferencia que importa.
- **MPL-2.0** — obligaría a publicar los archivos que modifique un fork. Ayudaría a que las mejoras vuelvan, pero agrega fricción en la contratación pública. Que vuelvan se logra mejor colaborando que por obligación.
- **GPL, AGPL o EUPL** — el copyleft fuerte frena a entidades y contratistas. La AGPL obligaría además a ofrecer el código a quien use el servicio por la red. Va contra la meta de adopción.

## Consecuencias

- **Lo que se gana:**
  - Cualquier operador, entidad o contratista puede usar, adaptar y redistribuir el kit sin pedir permiso.
  - Quien contribuye cede las patentes que su código use.
  - Apache-2.0 está aprobada por la OSI, como exige el estándar de bien público digital.
- **Compatibilidad:** todo lo que el kit enlaza es compatible:
  - wireguard-go, pgx y go-ora (MIT);
  - gVisor (Apache-2.0);
  - go-mssqldb (BSD-3);
  - el driver de MySQL (MPL-2.0). Su copyleft es por archivo y se cumple porque se usa sin cambios y su código está publicado.
- **Lo que se pierde:** nada obliga a un fork a devolver sus cambios.
- **Queda por hacer:**
  - Definir cómo se aceptan contribuciones externas. La recomendación es el DCO (`Signed-off-by` en cada commit), que pesa menos que un acuerdo de contribución.
  - Si un país registra el kit como software público, confirmar que su lineamiento acepta Apache-2.0.
