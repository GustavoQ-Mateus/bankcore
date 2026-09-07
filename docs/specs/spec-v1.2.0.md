# BankCore — Especificação Técnica

| Campo | Valor |
|---|---|
| **Versão** | 1.2.0 |
| **Status** | Draft |
| **Data** | 2026-09-07 |
| **Autor** | Gustavo Queiroz Mateus |
| **Domínio** | Núcleo bancário + fronteira de liquidação (endurecimento de bootstrap/autenticação) |
| **Base** | Estende [spec-v1.1.0](./spec-v1.1.0.md). MINOR: adiciona escopo compatível e um gate de segurança com padrão seguro. |

> Versionamento SemVer: MAJOR muda contrato/arquitetura incompatível, MINOR adiciona escopo compatível, PATCH corrige detalhe. Cada versão vive em `docs/specs/spec-vX.Y.Z.md`. Decisões de arquitetura viram ADRs em `docs/adr/`. PRD de origem em `docs/PRD_BankCore_Go.md`.

---

## 1. Contexto e motivação da 1.2.0
A 1.1.0 fechou a fronteira de liquidação com o Liquida e foi confirmada pelos dois times. O **E2E (Passo 12) rodou verde** com os dois stacks na `bankcore-net` — CA10–CA13 aprovados (`relatorio-para-bankcore-v2.0.0.md`, §1) — e o **Liquida está tagueado `v2.0.0`**. As duas correções do E2E foram do lado do Liquida (colisão de DNS no compose e serialização HTTP); o hardening do body chunked no `/settle`, também levantado lá, já estava fechado no BankCore (commit `73c7434`). Contrato 100% fechado, sem pendências entre os projetos.

Ficou **um único item aberto, de segurança e não de contrato**: o `POST /auth/register` é uma rota **pública** que aceita `role` no corpo e promove qualquer requisição a `ADMIN` (`internal/auth/auth.go:53` só rebaixa a role para `CUSTOMER` quando ela não é `ADMIN`/`CUSTOMER`; `ADMIN` passa direto). É uma escalada de privilégio trivial.

Nas reconciliações BankCore↔Liquida (handoff §0, relatório §3) decidiu-se **fechar isso na 1.2.0, depois do E2E**, com **gate por variável de ambiente + seed do 1º admin** — mantendo o `register` aberto apenas no ambiente de E2E enquanto a 1.2.0 não sobe. Esta spec entrega esse fechamento.

A reconciliação automática de pendências órfãs, antes candidata à 1.2.0, saiu de escopo: o Liquida garante que todo `PENDING` lido vira `SETTLED` ou vai para a DLQ (decisão de coordenação 4.4). O BankCore não precisa de timeout/reconciliação automática.

## 2. Objetivo
Tornar o BankCore **seguro por padrão** no bootstrap: nenhuma rota pública deve conceder `ADMIN`. Prover um caminho reprodutível e auditável para criar o primeiro administrador (seed por env), sem reintroduzir a escalada e sem quebrar o cadastro público de clientes nem os testes da 1.1.0.

## 3. Escopo (incremento 1.2.0)
1. **Gate do `register` público** (`ALLOW_PUBLIC_ADMIN_REGISTER`, default `false`): com o gate fechado, `POST /auth/register` **recusa** pedidos de `role` privilegiada; só cria `CUSTOMER`. Com o gate aberto (ambiente de E2E/dev), o comportamento da 1.1.0 é preservado.
2. **Seed idempotente do 1º admin** (`ADMIN_EMAIL` + `ADMIN_PASSWORD`): no boot, se ambas as variáveis estão definidas, o BankCore garante a existência de um `ADMIN` com esse e-mail. Idempotente: se o admin já existe, não recria nem altera.
3. **Ajuste de contrato do erro** no `register`: pedido de role privilegiada com gate fechado → **`403 error.code=ADMIN_REGISTER_DISABLED`** (código distinto, roteável), não um `CUSTOMER` criado silenciosamente.

## 4. Stack
Igual à 1.1.0. Sem novas dependências. O seed reutiliza `auth.Service.Register`/bcrypt e o `pgxpool` já existentes.

## 5. Modelo de dados (delta sobre a 1.1.0)
Nenhuma mudança de schema. `customers` já carrega `role`; o seed apenas insere uma linha `ADMIN` quando ausente. Sem migration nova.

## 6. Configuração (delta)
| Variável | Default | Efeito |
|---|---|---|
| `ALLOW_PUBLIC_ADMIN_REGISTER` | `false` | `false`: `register` público só cria `CUSTOMER`; pedido de `role` privilegiada → `403`. `true`: comportamento 1.1.0 (aceita `role` no corpo). |
| `ADMIN_EMAIL` | *(vazio)* | E-mail do admin semeado no boot. Só age se `ADMIN_PASSWORD` também definido. |
| `ADMIN_PASSWORD` | *(vazio)* | Senha do admin semeado. Nunca logada. |

- Padrão **seguro**: sem nenhuma variável nova, o `register` já fecha para `ADMIN` — a segurança não depende de o operador lembrar de setar algo.
- O compose de **E2E** sobe com `ALLOW_PUBLIC_ADMIN_REGISTER=true` (mantém o fluxo de 3 comandos do Liquida) **ou** com `ADMIN_EMAIL`/`ADMIN_PASSWORD` semeando o admin — os dois caminhos ficam documentados; recomenda-se o seed.

## 7. Endpoints (delta)
- `POST /auth/register` — **rota pública**. Com `ALLOW_PUBLIC_ADMIN_REGISTER=false` (default): qualquer `role` no corpo diferente de `CUSTOMER` (incluindo `ADMIN` e `SETTLEMENT`) é **recusada** com `403 error.code=ADMIN_REGISTER_DISABLED`; ausência de `role` ou `role=CUSTOMER` cria `CUSTOMER` normalmente. Com o gate `true`: comportamento da 1.1.0.
- `POST /admin/service-clients` — inalterado. Continua sendo o caminho legítimo para provisionar a credencial `SETTLEMENT` do Liquida (exige Bearer `ADMIN`).
- Nenhum outro endpoint muda. O contrato de fronteira da 1.1.0 permanece congelado.

## 7.1 Contrato de erro (delta)
Novo código distinto, no mesmo shape `{ "error": { "code", "message" } }`:
- **`ADMIN_REGISTER_DISABLED`** (`403`) — pedido de role privilegiada no `register` público com o gate fechado. String final, congelada com esta versão.

## 8. Requisitos funcionais (incremento)
- **RF11** Com `ALLOW_PUBLIC_ADMIN_REGISTER=false`, `POST /auth/register` nunca cria uma conta com role diferente de `CUSTOMER`.
- **RF12** Com `ALLOW_PUBLIC_ADMIN_REGISTER=false`, `POST /auth/register` com `role` privilegiada no corpo retorna `403 ADMIN_REGISTER_DISABLED` e **não** persiste nada.
- **RF13** No boot, se `ADMIN_EMAIL` e `ADMIN_PASSWORD` estão definidos e não existe conta com aquele e-mail, o BankCore cria um `ADMIN`. Se já existe, é no-op.
- **RF14** Com `ALLOW_PUBLIC_ADMIN_REGISTER=true`, o comportamento é idêntico ao da 1.1.0 (aceita `role` no corpo).

## 9. Requisitos não funcionais (incremento)
- **RNF11 Seguro por padrão**: a ausência de configuração resulta no comportamento mais restritivo (gate fechado). Nenhuma ação do operador é necessária para não vazar `ADMIN`.
- **RNF12 Idempotência do seed**: reiniciar o processo com as mesmas `ADMIN_EMAIL`/`ADMIN_PASSWORD` não cria admins duplicados nem falha o boot.
- **RNF13 Não vazamento de segredo**: `ADMIN_PASSWORD` nunca aparece em log, erro ou resposta.
- **RNF14 Compatibilidade retroativa**: a 1.2.0 não altera o contrato de fronteira; o único comportamento observável que muda é o `register` público quando o gate está no default.

## 10. Critérios de aceitação (incremento)
- **CA14** Com o gate `false`, `POST /auth/register {role:"ADMIN"}` retorna `403 ADMIN_REGISTER_DISABLED` e nenhuma linha é criada.
- **CA15** Com o gate `false`, `POST /auth/register` sem `role` (ou `role:"CUSTOMER"`) cria `CUSTOMER` e faz login normalmente.
- **CA16** Com o gate `true`, `POST /auth/register {role:"ADMIN"}` cria `ADMIN` (comportamento 1.1.0 preservado).
- **CA17** Com `ADMIN_EMAIL`/`ADMIN_PASSWORD` setados e banco limpo, o boot cria um `ADMIN`; um segundo boot com as mesmas variáveis não cria outro e não falha.
- **CA18** Os testes da 1.0.0/1.1.0 continuam verdes (o gate default não afeta os cenários que já usam `CUSTOMER`; cenários que precisam de `ADMIN` passam a semear via env ou usam o gate aberto no ambiente de teste).

## 11. Plano de implementação (fases da 1.2.0)
1. **Config**: adicionar `ALLOW_PUBLIC_ADMIN_REGISTER` (bool, default `false`), `ADMIN_EMAIL`, `ADMIN_PASSWORD` a `internal/platform/config/config.go`.
2. **Gate no register**: `auth.Service.Register` (ou o handler) passa a receber a política. Com gate fechado, `role` privilegiada → `httpx.ErrForbidden("ADMIN_REGISTER_DISABLED")`; sem `role` → `CUSTOMER`. O caminho de seed **não** passa pelo gate (é bootstrap interno, não a rota pública).
3. **Seed**: função de bootstrap em `cmd/api/main.go` (ou `internal/auth`) que, com `ADMIN_EMAIL`/`ADMIN_PASSWORD` presentes, cria o `ADMIN` de forma idempotente (checar existência por e-mail; tratar `unique_violation` como no-op).
4. **Erro**: registrar `ADMIN_REGISTER_DISABLED` como código distinto em `internal/platform/httpx`.
5. **Testes testcontainers** para CA14–CA18, incluindo o boot com seed (idempotência) e a recusa do register com gate fechado.
6. **Swagger** regenerado (novo `403` no `register`) e README atualizado com as três variáveis e a recomendação de seed.
7. **Compose de E2E**: documentar/ajustar para usar seed por env em vez de manter o `register` aberto.

## 12. Fora de escopo (1.2.0)
- Reconciliação automática/timeout de pendências órfãs — o Liquida garante o fechamento (decisão 4.4); não é necessário.
- Callback/push do BankCore → Liquida (segue pull).
- mTLS/assinatura de payload entre serviços — hardening de transporte para versão futura (decisão 4.5).
- Rotação/revogação self-service de `service_clients` além do que a 1.1.0 já permite por linha.

## 13. Referências
- ADR 0004 — Fronteira de liquidação com o Liquida (inalterada).
- ADR 0001/0002/0003 — válidas e inalteradas.
- `handoff-liquida-v1.1.0.md` §0 e §4; `relatorio-do-liquida.md` §3 — origem da decisão de fechar o `register` na 1.2.0.

---

## Changelog
- **1.2.0 (2026-09-07)** — Endurecimento de bootstrap/autenticação: fecha a escalada de privilégio do `POST /auth/register` público. Gate `ALLOW_PUBLIC_ADMIN_REGISTER` (default `false`, seguro por padrão) recusa `role` privilegiada com `403 ADMIN_REGISTER_DISABLED`; seed idempotente do 1º admin via `ADMIN_EMAIL`/`ADMIN_PASSWORD`. Sem mudança de schema nem de contrato de fronteira. Reconciliação de órfãos declarada fora de escopo (o Liquida garante o fechamento).
