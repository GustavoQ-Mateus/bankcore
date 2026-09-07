# Handoff BankCore → Liquida — Fechamento pós-E2E (BankCore v1.2.0)

| Campo | Valor |
|---|---|
| **De** | BankCore (Go) — `spec-v1.2.0` |
| **Para** | Liquida (.NET) — integração `v2.0.0` (entregue, tagueada) |
| **Data** | 2026-09-07 |
| **Responde a** | `relatorio-para-bankcore-v2.0.0.md` (Liquida → BankCore, 2026-09-07) |
| **Estado do BankCore** | E2E confirmado verde pelos dois lados. 1.2.0 fecha o último item aberto (segurança do `register`). |

> Recebido o relatório de fechamento da v2.0.0 do Liquida. Ciclo integrado provado ponta a ponta (CA10–CA13). Este handoff responde aos 3 pedidos da §5 e comunica o que a 1.2.0 muda.

---

## 1. E2E confirmado — nada pendente de contrato
CA10–CA13 verdes ao vivo na `bankcore-net`, Liquida tagueado `v2.0.0`. As duas correções do E2E foram do lado do Liquida (colisão de DNS `api`/`postgres` no compose §2.1; serialização HTTP §2.2) — **nenhuma exigiu código do BankCore**. Registrado do nosso lado que os nomes genéricos `api`/`postgres` são ambíguos quando um serviço externo entra na mesma rede; o alvo do BankCore (`bankcore-api`) já é único, então nada muda aqui.

## 2. Respostas aos 3 pedidos da §5

### Pedido 1 — hardening do body chunked no `/settle`
✅ **Já acatado e mergeado — não é release futuro.** O gate por `if r.ContentLength > 0` foi removido no commit `73c7434`: o `/settle` agora decodifica o corpo sempre que presente via `httpx.DecodeOptional`, tratando `io.EOF` como body vazio. Chunked (sem `Content-Length`), com body e sem body funcionam. O relatório de vocês descreve o handler ainda gateando por `ContentLength` porque foi gerado antes de enxergar esse commit — **está corrigido desde a 1.1.0 (nota de hardening no changelog da spec-v1.1.0)**. O contorno de vocês (`StringContent` com `Content-Length` explícito) continua válido, apenas deixou de ser necessário. Mesmo padrão fica disponível pro `/fail` se um dia aceitar body.

### Pedido 2 — plano do `register`/`role`
✅ **Confirmado e implementado na 1.2.0, exatamente como vocês recomendaram** (gate por env + seed do 1º admin):
- `POST /auth/register` fica **seguro por padrão**: `ALLOW_PUBLIC_ADMIN_REGISTER=false` (default) faz a rota só criar `CUSTOMER`; pedido de `role` privilegiada → **`403 error.code=ADMIN_REGISTER_DISABLED`** (código novo, congelado nesta versão).
- Seed idempotente do 1º admin no boot via `ADMIN_EMAIL` + `ADMIN_PASSWORD`.
- Com `ALLOW_PUBLIC_ADMIN_REGISTER=true` o comportamento antigo é preservado (ambiente de E2E/dev).

**Impacto no roteiro de provisionamento de vocês:** o fluxo de 3 comandos (`register ADMIN` → `login` → `service-clients`) só funciona com o gate **aberto**. Recomendação: no ambiente de E2E, subir o BankCore com **`ADMIN_EMAIL`/`ADMIN_PASSWORD`** (seed) e trocar o passo 1/2 por um `login` direto com essas credenciais; o passo 3 (`POST /admin/service-clients` para provisionar a credencial `SETTLEMENT`) **não muda**. Alternativa: manter `ALLOW_PUBLIC_ADMIN_REGISTER=true` só no compose de E2E e não mexer no roteiro. Os dois caminhos ficam documentados na `spec-v1.2.0.md` §6 e no README.

> Nota: `ADMIN_REGISTER_DISABLED` é no `register` público, que o Liquida **não** consome. O provisionamento da credencial `SETTLEMENT` segue por `/admin/service-clients` (inalterado). Nenhuma mudança de código no Liquida é exigida.

### Pedido 3 — estabilidade do contrato
✅ **Tudo congelado, sem mudança na 1.2.0:** strings `SETTLE_ON_FAILED`/`FAIL_ON_SETTLED`, rede `bankcore-net`, host interno `bankcore-api:8080`, envelope de `/settlement/transfers`. A 1.2.0 não toca no contrato de fronteira — só endurece o bootstrap/autenticação interna. Qualquer mudança futura vem com bump + aviso, como combinado.

## 3. O que muda no BankCore com a 1.2.0
| Item | Antes (1.1.0) | Depois (1.2.0) |
|---|---|---|
| `register` público + `role=ADMIN` | promovia a ADMIN | `403 ADMIN_REGISTER_DISABLED` (default seguro) |
| 1º admin | via `register` público | seed por `ADMIN_EMAIL`/`ADMIN_PASSWORD` (idempotente) |
| Contrato de fronteira | — | inalterado |
| Novas envs | — | `ALLOW_PUBLIC_ADMIN_REGISTER` (default `false`), `ADMIN_EMAIL`, `ADMIN_PASSWORD` |

## 4. Ação para o Liquida
Nada bloqueante. Só ao subir a próxima rodada de E2E/ambiente com a 1.2.0: escolher **seed** (recomendado) ou **gate aberto** para obter o admin, conforme §2 pedido 2. O provisionamento da credencial `SETTLEMENT` e todo o fluxo consumido continuam idênticos.

## 5. Referências
- `spec-v1.2.0.md` (BankCore) — gate + seed, CA14–CA18.
- `relatorio-para-bankcore-v2.0.0.md` (Liquida) — origem destes pedidos.
- ADR 0004 — fronteira de liquidação (inalterada).
