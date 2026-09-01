# ADR 0001 — Optimistic locking para concorrência de saldo

- **Status:** Aceita
- **Data:** 2026-08-31
- **Contexto:** Operações concorrentes na mesma conta (dois saques/transferências simultâneos) podem causar corrida de saldo e deixá-lo negativo ou inconsistente. Precisamos de uma estratégia de concorrência dentro da transação atômica.

## Decisão
Usar **optimistic locking** com uma coluna `version` em `accounts`. Toda atualização de saldo faz `UPDATE accounts SET balance_cents = ?, version = version + 1 WHERE id = ? AND version = ?`. Se `rows_affected = 0`, houve conflito e a operação é **repetida** (retry limitado, ex.: 3 tentativas).

## Justificativa
- **Sem lock pessimista** segurando linha durante I/O: melhor throughput sob concorrência normal, onde conflitos são raros.
- Combina bem com Go (goroutines): o teste de concorrência dispara N transferências simultâneas e prova que o saldo nunca fica negativo.
- Alternativa considerada: **pessimistic lock** (`SELECT ... FOR UPDATE`). Mais simples de raciocinar, mas serializa e reduz throughput; fica documentada como opção caso a contenção seja alta.

## Consequências
- Precisa de lógica de **retry** em conflito de versão.
- A transferência (que toca duas contas) ordena o acesso às contas por id para evitar deadlock e envolve ambas na mesma `tx`.
- Invariante garantida: `saldo = soma(lançamentos)`; saque nunca deixa saldo < 0.
