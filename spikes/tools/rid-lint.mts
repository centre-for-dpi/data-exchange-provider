// Spike helper: runs the portal's real RID lint on an OpenAPI file, with the options the portal
// uses for a personal-data API in the reference country config (noIdInQuery on, data subject
// required, person document types from config/country/co.yaml).
import { readFileSync } from 'node:fs';
import { lintOpenApi } from '../../../data-exchange/apps/api/src/apis/lint/openapi-lint.ts';

const file = process.argv[2];
if (!file) {
  console.error('usage: rid-lint.mts <openapi.json|yaml>');
  process.exit(2);
}

const result = await lintOpenApi(readFileSync(file, 'utf8'), {
  noIdInQuery: true,
  dataSubject: { required: true, documentTypes: ['CC', 'CE', 'TI', 'PEP', 'PPT', 'PA'] },
});

for (const issue of result.report.issues) {
  console.log(`${issue.severity}\t${issue.code}\tline ${issue.line}\t${issue.message}`);
}
const errors = result.report.issues.filter((i) => i.severity === 'error').length;
console.log(`\nvalid=${result.report.valid} errors=${errors} operations=${result.operations.length} sha256=${result.sha256}`);
process.exit(result.report.valid ? 0 : 1);
