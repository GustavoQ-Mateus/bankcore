# Relatório Liquida → BankCore

| Campo | Valor |
|---|---|
| **De** | Liquida (serviço de settlement, .NET 8) |
| **Para** | BankCore (núcleo bancário, Go) |
| **Data** | 2026-09-05 |
| **Spec de referência** | Liquida `spec-v2.0.0` (contrato de fronteira fechado) |
| **Base BankCore** | v1.1.0 / ADR 0004 |
| **Responde a** | `handoff-liquida-v1.1.0.md` (BankCore → Liquida, 2026-09-05) |

## 0. Respostas ao handoff do BankCore (2026-09-05)

Recebi e reconciliei o `handoff-liquida-v1.1.0.md`. Resumo do que ele resolve e do que devolvo:

**Confirmações que o handoff nos deu (obrigado, fecha 2 das nossas 3 perguntas):**
- **Códigos 409 estáveis** (§2.5 do handoff): roteamos DLQ por `SETTLE_ON_FAILED`/`FAIL_ON_SETTLED`; `CONFLICT` fica reservado. ✅
- **Rede/compose estáveis** (§1/§3): `bankcore-net`, `bankcore-api:8080` interno, `:8081` host; "nada muda sem bump + aviso". Anexamos como `external`. ✅

**✅ CONFIRMAÇÃO DA §4.2 (o que vocês estavam aguardando):** o Liquida **aceita as chaves `settled_at`/`settlement_ref` sempre presentes, com valor possivelmente `null`**. Verificado no código: nosso `TransferDto` (`BankCoreDtos.cs`) **nem declara** esses dois campos, e `GetFromJsonAsync` ignora chaves extras por padrão — então tanto `null` quanto omitido são indiferentes para nós. **A correção de vocês (remover `omitempty`) não exige nada do Liquida. O E2E do Passo 12 pode rodar sem mais pendências de contrato entre os projetos.**

**Respostas às 5 decisões de coordenação (handoff §4):**

| # | Tema | Decisão do Liquida |
|---|---|---|
| 4.1 | Cadência de polling | **Contínuo enquanto `has_next=true`; 5s de espera quando o backlog zera.** Baixa latência, custo baixo. (spec D7) |
| 4.2 | Política de `fail` | **O Liquida NÃO chama `PATCH /fail`.** Falha de settlement fica na DLQ para reconciliação manual — marcar `FAILED` é decisão de operação, não automática. (spec §2, D5) |
| 4.3 | Formato do `settlement_ref` | **`"liquida:" + transacaoId`** (= `id` da transfer). Texto opaco, rastreio ponta a ponta. (spec D4) |
| 4.4 | Reconciliação de órfãos | **O Liquida garante o fechamento:** todo `PENDING` lido vira `SETTLED` ou vai à DLQ (visível à operação). **O BankCore não precisa de timeout/reconciliação automática na 1.2.0.** (spec D8) |
| 4.5 | Transporte (sem mTLS) | **Aceitável agora** — JWT M2M basta na rede Docker interna. mTLS/assinatura ficam para versão futura com exposição externa. (spec D9) |

**Nossa única pergunta que o handoff NÃO respondeu:** o buraco do `POST /auth/register` público aceitar `role` (escalada trivial a ADMIN). Não bloqueia o E2E, mas segue aberto do lado de vocês — ver §3.

**🔎 Achado do E2E (hardening, não bloqueante) — `PATCH /settle` ignora body chunked.** O handler de settle lê o `settlement_ref` só `if r.ContentLength > 0` (`internal/transfer/handler.go`). Clientes HTTP que enviam o corpo **chunked** (sem `Content-Length`) — caso do `HttpClient` do .NET com `PatchAsJsonAsync` — têm o body **silenciosamente ignorado**, e o `settlement_ref` fica `NULL` mesmo com `200`. Chunked é HTTP válido. **Contornamos no lado do Liquida** (enviamos `StringContent` com `Content-Length` explícito, então o E2E já persiste `settlement_ref=liquida:<id>` corretamente). Sugestão de robustez pra vocês: tentar decodificar o corpo quando presente independente do `Content-Length` (tratar EOF como body vazio), em vez de gatear por `ContentLength > 0`. Mesmo padrão vale pro `/fail` se um dia aceitar body.

## TL;DR
O contrato de fronteira está **100% fechado e implementado** do lado do Liquida até o Passo 10. **R1–R6 foram todos entregues pelo BankCore e já estão consumidos no código** (não só na spec). O Liquida agora entra no **Passo 11** (callback `PATCH /settle`) e no **Passo 12** (E2E integrado) — nenhum dos dois exige mudança de código no BankCore.

**Resta uma única dependência entre os projetos, e ela é operacional/segurança, não de contrato:** provisionar a credencial de service-client `SETTLEMENT` e decidir o que fazer com o buraco do `POST /auth/register` público aceitar `role`. Ver seção 3.

---

## 1. O que o Liquida já fechou consumindo o BankCore

Confirmado contra o **código** do BankCore (não só a spec), tudo implementado e testado no lado do Liquida:

| Item | Contrato consumido | Status Liquida |
|---|---|---|
| **Auth** | `POST /auth/token` JSON estrito (`DisallowUnknownFields`), corpo só `{client_id, client_secret}`, resposta `{token, token_type:"Bearer", expires_in:900}` | ✅ `BankCoreTokenProvider` (cache + refresh proativo por `expires_in` + renovação reativa em 401) |
| **R5 `expires_in`** | segundos, default 900, de `SERVICE_JWT_TTL` | ✅ usado para agendar refresh; não decodifica o `exp` do JWT |
| **Leitura (R1)** | `GET /settlement/transfers?status=PENDING`, role `SETTLEMENT`, envelope `{page, page_size, has_next, status, transfers[]}` | ✅ `BankCoreClient.ListarPendentesAsync` |
| **R3 `has_next`** | paginação por `has_next`, sem round-trip extra | ✅ Producer pagina enquanto `has_next=true` |
| **R6 ordenação** | `created_at ASC, id ASC` (FIFO estável) | ✅ Producer assume FIFO |
| **Mapeamento** | `amount_cents` (int64 cents) → `valor = /100m`; `id` → `transacaoId`; sem `type`/`moeda` | ✅ `tipo` virou opcional no contrato; `moeda` default `BRL`. 15/15 testes unit verdes |
| **R4 códigos 409** | `SETTLE_ON_FAILED` / `FAIL_ON_SETTLED`, `CONFLICT` genérico preservado | ✅ mapeado no roteamento de DLQ (§4.3 da spec) — **a ser exercido no Passo 11** |

**Nada aqui pede ação do BankCore.** É só o registro de que o que vocês entregaram está de pé e sendo usado.

---

## 2. O que o Liquida já entregou (sem mudança no BankCore)

- **Passo 11 ✅ — callback `PATCH /transfers/{id}/settle`** implementado e testado (26/26 verdes). Disparado pelo Consumer enquanto o registro em `liquidacoes` não está confirmado (barreira `settle_confirmado_em`, fecha o gap crash-antes-do-settle). Body `{"settlement_ref":"liquida:<transacaoId>"}`. Roteamento de resposta:
  - `200` → sucesso (inclui no-op de transfer já `SETTLED`).
  - `401` → renova token e repete 1x; persistindo → retry.
  - `409 SETTLE_ON_FAILED` → **DLQ** (terminal, não reliquida algo `FAILED`).
  - `404`/`400`/`403` → **DLQ**.
  - `5xx` → retry com backoff; esgotou → DLQ.
- **Decisão registrada:** o Liquida **não** chama `PATCH /fail`. Falha de settlement fica na DLQ para reconciliação manual — marcar `FAILED` no BankCore é decisão de operação, não automática.

**Pergunta de confirmação (não bloqueante):** os códigos `SETTLE_ON_FAILED` / `FAIL_ON_SETTLED` estão estáveis no Swagger publicado e não vão mudar de string? O roteamento de DLQ do Liquida é keyed por `error.code` exato.

---

## 3. Única dependência aberta entre os projetos (AÇÃO BANKCORE)

Para rodar o E2E (Passo 12), o Liquida precisa de uma credencial de service-client com role `SETTLEMENT`. Hoje o fluxo documentado é:

1. `POST /auth/register` `{"name","email","password","role":"ADMIN"}` (rota **pública**).
2. `POST /auth/login` → JWT admin.
3. `POST /admin/service-clients` (Bearer admin) → `{client:{client_id:"svc_…", role:"SETTLEMENT"}, client_secret:"<hex-48>", warning}`.

O Liquida roda esses 3 comandos **na própria máquina** e guarda `client_id`/`client_secret` no `.env` (fora do git) — o segredo não trafega pelo terminal do BankCore. **Isso não bloqueia o E2E.**

**O que precisa de decisão do BankCore:**

> **Buraco de segurança conhecido:** `POST /auth/register` (rota pública) aceita `role` no corpo e promove qualquer um a `ADMIN` (`auth.go:53` só rebaixa pra `CUSTOMER` se a role não for `ADMIN`/`CUSTOMER`). Funciona pro E2E, mas é uma escalada de privilégio trivial em produção.

**Pergunta:** vocês vão fechar isso **antes** do E2E (gate por env tipo `ALLOW_PUBLIC_ADMIN_REGISTER=false` + seed do 1º admin) ou **depois** (mantém aberto só no ambiente de teste e fecha antes de qualquer deploy)?

- Se **depois**: o Liquida toca o E2E já com o fluxo atual, sem esperar vocês. **Recomendação do Liquida.**
- Se **antes**: precisamos combinar como o Liquida obtém o primeiro admin (seed? env com credencial fixa de teste?) para não travar o Passo 12.

Sugestão concreta: **fechem com gate por env** — `register` público recusa `role != CUSTOMER` por padrão; um seed/bootstrap cria o 1º admin a partir de `ADMIN_EMAIL`/`ADMIN_PASSWORD`. Assim o E2E fica reproduzível e a rota pública fica segura. Mas isso é melhoria do BankCore, não pré-req do Passo 12.

---

## 4. Coordenação do E2E (Passo 12)

Já alinhado pela ADR 0003 / R2, só confirmando para os dois lados baterem:

- BankCore roda com **`LIQUIDA_INTEGRATION=external`** (deixa transferências em `PENDING`, não auto-liquida).
- Rede Docker **`bankcore-net`**; o compose do Liquida a anexa como `external: true` e usa `http://bankcore-api:8080` como base URL.
- Portas no host: BankCore API **8081**, Postgres BankCore **5433** (Liquida usa 8080 / 5432 — sem colisão).
- Roteiro do E2E: BankCore com N transferências `PENDING` → Liquida lê, liquida a 25 rps, e as N ficam `SETTLED` no BankCore (CA10). Reexecução não gera segundo efeito (CA11). `settle` sobre `FAILED` → 409 → DLQ (CA12). Token expirado → renova sem derrubar o batch (CA13).

**Pergunta:** o compose do BankCore que sobe `postgres → migrate → bankcore-api` na `bankcore-net` está commitado e estável nessa forma? O Liquida vai referenciar a rede como externa — se o nome mudar, quebra o E2E.

---

## 5. Resumo de perguntas ao BankCore

1. ~~Códigos 409 estáveis?~~ **Respondido pelo handoff §2.5 — sim.** ✅
2. ~~Rede/compose estáveis?~~ **Respondido pelo handoff §1/§3 — sim.** ✅
3. **Única pergunta em aberto:** o buraco do `register` público com `role` será fechado **antes** ou **depois** do E2E? (Liquida recomenda **depois**, com gate por env + seed do 1º admin — não bloqueia o Passo 12.)

As 5 decisões de coordenação do handoff (§4) estão **todas respondidas** na §0 acima.
