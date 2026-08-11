# Будущие улучшения WB Core

## 1. Статус документа

Этот документ содержит deferred production-hardening backlog для архитектуры из
[`wb-service-architecture-plan.md`](./wb-service-architecture-plan.md).

Перечисленные механизмы не входят в `WB Core MVP` и не должны внедряться
скрытно во время его реализации. Каждый блок требует отдельного решения,
миграционного плана и review.

Документ не отменяет MVP API:

```text
Clientset
→ ForCabinet
→ ContentV1 typed client
→ feature transport/wb
```

Будущие изменения по возможности остаются внутри core и composition root.

---

## 2. Strict CabinetID и structural JWT validation

Для `CabinetID` добавить строгую проверку:

- формат `[a-z][a-z0-9_]{0,31}`;
- запрет surrounding whitespace;
- bounded длину;
- безопасное построение ENV suffix;
- обнаружение suffix collision;
- safe diagnostics без credential values.

Для Personal token добавить bounded parser:

- ровно три JWT segments;
- encoded/decoded size ceilings;
- base64url decode;
- duplicate JSON key rejection;
- strict claim types;
- Personal-token type validation;
- UUIDv4 `sid` validation;
- Content capability validation;
- read-only capability detection;
- `exp` validation с safety margin;
- отсутствие claims/token dump в errors/logs.

Локальный decode не считается authentication: сервер WB остаётся источником
истины.

---

## 3. Seller identity и SellerKey

Получать canonical seller identity из проверенного `sid` и вычислять:

```text
SellerKey = SHA-256("wb-seller-key-v1\x00" || canonical_sid_bytes)
```

`SellerKey`:

- хранится как 32 bytes;
- не имеет публичного formatter;
- не попадает в feature/Telegram/logs;
- заменяет CabinetID как physical rate-limiter realm;
- используется для duplicate seller detection и binding.

---

## 4. Duplicate seller detection

Registry должен отклонять все конфликтующие entries, если один seller настроен
под несколькими CabinetID.

Нельзя выбирать первый кабинет или молча сокращать target cohort.

---

## 5. Persistent cabinet-to-seller binding

Добавить таблицу:

```sql
wb.cabinet_seller_bindings (
    cabinet_id       text primary key,
    seller_key       bytea not null unique,
    first_seen_at    timestamptz not null,
    last_verified_at timestamptz not null
)
```

Правила:

- first binding только после authenticated WB probe;
- rotation token того же seller разрешена;
- новый seller под старым CabinetID запрещён;
- automatic rebind отсутствует;
- concurrency разрешается транзакцией/unique constraints.

---

## 6. Authenticated startup probe

До публикации CabinetClient выполнить безопасный catalog-defined read, например
`CardsLimits`.

Порядок:

```text
structural config/JWT validation
→ unpublished registry
→ singleton guard
→ authenticated safe read
→ binding transaction
→ publish Clientset
```

`401`, transport uncertainty или crash до binding не должны оставлять новую
binding.

---

## 7. Token rotation verification

Rotation lifecycle:

1. Прекратить новый mutation admission.
2. Drain или классифицировать in-flight mutations.
3. Заменить secret у прежнего CabinetID.
4. Проверить тот же SellerKey.
5. Выполнить authenticated probe.
6. Пересобрать immutable Clientset после restart.
7. Вернуть readiness.

Hot swap token внутри живого CabinetClient не планируется без отдельного review.

---

## 8. Singleton process guard

До Telegram intake и WB calls:

- получить PostgreSQL advisory lock на dedicated connection;
- создать monotonic process epoch;
- fail-fast завершить второй process;
- отменить root context при потере lock connection;
- прекратить новые claims/calls;
- классифицировать начатую mutation как unknown.

V1 hardening предполагает один container/process и один Telegram Long Poller.

---

## 9. PostgreSQL rate admission

Заменить in-memory limiter backend, сохранив flowcontrol port.

Physical key:

```text
(SellerKey, BucketID)
```

Требования:

- persistent token/debt state;
- FIFO waiters;
- bounded queue;
- cancellation cleanup;
- durable `blocked_until`;
- restart safety;
- response observation transaction;
- fail-closed при DB loss;
- один активный `RatePolicyEpoch`;
- conservative policy migration.

Typed Content clients и feature contracts при этом не меняются.

---

## 10. Mutation permits и egress barrier

Перед mutation PostgreSQL атомарно проверяет:

- durable AttemptID;
- catalog version;
- exact request digest;
- evidence version;
- fencing token;
- permit expiry;
- отсутствие предыдущего egress start.

После commit request получает право на один RoundTrip.

Повторное использование ticket выполняет zero HTTP.

Неизвестный результат остаётся reconciliation-only и не повторяется blind.

---

## 11. Canonical prepared mutation request

Добавить operation-specific:

```text
Prepare
Restore
```

`Prepare`:

- принимает exact typed DTO;
- сериализует canonical JSON;
- проверяет bounds;
- вычисляет digest;
- сохраняет catalog version.

`Restore`:

- bounded читает persisted bytes;
- проверяет digest/version;
- strict decode;
- canonical re-encode equality;
- не выполняет новый business mapping.

---

## 12. Расширенная readiness model

Ввести отдельные состояния:

```text
ProcessLive
WBReadReady
TransferMutationReady
TargetDispatchReady
TargetReconcileReady
```

Readiness сообщает только safe issue codes/counts и не раскрывает credentials,
seller identity или внутренние claims.

---

## 13. Complete mutation cohort

Вернуть отдельную конфигурацию ordered cohort:

```text
WB_TRANSFER_CABINETS=main,backup
```

Создавать immutable snapshot с revision. Любой отсутствующий, read-only,
unauthenticated или binding-mismatched target делает новый transfer unavailable.

Нельзя молча использовать healthy subset для нового Start.

---

## 14. Durable workers, leases и fencing

Production transfer требует:

- durable jobs;
- leases;
- heartbeat;
- fencing token;
- state-aware recovery;
- bounded concurrency;
- graceful shutdown;
- persisted retry/backoff;
- reconciliation jobs;
- late resolver.

Worker orchestration остаётся в `feature/<name>/service/*_worker.go`, а SQL — в
`feature/<name>/repository/postgres`.

---

## 15. Security hardening

Будущие security deliverables:

- secret/redaction canaries;
- allowlisted error fields;
- token expiry warning;
- safe credential issue diagnostics;
- SSRF protection для media fetch;
- signed public media URLs;
- media quotas;
- dependency/security scanning;
- operator rotation/revocation runbook.

---

## 16. Observability hardening

Добавить метрики и dashboards:

- rate admission/wait/429;
- active physical limiter keys;
- retry attempts/outcomes;
- unknown mutation deliveries;
- jobs queued/leased/dead;
- reconciliation lag;
- Error List feed lag;
- target readiness;
- token expiry;
- notification backlog;
- singleton/process epoch.

Один итоговый `wb_request_completed` log остаётся базовым request event.

---

## 17. Собственный User-Agent

После MVP можно добавить собственный фиксированный `User-Agent` для исходящих
WB-запросов.

Требования:

- значение определяется централизованно и одинаково для всех кабинетов;
- значение bounded и не содержит CR/LF;
- в значение не входят CabinetID, display name, token и другие credentials;
- wrapper клонирует request перед изменением headers;
- отсутствие собственного `User-Agent` не блокирует startup и WB-запросы;
- источник значения (`BuildInfo`, config или compile-time value) выбирается
  отдельным решением.

Добавление собственного `User-Agent` не должно менять typed Content API,
feature contracts или модель кабинетов.

---

## 18. Усиление границы catalog операций

В MVP валидируемый конструктор `Operation` экспортируется из `policy`, чтобы
отдельный пакет `api/content/v1` мог создавать закрытый catalog. Feature-коду
запрещено вызывать этот конструктор правилом зависимостей и review, но Go пока
не обеспечивает этот запрет на уровне компиляции. Client-go-подобное дерево
пакетов сохраняется; вложенная директория `wb/internal` не планируется.

Если review-границы окажется недостаточно, отдельно рассмотреть статический
import/API check либо совместное размещение factory и catalog definitions без
переноса существующих пакетов. Решение должно сохранять:

- `Operation` и общую валидацию в `policy`;
- определения операций в `api/content/v1`;
- невозможность использовать произвольные method/path в feature-коде;
- отсутствие runtime-проверок вызывающего пакета и `runtime.Caller` hacks.

Точная форма проверки требует отдельного архитектурного review.

---

## 19. Миграционный порядок после MVP

Рекомендуемая последовательность:

1. Catalog boundary enforcement.
2. Structural JWT parser.
3. SellerKey и duplicate seller detection.
4. Authenticated probe.
5. Persistent binding.
6. Rotation workflow.
7. Singleton process guard.
8. PostgreSQL rate backend.
9. RatePolicyEpoch migration.
10. Prepared mutation request.
11. Mutation permit/egress barrier.
12. Complete mutation cohort/revision.
13. Extended readiness.
14. Production rollout review.
15. Optional custom User-Agent.

Каждый этап должен сохранять публичную typed Content API и feature-owned
`WBTransport` contracts.

---

## 20. Production boundary

До реализации необходимых hardening blocks нельзя обещать:

- seller-level limiter safety после restart;
- защиту от второго process;
- доказанную token identity;
- безопасный automatic token rotation;
- durable one-egress enforcement;
- production-ready mutation execution.

MVP разрешает разработку features и controlled integration, но production
mutation rollout требует отдельного решения по этому документу.
