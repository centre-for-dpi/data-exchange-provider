# Kit proposals for the portal

> Versión en español: [docs/es/portal-proposals.md](es/portal-proposals.md)

For the **portal team** (`../data-exchange`). They come from building and testing the node against the real portal (K0–K4). The boundary is the node contract v1: these changes are decided and versioned in the portal, not in the kit. They are ordered by their impact on entities.

## 1. Deliver the entity slug to the node

**Problem.** The gateway assertion (ADR-0024) carries `aud` = the provider entity's slug. The node verifies it, but contract v1 does not tell the node its slug. Today the node infers it from `gatewayClientName` (`rid-gateway.<slug>`), a convention that is not written in the contract. If it changes, the node stops verifying, or does not start in `require` mode.

**Proposal.** Add `entitySlug` to the response of `POST /nodes/enroll` and of `GET /nodes/{id}/config`. It is a new field, so the change is backward compatible.

## 2. The network root and the gateway name in `/config`

**Problem.** `caCertificate` and `gatewayClientName` arrive only at enrollment. If the network root rotates, or the gateway client name changes, every entity has to re-enroll its node and associate its versions again in the portal.

**Proposal.** Return both in `/config` (and, if possible, the next root during a rotation). The node reloads them without re-enrolling.

## 3. Do not validate a version before the hub knows the node

**Problem.** A version submitted for review in the seconds after enrollment fails validation with 502, because the hub does not have the node's SNI route yet. It happened in spike S-2 and in every end-to-end test: the kit's test has to retry the validations.

**Proposal.** Have the validation wait until the hub state includes the node (the hub's `sync_state` is green), or retry on its own a couple of times before marking it failed.

## 4. Compare the OpenAPI hash from the heartbeat with the published one

**Problem.** In every heartbeat, the node sends the SHA-256 of the OpenAPI it serves (`openapiSha256`). For instant APIs, it is exactly the generated file. The portal stores it but does not compare it with the hash of the published version. If the entity changes a view or the service file, the published contract and what the node serves drift apart without anyone noticing.

**Proposal.** Show a drift alert on the version and in the sync console when the hashes do not match.

## 5. The assertion and an entity's own API behind the node

**Problem.** The gateway signs the URI that the node receives. The node verifies it and forwards the header, but the entity's internal API receives a different path (its upstream's base path), so it cannot verify the assertion on its own.

**Proposal.** Decide one of two options:
- the node passes the original URI in a header (for example `X-RID-Original-URI`);
- or, for services behind a node, the node's verification and its log are sufficient.

The provider guide has to say which.

## 6. Block undeclared parameters at the gateway

**Problem.** Instant APIs reject any query string with 400, but an entity's own API behind the node receives whatever the consumer sends.

**Proposal.** Have the gateway reject parameters that the OpenAPI does not declare (identifiers are already rejected with `noIdInQuery`).

## 7. `direct` as an exception

**Problem.** The kit's ADR-0001 made the node the channel for every provider. `direct` still exists for entities that publish their API on the internet.

**Proposal.** Keep `direct` as an exception approved by the operator, with mandatory mTLS and a test showing that the API rejects certificates that are not the gateway's. Adjust SPEC §3.1: `node` is the channel for any provider, not only for instant APIs.

## 8. Entity guide: the node's server

**Proposal.** State Ubuntu Server LTS with Docker as the node requirement (the kit's ADR-0002). The kit's guide (`docs/INSTALL.md`) already says so, and the portal's guide should match.
