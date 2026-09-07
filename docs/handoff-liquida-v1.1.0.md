# Handoff BankCore → Liquida — Fronteira de liquidação (BankCore v1.1.0)

| Campo | Valor |
|---|---|
| **De** | BankCore (Go) — `spec-v1.1.0`, ADR 0004 |
| **Para** | Liquida (.NET) — integração marcada como v2.0.0 do lado dele |
| **Data** | 2026-09-05 |
| **Estado do BankCore** | Fronteira **implementada e compilando** (`go build ./...` limpo). Endpoints, auth M2M, migration e Swagger prontos. |

> Objetivo deste doc: fechar o contrato que o Liquida consome, listar o que já está pronto do lado do BankCore, e apontar as decisões que ainda dependem dos dois times.

---

## 0. Resposta ao relatório do Liquida (reconciliação, 2026-09-05)

Recebido e cruzado o `relatorio-para-bankcore.md` do Liquida (cópia local em `docs/relatorio-do-liquida.md`). Respostas às perguntas da §5 deles:

**P1 — Códigos `SETTLE_ON_FAILED` / `FAIL_ON_SETTLED` estáveis no Swagger?**
✅ **Sim, congelados.** Constantes em `internal/platform/httpx/httpx.go:29-30`, refletidas no Swagger e na spec §6. Strings finais — não mudam sem bump de versão + aviso. Podem keyar a DLQ por elas.

**P3 — Rede `bankcore-net` e compose estáveis?**
✅ **Sim, exatamente como o Liquida assume.** `docker-compose.yml` commitado: rede com `name: bankcore-net` explícito (não recebe prefixo do projeto → `external: true` funciona); `postgres → migrate → api`; `container_name: bankcore-api` em `:8080` interno; host **API 8081 / Postgres 5433**; sobe com `LIQUIDA_INTEGRATION=external`.

**P2 — Buraco do `register` público aceitar `role=ADMIN`.**
✅ **Confirmado** (`internal/auth/auth.go:52-55` só rebaixa role desconhecida). Decisão: **fechar DEPOIS do E2E**, como o Liquida recomendou — mantém aberto só no ambiente de teste; o Liquida provisiona o service-client na própria máquina e segue para o E2E sem esperar. O fechamento (gate por env `ALLOW_PUBLIC_ADMIN_REGISTER=false` + seed do 1º admin via `ADMIN_EMAIL`/`ADMIN_PASSWORD`) entra na **spec 1.2.0**, junto com reconciliação de pendências órfãs.

**Política de `/fail`:** registrado e sem conflito — o Liquida **não** aciona `PATCH /fail` no fluxo automático (falha vai para DLQ/reconciliação manual). O endpoint continua existindo (idempotente, com estorno) para uso manual da operação.

### ✅ Correção de contrato — CONFIRMADA pelo Liquida (2026-09-06)
A §4.2 da spec v2.0.0 do Liquida foi corrigida para esperar `settled_at`/`settlement_ref` como **`null`** no `PENDING` (conforme §2.2 deste handoff). O struct Go do BankCore usava `omitempty`, que **omitia** os campos em vez de emitir `null` — divergência real entre código e contrato. **Corrigido:** removido `omitempty` de `Transfer.SettledAt`/`SettlementRef` (`internal/transfer/transfer.go:33-34`, commit `3395910`); Swagger regenerado. `PENDING` traz `"settled_at": null, "settlement_ref": null` (chaves presentes).
**Confirmação do Liquida:** o `TransferDto` deles nem declara esses dois campos e o deserializer ignora chaves extras — `null` ou omitido é indiferente para eles. **Contrato fechado, sem pendências entre os projetos.** O E2E (Passo 12) pode rodar.

### ✅ Achado do E2E — `PATCH /settle` ignorava body chunked — CORRIGIDO
O Liquida reportou que o handler de `settle` gateava o decode por `if r.ContentLength > 0`. Em transfer-encoding **chunked** (sem `Content-Length`, `ContentLength == -1`, caso do `HttpClient`/.NET) o body era silenciosamente ignorado e `settlement_ref` ficava `NULL` mesmo com `200`. **Corrigido:** novo `httpx.DecodeOptional` decodifica o corpo sempre que presente e trata `io.EOF` como body vazio; o `settle` deixou de gatear por `ContentLength` (`internal/transfer/handler.go`, `internal/platform/httpx/httpx.go`). Agora chunked, com body e sem body funcionam. Patch retrocompatível — nada muda para quem já mandava `Content-Length`. O Liquida já contornava enviando `Content-Length` explícito, então o E2E não depende disto; a correção só remove a pegadinha para clientes futuros. Mesmo padrão fica disponível para o `/fail` se um dia aceitar body.

### Status desta reconciliação
- **Contrato de fronteira:** ✅ 100% fechado e confirmado dos dois lados (auth M2M, backlog/paginação, settle/fail, códigos 409, `null` em `settled_at`/`settlement_ref`).
- **5 decisões de coordenação (§4):** ✅ todas respondidas pelo Liquida (polling contínuo + 5s idle; sem `/fail` automático; `settlement_ref = "liquida:<transacaoId>"`; Liquida garante fechamento, BankCore **não** precisa de reconciliação automática na 1.2.0; sem mTLS por ora).
- **Único item aberto (não-contrato, do BankCore):** fechar o `register` público que aceita `role=ADMIN`. **Decisão: DEPOIS do E2E**, na 1.2.0 (gate por env + seed do 1º admin). Não bloqueia o Passo 12.

---

## 1. Resumo — o que já está pronto no BankCore

Tudo do incremento 1.1.0 (RF7–RF10, CA6–CA10) está implementado:

- **Migration aditiva** `0002_settlement.up.sql`: colunas `settled_at`/`settlement_ref` em `transfers` + tabela `service_clients`.
- **Modo de liquidação** `LIQUIDA_INTEGRATION` (`standalone` default | `external`). Em `external`, `POST /transfers` move o dinheiro atomicamente e deixa `PENDING`.
- **Auth M2M** role `SETTLEMENT`: provisionamento `POST /admin/service-clients` e emissão `POST /auth/token` (client-credentials, JWT TTL curto).
- **Endpoints de fronteira**: `PATCH /transfers/{id}/settle`, `PATCH /transfers/{id}/fail`, `GET /settlement/transfers?status=` (least-privilege, com `has_next`).
- **Estorno append-only** no `fail` (par crédito/débito novo, ledger nunca reescrito).
- **Códigos de erro distintos** `SETTLE_ON_FAILED` / `FAIL_ON_SETTLED` para roteamento determinístico (DLQ).
- Swagger regenerado, `docker-compose` compartilhado na rede `bankcore-net` (host `:8081`, interno `bankcore-api:8080`).

**O contrato está estável do lado do BankCore.** Nada abaixo deve mudar sem bump de versão + aviso.

---

## 2. Contrato que o Liquida deve consumir

### 2.1 Obter token de serviço (M2M)
Pré-requisito: o BankCore te entrega `client_id` + `client_secret` uma única vez (via `POST /admin/service-clients`, feito por um ADMIN do BankCore). Guardar no secret store do Liquida — **nunca no código**.

```
POST /auth/token
Content-Type: application/json

{ "client_id": "svc_...", "client_secret": "..." }
```
- O decoder **rejeita campos desconhecidos**: enviar **exatamente** `client_id` e `client_secret`. **Não** enviar `grant_type`.
- Resposta `200`:
```json
{ "token": "<jwt>", "token_type": "Bearer", "expires_in": 900 }
```
- `expires_in` em segundos (default 15m, `SERVICE_JWT_TTL`). JWT HS256, claim `role=SETTLEMENT`. Renovar antes de expirar; credencial inválida → `401`.

Usar em todas as chamadas de fronteira: `Authorization: Bearer <jwt>`.

### 2.2 Descobrir o backlog (pull, polling)
```
GET /settlement/transfers?status=PENDING&page=1
Authorization: Bearer <jwt SETTLEMENT>
```
- Ordenação **FIFO estável**: `created_at ASC, id ASC` (paginação offset determinística).
- `page_size` fixo em **50**. Resposta:
```json
{
  "page": 1,
  "page_size": 50,
  "has_next": true,
  "status": "PENDING",
  "transfers": [ { "id": "...", "from_account_id": "...", "to_account_id": "...",
                   "amount_cents": 15000, "status": "PENDING",
                   "settled_at": null, "settlement_ref": null, "created_at": "..." } ]
}
```
- **Valor sempre em `amount_cents` (int64, centavos).** Sem float, sem string.
- Paginar enquanto `has_next == true`.

### 2.3 Confirmar liquidação
```
PATCH /transfers/{id}/settle
Authorization: Bearer <jwt SETTLEMENT>
Content-Type: application/json      (opcional)

{ "settlement_ref": "<id da liquidação do lado do Liquida>" }   (opcional)
```
- `PENDING → SETTLED`, preenche `settled_at`. **Idempotente**: repetir sobre `SETTLED` devolve `200` com o estado atual, sem efeito colateral.
- Sobre `FAILED` → **`409` `error.code=SETTLE_ON_FAILED`**.
- Body é opcional; sem `settlement_ref` a coluna fica `NULL`.

### 2.4 Sinalizar falha de liquidação
```
PATCH /transfers/{id}/fail
Authorization: Bearer <jwt SETTLEMENT>
```
- `PENDING → FAILED` + **estorno compensatório** (crédito na origem, débito no destino) na mesma transação. Sem body.
- **Idempotente**: repetir sobre `FAILED` devolve `200` sem novo estorno.
- Sobre `SETTLED` → **`409` `error.code=FAIL_ON_SETTLED`**.

### 2.5 Shape de erro (uniforme)
```json
{ "error": { "code": "SETTLE_ON_FAILED", "message": "..." } }
```
Códigos relevantes para o Liquida: `SETTLE_ON_FAILED`, `FAIL_ON_SETTLED` (ambos `409`), `401` (token inválido/expirado), `403` (role ≠ SETTLEMENT), `404` (transfer inexistente). `CONFLICT` genérico fica reservado para conflitos futuros — **não** trate os dois casos de transição como `CONFLICT`; use os códigos específicos para rotear DLQ.

### 2.6 Idempotência ponta a ponta
A `Idempotency-Key` (= `transfer_id` = `transacao_id` do Liquida, ADR 0003 dele) é a chave única de ponta a ponta. `settle`/`fail` são idempotentes por transferência: a transição só ocorre a partir de `PENDING`. Retries são seguros.

---

## 3. Contrato de rede / ambiente
- `docker-compose` único do BankCore expõe: rede `bankcore-net`, serviço `bankcore-api` em `:8080` interno, `:8081` no host (evita colisão com o Liquida).
- O Liquida anexa a rede como `external` e alcança `http://bankcore-api:8080`.
- Variáveis do lado BankCore relevantes ao contrato: `LIQUIDA_INTEGRATION=external`, `SERVICE_JWT_TTL` (default `15m`).

---

## 4. Decisões que dependem dos dois times (pendências de coordenação)

Estes pontos **não bloqueiam** o BankCore v1.1.0, mas precisam de alinhamento para o Liquida fechar a sua v2.0.0:

1. **Cadência de polling do backlog.** O BankCore é pull-only nesta versão (§10 spec — não há push/callback). O Liquida define o intervalo de polling e o tamanho de lote (respeitando `page_size=50` fixo). → *decisão do Liquida, informar ao BankCore para dimensionamento.*
2. **Política de `fail`.** Quando o Liquida decide chamar `/fail` (timeout de clearing? rejeição definitiva?) e como distingue disso um retry temporário. O BankCore trata `fail` como definitivo (estorna). → *decisão do Liquida.*
3. **Uso do `settlement_ref`.** Formato/semântica do id que o Liquida devolve no `settle` (rastreabilidade ponta a ponta). O BankCore só persiste como texto opaco. → *definir formato no Liquida.*
4. **Reconciliação / pendências órfãs.** Timeout de `PENDING` antigos está **fora de escopo da 1.1.0** (candidato à 1.2.0). Hoje, se o Liquida nunca liquidar, a transferência fica `PENDING` para sempre. → *acordar se a 1.2.0 do BankCore precisa de reconciliação automática ou se o Liquida garante fechamento.*
5. **Transporte.** v1.1.0 usa só JWT service-role. **Sem mTLS / assinatura de payload** (hardening futuro). → *aceitável para o ambiente atual? se não, vira requisito de versão futura.*

---

## 5. Fora de escopo da 1.1.0 (candidatos à 1.2.0)
- Callback/push do BankCore → Liquida (hoje é pull).
- Reconciliação automática / timeout de pendências antigas.
- mTLS / assinatura entre serviços.

---

## 6. Nota de versionamento entre repos
Este "v1.1.0" é do **BankCore**. Do lado do **Liquida**, a integração com o BankCore está marcada como **v2.0.0** (o "v1.1.0" do Liquida é o dashboard). Ao cruzar PRs, não confundir os dois "v1.1.0". Referências: `spec-v1.1.0.md` + ADR 0004 (BankCore); ADR 0003 (Liquida, modelo `PENDING/SETTLED` do lado consumidor).

---

## 7. Checklist de integração para o Liquida
- [ ] Recebeu `client_id`/`client_secret` do BankCore e guardou no secret store.
- [ ] Implementou `POST /auth/token` com renovação antes de `expires_in`.
- [ ] Polling de `GET /settlement/transfers?status=PENDING` paginando por `has_next`.
- [ ] Trata `amount_cents` como int64 centavos.
- [ ] `PATCH /settle` idempotente + roteia `409 SETTLE_ON_FAILED` para DLQ.
- [ ] `PATCH /fail` idempotente + roteia `409 FAIL_ON_SETTLED` para DLQ.
- [ ] Anexou a rede `bankcore-net` (external) e alcança `http://bankcore-api:8080`.
- [ ] Confirmou as 5 decisões de coordenação da §4.
