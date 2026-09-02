# BankCore

API de **núcleo bancário** em **Go**: contas, depósitos, saques e **transferências atômicas** entre contas, com extrato e **ledger append-only auditável**. Foco em consistência, concorrência e integridade monetária.

> Projeto-vitrine de backend em Go. Companheiro do [Liquida](../Liquida) (.NET + Angular), que **liquida** as transferências originadas aqui. Juntos formam um portfólio poliglota (Go + .NET + Angular) de sistemas bancários.

## Arquitetura (resumo)
- **Transferência atômica** dentro de uma transação de banco (pgx `tx`): débito + crédito + dois lançamentos, ou tudo ou nada.
- **Concorrência** com optimistic locking (coluna `version`) e retry em conflito. Ver ADR 0001.
- **Dinheiro** em centavos (`int64`), nunca `float64`. Ver ADR 0002.
- **Idempotência** de transferências via `Idempotency-Key`. Ver ADR 0003.
- **Ledger append-only**: saldo é projeção com invariante `saldo = soma(lançamentos)`.

```mermaid
flowchart LR
    C[Cliente] -->|JWT| API[BankCore API - chi]
    API --> AUTH[auth]
    API --> ACC[account]
    API --> TR[transfer]
    ACC --> LED[ledger append-only]
    TR --> LED
    AUTH --> PG[(PostgreSQL)]
    ACC --> PG
    TR --> PG
    LED --> PG
    TR -.transfers PENDING.-> LIQ[Liquida .NET - v-next]
```

Transferência atômica com optimistic lock e idempotência:

```mermaid
sequenceDiagram
    participant C as Cliente
    participant T as transfer.Service
    participant DB as PostgreSQL
    C->>T: POST /transfers (Idempotency-Key)
    T->>DB: INSERT transfers PENDING (key UNIQUE)
    alt chave repetida
        DB-->>T: unique_violation
        T-->>C: resultado original (idempotente)
    else nova
        T->>DB: BEGIN
        T->>DB: SELECT transfer FOR UPDATE
        T->>DB: UPDATE contas SET balance, version+1 WHERE version=?
        T->>DB: INSERT entries DEBIT + CREDIT
        T->>DB: UPDATE transfers SETTLED / COMMIT
        T-->>C: 201 SETTLED
    end
```

## Stack
Go 1.22+ · chi · pgx + PostgreSQL · golang-migrate · JWT · testcontainers-go · Docker · Makefile · GitHub Actions · Swagger (swaggo).

## Documentação (spec-driven)
- **Spec (versionada):** [`docs/specs/spec-v1.0.0.md`](docs/specs/spec-v1.0.0.md)
- **PRD:** [`docs/PRD_BankCore_Go.md`](docs/PRD_BankCore_Go.md)
- **ADRs:** [0001 optimistic locking](docs/adr/0001-optimistic-locking.md) · [0002 dinheiro int64](docs/adr/0002-dinheiro-int64-centavos.md) · [0003 idempotência](docs/adr/0003-idempotencia-transferencias.md)

## Como rodar
```bash
cp .env.example .env
make compose-up          # sobe o PostgreSQL
make migrate-up          # aplica as migrations (via docker migrate/migrate)
make run                 # sobe a API em :8080
```
Health check: `curl localhost:8080/health`.

## Endpoints
| Método | Rota | Auth |
|---|---|---|
| POST | `/auth/register` | pública |
| POST | `/auth/login` | pública |
| POST | `/accounts` | cliente |
| GET | `/accounts/{id}` | dono/admin |
| POST | `/accounts/{id}/deposit` | dono/admin |
| POST | `/accounts/{id}/withdraw` | dono/admin |
| GET | `/accounts/{id}/statement?page=` | dono/admin |
| POST | `/transfers` (header `Idempotency-Key`) | dono/admin |
| GET | `/transfers/{id}` | participante/admin |
| GET | `/admin/accounts` | admin |
| PATCH | `/admin/accounts/{id}/status` | admin |
| GET | `/admin/transfers?status=` | admin |
| GET | `/health` | pública |
| GET | `/swagger/index.html` | pública |

Valores monetários entram/saem em decimal (`"100.50"`) e são operados internamente em centavos `int64`.

### Documentação interativa (Swagger)
A API expõe **Swagger UI** em `http://localhost:8080/swagger/index.html` e o spec em `/swagger/doc.json`, gerados via **swaggo** a partir das anotações nos handlers. Regenerar após alterar rotas:
```bash
swag init -g cmd/api/main.go -o internal/apidocs --parseDependency --parseInternal
```

## Exemplo
```bash
curl -X POST localhost:8080/auth/register -H 'Content-Type: application/json' \
  -d '{"name":"Alice","email":"alice@bankcore.dev","password":"secret123"}'
TOKEN=$(curl -s -X POST localhost:8080/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"alice@bankcore.dev","password":"secret123"}' | jq -r .token)
curl -X POST localhost:8080/accounts -H "Authorization: Bearer $TOKEN"
```

## Testes
```bash
go test ./...            # sobe PostgreSQL real via testcontainers-go
```
Cobrem atomicidade (CA1), concorrência com optimistic lock (CA2) e idempotência (CA3).

## Status
Fase 3 (polish) em andamento. Concluído: auth JWT, conta, depósito/saque, transferência atômica com optimistic lock, ledger append-only, idempotência, testes de integração (testcontainers), admin, **Swagger (swaggo)**, consulta de transferências e fronteira `GET /admin/transfers?status=PENDING` para o Liquida. Spec v1.0.0.
