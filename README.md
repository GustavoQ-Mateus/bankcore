# BankCore

Núcleo bancário em **Go**: contas, depósitos, saques e **transferências atômicas** com **ledger append-only**. O foco é a mecânica que um banco não pode errar: nenhuma transferência deixa saldo inconsistente, operações concorrentes na mesma conta não corrompem o saldo e um retry de rede nunca debita duas vezes.

O BankCore é a metade de origem de um par. As transferências nascem aqui e são liquidadas pelo **[Liquida](https://github.com/GustavoQ-Mateus/Liquida)** (.NET), que respeita o limite de 25 requisições por segundo de um parceiro de liquidação e confirma cada uma de volta. Os dois se veem por um dashboard de tempo real com um botão que dispara uma simulação de carga real.

```mermaid
flowchart LR
    D[Dashboard React] -->|POST /simulation/start| SIM[simulador]
    SIM --> TR[transfer.Service]
    TR --> PG[(PostgreSQL)]
    LP[Liquida Producer] -->|GET /settlement/transfers| API[BankCore API]
    LC[Liquida Consumer] -->|PATCH /transfers/id/settle| API
    API --> TR
    D -->|GET /simulation/stats| API
```

## Destaques técnicos

| Decisão | Por quê | Referência |
|---|---|---|
| Transferência atômica | Débito, crédito e os dois lançamentos acontecem numa única transação: tudo ou nada | [`internal/transfer/transfer.go`](internal/transfer/transfer.go) |
| Optimistic locking com retry | Duas transferências simultâneas na mesma conta não deixam saldo negativo. `UPDATE ... WHERE version = ?`, contas acessadas em ordem de id para evitar deadlock | [ADR 0001](docs/adr/0001-optimistic-locking.md) |
| Dinheiro em centavos `int64` | Sem erro de ponto flutuante. A API aceita e devolve decimal (`"100.50"`), o núcleo opera em centavos | [ADR 0002](docs/adr/0002-dinheiro-int64-centavos.md) |
| Idempotência por `Idempotency-Key` | Retry devolve o resultado original. A chave é `UNIQUE` no banco, então a garantia não depende de memória | [ADR 0003](docs/adr/0003-idempotencia-transferencias.md) |
| Ledger append-only | Lançamento nunca é alterado; o saldo é projeção com a invariante `saldo = soma(CREDIT) - soma(DEBIT)`, conferida a cada segundo pelo `/simulation/stats` | [`internal/simulation/stats.go`](internal/simulation/stats.go) |
| Fronteira de liquidação | O Liquida autentica por client credentials (JWT de role `SETTLEMENT`), só enxerga transferências com dinheiro movido e confirma com `settle` idempotente | [ADR 0004](docs/adr/0004-fronteira-liquidacao-liquida.md) |
| Simulador de carga | Goroutines disparam transferências em lotes sobre poucas contas, gerando colisões reais de lock para mostrar que o ledger fecha mesmo assim | [ADR 0005](docs/adr/0005-simulador-rotas-demo-sem-auth.md) |
| Reset de demonstração | Cada demonstração começa do zero. Exceção controlada ao append-only, só com a flag de simulação ligada | [ADR 0006](docs/adr/0006-reset-ambiente-simulacao.md) |

## Como rodar

### Os dois sistemas juntos, com o dashboard
Pré-requisitos: Docker e Node. Com este repo e o [Liquida](https://github.com/GustavoQ-Mateus/Liquida) clonados:

```bash
cd bankcore && docker compose up -d --build
cd ../Liquida && docker compose -f docker-compose.yml -f docker-compose.bankcore.yml up -d --build
cd web/liquida-dashboard && npm install && npm run dev
```

Abra `http://localhost:4200` e clique em **Iniciar simulação**. O BankCore sobe primeiro porque cria a rede `bankcore-net`, que o Liquida usa. A credencial do Liquida é semeada pelo BankCore no boot, então não há passo manual.

### Só o BankCore
```bash
docker compose up -d --build        # Postgres, migrations e API em http://localhost:8081
curl localhost:8081/health
```

### API fora do Docker
```bash
docker compose up -d postgres migrate
DATABASE_URL="postgres://bankcore:bankcore@localhost:5433/bankcore?sslmode=disable" make run    # API em :8080
```
O Postgres do compose fica na porta **5433** do host, porque a 5432 é usada pelo Postgres do Liquida.

## Simulação

Com `SIMULATION_ENABLED=true` (o `docker-compose.yml` já liga):

```bash
curl -X POST localhost:8081/simulation/start -H 'Content-Type: application/json' \
  -d '{"accounts":40,"transfers":1500,"rate":60,"workers":16}'
curl localhost:8081/simulation/stats
curl -X POST localhost:8081/simulation/stop
curl -X POST localhost:8081/simulation/reset
```

- O body do `start` é opcional; os valores acima são os padrões. Limites: `accounts` 2 a 500, `transfers` 1 a 100.000, `rate` 1 a 500, `workers` 1 a 64.
- Uma execução por vez: um segundo `start` recebe `409 SIMULATION_RUNNING`. Cada execução é finita e termina sozinha em `finished`.
- O simulador cria um cliente técnico sem login possível, abre as contas, deposita de R$500 a R$5.000 em cada uma e dispara transferências de R$1 a R$400 pelo `transfer.Service` real. Em conflito de lock esgotado, repete com a mesma `Idempotency-Key`, como um cliente bem comportado.
- `GET /simulation/stats` devolve, num snapshot coerente do banco: contagem e volume por status, criadas e liquidadas por segundo, latência de liquidação p50 e p95 dos últimos 60s, colisões de lock e `ledger.divergent_accounts`, que deve ser sempre 0.
- `POST /simulation/reset` apaga contas, transferências e lançamentos numa transação e mantém clientes e credenciais de serviço. Com execução ativa responde `409`. Trava as tabelas na mesma ordem do `settle`, para não entrar em deadlock com liquidações em andamento.

## Endpoints

| Método | Rota | Acesso |
|---|---|---|
| POST | `/auth/register` | pública (só cria `CUSTOMER`) |
| POST | `/auth/login` | pública |
| POST | `/auth/token` | pública, client credentials do serviço de liquidação |
| POST | `/accounts` | cliente |
| GET | `/accounts/{id}` | dono ou admin |
| POST | `/accounts/{id}/deposit` | dono ou admin |
| POST | `/accounts/{id}/withdraw` | dono ou admin |
| GET | `/accounts/{id}/statement?page=` | dono ou admin |
| POST | `/transfers` (header `Idempotency-Key`) | dono ou admin |
| GET | `/transfers?status=` | dono |
| GET | `/transfers/{id}` | participante ou admin |
| GET | `/settlement/transfers?status=` | `SETTLEMENT` |
| PATCH | `/transfers/{id}/settle` | `SETTLEMENT` |
| PATCH | `/transfers/{id}/fail` | `SETTLEMENT` |
| GET | `/admin/accounts` | admin |
| PATCH | `/admin/accounts/{id}/status` | admin |
| GET | `/admin/transfers?status=` | admin |
| POST | `/admin/service-clients` | admin |
| POST | `/simulation/start`, `/simulation/stop`, `/simulation/reset` | sem auth, só com `SIMULATION_ENABLED=true` |
| GET | `/simulation/status`, `/simulation/stats` | sem auth, só com `SIMULATION_ENABLED=true` |
| GET | `/health` | pública |
| GET | `/swagger/index.html` | pública |

Na liquidação, `settle` repetido numa transferência já liquidada responde `200` sem efeito. Sobre uma `FAILED` responde `409 SETTLE_ON_FAILED`, e sobre uma `PENDING` ainda sem lançamentos, `409 TRANSFER_NOT_BOOKED`. Erros seguem sempre o formato `{"error":{"code","message"}}`.

O Swagger é gerado a partir das anotações dos handlers. Para regenerar depois de mudar rotas:
```bash
swag init -g cmd/api/main.go -o internal/apidocs --parseDependency --parseInternal
```

## Configuração

| Variável | Padrão | Efeito |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Endereço da API |
| `DATABASE_URL` | `postgres://bankcore:bankcore@localhost:5432/bankcore?sslmode=disable` | Conexão com o Postgres |
| `JWT_SECRET` | segredo de dev | Assinatura dos tokens |
| `JWT_TTL` / `SERVICE_JWT_TTL` | `24h` / `15m` | Validade do token de usuário e do token de serviço |
| `BCRYPT_COST` | `10` | Custo do hash de senha |
| `LIQUIDA_INTEGRATION` | `standalone` | `external` deixa a transferência `PENDING` para o Liquida liquidar; `standalone` liquida na hora |
| `ALLOW_PUBLIC_ADMIN_REGISTER` | `false` | `true` reabre o cadastro público com role privilegiada (só para dev) |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` | vazio | Semeiam o primeiro admin no boot |
| `SETTLEMENT_CLIENT_ID` / `SETTLEMENT_CLIENT_SECRET` | vazio | Semeiam a credencial `SETTLEMENT` do Liquida no boot, sem trocar uma existente |
| `SIMULATION_ENABLED` | `false` | Monta as rotas `/simulation/*` |
| `SIMULATION_CORS_ORIGIN` | `http://localhost:4200` | Única origem aceita pelo CORS das rotas de simulação |

## Testes e CI

```bash
go test ./...       # sobe PostgreSQL real via testcontainers-go; precisa do Docker ligado
```

- **Integração com Postgres real:** atomicidade, concorrência com optimistic lock, idempotência, fronteira de liquidação e RBAC da role `SETTLEMENT`, simulador, estatísticas, reset e seed da credencial. Os testes de deadlock do reset foram escritos antes da correção e falhavam no código anterior.
- **Unitários:** conversão de dinheiro, handlers, CORS e o roteador com a flag de simulação ligada e desligada.
- **GitHub Actions** ([`ci.yml`](.github/workflows/ci.yml)): `gofmt`, `go vet`, `go build` e `go test ./... -race` em todo PR.

## Resultados medidos

Registrados nos changelogs das specs, com o Liquida ligado e a simulação disparada pelo dashboard:

| Execução | Resultado | Fonte |
|---|---|---|
| Padrão (40 contas, 1.500 transferências a 60/s) | 1.463 aceitas e liquidadas, 37 recusadas por saldo, 412 colisões de lock resolvidas, 0 contas divergentes, nenhuma liquidação sem lançamentos | [spec v1.3.0](docs/specs/spec-v1.3.0.md) |
| Padrão com limpeza antes | 1.428 de 1.428 liquidadas, 0 pendentes, 0 divergentes, p95 de liquidação 43,7s | [spec v1.4.0](docs/specs/spec-v1.4.0.md) |
| Reset com liquidações em andamento | 11 resets, todos `200` em 0,5 a 0,8s | [spec v1.4.0](docs/specs/spec-v1.4.0.md) |

## Documentação

O projeto segue **Specification-Driven Development**: cada versão é uma spec aprovada antes do código, com critérios de aceitação verificados e registrados no changelog dela. Decisões de arquitetura viram ADRs, e a coordenação com o Liquida é feita por documentos de handoff e relatório.

- **Specs:** [v1.0.0](docs/specs/spec-v1.0.0.md) · [v1.1.0](docs/specs/spec-v1.1.0.md) · [v1.2.0](docs/specs/spec-v1.2.0.md) · [v1.3.0](docs/specs/spec-v1.3.0.md) · [v1.4.0](docs/specs/spec-v1.4.0.md)
- **ADRs:** [0001](docs/adr/0001-optimistic-locking.md) · [0002](docs/adr/0002-dinheiro-int64-centavos.md) · [0003](docs/adr/0003-idempotencia-transferencias.md) · [0004](docs/adr/0004-fronteira-liquidacao-liquida.md) · [0005](docs/adr/0005-simulador-rotas-demo-sem-auth.md) · [0006](docs/adr/0006-reset-ambiente-simulacao.md)
- **PRD:** [PRD_BankCore_Go.md](docs/PRD_BankCore_Go.md)
- **Integração com o Liquida:** [handoff v1.1.0](docs/handoff-liquida-v1.1.0.md) · [handoff v1.2.0](docs/handoff-liquida-v1.2.0.md) · [relatório do Liquida](docs/relatorio-do-liquida.md)

## Versões

| Versão | O que entrou |
|---|---|
| 1.0.0 | Auth JWT, contas, depósito e saque, transferência atômica com optimistic lock, extrato, ledger append-only e idempotência |
| 1.1.0 | Fronteira de liquidação com o Liquida: modo `external`, client credentials, `settle` e `fail` |
| 1.2.0 | Cadastro público de admin fechado por padrão e seed do primeiro admin |
| 1.3.0 | Simulador de carga, estatísticas do dashboard e credencial do Liquida semeada no boot (tag `v1.3.0`) |
| 1.4.0 | Reset do ambiente de simulação (tag `v1.4.0`) |

## Stack

Go 1.27 · chi 5.3 · pgx 5.10 · PostgreSQL 16 · golang-migrate (imagem `migrate/migrate`) · golang-jwt 5.3 · bcrypt · swaggo 1.16 · testcontainers-go 0.44 · Docker Compose · GitHub Actions

## Limites

Projeto de demonstração, não um banco real: não há KYC, múltiplas moedas, juros ou cartões. As rotas de simulação não têm autenticação e o reset apaga o ledger. Os dois só existem com `SIMULATION_ENABLED=true`, que o compose de desenvolvimento liga e que nunca deve ser ligada em ambiente exposto. As credenciais do compose são de desenvolvimento.
