# ADR 0005, Simulador com rotas de demonstração sem autenticação

- **Status:** Aceita; trava 3 ("superfície limitada") revista pela [ADR 0006](0006-reset-ambiente-simulacao.md)
- **Data:** 2026-10-31
- **Contexto:** A única interface do produto é o dashboard de tempo real (Angular, repo Liquida, `http://localhost:4200`). O botão "Iniciar simulação" precisa chamar o BankCore direto do navegador para gerar carga, e o dashboard precisa ler estatísticas agregadas com poll frequente. Todo o resto da API do BankCore exige Bearer JWT (`CUSTOMER`, `ADMIN` ou `SETTLEMENT`).

## Decisão
As rotas `/simulation/start`, `/simulation/stop`, `/simulation/status` e `/simulation/stats` ficam **sem autenticação**, com três travas:

1. **Flag de ambiente** `SIMULATION_ENABLED`, default `false`. Desligada, as rotas nem são montadas (`404`). Só o compose de dev local a liga.
2. **CORS restrito** a uma origem (`SIMULATION_CORS_ORIGIN`, default `http://localhost:4200`), sem credenciais. Só o dashboard consegue chamar pelo navegador.
3. **Superfície limitada**: as rotas não leem nem alteram dados de clientes reais. `start` só cria um customer técnico sem login possível e contas próprias da execução; `status` e `stats` devolvem apenas contadores e agregados, sem id de conta, nome, e-mail ou saldo individual. A config tem limites fixos (`accounts` até 500, `transfers` até 100000, `rate` até 500, `workers` até 64) e só uma execução roda por vez.

## Justificativa
- **Não existe segredo possível no navegador.** Qualquer credencial que o dashboard usasse (token `ADMIN`, API key) ficaria exposta no bundle ou no armazenamento do browser. Proteger as rotas com ela daria cerimônia sem segurança real, e pior: um token `ADMIN` vazado abre bloqueio de contas e provisionamento de credenciais, muito mais do que disparar uma simulação.
- **É recurso de demonstração local**, alinhado à regra do projeto: "recursos de demonstração ficam atrás de flag de ambiente, desligados por padrão". Em qualquer ambiente sem a flag, a superfície não existe.
- **Seguro por padrão**, no mesmo espírito da 1.2.0: a ausência de configuração resulta no comportamento mais restritivo.

## Alternativas consideradas
- **Exigir Bearer `ADMIN`**: rejeitada. O dashboard teria de guardar credencial de admin no navegador (ver acima) e o seed do admin teria de ser acoplado ao compose do Liquida.
- **Nova role `SIMULATION` com client-credentials (como o `SETTLEMENT` da ADR 0004)**: rejeitada. O client-credentials pressupõe um cliente capaz de guardar o secret; uma SPA não é. Seria a mesma credencial exposta com mais código para manter.
- **Proxy no backend do Liquida guardando a credencial**: rejeitada nesta versão. Funciona, mas cria um contrato extra entre os repos e um salto a mais só para uma demo local; reavaliar se a simulação um dia precisar rodar fora da máquina do desenvolvedor.

## Consequências
- Com a flag ligada, qualquer cliente que alcance a porta do BankCore consegue iniciar e parar simulações (o CORS só limita navegadores). Aceitável porque a flag só é ligada no compose local; **nunca** ligar `SIMULATION_ENABLED` em ambiente exposto.
- O pior abuso possível com a flag ligada é gerar carga dentro dos limites da config, uma execução por vez, sobre contas da própria simulação. Não há vazamento de dados nem acesso a contas reais.
- Se a simulação precisar sair do ambiente local, esta ADR deve ser substituída por uma nova que introduza autenticação (provavelmente o proxy no backend do Liquida).
