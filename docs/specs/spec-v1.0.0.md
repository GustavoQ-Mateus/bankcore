# BankCore — Especificação Técnica

| Campo | Valor |
|---|---|
| **Versão** | 1.0.0 |
| **Status** | Draft |
| **Data** | 2026-08-31 |
| **Autor** | Gustavo Queiroz Mateus |
| **Domínio** | Núcleo bancário (contas, transferências, ledger) |

> Versionamento SemVer: MAJOR muda contrato/arquitetura incompatível, MINOR adiciona escopo compatível, PATCH corrige detalhe. Cada versão vive em `docs/specs/spec-vX.Y.Z.md`. Decisões de arquitetura viram ADRs em `docs/adr/`. PRD de origem em `docs/PRD_BankCore_Go.md`.

---

## 1. Contexto de negócio
API de **núcleo bancário**: clientes têm contas e realizam depósitos, saques e **transferências atômicas** entre contas, com extrato e **ledger auditável**. O foco de engenharia é a mecânica correta de **consistência, concorrência e integridade monetária**, não virar um banco real com KYC/compliance. É a origem das transações que o **Liquida** (projeto companheiro, .NET) liquida.

## 2. Objetivo
Entregar em **Go** uma API que garanta que nenhuma transferência deixe saldo inconsistente, que operações concorrentes na mesma conta não corrompam o saldo, e que retries não gerem débito duplicado.

## 3. Escopo (MVP)
1. **Auth**: registro/login, JWT com roles `CUSTOMER`/`ADMIN`.
2. **Conta**: abrir conta, consultar saldo.
3. **Operações**: depósito, saque (com checagem de saldo), **transferência entre contas**.
4. **Extrato**: histórico paginado por conta.
5. **Ledger append-only**: toda operação vira um lançamento imutável.
6. **Idempotência**: header `Idempotency-Key` evita débito duplicado em retry.

## 4. Stack
Go 1.22+ · **chi** (HTTP) + `net/http` · **pgx** + PostgreSQL (opcional sqlc) · **golang-migrate** · JWT (`golang-jwt`) com middleware próprio · testes com `testing` + **testcontainers-go** + `httptest` · Docker + docker-compose · Makefile · GitHub Actions · Swagger via swaggo.

## 5. Modelo de dados (PostgreSQL)
```
customers (id uuid, name, email UNIQUE, password_hash, role, created_at)
accounts  (id uuid, owner_id uuid, number UNIQUE, balance_cents int64, status, version int, created_at)
entries   (id uuid, account_id uuid, type[CREDIT|DEBIT], amount_cents int64, balance_after_cents int64, transfer_id uuid?, created_at)  -- append-only
transfers (id uuid, from_account_id uuid, to_account_id uuid, amount_cents int64, idempotency_key text UNIQUE, status[PENDING|SETTLED|FAILED], created_at)
```
- Valores monetários em **centavos (`int64`)**, nunca `float64` (ver ADR 0002).
- `accounts.version` para **optimistic locking** (ver ADR 0001).
- `transfers.status` inclui `PENDING`/`SETTLED` para a fronteira com o Liquida (ver ADR 0003).

## 6. Endpoints
- `POST /auth/register` · `POST /auth/login`
- `POST /accounts` · `GET /accounts/{id}` (saldo)
- `POST /accounts/{id}/deposit` · `POST /accounts/{id}/withdraw`
- `POST /transfers` (body: from, to, amount; header `Idempotency-Key`)
- `GET /accounts/{id}/statement?page=` (extrato paginado)
- `GET /admin/accounts` · `PATCH /admin/accounts/{id}/status` (ADMIN)
- `GET /health`

## 7. Requisitos funcionais
- **RF1** Registro/login retornam JWT com role; rotas protegidas exigem token válido.
- **RF2** Abrir conta e consultar saldo.
- **RF3** Depósito e saque, saque bloqueado se saldo insuficiente.
- **RF4** Transferência debita a origem e credita o destino em uma única transação atômica, gerando dois lançamentos no ledger.
- **RF5** Extrato paginado por conta a partir do ledger.
- **RF6** Admin lista contas e bloqueia/desbloqueia.

## 8. Requisitos não funcionais (foco da avaliação)
- **RNF1 Atomicidade**: débito + crédito + lançamentos acontecem numa transação de banco (`tx`), com commit ou rollback total.
- **RNF2 Concorrência**: **optimistic locking** via `version`; duas transferências simultâneas na mesma conta não deixam saldo negativo nem inconsistente; conflito faz retry limitado. Ver ADR 0001.
- **RNF3 Integridade monetária**: centavos em `int64`; invariante `saldo = soma(lançamentos)`. Ver ADR 0002.
- **RNF4 Idempotência**: `Idempotency-Key` única por transferência; retry devolve o resultado original sem reprocessar. Ver ADR 0003.
- **RNF5 Ledger append-only**: nunca `UPDATE`/`DELETE` em `entries`; saldo é projeção mantida na conta.
- **RNF6 Validação**: valores positivos, conta ativa, saldo suficiente; erros com status HTTP corretos.

## 9. Estrutura do projeto (layout Go idiomático)
```
bankcore/
├─ cmd/api/main.go            # bootstrap do servidor
├─ internal/
│  ├─ account/               # domínio: conta, saldo, operações
│  ├─ transfer/              # transferência atômica + idempotência
│  ├─ ledger/                # lançamentos append-only
│  ├─ auth/                  # JWT, middleware, roles
│  └─ platform/              # db (pgx), http (chi), config
├─ migrations/               # golang-migrate
├─ docs/                     # specs, adr, PRD, openapi
├─ docker-compose.yml        # postgres
├─ Makefile  .env.example  .gitignore
└─ README.md
```

## 10. Critérios de aceitação
- **CA1** Transferência com saldo suficiente move o valor e cria dois lançamentos; com saldo insuficiente, falha sem alterar nada.
- **CA2** Duas transferências concorrentes na mesma conta: só uma vence por rodada de optimistic lock; saldo nunca fica negativo (teste de concorrência).
- **CA3** Mesma `Idempotency-Key` duas vezes gera um único débito (teste de idempotência).
- **CA4** `docker-compose up` sobe o Postgres; `make run` sobe a API; migrations aplicam o schema.
- **CA5** Testes de integração com **testcontainers-go** exercitam a transação real no Postgres.

## 11. Roadmap por fases
1. **Fase 1 — scaffold**: módulo Go, chi, pgx, golang-migrate, docker-compose (Postgres), CI de build/test. Roda local.
2. **Fase 2 — MVP**: auth JWT + conta + depósito/saque + **transferência atômica com optimistic lock** + extrato + idempotência + testes testcontainers. Repo PÚBLICO aqui.
3. **Fase 3 — polish**: Swagger, ledger/auditoria, admin, README com diagrama, exposição de `transfers PENDING` para o Liquida.

## 12. Integração com o Liquida (fronteira, v-next)
O **Liquida** (.NET) liquida as transferências originadas aqui. Na v1 o BankCore não depende do Liquida. A integração (expor `GET /transfers?status=PENDING` e receber `PATCH /transfers/{id}/settle`) entra numa versão futura, com a mesma chave de idempotência ponta a ponta. Detalhe no ADR 0003 do Liquida.

## 13. Fora de escopo (v1)
KYC/compliance, múltiplas moedas, juros, cartões, dashboard. Podem virar versões futuras da spec.

---

## Changelog
- **1.0.0 (2026-08-31)** — Primeira spec. Núcleo bancário em Go: auth JWT, contas, depósito/saque, transferência atômica com optimistic locking, ledger append-only, idempotência, modelo de dados em centavos int64 e critérios de aceitação.
