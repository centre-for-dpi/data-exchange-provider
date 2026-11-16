// Throwaway: rewrites the OpenAPI that DAB serves (/api/openapi) into one that passes the RID
// lint and matches network policy (lookup by key in the path, no listing, no query params, no
// DAB headers, operationIds, x-rid-data-subject, Spanish texts, 404 on miss).
// Usage: node postprocess.mjs out/openapi.json out/openapi-rid.json
import { readFileSync, writeFileSync } from 'node:fs';

const [input = 'out/openapi.json', output = 'out/openapi-rid.json'] = process.argv.slice(2);
const dab = JSON.parse(readFileSync(input, 'utf8'));

// What DAB cannot know and the kit would have to ask the entity (or read from the catalog):
// whose data each lookup returns, whether the key repeats, the JSON formats of date columns,
// and the Spanish texts. In the real kit this would come from the entity's choices, not code.
const ENTITIES = {
  afiliados: {
    schema: 'Afiliado',
    operationId: 'getAfiliado',
    summary: 'Consultar la afiliación de una persona',
    description:
      'Devuelve la afiliación vigente de la persona identificada por su número de documento. ' +
      'Responde 404 si no existe.',
    keyDescription: 'Número del documento de identidad de la persona.',
    dataSubject: { in: 'path', name: 'numero_documento', documentType: 'CC' },
    multiple: false,
    formats: { fecha_afiliacion: 'date', actualizado_en: 'date-time' },
  },
  hogares: {
    schema: 'Hogar',
    operationId: 'getHogar',
    summary: 'Consultar los integrantes de un hogar',
    description:
      'Devuelve las personas que integran el hogar indicado. Responde 404 si el hogar no existe.',
    keyDescription: 'Identificador del hogar.',
    dataSubject: 'none',
    multiple: true, // hogar_id repeats in the view: a lookup returns every member
    formats: {},
  },
};

const errorSchema = {
  type: 'object',
  description: 'Error devuelto por el servicio.',
  properties: {
    error: {
      type: 'object',
      properties: {
        code: { type: 'string', description: 'Código del error.' },
        message: { type: 'string', description: 'Descripción del error.' },
        status: { type: 'integer', description: 'Código de estado HTTP.' },
      },
    },
  },
};

const paths = {};
const usedSchemas = new Set();
for (const [path, item] of Object.entries(dab.paths ?? {})) {
  // DAB by-key route: /{entityPath}/{keyColumn}/{keyColumn}. Lists (/{entityPath}) are dropped.
  const m = /^\/([^/]+)\/([^/]+)\/\{([^}]+)\}$/.exec(path);
  if (!m) continue;
  const [, entityPath, keyColumn, keyParam] = m;
  const cfg = ENTITIES[entityPath];
  if (!cfg || keyColumn !== keyParam || !item.get) continue;

  const pathParam = (item.parameters ?? []).find((p) => p.in === 'path' && p.name === keyParam);
  const itemRef = { $ref: `#/components/schemas/${cfg.schema}` };
  usedSchemas.add(cfg.schema);

  paths[`/${entityPath}/{${keyParam}}`] = {
    get: {
      operationId: cfg.operationId,
      tags: item.get.tags,
      summary: cfg.summary,
      description: cfg.description,
      'x-rid-data-subject': cfg.dataSubject,
      parameters: [
        {
          name: keyParam,
          in: 'path',
          required: true,
          description: cfg.keyDescription,
          schema: pathParam?.schema ?? { type: 'string' },
        },
      ],
      responses: {
        200: {
          description: cfg.multiple ? 'Registros encontrados.' : 'Registro encontrado.',
          content: {
            'application/json': {
              schema: cfg.multiple ? { type: 'array', items: itemRef } : itemRef,
            },
          },
        },
        400: { description: 'La clave no tiene un formato válido.', content: errorContent() },
        404: { description: 'No existe un registro con esa clave.', content: errorContent() },
      },
    },
  };
}

function errorContent() {
  return { 'application/json': { schema: { $ref: '#/components/schemas/Error' } } };
}

const schemas = { Error: errorSchema };
for (const name of usedSchemas) {
  const src = structuredClone(dab.components.schemas[name]);
  const cfg = Object.values(ENTITIES).find((e) => e.schema === name);
  for (const [prop, s] of Object.entries(src.properties ?? {})) {
    if (s.format === '') delete s.format; // DAB emits format: "" for every column
    if (cfg.formats[prop]) s.format = cfg.formats[prop];
  }
  schemas[name] = src;
}

const rid = {
  openapi: '3.0.3',
  info: {
    title: 'Afiliaciones y hogares (spike)',
    version: '0.1.0',
    description: 'API de solo lectura sobre vistas de la entidad. Generada con Data API builder y ajustada por el kit.',
  },
  tags: dab.tags,
  paths,
  components: { schemas },
};

writeFileSync(output, JSON.stringify(rid, null, 2) + '\n');
console.log(`wrote ${output}: ${Object.keys(paths).length} operations (dropped ${Object.keys(dab.paths).length - Object.keys(paths).length} DAB paths)`);
