# BankCore

API de **núcleo bancário** em **Go**: contas, depósitos, saques e **transferências atômicas** entre contas, com extrato e **ledger append-only auditável**. Foco em consistência, concorrência e integridade monetária.

> Projeto-vitrine de backend em Go. Companheiro do [Liquida](../Liquida) (.NET + Angular), que **liquida** as transferências originadas aqui. Juntos formam um portfólio poliglota (Go + .NET + Angular) de sistemas bancários.

## Arquitetura (resumo)
- **Transferência atômica** dentro de uma transação de banco (pgx `tx`): débito + crédito + dois lançamentos, ou tudo ou nada.
- **Concorrência** com optimistic locking (coluna `version`) e retry em conflito. Ver ADR 0001.
- **Dinheiro** em centavos (`int64`), nunca `float64`. Ver ADR 0002.
- **Idempotência** de transferências via `Idempotency-Key`. Ver ADR 0003.
- **Ledger append-only**: saldo é projeção com invariante `saldo = soma(lançamentos)`.

## Stack
Go 1.22+ · chi · pgx + PostgreSQL · golang-migrate · JWT · testcontainers-go · Docker · Makefile · GitHub Actions · Swagger (swaggo).

## Documentação (spec-driven)
- **Spec (versionada):** [`docs/specs/spec-v1.0.0.md`](docs/specs/spec-v1.0.0.md)
- **PRD:** [`docs/PRD_BankCore_Go.md`](docs/PRD_BankCore_Go.md)
- **ADRs:** [0001 optimistic locking](docs/adr/0001-optimistic-locking.md) · [0002 dinheiro int64](docs/adr/0002-dinheiro-int64-centavos.md) · [0003 idempotência](docs/adr/0003-idempotencia-transferencias.md)

## Como rodar
_(preenchido ao longo do desenvolvimento)_
```bash
docker-compose up -d     # sobe o PostgreSQL
make migrate             # aplica as migrations
make run                 # sobe a API
```

## Status
Em desenvolvimento. Spec v1.0.0 (Draft).
