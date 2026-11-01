# BankCore, Especificação Técnica

| Campo | Valor |
|---|---|
| **Versão** | 1.4.0 |
| **Status** | Aprovada |
| **Data** | 2026-10-31 |
| **Autor** | Gustavo Queiroz Mateus |
| **Domínio** | Núcleo bancário + limpeza do ambiente de simulação |
| **Base** | Estende [spec-v1.3.0](./spec-v1.3.0.md). MINOR: adiciona um endpoint atrás da flag já existente, sem mudar os demais. |

> Versionamento SemVer: MAJOR muda contrato/arquitetura incompatível, MINOR adiciona escopo compatível, PATCH corrige detalhe. Cada versão vive em `docs/specs/spec-vX.Y.Z.md`. Decisões de arquitetura viram ADRs em `docs/adr/`. PRD de origem em `docs/PRD_BankCore_Go.md`.

---

## 1. Contexto e motivação da 1.4.0
Com a 1.3.0, cada clique em "Iniciar simulação" soma contas e transferências às das execuções anteriores. Os KPIs do dashboard (contagens, volume, latência) passam a misturar demonstrações, e o backlog antigo atrasa a liquidação da execução nova: no fluxo integrado da 1.3.0, a latência p50 ficou em cerca de 106s porque o Liquida drenou primeiro 1571 pendentes antigas.

O usuário pediu que toda demonstração comece do zero e que nada rode até clicar em Iniciar. O dashboard ganha um botão de lixeira "Limpar ambiente" e a opção "limpar antes de iniciar", marcada por padrão. O BankCore precisa de um endpoint que limpe o ambiente.

O dashboard também ganha cenários de carga (Tranquilo, Padrão, Estresse, Personalizado). São só presets do front sobre o `POST /simulation/start` da 1.3.0, e todos cabem nos limites atuais (§8). Nenhuma mudança no BankCore para eles.

O contrato desta versão foi definido pelo orquestrador e está **fechado**.

## 2. Objetivo
Permitir que o dashboard devolva o BankCore a um estado limpo e determinístico antes de cada demonstração, sem perder as credenciais necessárias para a stack integrada continuar funcionando (admin, customer técnico e credencial `SETTLEMENT` do Liquida).

## 3. Escopo (incremento 1.4.0)
1. **`POST /simulation/reset`**, atrás de `SIMULATION_ENABLED`, com o mesmo CORS das rotas de simulação.
2. **Zerar o contador** `version_conflicts` do `transfer.Service`.
3. **Zerar o estado do runner** do simulador (fase `idle`).
4. **ADR 0006**: exceção de demonstração ao ledger append-only e revisão da trava de superfície da ADR 0005.

## 4. Stack
Igual à 1.3.0. Sem dependência nova.

## 5. Modelo de dados (delta sobre a 1.3.0)
Nenhuma mudança de schema. Sem migration nova.

As FKs do schema atual (`accounts.owner_id → customers`, `transfers.*_account_id → accounts`, `entries.account_id → accounts`, `entries.transfer_id → transfers`) permitem limpar `entries`, `transfers` e `accounts` juntas sem tocar em `customers` nem `service_clients`, e sem `cascade`.

## 6. Endpoint

### 6.1 `POST /simulation/reset`
- Montado só com `SIMULATION_ENABLED=true`, fora do grupo autenticado, como as demais rotas `/simulation/*` (ADR 0005). Com a flag desligada, `404`.
- Mesmo CORS da 1.3.0. O preflight que já cobre `POST` cobre esta rota sem mudança.
- Sem body. Body enviado é ignorado.
- **Execução em `seeding` ou `running`**: `409` com `code=SIMULATION_RUNNING` (código da 1.3.0, sem código novo). O reset não para a simulação; o dashboard chama `POST /simulation/stop` antes. Nada é apagado e o runner segue como estava.
- **Sucesso** (fase `idle`, `finished` ou `stopped`): `200` com o objeto status da 1.3.0 (§10.4 da spec 1.3.0), já assim:
```json
{
  "phase": "idle",
  "run_id": null,
  "started_at": null,
  "finished_at": null,
  "config": {"accounts": 40, "transfers": 1500, "rate": 60, "workers": 16},
  "counters": {"dispatched": 0, "accepted": 0, "rejected_insufficient": 0, "conflict_exhausted": 0, "errors": 0}
}
```
- Falha de banco: `500 INTERNAL` via `httpx`, sem apagar nada (transação revertida) e sem mexer no runner nem no contador.

### 6.2 Efeito
Numa única transação:
```sql
LOCK TABLE transfers, entries, accounts IN ACCESS EXCLUSIVE MODE;
TRUNCATE transfers, entries, accounts;
```
- **Apaga** todos os `entries`, `transfers` e `accounts` do banco, inclusive contas de clientes que não sejam da simulação. Com a flag ligada, o banco é de demonstração (ADR 0006).
- **Mantém** `customers` (o admin semeado, o customer técnico da simulação e qualquer cliente cadastrado, que ficam sem contas) e `service_clients` (a credencial do Liquida sobrevive).
- `TRUNCATE` em vez de `DELETE`: é atômico, não depende do volume e toma lock `ACCESS EXCLUSIVE` nas três tabelas. Esse lock dá uma ordem limpa com as operações concorrentes (§7).
- **Ordem de lock.** O `LOCK TABLE` explícito trava as tabelas na mesma ordem que `Settle`, `Fail` e `processOnce` usam (`transfers`, depois `entries`, depois `accounts`). Sem ele, o `TRUNCATE` travaria na ordem listada e poderia segurar `entries` enquanto espera `transfers`, ao mesmo tempo que um `settle` segura uma linha de `transfers` e espera `entries`: deadlock (achado do fluxo integrado, ver changelog). Com a ordem igual, o reset espera em `transfers` sem segurar nada, e quem já tem `transfers` termina antes.
- A transação define `SET LOCAL lock_timeout` com uma constante nomeada `resetLockTimeout = 5s`, para nenhuma transação longa travar a requisição.
- **Defesa.** Se a transação falhar por deadlock (`40P01`) ou por timeout de lock (`55P03`), o reset inteiro é repetido até `maxResetAttempts = 3` tentativas, com espera de `resetRetryBackoff = 100ms` vezes o número da tentativa. Esgotadas as tentativas, ou em qualquer outro erro, responde `500` e nada muda. O caso que ainda pode cair aqui é um depósito concorrente (`account.Service` trava `accounts` antes de `entries` e não passa por `transfers`), que só acontece fora da simulação.

Só depois do commit:
- o contador `version_conflicts` do `transfer.Service` volta a `0` (novo método `ResetVersionConflicts()`, `sync/atomic`);
- o runner volta a `idle`: `run_id`, `started_at` e `finished_at` nulos, `config` com os defaults e contadores zerados.

### 6.3 Exclusão mútua com o simulador
O reset roda dentro do mutex do runner: a checagem de "execução ativa", o `TRUNCATE` e a volta a `idle` acontecem sob o mesmo lock que o `start` usa. Assim um `start` concorrente com o reset espera o reset terminar e nunca começa a semear contas que o `TRUNCATE` apagaria no meio. Enquanto o reset roda, `GET /simulation/status` e `GET /simulation/stats` esperam o lock (o reset leva milissegundos no volume da demo).

### 6.4 Depois do reset
`GET /simulation/stats` devolve tudo zero: `transfers` 0/0/0, `volume_cents` 0/0, `throughput` 0/0, `settlement_latency_ms` 0/0, `ledger.accounts = 0`, `ledger.divergent_accounts = 0`, `version_conflicts = 0` e `simulation` igual ao objeto do §6.1.

## 7. Janela de corrida com o Liquida
O reset pode acontecer com o Producer do Liquida no meio de um poll ou o Consumer no meio de um `settle`. O dashboard executa a ordem **parar se rodando → reset BankCore → reset Liquida → iniciar**, e o reset do Liquida esvazia a fila e a DLQ dele. O comportamento do BankCore nessa janela:

| Momento da operação do Liquida | O que acontece no BankCore |
|---|---|
| `settle` ou poll já em andamento quando o reset chega | O `TRUNCATE` espera a transação dele terminar (lock). O `settle` conclui normalmente e a linha é apagada em seguida pelo reset. |
| `settle` ou poll que começa durante o reset | Espera o lock e, depois do commit do reset, vê as tabelas vazias: o poll devolve lista vazia e o `settle` devolve `404 NOT_FOUND`. |
| `settle` de uma transferência que o Producer leu antes do reset | `404 NOT_FOUND`. O Liquida manda para a DLQ, que o reset dele esvazia. |
| `fail` de transferência apagada | `404 NOT_FOUND`, mesmo caminho. |

- Nenhum desses casos corrompe dados: as tabelas apagadas não têm linhas órfãs (tudo some na mesma transação) e não há `settle` que crie movimento.
- **Nada a fazer do lado do BankCore além do `404` limpo**, que já existe desde a 1.1.0 (`SELECT ... FOR UPDATE` sem linha → `ErrNotFound`). Esta spec registra o comportamento e o cobre com teste (CA41); não cria endpoint nem código novo para a janela.
- `transfer_id` são UUIDs gerados no banco e as `Idempotency-Key` da simulação levam o `run_id`, então uma mensagem velha do Liquida nunca acerta uma transferência nova depois do reset.

## 8. Cenários de carga do dashboard (verificação de limites)
Presets do front sobre `POST /simulation/start`. Limites da 1.3.0: `accounts` 2..500, `transfers` 1..100000, `rate` 1..500, `workers` 1..64.

| Cenário | Body | Cabe nos limites |
|---|---|---|
| Tranquilo | `{"accounts":40,"transfers":500,"rate":20,"workers":8}` | Sim |
| Padrão | defaults (`40`, `1500`, `60`, `16`) | Sim |
| Estresse | `{"accounts":15,"transfers":5000,"rate":200,"workers":32}` | Sim |
| Personalizado | escolhido pelo usuário | Validado pelo BankCore (`400 VALIDATION` fora dos limites) |

Nenhuma mudança de limite. Estimativa do Estresse: 5000 transferências a 200/s, cerca de 25s de despacho, com mais colisões que o Padrão (15 contas, 32 workers).

## 9. Requisitos funcionais (incremento)
- **RF28** Com `SIMULATION_ENABLED=false`, `POST /simulation/reset` não existe (`404`).
- **RF29** Com execução em `seeding` ou `running`, o reset devolve `409 SIMULATION_RUNNING` e não altera banco, runner nem contador.
- **RF30** Sem execução ativa, o reset apaga `entries`, `transfers` e `accounts` numa única transação, mantém `customers` e `service_clients`, zera `version_conflicts` e devolve o runner a `idle`.
- **RF31** O reset e o `start` são mutuamente exclusivos.
- **RF32** Operações de liquidação sobre transferências apagadas pelo reset devolvem `404 NOT_FOUND`.

## 10. Requisitos não funcionais (incremento)
- **RNF21 Atomicidade do reset**: ou as três tabelas são apagadas e o estado em memória zerado, ou nada muda.
- **RNF22 Credenciais preservadas**: depois do reset, o admin semeado continua logando e a credencial do Liquida continua emitindo token, sem reiniciar o processo.
- **RNF23 Exceção restrita**: o reset só existe com `SIMULATION_ENABLED=true` (ADR 0006).

## 11. Critérios de aceitação (incremento)
- **CA35** Reset com execução em `running` retorna `409 SIMULATION_RUNNING`; contas, transferências e lançamentos continuam lá, a execução segue e chega a `finished`.
- **CA36** Reset em `idle`, `finished` e `stopped` retorna `200` com o status do §6.1 (`idle`, campos de execução `null`, config padrão, contadores zero).
- **CA37** Depois do reset, `GET /simulation/stats` devolve todos os números zero, `divergent_accounts = 0` e `version_conflicts = 0`; `entries`, `transfers` e `accounts` estão vazias.
- **CA38** Depois do reset, `customers` e `service_clients` continuam com as mesmas linhas, a credencial do Liquida obtém token por `POST /auth/token` e o admin semeado faz login.
- **CA39** `start` depois do reset roda normalmente até `finished`, com contadores somando `transfers` e `divergent_accounts = 0`.
- **CA40** Com `SIMULATION_ENABLED=false`, `POST /simulation/reset` retorna `404`.
- **CA41** Depois do reset, `PATCH /transfers/{id}/settle` e `/fail` de uma transferência apagada retornam `404 NOT_FOUND`, e `GET /settlement/transfers?status=PENDING` devolve lista vazia.
- **CA42** Preflight `OPTIONS /simulation/reset` da origem configurada responde `204` com os cabeçalhos CORS da 1.3.0.
- **CA43** Os testes da 1.0.0 a 1.3.0 continuam verdes.
- **CA44** Fluxo integrado: com o Liquida ligado, "limpar antes de iniciar" pelo dashboard deixa os stats zerados antes do `start`, e a execução nova cumpre o CA19 da 1.3.0 (todas as aceitas `SETTLED`, `pending = 0`, `divergent_accounts = 0`) sem backlog antigo.

Testes com testcontainers cobrem CA35, CA36, CA37, CA38, CA39 e CA41. CA40 e CA42 em teste de roteador e handler com `httptest`. CA44 no fluxo integrado, registrado no changelog.

## 12. Plano de implementação (fases da 1.4.0)
Branch `feature/limpeza-simulacao` a partir de `feature/simulador` (empilhada sobre a 1.3.0, ainda não mergeada). Depois do merge da 1.3.0, `git merge origin/main` na branch.
1. `transfer.Service.ResetVersionConflicts()`.
2. `simulation.Runner.Reset(ctx)`: mutex, checagem de execução ativa, transação com `lock_timeout` e `TRUNCATE`, volta a `idle` e zera o contador depois do commit.
3. Handler `POST /simulation/reset` com anotações swaggo.
4. Testes (§11).
5. Swagger regenerado (`@version 1.4.0`) e README (rota nova e o que o reset apaga e mantém).
6. ADR 0006 e nota de status na ADR 0005.
7. Verificação do CLAUDE.md e fluxo integrado (CA44), resultado no changelog.

## 13. Fora de escopo (1.4.0)
- Parar a simulação dentro do reset: o dashboard chama `stop` antes (contrato).
- Reset seletivo (só a última execução, só contas da simulação): o pedido é começar do zero.
- Apagar `customers` ou `service_clients`.
- Coordenar o reset do Liquida a partir do BankCore: o dashboard orquestra os dois.
- Mudanças nos limites do `start` (§8).

## 14. Referências
- ADR 0006, Reset do ambiente de simulação como exceção ao ledger append-only (nova nesta versão).
- ADR 0005, rotas de simulação sem auth: a trava 3 ("superfície limitada") é revista pela ADR 0006.
- ADR 0004 e RNF5 da 1.0.0 (ledger append-only): continuam valendo fora da flag.
- ADR 0001, 0002 e 0003: válidas e inalteradas.

---

## Changelog
- **1.4.0 (2026-11-01, correção do fluxo integrado)**: Achado da sessão do Liquida no integrado, às 04:41:44Z: `POST /simulation/reset` com `settle` do Liquida em andamento respondeu `500` depois de cerca de 1s, e seis `PATCH /settle` simultâneos também deram `500`. Causa: deadlock (`40P01`, `deadlock_timeout` de 1s) por ordem de lock invertida. O `TRUNCATE entries, transfers, accounts` travava `entries` primeiro e esperava `transfers`, enquanto o `settle` segurava a linha de `transfers` (`FOR UPDATE`) e esperava `entries` (`isBooked`). Correção dentro do escopo aprovado, sem spec nova: `LOCK TABLE transfers, entries, accounts IN ACCESS EXCLUSIVE MODE` antes do `TRUNCATE`, na mesma transação e com o mesmo `lock_timeout`; repetição do reset inteiro em `40P01` ou `55P03` (até 3 tentativas, espera de 100ms vezes a tentativa) antes do `500`; §6.2 atualizado. Dois testes com testcontainers novos, escritos antes da correção e que falhavam com o código antigo: uma transação com `FOR UPDATE` numa transferência lê `entries` enquanto o reset espera, e o reset conclui sem deadlock; e `settle` concorrentes com o reset terminam todos em `200` antes ou `404` depois, com o reset concluindo.
- **1.4.0 (2026-10-31, implementação na `feature/limpeza-simulacao`)**: Código conforme a spec aprovada, empilhado sobre a `feature/simulador` e já com o merge da correção de testcontainers da 1.3.0 (`12466f7`: espera de `tcpostgres.BasicWaitStrategies()` nos helpers, que fechou a falha intermitente do CI do PR #1). `Runner.Reset` faz a checagem de execução ativa, a transação com `SET LOCAL lock_timeout` (5s) e o `TRUNCATE entries, transfers, accounts` sob o mutex do runner; o estado em memória e `version_conflicts` (`transfer.Service.ResetVersionConflicts`) só são zerados depois do commit. Nota de status na ADR 0005 apontando para a ADR 0006. `gofmt` limpo (sobre o conteúdo LF), `go vet`, `go build` e `go test ./...` verdes com Docker. O `-race` do CI não roda localmente no Windows (sem cgo). Swagger regenerado (`@version 1.4.0`) e README atualizado. Resultado por CA: CA35, CA36, CA37, CA38, CA39 e CA41 verificados por teste com testcontainers; CA40 e CA42 por teste de roteador e handler; CA43 pela suíte completa. **Pendente:** CA44 (fluxo integrado com o Liquida 2.2.0 e o dashboard). O teste por curl no compose também ficou para essa etapa, porque o compose no ar roda a imagem da 1.3.0 e recriá-lo apagaria dados em uso pela sessão do Liquida.
- **1.4.0 (2026-10-31, draft)**: `POST /simulation/reset` atrás de `SIMULATION_ENABLED`: `409 SIMULATION_RUNNING` com execução ativa; sem execução ativa, `TRUNCATE` de `entries`, `transfers` e `accounts` numa transação (mantém `customers` e `service_clients`), `version_conflicts` e runner zerados, `200` com status `idle`. Janela de corrida com o Liquida resolvida pelo lock do `TRUNCATE` e pelo `404` que já existe. Presets Tranquilo, Padrão e Estresse cabem nos limites da 1.3.0. Sem mudança de schema nem de outros endpoints. Ver ADR 0006.
