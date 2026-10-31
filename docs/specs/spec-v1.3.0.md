# BankCore, Especificação Técnica

| Campo | Valor |
|---|---|
| **Versão** | 1.3.0 |
| **Status** | Aprovada |
| **Data** | 2026-10-31 |
| **Autor** | Gustavo Queiroz Mateus |
| **Domínio** | Núcleo bancário + simulador de carga e estatísticas para o dashboard de tempo real |
| **Base** | Estende [spec-v1.2.0](./spec-v1.2.0.md). MINOR: adiciona escopo compatível, atrás de flag desligada por padrão. |

> Versionamento SemVer: MAJOR muda contrato/arquitetura incompatível, MINOR adiciona escopo compatível, PATCH corrige detalhe. Cada versão vive em `docs/specs/spec-vX.Y.Z.md`. Decisões de arquitetura viram ADRs em `docs/adr/`. PRD de origem em `docs/PRD_BankCore_Go.md`.

---

## 1. Contexto e motivação da 1.3.0
O produto não tem interface de usuário final. A única interface é o dashboard de tempo real do repo Liquida (fundo preto, linha de KPIs, grade de gráficos e um botão "Iniciar simulação"). Para o dashboard mostrar algo real, o botão precisa disparar carga de verdade no BankCore: contas, depósitos e transferências concorrentes passando pelo `transfer.Service` real, com o Liquida drenando os `PENDING` (poll de 5s) e chamando `settle` como já faz desde a 1.1.0.

O ponto da vitrine é a concorrência: poucas contas e muitas transferências simultâneas geram colisões reais de optimistic lock (ADR 0001), e o dashboard mostra que, mesmo assim, o ledger fecha (`divergent_accounts = 0`).

Hoje a stack integrada também exige provisionar a credencial do Liquida à mão (`POST /admin/service-clients`). A 1.3.0 semeia essa credencial no boot para os dois composes subirem sem passo manual.

O contrato HTTP desta versão foi definido pelo orquestrador e está **fechado**: o Liquida o consome em paralelo. Nomes e shape não mudam sem decisão do orquestrador.

O simulador também expõe um defeito que existe desde a 1.0.0: quando o `transfer.Service` esgota os retries de optimistic lock e devolve `409`, a linha inserida em `acquire` fica `PENDING` sem lançamentos. Em modo `external` o Liquida a lia no backlog e chamava `settle`, e ela virava `SETTLED` sem dinheiro movido, contra a ADR 0004. Sob carga concorrente isso deixa de ser raro. A 1.3.0 corrige na fronteira (§11), por decisão do usuário.

## 2. Objetivo
1. Gerar carga real e controlável a partir de um botão do dashboard, sem tocar no contrato de fronteira da 1.1.0.
2. Expor estatísticas agregadas (contagens, volume, throughput, latência de liquidação, integridade do ledger, colisões de lock) num único endpoint de leitura para o dashboard.
3. Subir a stack integrada sem provisionamento manual da credencial do Liquida.

## 3. Escopo (incremento 1.3.0)
1. **Simulador** (novo pacote `internal/simulation`), atrás de `SIMULATION_ENABLED` (default `false`).
2. **Contador de colisões de optimistic lock** no `transfer.Service`, exposto nas stats.
3. **Endpoint de estatísticas** `GET /simulation/stats`.
4. **Seed da credencial do Liquida** (`SETTLEMENT_CLIENT_ID`/`SETTLEMENT_CLIENT_SECRET`) no boot, idempotente.
5. **CORS** restrito a `SIMULATION_CORS_ORIGIN` nas rotas `/simulation/*`, com preflight.
6. **docker-compose** com o simulador ligado e a credencial de dev semeada.
7. **Correção da fronteira de liquidação** (§11): o backlog do Liquida só lista `PENDING` com lançamentos, e `settle` em `PENDING` sem lançamentos é recusado com `409 TRANSFER_NOT_BOOKED`.
8. **README** com as variáveis novas e como rodar a simulação (autorizado pelo usuário).

## 4. Stack
Igual à 1.2.0. **Sem dependência nova**:
- Ritmo do simulador com `time.Ticker` da stdlib (sem `golang.org/x/time/rate`).
- CORS com middleware próprio de poucas linhas (sem `go-chi/cors`), já que só três rotas usam e a política é uma origem fixa.
- Aleatoriedade com `math/rand/v2`.

## 5. Modelo de dados (delta sobre a 1.2.0)
**Nenhuma tabela ou coluna nova.** Uma migration aditiva, só de índice:
```sql
-- 0003_entries_transfer_index.up.sql
create index if not exists idx_entries_transfer on entries(transfer_id);
-- 0003_entries_transfer_index.down.sql
drop index if exists idx_entries_transfer;
```
Motivo: o filtro de lançamentos do §11 (`exists` em `entries` por `transfer_id`) roda no backlog do Liquida a cada poll, no `settle` e nas stats. O único índice de `entries` hoje é `(account_id, created_at)`; sem índice por `transfer_id`, cada `exists` varre a tabela, e com o limite de 100000 transferências por execução (cerca de 200000 lançamentos) o backlog ficaria caro. O `processOnce` e o `reverse` já fazem `count(*)` por `transfer_id` e também ganham com o índice.

- O customer técnico da simulação é uma linha comum em `customers` (role `CUSTOMER`), com `password_hash` que não é hash bcrypt válido; `bcrypt.CompareHashAndPassword` sempre falha e o `login` devolve `401`. Não há como logar com ele.
- As contas da simulação são linhas comuns em `accounts`, abertas pelo `account.Service` real; depósitos e transferências geram lançamentos comuns em `entries`.
- O seed da credencial insere uma linha comum em `service_clients`.
- As consultas de throughput e latência filtram por `created_at`/`settled_at` sem índice dedicado. No volume da demo (milhares de linhas) o custo é desprezível; índice só entra em versão futura se a medição pedir (YAGNI).

## 6. Configuração (delta)
| Variável | Default | Efeito |
|---|---|---|
| `SIMULATION_ENABLED` | `false` | `true` monta as rotas `/simulation/*`. `false`: rotas inexistentes (`404`). |
| `SIMULATION_CORS_ORIGIN` | `http://localhost:4200` | Única origem aceita pelo CORS das rotas `/simulation/*`. |
| `SETTLEMENT_CLIENT_ID` | *(vazio)* | `client_id` da credencial do Liquida semeada no boot. Só age se `SETTLEMENT_CLIENT_SECRET` também definido. |
| `SETTLEMENT_CLIENT_SECRET` | *(vazio)* | Secret da credencial semeada. Persistido só como hash bcrypt. Nunca logado. |

- O seed da credencial é independente de `SIMULATION_ENABLED`, no mesmo padrão do seed do admin da 1.2.0.
- Padrão seguro mantido: sem variável nenhuma, não há rota de simulação nem credencial semeada.

### 6.1 docker-compose (delta)
No serviço `api`:
```
SIMULATION_ENABLED: "true"
SIMULATION_CORS_ORIGIN: "http://localhost:4200"
SETTLEMENT_CLIENT_ID: "${SETTLEMENT_CLIENT_ID:-svc_liquida_dev}"
SETTLEMENT_CLIENT_SECRET: "${SETTLEMENT_CLIENT_SECRET:-dev-liquida-secret-change-me}"
```
Os defaults `svc_liquida_dev`/`dev-liquida-secret-change-me` são os mesmos do compose do Liquida. São credenciais de dev, só em default de compose (nunca em código nem `.env` commitado).

## 7. Simulador

### 7.1 Execução
Uma execução (`run`) tem um `run_id` (UUID) e segue as fases:

`idle` → `seeding` → `running` → `finished` | `stopped`

1. **seeding**: garante o customer técnico (`simulacao@bankcore.local`, criado uma vez e reaproveitado entre execuções), abre `accounts` contas novas via `account.Service.Open` e deposita em cada uma um saldo inicial aleatório entre **R$500,00 e R$5.000,00** (`50000` a `500000` centavos) via `account.Service.Deposit`.
2. **running**: dispara `transfers` transferências entre pares aleatórios distintos das contas desta execução, valor aleatório entre **R$1,00 e R$400,00** (`100` a `40000` centavos), via `transfer.Service.Transfer` real, com `Idempotency-Key` `sim-<run_id>-<n>` (`n` de 1 a `transfers`).
3. **finished**: todas as transferências foram despachadas e concluídas.
4. **stopped**: execução cancelada por `POST /simulation/stop` (ou pelo shutdown do processo) antes de terminar.

`finished` e `stopped` mantêm o `run_id`, os horários, a config e os contadores da última execução até um novo `start`. `idle` só existe antes da primeira execução do processo; nele `run_id`, `started_at` e `finished_at` são `null`, `config` mostra os defaults e os contadores são zero. O estado vive em memória e zera no restart.

### 7.2 Ritmo e concorrência
- `workers` goroutines consomem de um canal de trabalho.
- O despachante libera o trabalho em **lotes a cada 100ms**: `rate/10` transferências por lote (o resto da divisão é distribuído entre os lotes do segundo, para totalizar `rate` por segundo). Lote, e não intervalo uniforme de `1/rate`, porque com espaçamento uniforme as transações quase nunca se sobrepõem e não há colisão; o lote faz várias transferências começarem juntas sobre poucas contas, o que gera colisões reais de optimistic lock.
- Se os workers não dão conta do ritmo, o despachante bloqueia no canal; o ritmo efetivo fica abaixo de `rate`, nunca acima.

### 7.3 Uma execução por vez e cancelamento
- `start` com execução em `seeding` ou `running` → `409 SIMULATION_RUNNING`.
- A execução roda num `context` próprio derivado do contexto do processo, nunca do contexto da requisição HTTP.
- `stop` cancela esse contexto: o despachante para de liberar trabalho, cada worker termina a transferência em andamento e sai. `stop` responde depois que todos os workers saíram (fase `stopped`).
- A transferência em andamento roda com `context.WithoutCancel` do contexto da execução: o cancelamento interrompe o despacho, nunca uma transferência no meio. Assim o cancelamento nunca deixa uma linha `PENDING` sem lançamentos.
- O shutdown do processo (`SIGTERM`) cancela a execução ativa antes do `srv.Shutdown`.

### 7.4 Retry do simulador em conflito
O simulador age como um cliente bem-comportado da ADR 0003: quando `Transfer` devolve `409` com `code=CONFLICT`, ele repete a chamada com a **mesma** `Idempotency-Key`.
- Constantes nomeadas no pacote: `maxConflictAttempts = 5` (tentativas totais por transferência), `conflictBackoffBase = 10ms` e `conflictBackoffJitter = 20ms`. A espera antes da tentativa `k` é `conflictBackoffBase * k` mais um jitter aleatório em `[0, conflictBackoffJitter)`.
- A repetição reaproveita a linha já inserida (o `acquire` devolve a existente com os mesmos parâmetros), então não cria transferência nova.
- As tentativas e as esperas usam o mesmo contexto sem cancelamento da transferência (§7.3): o `stop` espera no máximo as tentativas restantes, que são limitadas.

### 7.5 Classificação dos resultados
Por código, nunca por mensagem (regra do projeto). Um resultado por transferência, depois das tentativas do §7.4:

| Contador | Condição |
|---|---|
| `dispatched` | Incrementado uma vez por transferência, quando um worker inicia a primeira chamada a `Transfer`. |
| `accepted` | `Transfer` sem erro (status `PENDING` em `external`, `SETTLED` em `standalone`). |
| `rejected_insufficient` | Erro `400` com a transferência devolvida em `FAILED`. No simulador as contas são ativas e distintas, então esse é o caminho de saldo insuficiente. |
| `conflict_exhausted` | Erro `409` com `code=CONFLICT` em todas as `maxConflictAttempts` tentativas. |
| `errors` | Qualquer outro erro. |

Ao fim de uma execução `finished`: `dispatched = transfers = accepted + rejected_insufficient + conflict_exhausted + errors`. Contadores com `sync/atomic`.

## 8. Contador de colisões de optimistic lock
- `transfer.Service` ganha um contador `sync/atomic` (`int64`) incrementado a cada `errVersionConflict` em `process`, antes do retry (inclusive na última tentativa, que vira `409`). As repetições do simulador (§7.4) passam de novo por `process`, então cada colisão delas também conta.
- Leitura por método `VersionConflicts() int64`.
- Conta colisões de **todas** as transferências do processo, não só as da simulação, desde o boot. Zera no restart. Colisões de `account.Service` (depósito/saque) não entram: o contrato fala do `transfer.Service`.

## 9. Seed da credencial do Liquida
- No boot, se `SETTLEMENT_CLIENT_ID` e `SETTLEMENT_CLIENT_SECRET` estão definidos, `auth.Service.EnsureServiceClient` insere em `service_clients` (`role=SETTLEMENT`, secret em bcrypt) com `on conflict (client_id) do nothing`.
- Idempotente: se o `client_id` já existe, é no-op; **o secret existente não é alterado** (rotação continua sendo por linha, como na 1.1.0).
- Log só do `client_id` quando a linha é criada; o secret nunca aparece em log, erro ou resposta.
- Com a credencial semeada, o Liquida obtém token pelo `POST /auth/token` da 1.1.0, sem mudança.

## 10. Endpoints de simulação
Montados só com `SIMULATION_ENABLED=true`. **Sem autenticação** (recurso de demo local, ver ADR 0005). JSON em `snake_case`. Erros via `httpx` (`{"error":{"code","message"}}`). Horários em RFC3339 UTC. Valores monetários sempre em centavos `int64`.

### 10.1 CORS
- Só nas rotas `/simulation/*`.
- Requisição com `Origin` igual a `SIMULATION_CORS_ORIGIN`: responde com `Access-Control-Allow-Origin: <origem>` e `Vary: Origin`. Outra origem: sem cabeçalhos CORS (o navegador bloqueia).
- Preflight `OPTIONS` da origem permitida: `204` com `Access-Control-Allow-Methods: GET, POST, OPTIONS`, `Access-Control-Allow-Headers: Content-Type` e `Access-Control-Max-Age: 600`. Cobre o `POST` com `Content-Type: application/json`.
- Sem credenciais (`Allow-Credentials` ausente), já que não há auth.

### 10.2 `POST /simulation/start`
Body opcional:
```json
{"accounts":40,"transfers":1500,"rate":60,"workers":16}
```
- Body ausente ou vazio: todos os defaults. Campo omitido: default daquele campo. Campo desconhecido: `400`.
- Limites (inclusivos): `accounts` 2..500, `transfers` 1..100000, `rate` 1..500, `workers` 1..64. Campo presente fora do limite (inclusive `0`): `400 VALIDATION`.
- Sucesso: `202` com o objeto status (§10.4), já em `seeding`.
- Execução ativa: `409` com `code=SIMULATION_RUNNING`.

### 10.3 `POST /simulation/stop`
- `200` com o objeto status. Com execução ativa, cancela e responde já em `stopped`. Ociosa (`idle`, `finished`, `stopped`): no-op, devolve o status atual.

### 10.4 `GET /simulation/status`
```json
{
  "phase": "idle|seeding|running|finished|stopped",
  "run_id": "uuid|null",
  "started_at": "RFC3339|null",
  "finished_at": "RFC3339|null",
  "config": {"accounts": 40, "transfers": 1500, "rate": 60, "workers": 16},
  "counters": {
    "dispatched": 0,
    "accepted": 0,
    "rejected_insufficient": 0,
    "conflict_exhausted": 0,
    "errors": 0
  }
}
```
- Chaves sempre presentes; `run_id`, `started_at` e `finished_at` serializam como `null` quando ausentes (mesma regra de `settled_at` na 1.1.0).
- `finished_at` é preenchido em `finished` e em `stopped`.

### 10.5 `GET /simulation/stats`
```json
{
  "captured_at": "RFC3339",
  "transfers": {"pending": 0, "settled": 0, "failed": 0},
  "volume_cents": {"pending": 0, "settled": 0},
  "throughput": {"created_per_sec": 0, "settled_per_sec": 0},
  "settlement_latency_ms": {"p50": 0, "p95": 0},
  "ledger": {"accounts": 0, "divergent_accounts": 0},
  "version_conflicts": 0,
  "simulation": {"phase": "...", "...": "objeto status do §10.4"}
}
```
| Campo | Tipo | Regra |
|---|---|---|
| `captured_at` | RFC3339 | Instante da leitura. |
| `transfers.pending` | int64 | Contagem de `PENDING` **com lançamentos** (`exists` em `entries` por `transfer_id`), banco inteiro. É o mesmo conjunto que o Liquida enxerga no backlog (§11.1). Linha `PENDING` sem lançamentos está "em processamento" ou órfã de conflito esgotado e não entra. |
| `transfers.settled`, `transfers.failed` | int64 | Contagem de `SETTLED` e de `FAILED`, banco inteiro (não só a simulação). |
| `volume_cents.pending` | int64 | Soma de `amount_cents` do mesmo conjunto de `transfers.pending`. `0` se não houver. |
| `volume_cents.settled` | int64 | Soma de `amount_cents` das `SETTLED`. `0` se não houver. |
| `throughput.created_per_sec` | int64 | `count(*)` de `transfers` com `created_at > now() - interval '1 second'`. |
| `throughput.settled_per_sec` | int64 | `count(*)` de `transfers` com `settled_at > now() - interval '1 second'`. |
| `settlement_latency_ms.p50`/`p95` | number | `percentile_cont(0.5)`/`(0.95)` sobre `settled_at - created_at` em ms, das liquidadas com `settled_at > now() - interval '60 seconds'`. `0` se não houver. |
| `ledger.accounts` | int64 | Total de contas. |
| `ledger.divergent_accounts` | int64 | Contas com `balance_cents != soma(CREDIT) - soma(DEBIT)` dos seus `entries` (conta sem lançamento conta como soma `0`). Deve ser sempre `0`: é o KPI de integridade do dashboard. |
| `version_conflicts` | int64 | `transfer.Service.VersionConflicts()` (§8). |
| `simulation` | objeto | O mesmo objeto de `GET /simulation/status`. |

- As consultas de banco rodam numa única transação `repeatable read, read only`, para os números do snapshot serem coerentes entre si.
- Janela de throughput de 1s no banco, igual ao que o Liquida já faz do lado dele.

### 10.6 Contrato de erro (delta)
Novos códigos distintos, mesmo shape, strings finais congeladas com esta versão:
- **`SIMULATION_RUNNING`** (`409`): `start` com execução ativa.
- **`TRANSFER_NOT_BOOKED`** (`409`): `settle` em `PENDING` sem lançamentos (§11.2).

O gate da 1.2.0 permanece como está. As mudanças na fronteira de liquidação estão no §11.

## 11. Correção da fronteira de liquidação
O shape e as rotas da 1.1.0 não mudam; muda quais linhas a fronteira aceita.

### 11.1 Backlog do Liquida
`GET /settlement/transfers?status=PENDING` passa a listar só as `PENDING` que já têm lançamentos (`exists (select 1 from entries where transfer_id = t.id)`). Uma `PENDING` sem lançamentos está "em processamento" (entre o `acquire` e o commit do movimento) ou órfã de um `409` esgotado; não é backlog de liquidação. Ordenação FIFO, paginação e `has_next` iguais à 1.1.0. Para `status=SETTLED` e `status=FAILED` nada muda. `GET /admin/transfers` e `GET /transfers?status=` (cliente) continuam mostrando o status cru, sem o filtro.

### 11.2 Defesa no `settle`
`PATCH /transfers/{id}/settle` sobre `PENDING` sem lançamentos responde `409 TRANSFER_NOT_BOOKED` e não altera nada. A checagem roda na mesma transação, depois do `select ... for update` na linha da transferência, que é o mesmo lock que o `processOnce` toma antes de lançar; então não há corrida entre "contar lançamentos" e "lançar". `settle` sobre `PENDING` com lançamentos, sobre `SETTLED` e sobre `FAILED` mantém o comportamento da 1.1.0.

**Impacto no Liquida, verificado pelo orquestrador:** o `BankCoreSettleClient` trata todo `409` como terminal e manda para a DLQ, então o código novo não exige mudança do lado dele. Com o filtro do §11.1 o caso nem deveria chegar ao `settle`; o `409` é defesa em profundidade.

### 11.3 Relação com a ADR 0004
A ADR 0004 já afirma que, em ambos os modos, `PENDING` nunca significa dinheiro não movido. O defeito era a implementação não garantir isso para a linha órfã de um `409`; a decisão em si não muda, por isso não há ADR nova. Esta spec registra o esclarecimento: **toda `PENDING` exposta ao settlement (backlog e `settle`) sempre tem dinheiro movido**. Uma `PENDING` sem lançamentos é estado interno do `transfer.Service` e só sai dele por nova chamada com a mesma `Idempotency-Key`.

## 12. Requisitos funcionais (incremento)
- **RF15** Com `SIMULATION_ENABLED=false`, nenhuma rota `/simulation/*` existe (`404`).
- **RF16** `POST /simulation/start` valida a config (§10.2), inicia uma execução assíncrona e responde `202` sem esperar o fim.
- **RF17** No máximo uma execução ativa por processo; `start` concorrente recebe `409 SIMULATION_RUNNING`.
- **RF18** Toda transferência do simulador passa por `transfer.Service.Transfer` com `Idempotency-Key` `sim-<run_id>-<n>`.
- **RF19** `POST /simulation/stop` cancela a execução ativa sem interromper uma transferência no meio.
- **RF20** Cada transferência é contada em exatamente um contador de resultado (§7.5), depois das tentativas do §7.4.
- **RF21** `transfer.Service` conta cada colisão de optimistic lock antes do retry.
- **RF22** `GET /simulation/stats` devolve os campos do §10.5 calculados no banco num snapshot coerente.
- **RF23** No boot, com `SETTLEMENT_CLIENT_ID`/`SETTLEMENT_CLIENT_SECRET` definidos, a credencial `SETTLEMENT` existe ao fim do boot; se já existia, nada muda.
- **RF24** O customer técnico da simulação não consegue autenticar por `POST /auth/login`.
- **RF25** Em `409 CONFLICT`, o simulador repete a mesma `Idempotency-Key` até `maxConflictAttempts` tentativas, com backoff e jitter.
- **RF26** `GET /settlement/transfers?status=PENDING` nunca devolve transferência sem lançamentos.
- **RF27** `PATCH /transfers/{id}/settle` sobre `PENDING` sem lançamentos devolve `409 TRANSFER_NOT_BOOKED` e não persiste nada.

## 13. Requisitos não funcionais (incremento)
- **RNF15 Desligado por padrão**: sem `SIMULATION_ENABLED=true`, o BankCore se comporta exatamente como na 1.2.0.
- **RNF16 Integridade sob carga**: com qualquer config válida, `divergent_accounts` permanece `0` durante e depois da execução.
- **RNF17 Cancelamento limpo**: `stop` e shutdown não deixam goroutine viva nem transferência `PENDING` sem lançamentos causada pelo cancelamento.
- **RNF18 Não vazamento de segredo**: `SETTLEMENT_CLIENT_SECRET` nunca aparece em log, erro ou resposta.
- **RNF19 Leitura barata**: `GET /simulation/stats` responde em menos de 100ms no volume da demo (dashboard faz poll frequente).
- **RNF20 Nenhuma liquidação sem movimento**: nenhuma transferência chega a `SETTLED` sem ter seus lançamentos de débito e crédito.

## 14. Critérios de aceitação (incremento)
- **CA19** Com o Liquida ligado (fluxo integrado), a simulação com a config padrão chega a `finished` e, depois que o Liquida drena o backlog, todas as transferências `accepted` da execução estão `SETTLED` (`transfers.pending = 0`).
- **CA20** Ao fim da execução (CA19 e testes), `ledger.divergent_accounts = 0`.
- **CA21** Com a config padrão, `version_conflicts > 0` ao fim da execução (prova de concorrência real).
- **CA22** `start` com execução ativa retorna `409 SIMULATION_RUNNING`; a execução original segue sem efeito.
- **CA23** Com `SIMULATION_ENABLED=false`, `GET /simulation/status`, `GET /simulation/stats`, `POST /simulation/start` e `POST /simulation/stop` retornam `404`.
- **CA24** Seed da credencial: com banco limpo, o boot cria a linha e `POST /auth/token` com o par semeado devolve token `SETTLEMENT`; um segundo boot com as mesmas variáveis não cria outra linha, não altera o hash e não falha.
- **CA25** Config fora dos limites (cada campo, abaixo e acima) retorna `400 VALIDATION`; body vazio usa os defaults.
- **CA26** `stop` com execução ativa responde `200` em `stopped`, e nenhuma transferência da execução fica `PENDING` sem lançamentos.
- **CA27** Ao fim de uma execução `finished`, `dispatched = transfers = accepted + rejected_insufficient + conflict_exhausted + errors`.
- **CA28** Preflight `OPTIONS /simulation/start` da origem configurada responde `204` com os cabeçalhos do §10.1; de outra origem, sem `Access-Control-Allow-Origin`.
- **CA29** `POST /auth/login` com o e-mail do customer técnico retorna `401`.
- **CA30** Os testes da 1.0.0, 1.1.0 e 1.2.0 continuam verdes.
- **CA31** `PATCH /transfers/{id}/settle` sobre `PENDING` sem lançamentos retorna `409 TRANSFER_NOT_BOOKED`; status, `settled_at` e `settlement_ref` ficam como estavam.
- **CA32** `GET /settlement/transfers?status=PENDING` não traz linha `PENDING` sem lançamentos e traz as `PENDING` com lançamentos.
- **CA33** Ao fim de uma execução, com tudo liquidado, `volume_cents.settled` é igual à soma dos `DEBIT` (em `entries`) das transferências da execução; `transfers.pending` e `volume_cents.pending` ignoram linhas `PENDING` sem lançamentos.
- **CA34** Regressão do defeito: uma transferência que esgota os retries do `transfer.Service` (linha `PENDING` sem lançamentos) não aparece no backlog, recusa `settle` com `409 TRANSFER_NOT_BOOKED` e, repetida com a mesma `Idempotency-Key`, conclui o movimento e passa a aparecer no backlog.

Testes com testcontainers cobrem runner (CA20, CA21, CA22, CA26, CA27, CA33), stats (§10.5 com dados conhecidos, inclusive uma divergência forçada por `update` direto para provar que o KPI detecta, e uma `PENDING` sem lançamentos fora das contagens), seed (CA24), login do customer técnico (CA29) e fronteira (CA31, CA32, CA34, com a linha órfã criada por `insert` direto em `transfers`). CA23, CA25 e CA28 em testes de handler com `httptest`. O CA21 roda em teste com config que force colisão (poucas contas, `rate` alto) e é conferido com a config padrão no fluxo integrado. O CA19 é conferido no fluxo integrado (dois composes + dashboard), registrado no changelog.

## 15. Plano de implementação (fases da 1.3.0)
Branch `feature/simulador` a partir de `origin/main`.
1. **Config**: `SIMULATION_ENABLED`, `SIMULATION_CORS_ORIGIN`, `SETTLEMENT_CLIENT_ID`, `SETTLEMENT_CLIENT_SECRET` em `internal/platform/config`.
2. **Seed da credencial**: `auth.Service.EnsureServiceClient` + chamada no boot (`cmd/api/main.go`), com teste.
3. **Fronteira**: migration `0003_entries_transfer_index` (aplicada também no `setupPool` dos testes), filtro de lançamentos no backlog `SETTLEMENT` e `TRANSFER_NOT_BOOKED` no `settle`, com teste de regressão (CA31, CA32, CA34).
4. **Contador** em `transfer.Service` + `VersionConflicts()`, com teste.
5. **`internal/simulation`**: runner (fases, lotes de 100ms, workers, retry em conflito, cancelamento, contadores), stats (snapshot `repeatable read`), handler com anotações swaggo e middleware CORS.
6. **`httpx`**: `ErrSimulationRunning` e `ErrTransferNotBooked`.
7. **main**: monta `/simulation` só com a flag, fora do grupo autenticado; cancela a execução no shutdown.
8. **Testes** (§14).
9. **Swagger** regenerado (`@version 1.3.0`, novo `409` no `settle`).
10. **docker-compose** (§6.1) e **README** (variáveis novas e como rodar a simulação).
11. **Verificação** do CLAUDE.md e fluxo integrado com o Liquida; resultado no changelog.

## 16. Decisões das questões abertas
- **Q1, `409` de conflito esgotado deixava `PENDING` sem lançamentos exposta ao settlement.** Decidido pelo usuário (via orquestrador): corrigir na própria 1.3.0. Resultado: §7.4 (simulador repete a mesma chave), §10.5 (contagens de pendência só com lançamentos), §11 (filtro no backlog e `TRANSFER_NOT_BOOKED` no `settle`) e CA31 a CA34.
- **Q2, README.** Autorizado pelo usuário: variáveis novas e como rodar a simulação, na fase de código.

## 17. Fora de escopo (1.3.0)
- SSE/push de stats: o dashboard faz poll em `GET /simulation/stats`. Push fica para versão futura se o Liquida pedir.
- Disparar a simulação no Liquida a partir do BankCore: o Liquida já drena `PENDING` sozinho.
- Persistir histórico de execuções ou o contador de colisões entre restarts.
- Limpeza das contas e transferências de execuções anteriores.
- Autenticação nas rotas de simulação (ADR 0005).

- Limpeza das linhas `PENDING` órfãs de conflito esgotado: ficam fora da fronteira e das contagens, e só saem desse estado se o cliente repetir a chamada.

## 18. Referências
- ADR 0005, Simulador com rotas de demo sem autenticação (nova nesta versão).
- ADR 0001 (optimistic locking): o contador mede as colisões que ela prevê.
- ADR 0003 (idempotência): base do retry do simulador com a mesma chave.
- ADR 0004 (fronteira de liquidação): decisão válida; o §11.3 registra o esclarecimento de que toda `PENDING` exposta ao settlement tem dinheiro movido.
- ADR 0002 (dinheiro int64): válida e inalterada.

---

## Changelog
- **1.3.0 (2026-10-31, implementação na `feature/simulador`)**: Código conforme a spec aprovada. `gofmt` limpo (conferido sobre o conteúdo LF que vai para o índice, já que o working copy no Windows é CRLF), `go vet`, `go build` e `go test ./...` verdes com Docker ligado (testcontainers), em duas rodadas completas. Swagger regenerado (`@version 1.3.0`); o swag atual passa a nomear as definições com prefixo de caminho (`internal_transfer.Transfer`), o que também acontece regenerando o commit base, então não é efeito desta versão e não muda o JSON da API. Um teste de stats instável sob carga (janela de throughput de 1s) foi tornado determinístico fixando os horários antes da leitura. Smoke test no compose do BankCore sozinho (sem Liquida), config padrão: `finished` em cerca de 26s, `dispatched=1500` (1451 aceitas, 49 por saldo insuficiente, 0 conflito esgotado, 0 erros), `version_conflicts=349`, `divergent_accounts=0`; credencial semeada no primeiro boot e no-op no segundo, com `POST /auth/token` em `200`. Resultado por CA: CA20, CA21, CA22, CA23, CA24, CA25, CA26, CA27, CA28, CA29, CA30, CA31, CA32, CA33 e CA34 verificados por teste; CA21 também com a config padrão no smoke test. **Pendente:** CA19 (fluxo integrado com o Liquida e o dashboard), que é a condição para `Status: Entregue`.
- **1.3.0 (2026-10-31, draft rev. 2)**: Questões abertas decididas pelo usuário. Q1 corrigida na própria versão: `GET /settlement/transfers?status=PENDING` só lista `PENDING` com lançamentos; `settle` em `PENDING` sem lançamentos responde `409 TRANSFER_NOT_BOOKED` (o Liquida já trata todo `409` como terminal, verificado pelo orquestrador); o simulador repete a mesma `Idempotency-Key` em `409 CONFLICT` com até 5 tentativas, backoff e jitter; `transfers.pending` e `volume_cents.pending` contam só `PENDING` com lançamentos; esclarecimento da ADR 0004 no §11.3, sem ADR nova; CA31 a CA34. Migration aditiva `0003` com índice `entries(transfer_id)` para o filtro de lançamentos. Q2: README autorizado.
- **1.3.0 (2026-10-31, draft)**: Simulador de carga atrás de `SIMULATION_ENABLED` (default `false`), com rotas `/simulation/start|stop|status|stats` sem auth e CORS restrito a `SIMULATION_CORS_ORIGIN`; contador de colisões de optimistic lock no `transfer.Service`; seed idempotente da credencial do Liquida via `SETTLEMENT_CLIENT_ID`/`SETTLEMENT_CLIENT_SECRET`; compose com o simulador ligado. Sem mudança de schema nem do contrato de fronteira. Ver ADR 0005.
