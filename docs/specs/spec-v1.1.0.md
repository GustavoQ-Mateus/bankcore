# BankCore — Especificação Técnica

| Campo | Valor |
|---|---|
| **Versão** | 1.1.0 |
| **Status** | Draft |
| **Data** | 2026-09-02 |
| **Autor** | Gustavo Queiroz Mateus |
| **Domínio** | Núcleo bancário (contas, transferências, ledger) + fronteira de liquidação com o Liquida |
| **Base** | Estende [spec-v1.0.0](./spec-v1.0.0.md). MINOR: adiciona escopo compatível, sem quebrar contrato existente. |

> Versionamento SemVer: MAJOR muda contrato/arquitetura incompatível, MINOR adiciona escopo compatível, PATCH corrige detalhe. Cada versão vive em `docs/specs/spec-vX.Y.Z.md`. Decisões de arquitetura viram ADRs em `docs/adr/`. PRD de origem em `docs/PRD_BankCore_Go.md`.

---

## 1. Contexto e motivação da 1.1.0
A v1.0.0 entregou o núcleo bancário completo e **auto-liquidante**: `POST /transfers` move o dinheiro atomicamente e marca a transferência como `SETTLED` na mesma requisição. A v1.1.0 abre a **fronteira de liquidação** com o **Liquida** (.NET), que passa a ser o responsável por confirmar a liquidação externa (clearing) das transferências originadas aqui.

Nada da v1.0.0 é removido. A integração é **opt-in** por configuração; com ela desligada o BankCore continua se comportando exatamente como na v1.0.0 (por isso MINOR, não MAJOR).

## 2. Objetivo
Permitir que o Liquida (a) descubra as transferências pendentes de liquidação e (b) confirme sua liquidação, mantendo a **mesma chave de idempotência ponta a ponta** e sem jamais comprometer as invariantes monetárias da v1.0.0 (atomicidade, saldo = soma dos lançamentos, ledger append-only).

## 3. Escopo (incremento 1.1.0)
1. **Modo de liquidação configurável** (`LIQUIDA_INTEGRATION`): `standalone` (padrão, = v1.0.0) ou `external` (Liquida liquida).
2. **Descoberta de pendências**: `GET /settlement/transfers?status=PENDING` para o Liquida (role SETTLEMENT, least-privilege), `GET /transfers?status=PENDING` para o cliente (participante) e `GET /admin/transfers?status=` para operação.
3. **Confirmação de liquidação**: `PATCH /transfers/{id}/settle`, chamado pelo Liquida, transição `PENDING → SETTLED`, idempotente.
4. **Falha de liquidação**: `PATCH /transfers/{id}/fail`, transição `PENDING → FAILED` com estorno compensatório no ledger (ver §7).
5. **Credencial de serviço (M2M)** para o Liquida: fluxo **client-credentials** (`POST /auth/token`) que emite JWT `role=SETTLEMENT` com **TTL curto**, distinto de `CUSTOMER`/`ADMIN`. Sem token estático/eterno (ver ADR 0004).

## 4. Stack
Igual à v1.0.0. Sem novas dependências obrigatórias. A credencial de serviço reutiliza o JWT existente com uma nova role.

## 5. Modelo de dados (delta sobre a v1.0.0)
```
transfers       (... , status[PENDING|SETTLED|FAILED], settled_at timestamptz?, settlement_ref text?)
service_clients (id uuid, client_id text UNIQUE, secret_hash text, role, created_at)  -- credencial M2M do Liquida
```
- **`service_clients`**: identidade de serviço para o fluxo client-credentials. `secret` é gerado no servidor, **exibido uma única vez** na criação e persistido apenas como **bcrypt hash** (mesmo mecanismo de `customers.password_hash`). Rotacionável/revogável por linha, sem redeploy.
- **`settled_at`**: quando a liquidação externa foi confirmada (NULL enquanto `PENDING`).
- **`settlement_ref`**: referência opcional devolvida pelo Liquida (id da liquidação do lado dele), para rastreabilidade ponta a ponta.
- **Semântica do `status` na 1.1.0** (ver ADR 0004): o **movimento monetário continua atômico e imutável no `POST`** (saldo + dois lançamentos no ledger). O `status` passa a refletir o **ciclo de liquidação externa**:
  - `standalone`: `POST` move o dinheiro e marca `SETTLED` (comportamento v1.0.0).
  - `external`: `POST` move o dinheiro e deixa `PENDING`; o Liquida confirma via `PATCH /settle`.
- Nenhuma coluna existente muda de tipo ou é removida. `settled_at`/`settlement_ref` entram por migration aditiva (`0002_settlement.up.sql`).

## 6. Endpoints (delta)
- `POST /admin/service-clients` — **role `ADMIN`** — provisiona a credencial do Liquida. Retorna `client_id` + `client_secret` **uma única vez** (o servidor só guarda o hash).
- `POST /auth/token` — **client-credentials grant**. `Content-Type: application/json`, body **exatamente** `{ "client_id", "client_secret" }` (decoder rejeita campos desconhecidos — não enviar `grant_type`). Resposta `{ "token", "token_type": "Bearer", "expires_in": <segundos> }`, JWT `role=SETTLEMENT` com **TTL curto** (`SERVICE_JWT_TTL`, default 15m). Pública (autentica pela própria credencial), mas só emite token se o par bater com o hash.
- `GET /settlement/transfers?status=PENDING&page=` — **role `SETTLEMENT`** — backlog de liquidação para o Liquida (least-privilege, sem ADMIN). **Ordenação FIFO estável** (`ORDER BY created_at ASC, id ASC`) para paginação offset determinística. Paginação offset (`page`, `page_size` fixo 50) e `has_next` na resposta. Envelope: `{ page, page_size, has_next, status, transfers[] }` (array sob `transfers`, valor em `amount_cents` int64).
- `GET /transfers?status=PENDING&page=` — participante lista suas transferências pendentes (novo, cliente).
- `PATCH /transfers/{id}/settle` — **role `SETTLEMENT`** — confirma liquidação. Body opcional `{ "settlement_ref": "..." }`. Conflito sobre `FAILED` → **409 `error.code=SETTLE_ON_FAILED`**.
- `PATCH /transfers/{id}/fail` — **role `SETTLEMENT`** — marca falha de liquidação e dispara estorno compensatório. Conflito sobre `SETTLED` → **409 `error.code=FAIL_ON_SETTLED`**.
- **Contrato de erro**: shape `{ "error": { "code", "message" } }`. Códigos de transição inválida são distintos (`SETTLE_ON_FAILED`/`FAIL_ON_SETTLED`) para roteamento determinístico pelo consumidor (DLQ), reservando `CONFLICT` genérico para conflitos futuros.
- Reaproveitados da v1.0.0/Fase 3: `GET /transfers/{id}`, `GET /admin/transfers?status=`.

## 7. Requisitos funcionais (incremento)
- **RF7** Em modo `external`, `POST /transfers` conclui o movimento monetário atômico e retorna a transferência com `status = PENDING`.
- **RF8** `PATCH /transfers/{id}/settle` só transiciona `PENDING → SETTLED`; chamada repetida na mesma transferência é **idempotente** (devolve o estado atual sem reprocessar) e chamada sobre `SETTLED`/`FAILED` é no-op segura ou erro de conflito conforme o estado.
- **RF9** `PATCH /transfers/{id}/fail` transiciona `PENDING → FAILED` e gera **lançamentos de estorno** (crédito na origem, débito no destino) numa única transação, preservando `saldo = soma(lançamentos)`. O ledger nunca é alterado retroativamente (append-only); o estorno é um novo par de lançamentos.
- **RF10** Só a role `SETTLEMENT` (credencial do Liquida) pode chamar `/settle` e `/fail`.

## 8. Requisitos não funcionais (incremento)
- **RNF7 Atomicidade da liquidação**: settle e fail rodam em transação de banco; a transição de status e (no fail) os lançamentos de estorno são all-or-nothing.
- **RNF8 Idempotência ponta a ponta**: a `Idempotency-Key`/`transfer_id` usada no `POST` é a mesma chave que o Liquida usa para liquidar; `settle`/`fail` são idempotentes por transferência (a transição só ocorre a partir de `PENDING`).
- **RNF9 Compatibilidade retroativa**: com `LIQUIDA_INTEGRATION=standalone` (padrão), todo o comportamento observável é idêntico à v1.0.0.
- **RNF10 Autorização de serviço**: `SETTLEMENT` não pode abrir contas, transferir nem ler extratos; escopo restrito a `/settle` e `/fail`.

## 9. Critérios de aceitação (incremento)
- **CA6** Em modo `external`, `POST /transfers` com saldo suficiente move o dinheiro e retorna `PENDING`; `GET /transfers?status=PENDING` a lista.
- **CA7** `PATCH /transfers/{id}/settle` leva `PENDING → SETTLED` e preenche `settled_at`; repetir a chamada mantém `SETTLED` sem efeito colateral (idempotente).
- **CA8** `PATCH /transfers/{id}/fail` leva `PENDING → FAILED`, estorna os valores e mantém `saldo = soma(lançamentos)`; os lançamentos originais permanecem intactos (append-only).
- **CA9** Um `CUSTOMER` ou `ADMIN` recebe `403` ao chamar `/settle` ou `/fail`; só `SETTLEMENT` passa.
- **CA10** Com `standalone`, os testes da v1.0.0 continuam verdes sem alteração.

## 10. Fora de escopo (1.1.0)
- Callback/push do BankCore para o Liquida (aqui o Liquida faz **pull** via `GET ...?status=PENDING`).
- Reconciliação automática/timeout de pendências antigas (candidato a 1.2.0).
- Assinatura/mTLS entre serviços — a v1.1.0 usa JWT com role de serviço; hardening de transporte fica para versão futura.

## 11. Plano de implementação (fases da 1.1.0)
1. **Migration aditiva** `0002_settlement.up.sql`: `settled_at`, `settlement_ref` em `transfers` e tabela `service_clients`; sem alterar dados existentes.
2. **Config** `LIQUIDA_INTEGRATION` (default `standalone`), `SERVICE_JWT_TTL` (default 15m); propagação ao `transfer.Service` e ao `auth.Service`.
3. **Auth M2M**: role `SETTLEMENT`, provisionamento `POST /admin/service-clients` (secret gerado, hash persistido) e `POST /auth/token` (client-credentials, TTL curto).
4. **POST em modo external**: parar de auto-marcar `SETTLED`; deixar `PENDING` após o commit do movimento.
5. **Endpoints** `PATCH /settle` e `/fail` protegidos por `RequireRole(SETTLEMENT)`.
6. **`GET /transfers?status=PENDING`** para o cliente e **`GET /settlement/transfers?status=`** para a role SETTLEMENT (com `has_next`).
7. **Testes testcontainers** para CA6–CA10, incluindo emissão/expiração do token de serviço, idempotência do settle, estorno do fail e paginação `has_next`.
8. **Swagger** regenerado e README/diagrama atualizados com o fluxo de liquidação.
9. **Orquestração**: `docker-compose` único (Postgres + migrations + `bankcore-api`) na rede `bankcore-net`, alcançável em `http://bankcore-api:8080`; host `:8081` para não colidir com o Liquida. O Liquida anexa a rede como `external`.

## 12. Referências
- ADR 0004 — Fronteira de liquidação com o Liquida (novo nesta versão).
- ADR 0001 (optimistic locking), 0002 (dinheiro int64), 0003 (idempotência) — inalteradas e válidas.
- ADR 0003 do Liquida — modelo de status `PENDING/SETTLED` do lado consumidor.

> **Nota de versionamento entre repos:** este "v1.1.0" é do **BankCore**. Do lado do **Liquida**, a integração com o BankCore está marcada como **v2.0.0** (spec §13/§14 + ADR 0003 dele); o "v1.1.0" do Liquida é o dashboard. Quando o contrato do `/settle` fechar, ele entra numa `spec-v2.0.0.md` no repo do Liquida referenciando a ADR 0003 dele. Não confundir os dois "v1.1.0" ao cruzar os PRs.

---

## Changelog
- **1.1.0 (2026-09-02)** — Fronteira de liquidação com o Liquida: modo `standalone`/`external` configurável, `PATCH /transfers/{id}/settle` e `/fail` (role `SETTLEMENT`), `GET /transfers?status=PENDING`, colunas `settled_at`/`settlement_ref`, idempotência ponta a ponta e estorno append-only no fail. Compatível com a v1.0.0 (standalone é o padrão). Ver ADR 0004.
- **1.0.0 (2026-08-31)** — Primeira spec. Núcleo bancário em Go: auth JWT, contas, depósito/saque, transferência atômica com optimistic locking, ledger append-only, idempotência, modelo de dados em centavos int64 e critérios de aceitação.
