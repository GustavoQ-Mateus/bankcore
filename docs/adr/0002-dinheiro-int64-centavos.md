# ADR 0002 — Dinheiro como int64 em centavos

- **Status:** Aceita
- **Data:** 2026-08-31
- **Contexto:** Valores monetários exigem exatidão. `float64` acumula erro de ponto flutuante e é inaceitável para saldo bancário.

## Decisão
Representar todo valor monetário como **`int64` em centavos** (a menor unidade da moeda). Entrada/saída da API em decimal (ex.: `100.50`) é convertida para/de centavos (`10050`) na borda.

## Justificativa
- **Exatidão**: soma e subtração de inteiros são exatas; nada de `0.1 + 0.2`.
- **Performance e simplicidade** em Go: aritmética de inteiros direta, comparações triviais.
- Alternativa: `shopspring/decimal`. Mais expressiva para múltiplas escalas/moedas, mas overhead desnecessário para uma moeda com 2 casas. Fica como opção se surgir multi-moeda.

## Consequências
- Conversão decimal↔centavos concentrada na camada HTTP/serialização.
- O banco guarda `balance_cents`, `amount_cents`, `balance_after_cents` como `bigint`.
- Documentar no README que a API aceita/retorna decimal mas internamente opera em centavos.
