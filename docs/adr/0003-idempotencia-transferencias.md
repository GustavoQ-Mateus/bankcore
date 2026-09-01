# ADR 0003 — Idempotência de transferências via Idempotency-Key

- **Status:** Aceita
- **Data:** 2026-08-31
- **Contexto:** Clientes fazem retry (timeout, rede). Sem proteção, um retry de `POST /transfers` geraria um segundo débito. É um tema clássico de fintech e conecta com o Liquida, que consome estas transações.

## Decisão
`POST /transfers` exige o header **`Idempotency-Key`**, persistido em `transfers.idempotency_key` com **constraint UNIQUE**. Na primeira chamada a transferência é processada e o resultado guardado. Em chamadas seguintes com a mesma chave, a API **devolve o resultado original** sem reprocessar.

## Justificativa
- **Segurança financeira**: garante exactly-once do ponto de vista do cliente, mesmo com entrega at-least-once na rede.
- A constraint UNIQUE no banco é a fonte da verdade: uma inserção duplicada falha de forma atômica, sem depender de checagem em memória sujeita a corrida.
- **Consistência ponta a ponta com o Liquida**: a mesma chave (`transfer_id`/`Idempotency-Key`) é usada pelo Liquida como chave de idempotência da liquidação, fechando o ciclo entre os dois serviços.

## Consequências
- O cliente deve gerar e reenviar a mesma `Idempotency-Key` no retry.
- Tratar a corrida de inserção concorrente com a mesma chave (violação de UNIQUE → devolver o registro existente).
- `transfers.status` evolui `PENDING → SETTLED/FAILED`, e o `SETTLED` pode ser marcado pelo próprio BankCore ou, na integração futura, pelo callback do Liquida.
