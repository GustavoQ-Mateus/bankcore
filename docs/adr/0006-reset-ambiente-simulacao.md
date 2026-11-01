# ADR 0006, Reset do ambiente de simulação como exceção ao ledger append-only

- **Status:** Aceita
- **Data:** 2026-10-31
- **Contexto:** O ledger do BankCore é append-only (RNF5 da 1.0.0, reforçado pela ADR 0004: o `fail` gera estorno em vez de apagar lançamentos). O dashboard de demonstração precisa que cada apresentação comece do zero; sem isso, os KPIs misturam execuções e o backlog antigo atrasa a liquidação da execução nova. A 1.4.0 adiciona `POST /simulation/reset`, que apaga todos os lançamentos, transferências e contas. Além disso, a ADR 0005 justificou as rotas de simulação sem auth com a trava "as rotas não leem nem alteram dados de clientes reais", e o reset apaga contas de qualquer cliente.

## Decisão
1. **Exceção de demonstração ao append-only.** `POST /simulation/reset` pode apagar `entries`, `transfers` e `accounts` (com `TRUNCATE`, numa única transação). A exceção só existe quando `SIMULATION_ENABLED=true`; com a flag desligada a rota nem é montada e o ledger continua estritamente append-only.
2. **Com a flag ligada, o banco é de demonstração.** Ligar `SIMULATION_ENABLED` declara que os dados de contas, transferências e lançamentos daquele banco são descartáveis. `customers` e `service_clients` não são apagados, para o admin e a credencial do Liquida sobreviverem ao reset.
3. **Revisão da trava 3 da ADR 0005.** A trava "superfície limitada" deixa de valer como estava escrita: com a flag ligada, qualquer cliente que alcance a porta do BankCore pode apagar contas, transferências e lançamentos, não só gerar carga. As travas 1 (flag desligada por padrão) e 2 (CORS restrito) continuam. A regra operacional fica mais forte: **`SIMULATION_ENABLED` nunca é ligada em ambiente exposto ou com dados que importem**, só no compose de dev local.

## Justificativa
- O append-only protege a auditabilidade de um ledger real. Um ambiente de demonstração cujo propósito é ser recriado a cada apresentação não tem o que auditar entre apresentações; preservar lá o histórico só degrada a demonstração.
- Restringir a exceção à flag mantém o princípio intacto onde ele importa: o mesmo binário, sem a flag, não tem caminho para apagar o ledger.
- `TRUNCATE` das três tabelas juntas não deixa estado intermediário: nunca existe lançamento sem conta, transferência sem conta ou saldo divergente da soma dos lançamentos (`divergent_accounts` continua `0` depois do reset).
- Manter `customers` e `service_clients` evita reprovisionar credenciais a cada demonstração, que era justamente o que o seed da 1.3.0 eliminou.

## Alternativas consideradas
- **Recriar o banco (derrubar o volume do compose):** rejeitada. Exige acesso ao Docker, não cabe num botão do dashboard e derruba também o Liquida conectado.
- **Reset seletivo só das contas da simulação:** rejeitada. As transferências da simulação podem envolver só contas dela, mas o pedido é "começar do zero", incluindo backlog antigo de qualquer origem; o seletivo deixaria resíduo nos KPIs.
- **Marcar dados como arquivados em vez de apagar:** rejeitada. Exigiria filtro em todas as consultas e nas stats, e mudança de schema, para preservar um histórico que no ambiente de demonstração não tem uso.
- **Exigir autenticação só no reset:** rejeitada pelo mesmo motivo da ADR 0005: não há segredo possível no navegador.

## Consequências
- Com a flag ligada, o pior abuso possível passa a ser apagar todas as contas, transferências e lançamentos do banco. Aceitável só porque a flag fica restrita ao compose local; o README e esta ADR registram que ela nunca deve ser ligada em ambiente exposto.
- Clientes cadastrados continuam existindo depois do reset, sem contas.
- O contador de colisões e o estado do simulador, que vivem em memória, também são zerados pelo reset, para os KPIs do dashboard ficarem coerentes com o banco.
- Se um dia a simulação precisar de dados persistentes ou rodar fora do ambiente local, esta ADR e a ADR 0005 devem ser substituídas por uma decisão que introduza autenticação e um banco separado para a demonstração.
