# PRD — BankCore (API de núcleo bancário)

> **Atualização (2026-08-31):** stack migrada para **Go**, aproveitando a alta de vagas Golang. Java/Spring deixa de ser o foco deste projeto; o showcase de Java será um projeto separado (a definir). Idealmente renomear este arquivo para `PRD_BankCore_Go.md`.
>
> Projeto-vitrine em **Go** — linguagem em alta no mercado e excelente para um núcleo bancário: transferência de saldo é um problema clássico de **consistência, concorrência e integridade**, e Go brilha em concorrência (goroutines, `sync`, transações) — perfeito para provar backend sério, não CRUD. Companheiro do **Liquida** (.NET + Angular/SCSS), que liquida as transações originadas aqui.

## 1. Visão
API de núcleo bancário: clientes têm contas, fazem depósitos, saques e **transferências atômicas** entre contas, com extrato e ledger auditável. Escopo MVP demonstrável, defensável em entrevista técnica bancária.

**Não-objetivo:** virar banco real / compliance/KYC completo. Foco é a mecânica correta de transações e concorrência.

## 2. Stack (Go)
| Camada | Tech |
|---|---|
| Linguagem | **Go 1.22+** |
| HTTP | **chi** (ou Gin) + `net/http` |
| Persistência | **pgx** + PostgreSQL (SQL explícito; opcional sqlc para type-safety) |
| Migrations | **golang-migrate** |
| Auth | JWT (`golang-jwt`) com roles, middleware próprio |
| Testes | `testing` + **testcontainers-go** (Postgres real) + `httptest` |
| Infra | **Docker** · docker-compose · Makefile · GitHub Actions (build + test) |
| Docs | **OpenAPI/Swagger** (swaggo) |

## 3. Personas & escopo
- **Cliente:** cria conta, consulta saldo/extrato, faz transferência.
- **Admin:** lista contas, bloqueia/desbloqueia conta.

## 4. Funcionalidades (MVP)
1. **Auth** — registro/login, JWT com roles `CUSTOMER`/`ADMIN`.
2. **Conta** — abrir conta, consultar saldo.
3. **Operações** — depósito, saque (com checagem de saldo), **transferência entre contas**.
4. **Extrato** — histórico paginado por conta.
5. **Ledger append-only** — toda operação vira um lançamento imutável (auditoria).
6. **Idempotência** — header `Idempotency-Key` evita débito duplicado em retry.

## 5. Pontos de arquitetura (o que impressiona numa entrevista bancária)
- **Transação atômica:** transferência dentro de uma transação de banco (`tx, _ := db.Begin(ctx)` com pgx); débito+crédito+lançamentos, commit ou rollback (tudo ou nada).
- **Concorrência:** **optimistic locking** (coluna `version` na conta, `UPDATE ... WHERE version = ?`) para evitar corrida de saldo; retry em conflito. Alternativa discutível: pessimistic lock via `SELECT ... FOR UPDATE`. Bom para narrar goroutines + corrida de saldo.
- **Consistência monetária:** valores em **centavos como `int64`** (nunca `float64`); ou biblioteca de decimal (`shopspring/decimal`) com escala fixa.
- **Idempotência:** chave única por requisição persistida; retry devolve o resultado original em vez de reprocessar.
- **Ledger append-only:** nunca UPDATE em lançamento; saldo é projeção/coluna com invariante `saldo = soma(lançamentos)`.
- **Validação:** DTO + `@Valid`, valores positivos, conta ativa, saldo suficiente.

## 6. Modelo de dados (esboço)
```
Account   (id, owner_id, number, balance_cents int64, status, version)   // version p/ optimistic lock
Customer  (id, name, email, password_hash, role)
Entry     (id, account_id, type[CREDIT|DEBIT], amount_cents, balance_after_cents, transfer_id?, created_at)  // append-only
Transfer  (id, from_account_id, to_account_id, amount_cents, idempotency_key UNIQUE, status, created_at)
```
> Valores monetários em **centavos (`int64`)** para evitar erros de ponto flutuante. `status` do Transfer inclui `PENDING`/`SETTLED` para a fronteira com o Liquida (ADR 0003).

## 7. Endpoints principais
- `POST /auth/register` · `POST /auth/login`
- `POST /accounts` · `GET /accounts/{id}` (saldo)
- `POST /accounts/{id}/deposit` · `POST /accounts/{id}/withdraw`
- `POST /transfers` (body: from, to, amount; header `Idempotency-Key`)
- `GET /accounts/{id}/statement?page=` (extrato)
- `GET /admin/accounts` · `PATCH /admin/accounts/{id}/status` (ADMIN)

## 8. Testes (prova de qualidade)
- **Testcontainers** subindo Postgres real → testa a transação de verdade.
- Teste de **concorrência**: duas transferências simultâneas na mesma conta → só uma vence, saldo nunca fica negativo.
- Teste de **idempotência**: mesma `Idempotency-Key` duas vezes → um único débito.

## 9. Roadmap por fases (anti-projeto-fantasma)
- **Fase 1 — scaffold:** módulo Go + chi + pgx + golang-migrate + docker-compose (Postgres) + CI de build/test. → *roda local*.
- **Fase 2 — MVP:** auth JWT + conta + depósito/saque + **transferência atômica com optimistic lock** + extrato + idempotência + testes testcontainers-go. → **tornar repo PÚBLICO aqui.**
- **Fase 3 — polish:** Swagger (swaggo), ledger/auditoria, admin, README com diagrama. Expor origem de transações pendentes para o **Liquida** consumir (fronteira do ADR 0003).

## 10. Pontos de fala (mapa vaga → projeto)
- "**Go** + chi + pgx + Postgres" → o que as vagas de Golang pedem (backend concorrente e performático).
- "**Transação atômica** e concorrência com optimistic lock + goroutines" → integridade financeira e concorrência de verdade.
- "Testes com **testcontainers-go**" → maturidade de testes de integração.
- "**Idempotência** em pagamentos" → tema recorrente em fintech (PicPay/BTG), e conecta com o Liquida.
