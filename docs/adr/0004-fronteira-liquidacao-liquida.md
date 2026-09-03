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

## Autenticação da fronteira (M2M)
A credencial do Liquida é **JWT com role `SETTLEMENT`**, emitido por um fluxo **client-credentials** próprio: `POST /auth/token` recebe `client_id`/`client_secret`, valida contra o bcrypt hash em `service_clients` e devolve um JWT com **TTL curto** (`SERVICE_JWT_TTL`, default 15m).

- **Condição inegociável**: JWT service-role só é superior a uma API key porque há **emissão real com expiração**. Um JWT estático/eterno enfiado no env seria "uma API key fantasiada de JWT" — mesma superfície de risco, mais cerimônia. Por isso a decisão exige o endpoint de client-credentials com TTL curto; sem ele, a escolha correta seria uma API key dedicada validada por hash.
- **Reaproveitamento do RBAC**: `SETTLEMENT` é só mais uma claim de role; `RequireRole(SETTLEMENT)` é o mesmo middleware do RBAC de usuário. Nenhum segundo mecanismo de auth para construir, testar, revogar e auditar em paralelo.
- **Provisionamento**: `POST /admin/service-clients` (role `ADMIN`) gera o `client_secret` no servidor, exibe **uma única vez** e persiste apenas o hash. Rotação/revogação por linha em `service_clients`, sem redeploy. O Liquida guarda o secret no seu próprio secret store — **nunca no código**.
- **Idempotência ortogonal ao auth**: a `Idempotency-Key`/`transfer_id` = `transacao_id` do Liquida (ADR 0003) segue como chave ponta a ponta, independente do mecanismo de token.
- **Leitura least-privilege do backlog**: em vez de conceder `ADMIN` ao Liquida (que abriria listagem de contas, bloqueio etc.), a role SETTLEMENT ganha um endpoint próprio `GET /settlement/transfers?status=` — mesmo schema e paginação do `/admin/transfers`, escopo restrito. Mantém o princípio de menor privilégio: SETTLEMENT só lê o backlog e chama `settle`/`fail`.

## Alternativas consideradas
- **Autorização em duas fases (POST só reserva, dinheiro move no settle)**: rejeitada — quebraria a semântica de atomicidade da v1.0.0, exigiria estado de "reserva" e seria um MAJOR.
- **Novo campo `settlement_status` separado de `status`**: rejeitada por ora — dobraria a máquina de estados sem ganho na v1.1.0, já que o Liquida consome o modelo `PENDING/SETTLED` (ADR 0003 do Liquida). Reavaliar se surgir necessidade de distinguir "movido internamente" de "liquidado externamente" num mesmo registro.

## Consequências
- Nova migration aditiva (`settled_at`, `settlement_ref`) e nova role de serviço `SETTLEMENT`, restrita a `/settle` e `/fail` (RNF10).
- O modo `external` cria pendências que dependem do Liquida para fechar; timeout/reconciliação de pendências órfãs fica para a 1.2.0 (fora de escopo aqui).
- O significado de `PENDING` é sensível ao modo — precisa estar claro em README, Swagger e para o time do Liquida para evitar interpretação errada de "pagamento não efetivado".
- Transporte entre serviços continua em JWT nesta versão; mTLS/assinatura de payload é hardening futuro.
