# ADR-0004 — English-first documentation and bilingual messages

- Status: Accepted
- Date: 2026-11-17
- Changes: the language rule (5) of `CLAUDE.md`, which said documentation and texts for the entities are in Spanish

## Context

The kit must serve any country, and its operator in each country will fork it, build it and maintain it (ADR-0003). Spanish-only documentation and messages narrow that audience to one language. The first deployment, Colombia, still needs everything in Spanish.

## Decision

1. **Documentation.** The official documentation is in English, at the main paths. A Spanish version lives under `docs/es/`, mirroring the English paths, and is updated in the same commit as the English one. ADRs, spikes, plans and `docs/BACKLOG.md` written before this decision stay in Spanish as a historical record; new ones are written in English.
2. **Messages.** What a person reads from the kit is bilingual, English by default:
   - the installer (`install.sh`);
   - the `rid-node` CLI (`doctor`, `status`, `validate`, `catalog`, `scaffold`, `openapi`) and its errors;
   - the problem+json titles the node answers;
   - the texts of the OpenAPI it generates.

   `RID_LANG=es` switches all of them to Spanish. The installer stores the language in `/opt/rid-node/.env` and passes it to the node. An installation from before this decision (a `.env` without `RID_LANG`) stays in Spanish, so its behavior and its OpenAPI do not change on update.
3. **How.** The English text is written in the code and is the key of a Spanish catalog: `internal/i18n/catalog_es.go` for Go, and `MSG_ES` in `install.sh`. Tests fail when a message has no translation, when the format verbs differ, or when a translation is no longer used. Logs stay in English only.
4. **Commands.** The installer's commands are in English (`service`, `restore`, `mode`, `reenroll`; `service resource`, `service remove`). The Spanish names keep working. So do the Spanish answers (`ninguna`, `cabecera`, `certificado`, `ninguno`, `s`). The variant names `canal` and `completo` are identifiers and do not change.

## Alternatives considered

- **Keep Spanish as the primary language, with an English translation.** Simpler for Colombia, but every new country would read a translation that might lag behind.
- **Translation keys (`msg.enroll_failed`) instead of English text as the key.** These are harder to read in the code and in the tests. With English as the key, an untranslated message still makes sense.
- **Follow `LANG`/`LC_ALL`.** Containers do not carry the host's locale, and a server's locale says little about its operator. An explicit `RID_LANG` is predictable.

## Consequences

- Every documentation change touches two files, and every new message needs its Spanish translation; the tests enforce the messages, the reviewer the documents.
- The OpenAPI of an instant service depends on the language: changing `RID_LANG` changes its SHA-256, and the portal needs a new version.
- Tests that assert a message now assert the English one, unless they set Spanish explicitly.
