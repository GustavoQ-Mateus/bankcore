# ADR 0004 — Fronteira de liquidação com o Liquida

- **Status:** Aceita
- **Data:** 2026-09-02
- **Contexto:** A v1.0.0 é auto-liquidante: `POST /transfers` move o dinheiro e marca `SETTLED` na mesma requisição. Para a integração com o **Liquida** (.NET), que existe justamente para liquidar (clearing) as transferências originadas no BankCore, precisamos de um ponto de fronteira onde o Liquida confirme a liquidação — sem quebrar o comportamento standalone nem as invariantes monetárias.

## Decisão
Introduzir um **modo de liquidação configurável** (`LIQUIDA_INTEGRATION`) com dois valores:

- **`standalone`** (padrão): comportamento idêntico à v1.0.0. `POST /transfers` move o dinheiro atomicamente e marca `SETTLED`.
- **`external`**: `POST /transfers` executa **o mesmo movimento monetário atômico e imutável** (débito + crédito + dois lançamentos no ledger, tudo commitado), mas deixa a transferência em `PENDING`. O Liquida descobre pendências via `GET /transfers?status=PENDING`, liquida do lado dele e confirma via `PATCH /transfers/{id}/settle` (`PENDING → SETTLED`), ou sinaliza falha via `PATCH /transfers/{id}/fail` (`PENDING → FAILED` com estorno compensatório).

O `status` da transferência passa a ter dois significados conforme o modo, documentado explicitamente: em `standalone` reflete o resultado do próprio movimento; em `external` reflete o **ciclo de liquidação externa**. Em ambos os modos o **movimento monetário é sempre atômico no `POST`** — `PENDING` nunca significa "dinheiro não movido".

## Justificativa
- **Compatibilidade (MINOR, não MAJOR)**: com o padrão `standalone`, nenhum comportamento observável da v1.0.0 muda; os testes existentes continuam válidos (RNF9/CA10). A integração é estritamente aditiva.
- **Invariantes preservadas**: manter o movimento atômico no `POST` (mesmo em `external`) preserva RNF1/RNF3 da v1.0.0. Não introduzimos um estado intermediário onde o saldo diverge da soma dos lançamentos.
- **Idempotência ponta a ponta**: a mesma `Idempotency-Key`/`transfer_id` do `POST` é a chave que o Liquida usa para liquidar (coerente com a ADR 0003). `settle`/`fail` só transicionam a partir de `PENDING`, então retries são naturalmente idempotentes.
- **Append-only respeitado (ADR/RNF5)**: o `fail` não reescreve os lançamentos originais — gera um **par de estorno** (crédito na origem, débito no destino) numa nova transação, mantendo `saldo = soma(lançamentos)`.
- **Pull em vez de push**: o Liquida puxa pendências por polling; o BankCore não precisa conhecer o endpoint do Liquida na v1.1.0, reduzindo acoplamento.

## Alternativas consideradas
- **Autorização em duas fases (POST só reserva, dinheiro move no settle)**: rejeitada — quebraria a semântica de atomicidade da v1.0.0, exigiria estado de "reserva" e seria um MAJOR.
- **Novo campo `settlement_status` separado de `status`**: rejeitada por ora — dobraria a máquina de estados sem ganho na v1.1.0, já que o Liquida consome o modelo `PENDING/SETTLED` (ADR 0003 do Liquida). Reavaliar se surgir necessidade de distinguir "movido internamente" de "liquidado externamente" num mesmo registro.

## Consequências
- Nova migration aditiva (`settled_at`, `settlement_ref`) e nova role de serviço `SETTLEMENT`, restrita a `/settle` e `/fail` (RNF10).
- O modo `external` cria pendências que dependem do Liquida para fechar; timeout/reconciliação de pendências órfãs fica para a 1.2.0 (fora de escopo aqui).
- O significado de `PENDING` é sensível ao modo — precisa estar claro em README, Swagger e para o time do Liquida para evitar interpretação errada de "pagamento não efetivado".
- Transporte entre serviços continua em JWT nesta versão; mTLS/assinatura de payload é hardening futuro.
