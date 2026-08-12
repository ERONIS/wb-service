# Master-план cardimport, transfer и live statistics

> Фактическая архитектура завершённого WB Core описана в
> [`wb-core-architecture.md`](./wb-core-architecture.md). Этот документ
> планирует только продуктовые features поверх готового typed API и не
> переопределяет устройство ядра.

## 1. Статус документа

Это канонический end-to-end product/backlog план cardimport, transfer и
statistics в `wb-service`. Описание используемого WB Core находится в
[`wb-core-architecture.md`](./wb-core-architecture.md).

Документ заменяет архивный `doc/featers-transfer.md`. Если его текст расходится
с этим планом, действует этот документ.

План закрывает вместе:

- WB transport core;
- реестр кабинетов и токенов;
- закрытый Content operation catalog;
- импорт и валидацию XLSX;
- immutable card batch;
- копирование карточек во все доступные для записи кабинеты;
- upload/add и их reconciliation;
- загрузку media по ссылкам;
- durable workers, leases и recovery;
- live statistics и уведомления;
- security, rollout, миграции и тестирование.

В документе нет разрешения на слепой повтор WB mutation. Любой новый случай
неизвестного результата разрешается только через расширение наблюдаемой
reconciliation-модели и отдельный review.

Официальные источники, по которым фиксируется первый snapshot:

- [авторизация WB API](https://dev.wildberries.ru/docs/openapi/api-information);
- [работа с товарами WB API](https://dev.wildberries.ru/docs/openapi/work-with-products).

Дата исходного review: 2026-08-03. Перед началом реализации Stage 0 повторно
проверяет актуальность методов, DTO, лимитов и token policy.

Принятые product/deployment решения ревизии 2026-08-06:

- v1 запускается как один container/process, один Telegram bot и один Long
  Poller; horizontal replicas отсутствуют;
- singleton advisory lock является fail-fast guard от случайного второго
  process, а не leader election/follower topology;
- permission окончательно проверяется при Finalize; созданный тогда immutable
  grant разрешает асинхронный Start независимо от последующего revoke/delete;
- legacy `wb.card_imports` удаляется forward migration без backfill;
- точный XLSX/BatchSchemaV1 freeze отложен до начала Stage 8 перед parser code;
- transfer semantics `GroupID` отложены до Gate GROUP-0 в начале Stage 11;
- HTTP server/public media topology реализуется вместе с media foundation на
  Stage 14; до этого используется один Telegram Long Polling process;
- каждая WB operation использует ровно один rate-limit `BucketID`; simultaneous
  global + endpoint overlays не входят в v1.

---

## 2. Цель и пользовательский результат

Оператор с отдельным permission загружает один или несколько XLSX-файлов,
исправляет найденные ошибки и нажимает «Готово». Система создаёт immutable batch
и один раз запускает transfer во весь обязательный mutation cohort из
`WB_TRANSFER_CABINETS`, зафиксированный snapshot-ом.

Для каждого исходного товара и каждого кабинета система должна получить один из
объяснимых результатов:

- карточка уже существовала и не изменялась;
- новая карточка создана;
- вариант присоединён к существующей группе;
- запрос явно отклонён WB;
- обнаружен конфликт существующей группы;
- результат остался неоднозначным и требует ручного решения.

Система не обещает exactly-once от внешнего WB API, потому что API не предоставляет
универсальный idempotency key. Она обеспечивает:

- exactly-once создание локального batch и transfer;
- не более одного активного локального mutation-intent на продавца и артикул;
- at-least-once выполнение безопасных read/reconciliation шагов;
- отсутствие автоматического повтора mutation при неизвестной доставке;
- crash-safe восстановление из PostgreSQL;
- сохранение всех решений, request digest и наблюдений для аудита.

---

## 3. Граница плана

### 3.1. Входит

- Content API одного разрешённого WB origin.
- Personal token v1 для собственной/on-premise интеграции.
- Несколько кабинетов, каждый принадлежащий уникальному WB seller.
- Content read и write operations, необходимые transfer.
- Telegram flow для cardimport, transfer и current statistics.
- XLSX с несколькими файлами в одной import session.
- Создание отдельных и объединённых карточек.
- Присоединение отсутствующих вариантов к уже существующей группе.
- Media upload по URL через `/content/v3/media/save`.
- Цена внутри актуального create-card DTO.
- PostgreSQL как source of truth.
- Ровно один container/process `wb-service` на deployment.
- Один Telegram bot работает через Long Polling в этом же процессе.
- Горизонтальные replicas, follower-процессы и multi-active execution в v1
  отсутствуют.
- Startup singleton guard через PostgreSQL advisory lock запрещает случайный
  одновременный запуск второго процесса; второй процесс не становится follower,
  а завершает startup с safe diagnostic.
- Внутри единственного процесса могут работать несколько bounded goroutines,
  но любой WB Content call проходит PostgreSQL admission.

### 3.2. Не входит

- облачный сервис для сторонних продавцов на Personal tokens;
- OAuth/service-token интеграция;
- произвольный HTTP client или произвольные operation path/method;
- multipart media upload;
- отдельный prices/discounts transport;
- stocks, orders, analytics и marketplace API;
- редактирование уже существующих карточек;
- автоматическое исправление неоднозначного remote state;
- удаление карточек;
- multi-region active-active;
- runtime scraping документации WB;
- пользовательское сопоставление «пользователь ↔ часть кабинетов».

Extension seam для будущего `edit` сохраняется, но edit DTO, state machine и
worker в этот план не входят.

---

## 4. Обязательные deployment preconditions

### 4.1. Режим токенов

Personal token используется только программой владельца продавца на собственной
или арендованной инфраструктуре. Если хотя бы один кабинет принадлежит стороннему
клиенту облачного сервиса, rollout останавливается до проектирования разрешённого
WB способа авторизации.

Это не warning, а startup/deployment gate.

### 4.2. Модель доступа

Операция копирует batch во весь явно заданный и полностью ready mutation cohort
из `WB_TRANSFER_CABINETS`. Запускать её может только пользователь с permission:

`cards.transfer_all_cabinets`

Permission проверяется:

- при открытии cardimport flow;
- при каждом изменяющем session callback;
- при Finalize;
- при просмотре его подробных ошибок.

Успешный Finalize создаёт immutable authorization grant вместе с batch и
handoff. `Transfer.Start` является внутренним асинхронным consumer и не повторяет
проверку текущих прав пользователя. Отзыв permission, блокировка или удаление
пользователя после успешного Finalize не отменяют уже подтверждённый batch и его
handoff. Автор и grant сохраняются как nullable reference плюс immutable
Telegram/permission snapshot.

### 4.3. Модель процессов

V1 использует ровно один container/process `wb-service` и обязательный
PostgreSQL admission.

- deployment strategy — stop-before-start/Recreate, без rolling overlap;
- startup до Telegram intake и любых WB calls получает singleton advisory lock
  на dedicated PostgreSQL connection;
- если lock занят, второй процесс fail-fast завершается и не обслуживает
  Telegram, HTTP или workers;
- один процесс запускает один Telegram Long Poller;
- cardimport, transfer и reconciliation выполняются bounded goroutines внутри
  этого процесса;
- потеря singleton connection немедленно отменяет process root context,
  прекращает новые claims и запускает shutdown;
- уже начатая mutation после такой отмены считается unknown и уходит в
  reconciliation после следующего clean startup;
- rate safety при restart хранится в PostgreSQL, а не только в памяти процесса.

Горизонтальные replicas, leader election с follower replicas и два независимых
deployments с одним набором seller tokens в v1 запрещены.

### 4.4. Health/readiness states

Состояния разделены:

- `ProcessLive` — config/schema позволяют процессу работать и обслуживать
  health endpoint после появления HTTP server на media stage;
- `WBReadReady` — singleton guard удерживается и существует valid read registry;
- `TransferMutationReady` — весь target cohort valid/write, bindings/catalog,
  singleton guard и DB готовы; batch-aware gate дополнительно требует media
  infrastructure, только если batch содержит requested media.

`TransferMutationReady` является gate только для нового Start и
`AuthorizeDispatch` полного cohort. Runtime дополнительно вычисляет per-job
gates:

- `TargetDispatchReady(CabinetID)` — current target write-ready, authorization,
  supported request/rate version, singleton guard, admission, Error Feed и нужные
  pre-upload media assets готовы;
- `TargetReconcileReady(CabinetID, persistedVersion)` — target read-ready и
  binary умеет persisted DTO; write capability и media-store readiness не
  требуются;
- local projection/follower/outbox jobs не зависят от WB readiness.

Invalid/read-only target не обязан аварийно завершать Telegram/cardimport
process, но:

- `TransferMutationReady=false`;
- новые Start получают `targets_unavailable`;
- новые dispatch jobs не claim-ятся для неготового target;
- reconciliation/late resolver продолжаются для каждого read-ready target, а
  здоровые targets существующего transfer продолжают работу;
- readiness detail содержит только safe issue codes/counts.

Fatal core config, invalid origin или несовместимая DB schema не позволяют
`ProcessLive`.

---

## 5. Единые термины и идентичности

### 5.1. CabinetID

`CabinetID` — стабильный технический ID из конфигурации. Он:

- не является display name;
- не является `sid`;
- не переиспользуется для другого продавца;
- хранится в DB snapshots;
- допустим в безопасных логах.

### 5.2. Seller identity

`sid` из проверенного token claim является реальной seller identity WB.

Raw `sid`:

- остаётся внутри core registry;
- не передаётся business feature;
- не логируется;
- используется только для derivation core-private SellerKey; rate admission
  partition-ится по SellerKey, raw `sid` в backend не передаётся.

Parser требует UUIDv4 и декодирует `sid` в ровно 16 canonical binary bytes
(RFC 4122 network byte order). Регистр и textual representation UUID не входят
в identity.

Core вычисляет стабильный opaque:

`SellerKey = SHA-256("wb-seller-key-v1\\x00" || sid[16])`

`SellerKey`:

- не имеет публичного string formatter;
- хранится в DB как 32-byte value;
- используется для защиты от CabinetID rebinding;
- используется только infrastructure binding verifier и core;
- не выводится в Telegram и обычные логи.

### 5.3. Однозначность кабинетов

В первой версии действует строгая биекция:

`one CabinetID ↔ one SellerKey`

Registry отклоняет все конфликтующие entries, а не выбирает «первый»:

- duplicate CabinetID;
- duplicate normalized cabinet name;
- один `sid` под несколькими CabinetID;
- один CabinetID, который в DB ранее был связан с другим SellerKey.

Ротация токена выполняется заменой token у того же CabinetID. Одновременно
публиковать два токена одного seller нельзя.

### 5.4. Capability

Registry различает:

- `ContentRead`;
- `ContentWrite`.

Публичные snapshots:

```go
type CabinetInfo struct {
	ID        CabinetID
	Name      string
	CanRead   bool
	CanMutate bool
}

type MutationTargetSnapshot struct {
	Revision string
	Targets  []CabinetInfo
}

func (c *Client) Cabinets() []CabinetInfo
func (c *Client) MutationTargets() (MutationTargetSnapshot, error)
```

`MutationTargets()` возвращает полный ordered cohort из `WB_TRANSFER_CABINETS`
только если каждый entry valid и ContentWrite. Transfer использует только этот
метод. Read-only кабинет никогда не создаёт заведомо падающую mutation task.
SellerKey наружу не возвращается: благодаря persistent биекции global feature
identity безопасно использует CabinetID.

`MutationTargetSnapshot.Revision` вычисляется core как SHA-256 от version tag,
ordered CabinetID, соответствующего SellerKey и capability каждого target.
Display name и token ID в digest не входят: rename и ротация того же seller не
меняют identity cohort. Любая замена seller, порядка или write capability меняет
revision либо делает snapshot unavailable.

`TransferMutationReady` требует минимум один mutation-capable кабинет;
`ProcessLive` может оставаться true без него.

---

## 6. Главные архитектурные решения

1. В процессе существует один immutable `*wb.Client`.
2. Credentials принадлежат core и не попадают в context или feature DTO.
3. Operation catalog закрыт: method, path, query, DTO, bounds, retry и bucket
   нельзя создать или изменить из feature.
4. Core владеет read transport retry и PostgreSQL rate admission; mutation
   имеет zero hidden retries.
5. Feature владеет durable mutation attempts, business rescheduling,
   persistence и reconciliation.
6. После входа в HTTP dispatch mutation никогда не считается безопасной для
   повтора без явного response WB, доказывающего неприменение.
7. Async `200` от upload означает только `submitted`.
8. `200` от media означает только `media_submitted`.
9. Existing-card решение принимается для всей исходной группы, а не отдельно
   для каждого варианта.
10. Подготовка делится на global parsing и per-cabinet preflight.
11. Любой network side effect предваряется durable state commit.
12. Любой worker update защищён lease owner и fencing token.
13. Raw XLSX сохраняется в durable blob storage до начала validation.
14. Vendor code не подвергается скрытому case folding или Unicode folding.
15. Пользовательские записи не владеют жизненным циклом mutation history.
16. Statistics является read model и не запускает retry или mutation.
17. Один singleton process выполняет все WB calls; каждая operation использует
    ровно один `BucketID`, а rate state переживает restart в PostgreSQL.
18. Большой transfer materialize-ится chunked и становится runnable только
    atomic activation.
19. Non-empty media materialize-ится и проходит quota gate до product mutation
    claim.

---

## 7. Целевая схема компонентов

```text
Telegram transport
    |
    +--> authorization
    |
    +--> cardimport application
    |       |
    |       +--> durable blob store
    |       +--> XLSX parser/validator
    |       +--> PostgreSQL repository
    |       +--> immutable batch
    |
    +--> transfer application
    |       |
    |       +--> PostgreSQL work queue, leases, intents
    |       +--> feature ContentGateway
    |       |       |
    |       |       +--> mapper to core catalog DTO
    |       |       +--> shared wb.Client
    |       |
    |       +--> upload/media reconciliation
    |       +--> notification outbox
    |
    +--> statistics read model

shared wb.Client
    |
    +--> cabinet registry / opaque credentials
    +--> immutable Content operation catalog
    +--> prepare and request bounds
    +--> PostgreSQL rate admission by (SellerKey, BucketID)
    +--> per-attempt timeout and trace
    +--> sealed HTTP executor
    +--> decode/classification
    +--> evidence-based retry
    +--> safe logging
```

Направление зависимостей:

```text
transport/telegram -> feature application -> feature ports
feature gateway/wb -> core/transport/wb
core/transport/wb -> stdlib + shared observability only
statistics/source/transfer -> transfer query port
```

Запрещены зависимости:

- core → feature;
- transfer → cardimport repository implementation;
- transfer → Telegram;
- transfer → statistics;
- domain → WB wire DTO;
- feature → raw token;
- feature → `http.Client`;
- statistics → WB.

---

## 8. End-to-end flow

### 8.1. Import

1. Авторизованный оператор открывает import session.
2. Telegram adapter получает file metadata.
3. Файл полностью сохраняется в durable blob store с size и SHA-256.
4. Conditional DB transitions ведут
   `reserved → fetch_queued → fetching → stored → parse_queued`.
5. Validator worker открывает blob по storage key.
6. XLSX проходит structural limits, typed parsing и validation.
7. Valid cards агрегируются в session revision.
8. Любая issue блокирует readiness.
9. Повторная загрузка или удаление файла увеличивает revision.
10. Finalize с idempotency key создаёт immutable batch ровно один раз.

### 8.2. Transfer

1. Transfer создаётся идемпотентно для batch.
2. В одной транзакции сохраняется полный `MutationTargets()` snapshot.
3. Global parse формирует typed internal cards без WB DTO.
4. Для каждого cabinet выполняется catalog/preflight и строится cabinet-specific
   wire payload.
5. Worker атомарно claim-ит global product identities всех vendor codes группы.
6. Уже под persistent claim Cards List повторно определяет authoritative
   состояние целой группы; более ранний advisory read не используется для
   mutation decision.
7. До mutation сохраняются payload digest, group membership, local error-feed
   baseline sequence и opaque WB cursor.
8. Группа выбирает create, add, already-present либо conflict.
9. До HTTP state становится `dispatching`.
10. Любой accepted/unknown upload переходит в reconciliation.
11. Cards List и Cards Error List определяют наблюдаемый outcome.
12. Только созданные этим transfer карточки получают media.
13. Каждый media HTTP response также проходит reconciliation.
14. Intents освобождаются только после доказанного applied/rejected outcome;
    unresolved сохраняет global block.
15. Notification создаётся через transactional outbox.

### 8.3. Statistics

Statistics читает агрегированное состояние transfer из PostgreSQL. Она:

- показывает только текущие operations;
- группирует ошибки по safe code;
- не читает credentials или raw WB body;
- не меняет worker state;
- не вызывает WB.

---

## 9. Конфигурация кабинетов, token parser и registry

### 9.1. ENV schema

```text
WB_API_CABINETS=main,backup
WB_API_CABINET_MAIN_NAME=Основной
WB_API_CABINET_MAIN_TOKEN=<secret>
WB_API_CABINET_BACKUP_NAME=Резервный
WB_API_CABINET_BACKUP_TOKEN=<secret>
WB_TRANSFER_CABINETS=main,backup
```

`WB_API_CABINETS` задаёт полный credential registry. `WB_TRANSFER_CABINETS`
задаёт ordered и обязательный mutation cohort.

Правила:

- ID соответствует `[a-z][a-z0-9_]{0,31}`;
- список не пуст, без пустых элементов и duplicate;
- case normalization для ENV suffix выполняется один раз и проверяется на
  collision;
- name после Unicode-aware trim имеет длину `1..128`;
- raw token не trim-ится: surrounding whitespace является config error;
- token length ограничен compile-time ceiling;
- неизвестные credential-like ENV не выводятся по имени или value;
- диагностируется только safe code и количество orphan keys.

Если `WB_TRANSFER_CABINETS` содержит хотя бы один отсутствующий, invalid,
read-only или binding-mismatched кабинет, transfer readiness становится false.
Нельзя молча запускать transfer на валидном подмножестве.

### 9.2. Правильный порядок сборки

```text
parse non-secret config
→ load bounded raw credential entries
→ structurally decode JWT claims
→ validate declared Personal-token claims/capabilities
→ derive provisional SellerKey
→ detect all duplicate/collisions
→ build unpublished provisional registry/client
→ acquire startup singleton advisory lock or fail startup
→ perform bounded authenticated safe-read probe for every structural candidate
→ verify/insert persistent cabinet bindings only for authenticated identities
→ publish immutable read registry/shared Client from authenticated identities
→ resolve complete mutation cohort or publish safe unready reasons
```

Local JWT decode не является token authentication. Конкретный candidate не
публикуется в read registry и его first binding не записывается до успешного
catalog-defined authenticated probe.
Ошибка одного configured target не удаляет уже authenticated targets из read
registry, но complete mutation cohort остаётся unready.

Probes выполняет единственный процесс только после singleton guard. Режима
ожидания для второго экземпляра нет: он не публикует readiness и завершает
startup. После каждого нового clean startup current tokens probe-ятся заново до
любых Content calls.

### 9.3. Structural JWT validation

Local decode не доказывает криптографическую подлинность token: server WB остаётся
источником истины. Decode нужен для fail-fast capability, expiry и seller routing.

Parser:

- принимает ровно три JWT segments;
- ограничивает encoded token и decoded header/payload;
- использует base64url без внешних online tools;
- запрещает duplicate JSON keys;
- проверяет тип каждого используемого claim;
- проверяет Personal-token claims актуального snapshot;
- требует корректный UUID `sid`;
- требует Content capability;
- вычисляет effective expiry с фиксированным safety margin;
- различает read-only и read-write;
- не сохраняет decoded JSON после сборки credentials;
- никогда не включает token или claims dump в error.

Startup issue codes:

- `missing_name`;
- `missing_token`;
- `invalid_id`;
- `invalid_token_shape`;
- `invalid_claims`;
- `unsupported_token_type`;
- `missing_content_capability`;
- `read_only_target`;
- `expired_or_near_expiry`;
- `duplicate_name`;
- `duplicate_token`;
- `duplicate_seller`;
- `credential_probe_failed`;
- `cabinet_rebound`;
- `orphan_credential_key`.

Для duplicate конфликтов issue присваивается всем участникам.

### 9.4. Persistent seller binding

Infrastructure table:

```sql
wb.cabinet_seller_bindings (
    cabinet_id       text primary key,
    seller_key       bytea not null unique
                     check (octet_length(seller_key) = 32),
    first_seen_at    timestamptz not null,
    last_verified_at timestamptz not null
)
```

Composition root передаёт registry builder интерфейс `SellerBindingVerifier`.
Первый запуск создаёт binding через insert-on-conflict transaction только после
успешного bounded authenticated WB read probe данным token. `401`, invalid
signature, transport uncertainty или crash до probe/transaction не оставляют
binding. Последующие запуски могут заранее сравнить provisional SellerKey с
existing binding для fail-fast, но current token всё равно обязан пройти probe
до `WBReadReady`/`TransferMutationReady`.

Probe использует отдельную catalog operation с read-only semantics, exact
response DTO и PostgreSQL admission. Его response не кэшируется как доказательство
следующего запуска. Binding transaction повторно сравнивает SellerKey, поэтому
два first-boot процесса не могут записать разные identities.

Автоматического rebind нет. Для нового seller создаётся новый CabinetID.

### 9.5. Credentials и auth

`Credentials`:

- immutable;
- не экспортирует token, `sid` или raw claims;
- выбирается только registry по CabinetID;
- добавляет `Authorization: Bearer <token>` только после final URL validation;
- не помещается в context;
- не реализует `fmt.Stringer`;
- redaction tests обязательны.

Stage 0 подтверждает точный wire-format Authorization по актуальному OpenAPI и
smoke test. Если WB snapshot требует raw token без scheme, меняется только sealed
auth middleware и snapshot version, не feature.

Rotation:

1. worker admission останавливается;
2. in-flight steps drain или становятся reconciliation;
3. secret заменяется у того же CabinetID;
4. новый token обязан иметь тот же SellerKey и пройти authenticated probe;
5. процесс корректно завершается;
6. composition root нового процесса строит новый immutable registry и Client;
7. readiness возвращается только после validation.

Hot swap injected `*wb.Client` в v1 отсутствует. Ротация всегда
`drain → restart → rebuild`; это сохраняет один immutable Client и один limiter
realm на lifetime процесса.

---

## 10. Закрытый и версионируемый Content operation catalog

### 10.1. Manifest

Каждая operation является immutable manifest со следующими обязательными полями:

```text
OperationID
CatalogVersion
CanonicalConstructor
OriginID
Method
PathTemplate
QueryPlan
RequestDTOType
RequestPlan
ResponseDTOType
ResponsePlan
AllowedSuccessStatuses
ResponseMode
RequiredCapability
ReadOrMutation
BucketID
RatePolicyEpoch
RetryPolicy
BusinessFollowUp
EvidenceURL
VerifiedAt
```

Нельзя зарегистрировать operation без любого из этих полей. Feature не может
создать operation literal или изменить manifest.

`RequestPlan` до network:

- проверяет exact request DTO type;
- проверяет обязательность body;
- валидирует query grammar;
- материализует path parameters;
- проверяет item/group/variant/media counts;
- проверяет строковые и числовые bounds;
- сериализует immutable body bytes один раз;
- проверяет encoded query, URL и body size;
- вычисляет SHA-256 body digest без логирования body.

Для upload/add/media catalog дополнительно предоставляет `Prepare/Restore`:

- `Prepare` принимает exact typed wire DTO и возвращает canonical JSON bytes,
  digest и catalog version;
- canonical encoder запрещает map-valued wire fields и имеет frozen encoding;
- `Restore` принимает persisted bytes + expected digest/version, bounded
  проверяет hash, strict decode и exact canonical re-encode;
- prepared-mutation ticket executor отправляет эти exact bytes, не выполняя
  новый business mapping и не входя в read `DoJSON`;
- mismatch даёт `CatalogContractError/NotDispatched` и никогда не отправляется.

`ResponsePlan`:

- задаёт exact response DTO type;
- различает empty, JSON object и JSON array;
- ограничивает body;
- декодирует в attempt-local value;
- публикует caller output только после окончательного success;
- запрещает unknown trailing JSON и второй JSON value;
- не раскрывает raw error body.

### 10.2. Обязательный catalog v1

| Constructor | Method | Path | Назначение |
|---|---|---|---|
| `ParentCategories` | GET | `/content/v2/object/parent/all` | родительские категории |
| `Subjects` | GET | `/content/v2/object/all` | предметы |
| `SubjectCharacteristics` | GET | `/content/v2/object/charcs/{subjectId}` | schema характеристик |
| `CardsLimits` | GET | `/content/v2/cards/limits` | seller create limits |
| `Brands` | GET | `/api/content/v1/brands` | бренды предмета |
| `DirectoryColors` | GET | `/content/v2/directory/colors` | цвет |
| `DirectoryKinds` | GET | `/content/v2/directory/kinds` | пол |
| `DirectoryCountries` | GET | `/content/v2/directory/countries` | страна |
| `DirectorySeasons` | GET | `/content/v2/directory/seasons` | сезон |
| `DirectoryVAT` | GET | `/content/v2/directory/vat` | НДС |
| `DirectoryTNVED` | GET | `/content/v2/directory/tnved` | ТНВЭД |
| `CardsList` | POST | `/content/v2/get/cards/list` | remote state |
| `TrashCardsList` | POST | `/content/v2/get/cards/trash` | cards в корзине для capacity |
| `CardsErrorList` | POST | `/content/v2/cards/error/list` | async upload errors |
| `UploadCards` | POST | `/content/v2/cards/upload` | create group |
| `UploadCardsAdd` | POST | `/content/v2/cards/upload/add` | add to group |
| `SaveMediaByLinks` | POST | `/content/v3/media/save` | replace full media set |

`UpdateCards`, `MoveCards`, delete/recover mutations и multipart media не
регистрируются в v1. Read-only `TrashCardsList` обязателен для точного расчёта
create capacity. Mutations добавляются только вместе с использующим их feature
и полным manifest.

`CardsLimits` дополнительно является единственным
`CredentialProbeOperation`: это не второй alias/manifest, а catalog-marked safe
authenticated read purpose с теми же exact DTO/rate rules.

### 10.3. Зафиксированные bounds

Первый checked-in snapshot отражает официальные limits:

- upload: не более 100 request groups;
- объединённая create group: не более 30 variants;
- upload/add: не более 29 новых variants и не более 30 total variants в target;
- create/update body: не более 10 MB official bound;
- Cards List page: не более 100 cards;
- Trash Cards List использует собственный exact cursor/page bound;
- Cards Error List page: не более 100 error batches;
- media: не более 30 images и одного video;
- generic core hard request ceiling остаётся выше operation bound, но не
  ослабляет его.

Для каждого operation точный bound находится в manifest и покрыт boundary tests.
Gateway chunking выбирает минимум:

`min(operation count bound, operation byte bound, configured safety bound)`.

### 10.4. Rate buckets

Каждая operation catalog v1 ссылается ровно на один `BucketID`:

- обычный Content endpoint использует общий `content_common`;
- endpoint, который frozen official snapshot исключает из общего лимита,
  использует только свой отдельный bucket;
- simultaneous global + operation-specific overlays в v1 не моделируются.

Brands/media не получают отдельный bucket «по названию»: они используют
`content_common`, если frozen snapshot прямо не объявляет их исключением.

Physical bucket key:

`(core-private SellerKey, BucketID)`

CabinetID не является limiter partition. Все calls одного seller используют общий
realm.

Manifest содержит один `BucketID`. Все обычные Content methods ссылаются на один
и тот же `content_common` bucket. Exception endpoint получает отдельный bucket
только если frozen source явно исключает его из common limit. Если будущая
документация введёт одновременно действующие global и operation-specific
ограничения, это требует отдельного architecture review и нового rate-policy
contract; runtime v1 не угадывает такое наложение.

Rate snapshot хранит для каждого physical bucket interval, burst, queue bound,
source URL и verified date. `BucketID` стабилен между catalog versions и никогда
не version-ится способом, который создаёт второй seller bucket.

`RatePolicyEpoch` — отдельная deployment-wide версия rate semantics. Один
current conservative policy применяется к calls всех одновременно
поддерживаемых `OperationCatalogVersion`. Изменение wire DTO требует catalog
bump; изменение лимита требует нового RatePolicyEpoch и regression tests, но
эти версии не обязаны совпадать.

Rate-policy rollout:

1. новый binary сначала объявляет поддержку current и next epoch;
2. одна DB migration/coordination transaction lock-ит каждый затронутый bucket,
   сохраняет следующий epoch, не увеличивает available tokens и применяет
   `blocked_until = GREATEST(old,new)`;
3. уменьшение limit действует сразу консервативно; ослабление требует drain
   старого debt/window и отдельного activation;
4. old binary, не поддерживающий active epoch, теряет WB readiness и завершает
   startup;
5. mixed catalog versions продолжают использовать один active rate epoch.

### 10.5. Retry evidence

`RetrySafeRead` разрешён только semantic reads, включая POST reads.

`RetryExplicitNonApplied` у mutation означает не внутренний transport retry, а
разрешение feature создать следующий durable attempt только после конкретного
HTTP response, который snapshot квалифицирует как неприменённый, например
подтверждённый `429`.

После вызова `RoundTrip` mutation transport error без response всегда получает
delivery state `Unknown`. DNS, TLS и connection errors не используются как
доказательство «сервер точно не видел запрос».

### 10.6. Persisted catalog-version compatibility

Binary объявляет `SupportedCatalogVersions`. Перед transfer readiness он читает
distinct catalog versions всех non-terminal prepared/dispatching/reconciling
rows.

Rules:

- binary, не умеющий хотя бы одну active persisted version, не получает
  WB readiness и завершает startup до workers;
- prepared, но ещё не dispatching unit можно атомарно reprepare на current
  version с новым digest;
- после dispatch catalog/request version immutable;
- dispatched intent никогда не отправляется повторно новой version;
- reconciliation adapter/DTO для legacy version сохраняется до исчезновения всех
  active rows этой version;
- удаление legacy manifest требует drain query, migration review и отдельный
  release gate;
- current metadata snapshot не подменяет OperationCatalogVersion.

---

## 11. Граница feature DTO и WB wire DTO

Business слой использует собственные типы:

```text
ImportedCard
PreparedCard
PreparedGroup
RemoteCard
UploadObservation
MediaObservation
```

Только `internal/feature/transfer/gateway/wb` одновременно импортирует feature
contracts и `internal/core/transport/wb/policy/content`.

Mapping:

```text
batch item
→ global PreparedCard
→ per-cabinet PreparedCard
→ feature CreateGroup command
→ adapter mapper
→ catalog UploadCardsRequest
→ catalog Prepare
→ BeginPreparedMutation / ExecutePreparedMutation
→ catalog response/error
→ adapter observation
→ transfer state transition
```

Никакой feature-owned struct не передаётся в exact-type `RequestPlan`.

Gateway v1:

```go
type Target struct {
	CabinetID CabinetID
	Name      string
	Ordinal   int
}

type MutationTargetSnapshot struct {
	Revision string
	Targets  []Target
}

type ContentGateway interface {
	MutationTargets(ctx context.Context) (MutationTargetSnapshot, error)

	GetCatalog(ctx context.Context, target CabinetID) (CatalogSnapshot, error)
	GetCreationCapacity(ctx context.Context, target CabinetID) (CreationCapacity, error)
	FindCards(ctx context.Context, target CabinetID, codes []VendorCode) (CardMatches, error)

	GetErrorTail(ctx context.Context, target CabinetID) (ErrorCursor, error)
	ScanErrorsAfter(
		ctx context.Context,
		target CabinetID,
		after ErrorCursor,
		pageLimit int,
	) (ErrorPage, error)

	PrepareCreate(target CabinetID, request CreateRequest) (PreparedCreateRequest, error)
	PrepareAdd(target CabinetID, request AddRequest) (PreparedAddRequest, error)
	PrepareMedia(target CabinetID, media MediaSet) (PreparedMediaRequest, error)

	BeginPreparedMutation(
		ctx context.Context,
		target CabinetID,
		attemptID MutationAttemptID,
		request PreparedMutationRequest,
		fence ExecutionFence,
	) (DispatchTicket, error)
	ExecutePreparedMutation(
		ctx context.Context,
		ticket DispatchTicket,
		evidence EvidenceSeal,
		fence ExecutionFence,
	) (MutationAttemptResult, error)
}
```

Feature `CabinetID` является собственным opaque string type и маппится 1:1 в
core `wb.CabinetID` только внутри adapter. Snapshot revision проходит boundary
без пересчёта.

`CreateRequest` содержит один или несколько complete `CreateGroup` в пределах
catalog bounds. `AddRequest` содержит ровно один target imtID и одну complete
missing-variants group. Adapter не выполняет скрытое дополнительное batching
после того, как request digest сохранён.

`PreparedCreateRequest`/`PreparedAddRequest`/`PreparedMediaRequest` реализуют
закрытый feature sum type `PreparedMutationRequest` и содержат exact canonical
bytes, digest, operation/catalog/rate-policy versions и bounded metadata, но не
raw credentials. Они сохраняются до dispatch; restore path проверяет digest
перед каждой `NotDispatched` попыткой.

`DispatchTicket` — opaque one-shot value: feature видит только random ticket ID,
AttemptID, admission time и `NotAfter`. SellerKey, BucketID и backend permit
остаются core-private. `EvidenceSeal` содержит только kind и durable evidence
row ID; raw Error List cursor/media state gateway повторно читает из
`MutationEgressStore`.

`FindCards`:

- принимает bounded list;
- внутри adapter выполняет допустимый chunking/query;
- проходит все нужные normal Cards List и Trash Cards List cursor pages;
- применяет exact vendor-code equality client-side;
- возвращает active и trash matches раздельно;
- возвращает duplicate/ambiguous matches отдельно, а не выбирает первый.

`ScanErrorsAfter` не принимает vendor codes как server filter. Correlation
принадлежит transfer и использует persisted cursor, time window и membership.

---

## 12. WB execution pipeline

### 12.1. Уровни

```text
read DoJSON
  → operation middleware, ровно один вызов next
      → overall deadline
      → prepare immutable operation
      → read retry orchestrator
          → PostgreSQL rate admission per attempt
          → attempt deadline
          → sealed HTTP/decode/classification

prepared mutation
  → BeginPreparedMutation: exactly one PostgreSQL admission
  → opaque short-lived ticket bound to AttemptID/body/fence
  → ExecutePreparedMutation: durable evidence/fence checks
  → one egress-start transaction
  → exactly one sealed RoundTrip
  → observe/decode/classification
```

Attempt middleware не управляет retry. Retry middleware не читает body. Raw
RoundTripper wrappers могут наблюдать request/response metadata, но не читают и
не закрывают body.

Mutation path не вызывает обычный `DoJSON` повторно и не проходит второй
admission middleware. Оба paths сходятся только в sealed request/auth/terminal
части.

### 12.2. One-shot invariant

Каждый middleware получает one-shot `next`. Второй вызов:

- выставляет shared invariant-violation flag;
- не выполняет downstream повторно;
- заставляет outer executor вернуть `MiddlewareContractError`, даже если buggy
  middleware проигнорировал ошибку второго вызова;
- покрывается race и adversarial tests.

### 12.3. Request immutability

Prepared operation содержит:

- immutable method/origin/path/query;
- immutable body bytes;
- body digest;
- operation metadata;
- output factory;
- mutation/read class.

На каждый attempt создаётся новый `http.Request` и новый reader над теми же
immutable bytes. Auth header добавляется после clone. Caller request/output не
мутируется до final success.

### 12.4. Response body ownership

Реализуемый контракт на границе `http.Client.Do`:

- `err != nil`: executor не предполагает наличие доступного response body;
- `resp != nil`: attempt executor является единственным владельцем `resp.Body`;
- body читается bounded;
- после чтения выполняется bounded drain только в разрешённых случаях;
- body закрывается ровно один раз;
- middleware выше executor получает только `AttemptResult`, не raw response;
- nil body защищается нормализующим guard, но не является основным control flow.

План не требует от terminal увидеть `response != nil && err != nil`, который
стандартный `http.Client` может скрыть/закрыть при собственной обработке.

### 12.5. Timeout causes

Dependency:

```go
type DeadlineFactory interface {
	WithTimeoutCause(
		parent context.Context,
		timeout time.Duration,
		cause error,
	) (context.Context, context.CancelFunc)
}
```

Typed causes:

- `ErrCallerCanceled`;
- `ErrOverallTimeout`;
- `ErrAdmissionTimeout`;
- `ErrAttemptTimeout`;
- `ErrBackoffInterrupted`;
- `ErrShutdown`.

Classifier использует `context.Cause` и recorded phase. Строка `ctx.Err()` не
используется для выбора feature action.

При совпадающих deadlines действует фиксированный priority:

`caller/shutdown → overall → admission → attempt → backoff`.

Fake clock и barrier tests проверяют каждый tie.

### 12.6. Origin validation

Production origin:

- scheme строго `https`;
- hostname строго allowlisted Content hostname;
- userinfo отсутствует;
- explicit port отсутствует;
- path пустой или `/`;
- `RawPath` пуст;
- query, `RawQuery`, fragment отсутствуют;
- `ForceQuery == false`;
- join path не может сменить origin.

Redirect policy запрещает смену origin и удаляет Authorization до любого
redirect follow. Для Content v1 redirects проще полностью запретить и считать
их protocol error.

### 12.7. Decode и classification

Expected success задаётся exact set в operation manifest, а не правилом «любой
2xx». Для success:

- body bounded;
- JSON strict;
- output attempt-local;
- trailing value запрещён;
- semantic envelope error превращается в typed API error.

Для non-success:

- читается bounded private preview;
- raw body не входит в public error;
- извлекаются только allowlisted code/request ID/status/retry headers;
- secrets и произвольный nested text redacted.

Mutation:

- explicit expected response → `ResponseReceived`;
- explicit rejection response → `ResponseReceived`;
- отмена/admission до вызова HTTP → `NotDispatched`;
- любой error после начала HTTP без response → `Unknown`.

### 12.8. Retry

Core содержит единственный transport retry loop, и он используется только для
reads.

Read:

- может повторять allowlisted transient transport errors;
- может повторять explicit 429/5xx согласно manifest;
- учитывает overall deadline и max attempts.

Mutation:

- один вызов core mutation executor соответствует ровно одному durable feature
  attempt и максимум одному `RoundTrip`;
- core никогда внутренне не повторяет mutation после HTTP response;
- `Unknown` никогда не повторяется;
- evidence-backed non-applied response возвращает typed
  `NewAttemptAllowed`/`ResponseReceived` и bounded retry hint;
- feature создаёт новый immutable attempt только по catalog policy и своему
  durable budget;
- interruption возвращает typed phase/cause, а не строковый wrapper.

Backoff применяет full jitter, server `Retry-After`/rate headers только после
bounded parsing и не выходит за overall deadline.

### 12.9. Rate admission

Core зависит от port:

```go
type AdmissionBackend interface {
	AcquireRead(
		ctx context.Context,
		realm RateRealmKey,
		bucketID BucketID,
		ratePolicyEpoch string,
	) (Permit, error)
	AcquireMutation(
		ctx context.Context,
		realm RateRealmKey,
		bucketID BucketID,
		ratePolicyEpoch string,
		key MutationAdmissionKey,
	) (MutationPermit, error)
	Observe(
		ctx context.Context,
		realm RateRealmKey,
		bucketID BucketID,
		observation RateObservation,
	) error
}
```

`RateRealmKey` является core-private representation SellerKey. Feature его не
получает.

Production backend — PostgreSQL:

- token bucket key `(realm, BucketID)`;
- PostgreSQL clock для refill/deadlines;
- bounded persistent FIFO waiters per physical bucket;
- FIFO sequence per key;
- cancellation/crashed waiter cleanup;
- rate-policy-epoch mismatch fail closed;
- server `429`/headers atomарно обновляют shared `blocked_until`/availability;
- более короткое observation не сокращает более длинный block;
- PostgreSQL unavailable → `AdmissionError/NotDispatched` и zero HTTP calls.

Manifest фиксирует единственный physical bucket, к которому относится response
header или `429`. `Observe` lock-ит и обновляет его; runtime не угадывает scope
по имени header.

Read acquire:

1. Создаёт bounded waiter с monotonic sequence/deadline для одного bucket.
2. Lock-ит bucket row.
3. Refill-ит tokens по DB time и учитывает `blocked_until`.
4. Waiter получает permit только если он FIFO head и capacity есть.
5. Token списывается той же transaction.
6. Cancellation/crash cleanup идемпотентно удаляет waiter.

Mutation acquire дополнительно:

- идемпотентен по AttemptID и никогда не списывает второй permit для того же
  attempt;
- связывает permit с realm/BucketID/operation/body digest/rate epoch,
  singleton process epoch и work fence;
- возвращает opaque ticket с `admitted_at` и коротким immutable `not_after`;
- создаёт durable `rate_limit_permits` row;
- не renew-ится и не refund-ится: expiry до `ClaimAndStart` даёт
  `NotDispatched`, а следующий mutation использует новый AttemptID;
- резервирует весь допустимый send window `[admitted_at, not_after]`
  консервативно: будущие grants рассчитываются от худшего возможного момента
  использования, поэтому поздний send внутри window не создаёт burst.

Core вычисляет
`start_not_after = permit.not_after - WB_API_MUTATION_START_MARGIN`.
`ClaimAndStart` требует `db_now <= start_not_after`; equality/позднее время
отклоняется до egress commit.

Между успешным mutation admission и `RoundTrip` разрешены только bounded DB
checks/commit; WB/Telegram/media-fetch network отсутствует. Error-feed baseline
или media pre-state собираются заранее. Если после ожидания admission evidence
старше своего max-age, permit теряется, attempt становится
`NotDispatched/evidence_stale` через atomic `CancelBeforeStart` и весь cycle
начинается с новым AttemptID.

`ExecutePreparedMutation` вызывает отдельный core port
`MutationEgressStore.ClaimAndStart`. PostgreSQL implementation одной transaction:

```go
type MutationEgressStore interface {
	ClaimAndStart(
		ctx context.Context,
		permit MutationPermit,
		evidence EvidenceKey,
		fence EgressFence,
	) (EgressClaim, bool, error)
	CancelBeforeStart(
		ctx context.Context,
		permit MutationPermit,
		fence EgressFence,
		cause SafeCode,
	) (bool, error)
}
```

1. под DB clock повторно проверяет persisted attempt/evidence, configured
   evidence max-age, body digest, permit binding,
   `db_now <= start_not_after`, singleton process epoch, lease и fence;
2. атомарно помечает permit used и attempt `egress_started` с DB timestamp;
3. создаёт одноразовый random egress token;
4. возвращает `newly_started=true` ровно одному caller.

Только этот caller немедленно входит в sealed `RoundTrip`. Existing claim,
expired permit или stale fence не выполняют HTTP. Core не зависит от feature
package: attempt store — узкий port, а PostgreSQL adapter и transaction manager
подключаются composition root.

`NotDispatched` возможен только если `ClaimAndStart` не commit-ил
`egress_started`. После успешного commit любой timeout/cancel/expiry до или
внутри transport консервативно возвращает `UnknownDelivery` и допускает только
reconciliation; обратного CAS в `NotDispatched` нет.

In-memory backend сохраняется только для unit tests и explicit local development.
`dry-run` и `live` требуют `WB_API_ADMISSION_MODE=postgres`. Automatic fallback
на memory запрещён.

Singleton deployment остаётся orchestration constraint, но rate-limit safety при
restart хранится в PostgreSQL admission, а не зависит от пустого in-memory
limiter после старта нового процесса.

`Observe` является частью attempt, а не best-effort telemetry. Если HTTP
response/headers уже получены, но PostgreSQL observation сохранить нельзя:

- исходные `Delivery=ResponseReceived`, HTTP classification и bounded retry hint
  не теряются;
- core прекращает любые следующие attempts;
- возвращается typed `RateObservationPersistenceError` с original delivery;
- feature сохраняет target/backend block не раньше conservative parsed delay;
- WB readiness запрещает новые calls этого realm/bucket до DB health и
  successful policy refresh.

При `UnknownDelivery` без response observation отсутствует, но исходная
delivery остаётся unknown. Failure между `Observe` и потенциальным следующим
read retry тоже останавливает loop fail closed.

### 12.10. Закрытие Client

Shutdown:

1. singleton process перестаёт claim-ить work;
2. application contexts отменяются typed cause `ErrShutdown`;
3. mutation с начатым HTTP сохраняется как unknown;
4. bounded grace ждёт commits;
5. `CloseIdleConnections` вызывается один раз;
6. secrets не очищаются логированием или dump.

---

## 13. Core error contract и feature action

Каждая runtime error имеет:

```go
type DeliveryState uint8

const (
	NotDispatched DeliveryState = iota
	ResponseReceived
	UnknownDelivery
)

type ClassifiedError interface {
	error
	Code() string
	Delivery() DeliveryState
	RetryHint() RetryHint
}
```

Обязательная mapping table:

| Core outcome | Delivery | Feature action |
|---|---|---|
| request/config/catalog validation | NotDispatched | terminal implementation/config error |
| unknown/unavailable cabinet | NotDispatched | block target; operator action |
| read-only credentials | NotDispatched | invariant violation; block target |
| admission timeout/capacity | NotDispatched | persisted `retry_wait` |
| evidence stale / ticket expired before RoundTrip | NotDispatched | terminal old attempt; rebuild evidence in new attempt |
| caller cancellation before HTTP | NotDispatched | safe reschedule unless user cancellation is terminal |
| catalog-proven terminal business rejection | ResponseReceived | persist normalized rejection; no retry |
| иной HTTP response mutation без доказательства non-applied | ResponseReceived | operation-specific reconciliation |
| catalog-proven non-applied 401 | ResponseReceived | block target; after authenticated rotation create new attempt |
| 403 без catalog-documented product code | ResponseReceived | block target; capability/entitlement operator action |
| catalog-proven non-applied 403 | ResponseReceived | block target; after capability recovery create new attempt |
| evidence-backed 429 | ResponseReceived | terminal old attempt; durable backoff, then new attempt |
| expected upload response | ResponseReceived | `submitted → reconciling_upload` |
| expected media response | ResponseReceived | `media_submitted → media_reconciling` |
| rate observation persistence failure after response | original delivery | preserve response class; block realm/bucket, no next attempt |
| transport/timeout after dispatch | UnknownDelivery | persist uncertain and reconcile |
| decode failure after mutation response | UnknownDelivery | reconcile |
| shutdown after dispatch | UnknownDelivery | reconcile after restart |
| read retry exhausted | n/a | persisted safe retry/backoff policy |

Feature switches on typed code and delivery enum. `errors.Error()` text не
участвует в control flow.

---

## 14. Core runtime configuration

| Environment | Default | Constraint |
|---|---:|---|
| `WB_API_BASE_URL` | `https://content-api.wildberries.ru` | exact allowed origin |
| `WB_API_ADMISSION_MODE` | `postgres` | production/dry-run shared backend |
| `WB_API_ADMISSION_POLL_INTERVAL` | `50ms` | bounded/cancellation-aware |
| `WB_API_MUTATION_TICKET_TTL` | `5s` | short reserved send window |
| `WB_API_MUTATION_START_MARGIN` | `500ms` | permit must retain this window before RoundTrip |
| `WB_API_MUTATION_CLAIM_TIMEOUT` | `2s` | DB evidence/fence/egress transaction |
| `WB_API_TIMEOUT` | `60s` | overall |
| `WB_API_RATE_LIMIT_WAIT_TIMEOUT` | `30s` | ≤ overall |
| `WB_API_ATTEMPT_TIMEOUT` | `20s` | ≤ overall |
| `WB_API_RETRY_MAX_ATTEMPTS` | `3` | `1..5`, reads only |
| `WB_API_RETRY_BASE_DELAY` | `250ms` | positive |
| `WB_API_RETRY_MAX_DELAY` | `5s` | ≤ overall |
| `WB_API_MAX_SERVER_RETRY_DELAY` | `1m` | bounded |
| `WB_API_MAX_QUERY_SIZE` | `16KiB` | ≤ hard ceiling |
| `WB_API_MAX_URL_SIZE` | `32KiB` | > query bound |
| `WB_API_MAX_REQUEST_BODY_SIZE` | `16MiB` | catalog op may be smaller |
| `WB_API_MAX_RESPONSE_BODY_SIZE` | `32MiB` | ≤ hard ceiling |
| `WB_API_MAX_ERROR_BODY_SIZE` | `4KiB` | private preview |
| `WB_API_MAX_LIMITER_KEYS` | `1024` | bounded |
| `WB_API_MAX_WAITERS_PER_LIMITER` | `256` | bounded |
| `WB_API_CREDENTIAL_EXPIRY_WARNING` | `168h` | `1h..720h` |

Дополнительные cross-field checks:

- operation body bound ≤ global body bound;
- all durations overflow-safe;
- retry max delay ≤ overall;
- max server delay покрывает зарегистрированные intervals, но не превышает
  hard safety ceiling;
- number of unique sellers × buckets ≤ limiter key limit;
- base URL и catalog origin совпадают;
- production/dry-run admission mode равен `postgres`;
- v1 принимает только production Personal-token contract из раздела 3;
  sandbox/test-token режим не конфигурируется.

«Target cohort не пуст» и «каждый target authenticated/write-ready» являются
`TransferMutationReady` checks, а не fatal `CoreConfig`/`ProcessLive` checks.

---

## 15. Cardimport domain и durable file pipeline

### 15.1. Ответственность

Cardimport:

- принимает несколько XLSX;
- durable сохраняет файл до parsing;
- выполняет только WB-независимую validation;
- агрегирует данные детерминированно;
- создаёт immutable batch;
- доставляет `batchID` через outbox.

Cardimport не:

- выбирает кабинеты;
- вызывает WB;
- создаёт WB wire DTO;
- отображается как transfer statistics;
- создаёт edit batch, пока edit consumer не реализован.

### 15.2. State machines

Session:

```text
collecting
  ├── Cancel ───────────────> cancelled
  └── Finalize transaction ─> finalized ── outbox delivered ─> dispatched
```

`ready` является вычисляемым predicate/view, а не отдельным промежуточным state.

File:

```text
reserved → fetch_queued → fetching → stored → parse_queued → parsing → valid
                          |                                      └──> invalid
                          └──> retry_wait(fetch) → fetch_queued
                                                     parsing ──> retry_wait(parse)
                                                                   └→ parse_queued

retry budget exhausted → processing_failed

non-terminal ── session cancel ─> abandoned
any file state + collecting session ── RemoveFile ─> removed
```

Worker claim добавляет `lease_owner`, `lease_until` и `fencing_token`. После
restart:

- expired `fetching` возвращается в `fetch_queued`;
- `stored/parse_queued` валидируется;
- expired `parsing` возвращается в `parse_queued`;
- commit старого worker с устаревшим fence отклоняется.

`RemoveFile` lock-ит session/file, требует `collecting` и owner permission,
увеличивает file fence, ставит `removed`, исключает его active parse run из
session aggregate, rebuild-ит items/issues и увеличивает revision одной
transaction. Row/parse evidence сохраняются для audit, blob освобождается только
по retention. Поздний fetch/parse commit удалённого файла всегда отсекается
fence/status predicate.

### 15.3. Durable FileSource

Ports:

```go
type FileSource interface {
	Open(ctx context.Context, telegramFileID string) (io.ReadCloser, FileInfo, error)
}

type ImportBlobStore interface {
	Put(ctx context.Context, fileID FileID, src io.Reader, maxBytes int64) (BlobMeta, error)
	Open(ctx context.Context, key BlobKey) (io.ReadCloser, BlobMeta, error)
	DeleteAfterRetention(ctx context.Context, key BlobKey) error
}
```

V1 `ImportBlobStore` хранит bounded XLSX в PostgreSQL `bytea` в отдельной table.
Интерфейс позволяет позже перейти на object storage без изменения domain.

Порядок:

1. Зарезервировать file row `reserved/fetch_queued`.
2. Claim перевести его в `fetching` и получить lease token/fence.
3. Вне DB transaction открыть Telegram file.
4. Ограниченно прочитать stream, одновременно вычисляя SHA-256.
5. Одной transaction сохранить blob и conditional перевести file в
   `stored/parse_queued` только при:
   - session всё ещё `collecting`;
   - file всё ещё `fetching`;
   - lease token/fence совпадают.
6. Validation всегда открывает уже durable blob.

Cancel atomically ставит `abandoned` и увеличивает fence. Если worker потерял
race шага 5, его blob не связывается с file и помечается orphan для retention GC.
Два reclaimers не могут оба активировать результат.

Если процесс падает до шага 5, reclaimer повторяет fetch по persisted Telegram
file ID. Если Telegram больше не отдаёт файл, file получает structured
`source_unavailable`, а session остаётся неготовой.

Blob удаляется только после terminal session и retention period. Активный,
finalized или не доставленный batch никогда не теряет blob из-за cleanup.

### 15.4. Idempotency входящих файлов

Unique keys:

- `(session_id, telegram_file_unique_id)`;
- `(session_id, telegram_message_id)`.

Повторная Telegram delivery возвращает существующий file view. Она не создаёт
второй blob или validation job.

Имя, MIME и extension являются hints. Обязательна проверка ZIP/XLSX signature и
структуры workbook.

### 15.5. Atomic parse activation

Parsing не пишет напрямую authoritative `card_import_file_cards`/aggregate.

1. Worker создаёт `card_import_parse_runs(status=staging)` с file ID,
   schema version, lease fence и expected session revision.
2. В staging rows записывает typed cards/issues и stable ordinals.
3. После полного parse сохраняет row count и checksum, переводя run в
   `complete_unactivated`.
4. В short activation transaction:
   - lock session и file;
   - проверить `session=collecting`;
   - проверить file `parsing`, lease token/fence и complete checksum;
   - назначить file `active_parse_run_id`;
   - детерминированно перестроить session aggregate из active runs всех
     non-removed files;
   - пересчитать authoritative issues/counters;
   - увеличить session revision;
   - поставить file `valid` или `invalid`;
   - commit.

Если activation transaction падает, старый aggregate и active pointers остаются
целиком. Staging run не влияет на readiness. Validators разных files
сериализуются session lock-ом; второй rebuild обязательно видит activated result
первого, поэтому lost update отсутствует.

---

## 16. XLSX schema, parsing и identity

### 16.1. BatchSchemaV1

Точный `card-batch/v1` freeze выполняется в начале Stage 8 непосредственно перед
реализацией XLSX parser. До Gate XLSX-0 перечисление ниже является обязательным
inventory, но не фиксирует окончательные типы, units и requiredness:

- source `GroupID`;
- seller `vendorCode`;
- category/subject name;
- title, description, brand;
- dimensions и weight;
- sizes;
- barcodes как strings;
- price в minor units или exact decimal по frozen wire contract;
- named characteristics;
- ordered media URLs;
- source locations.

В Gate XLSX-0 отдельно решаются price representation, поддержка или явное
удаление `Wholesale.Enabled`/`Wholesale.Quantum`, необходимость `kizMarked`,
header aliases и поведение каждого отсутствующего/невалидного поля. Parser code
не начинается до этого решения.

После прохождения Gate XLSX-0 каждое поле имеет frozen header aliases,
requiredness, type, units и bounds. Mapping хранится в одном schema package, а
не в Telegram handler.

### 16.2. Parser safety

До чтения rows проверяются:

- compressed file size;
- total decompressed size;
- number of ZIP entries;
- workbook/sheet count;
- rows, columns и total cells;
- shared strings size;
- formula/cell text size;
- merged cells и supported worksheet;
- maximum issues retained.

Формулы не вычисляются. Если schema field содержит formula без cached literal,
создаётся blocking issue.

`parseInt`, `parseDecimal` и `parseBool` возвращают typed issue. Silent fallback
в `0`, empty или `false` запрещён.

### 16.3. VendorCodeIdentityV1

Чтобы не слить разные WB articles без официального доказательства равенства,
v1 использует lossless identity:

1. cell должен быть valid UTF-8 string;
2. empty и control characters запрещены;
3. leading/trailing Unicode whitespace не удаляется, а создаёт blocking issue;
4. case folding отсутствует;
5. Unicode NFC/NFKC folding отсутствует;
6. canonical key равен точным UTF-8 bytes принятого value;
7. DB equality использует deterministic `COLLATE "C"`;
8. `normalization_version = 1` сохраняется в session, batch, transfer и intent.

Хранятся:

- `vendor_code_raw`;
- `vendor_code_key`;
- `normalization_version`.

Для advisory/hash индексов можно дополнительно хранить SHA-256 key, но equality
всегда подтверждается полным value, а не одним hash.

Remote ownership identity version-independent: один exact
`vendor_code_key` в одном CabinetID всегда адресует одну persistent row,
независимо от версии parser/normalization metadata. Будущая normalization,
которая способна изменить wire value или признать два старых value равными,
не включается обычным catalog bump: сначала выполняются offline
alias/collision migration, drain всех active intents и отдельный identity gate.

Barcode всегда string. Leading zero сохраняется. Преобразование barcode через
integer/float запрещено.

### 16.4. GroupID

Cardimport не принимает transfer-решение о смысле `GroupID`. Он сохраняет
optional `group_id_raw` как bounded lossless string, не преобразует его в
integer/float, не удаляет leading zero и не отправляет в WB.

Перед реализацией transfer aggregate в начале Stage 11 обязателен Gate GROUP-0.
Он фиксирует:

- означает ли empty GroupID singleton group по vendorCode;
- допустимые символы/whitespace/control rules;
- поведение одного vendorCode с разными GroupID;
- совместимость subject/category внутри одной группы;
- максимальный размер группы и stable group ordinal;
- точный transfer grouping key и conflict codes.

До Gate GROUP-0 transfer aggregate/group claims не реализуются. Это явный
отложенный product decision, а не скрытая parser normalization.

### 16.5. Multi-file merge

Aggregate строится независимо от порядка завершения workers:

`file stable ordinal → worksheet ordinal → source row`.

Для exact одинакового vendorCode:

- одинаковые scalar values совместимы;
- одно пустое и одно заполненное значение дают заполненное;
- два разных непустых значения создают blocking conflict;
- размеры deduplicate по frozen size identity;
- barcode не может принадлежать двум vendor codes;
- characteristics deduplicate по canonical name и exact typed value;
- media deduplicate со stable first-seen order;
- конфликт содержит обе source locations.

User card count равен числу exact VendorCodeIdentityV1, а не числу XLSX rows.

### 16.6. Structured issues

```go
type ValidationIssue struct {
	Code          IssueCode
	Scope         IssueScope
	FileID        FileID
	Filename      string
	Sheet         string
	Row           int
	Column        int
	Field         string
	VendorCode    string
	Details       map[string]string
	Blocking      bool
	SchemaVersion string
}
```

Renderer строит безопасный текст из `Code + allowlisted Details`. Raw parser
error, formula, cell payload и stack trace пользователю не передаются.

В v1 все issues blocking.

---

## 17. Readiness и идемпотентный Finalize

### 17.1. Readiness predicate

Под row lock повторно проверяется:

```text
session.status == collecting
non_removed_files_count > 0
fetching_or_parsing_or_retry_wait_non_removed_files == 0
all non-removed files == valid
aggregate_cards > 0
blocking_issues == 0
aggregate_revision == expected_revision
purpose has a production consumer
caller still has cards.transfer_all_cabinets
```

UI-показ кнопки «Готово» не является авторизацией или гарантией readiness.

### 17.2. Finalize command

```go
type FinalizeCommand struct {
	SessionID        SessionID
	ExpectedRevision int64
	IdempotencyKey   string
	ActorID          int64
}
```

Порядок принципиален:

1. Открыть transaction.
2. Прочитать только session ownership/access metadata и проверить, что caller —
   тот же session actor и всё ещё имеет `cards.transfer_all_cabinets`. До этого
   existence finalization/batch не раскрывается.
3. Найти existing finalization по `(session_id, idempotency_key)`.
4. Если она существует и её actor совпадает, вернуть тот же batch независимо от
   текущего session status/revision.
5. Иначе lock session и повторить ownership/permission check.
6. После получения lock повторно проверить existing finalization с тем же key.
7. Если она появилась с тем же actor, вернуть её batch.
8. Проверить collecting, revision и readiness.
9. Создать batch, items, finalization, immutable authorization grant и outbox
   одной transaction.
10. Перевести session в `finalized`.

Insert finalization использует unique constraint и
`INSERT ... ON CONFLICT ... RETURNING batch_id` как последнюю защиту. Таким
образом, два concurrent вызова с одним key оба получают один batch.

Дополнительный unique `(session_id, finalized_revision)` не позволяет двум разным
keys финализировать одну revision.

Stale callback с другим key получает `stale_revision`. Повтор того же callback
авторизованным исходным actor получает исходный batch. Revoked/deleted actor
получает `forbidden` без раскрытия BatchID при пользовательском retry, но уже
созданный authorization grant и durable handoff продолжают работу независимо от
текущего состояния пользователя.

### 17.3. Immutable batch

Batch checksum:

```text
SHA-256(
  schema_version
  || normalization_version
  || purpose
  || ordered canonical item encoding
)
```

Canonical encoding имеет frozen field order и number/string representation.

После commit:

- repository не предоставляет update item API;
- DB trigger запрещает UPDATE/DELETE batch и item до отдельной retention procedure;
- source locations и original values доступны для аудита;
- transfer читает batch paginated в stable position order.

### 17.4. Transactional handoff

В Finalize transaction создаётся `card_import_handoffs`.

Dispatcher:

```text
claim with lease/fence
→ BatchConsumer.Start(batchID)
→ consumer INSERT ... ON CONFLICT(batch_id)
→ mark handoff delivered
→ session dispatched
```

Delivery at-least-once, transfer creation exactly-once по `UNIQUE(batch_id)`.

---

## 18. PostgreSQL schema cardimport

### 18.1. Tables

`wb.card_import_sessions`:

- ID, purpose, status, revision;
- nullable `author_user_id ON DELETE SET NULL`;
- `author_telegram_id_snapshot`;
- chat/message snapshot;
- counters;
- schema/normalization versions;
- timestamps.

Partial unique запрещает две collecting/finalizing sessions одного actor/purpose.

`wb.card_import_files`:

- session ID;
- Telegram file ID, unique ID, message ID;
- original filename, safe filename;
- MIME/declared/actual size;
- SHA-256, blob key;
- status, attempt, next attempt, removed-at/by snapshot;
- active parse run ID;
- lease owner/until/fence;
- row/card/issue counters;
- timestamps.

`wb.card_import_file_blobs`:

- file ID primary key;
- bounded `bytea`;
- actual size;
- SHA-256;
- created/retention timestamps.

`wb.card_import_file_cards`:

- parse run ID and file ID;
- source sheet/row/stable ordinal;
- raw vendor code and exact key;
- raw `group_id_raw` lossless string;
- typed payload JSONB;
- source locations;
- schema version.

`wb.card_import_parse_runs`:

- file ID;
- monotonically increasing parse version;
- status `staging|complete_unactivated|active|superseded`;
- schema/normalization version;
- expected session revision;
- worker lease fence;
- row/issue counts and checksum;
- timestamps;
- unique `(file_id, parse_version)`;
- at most one active run per file.

`wb.card_import_parse_issues`:

- parse run/file reference;
- source coordinate;
- safe structured issue;
- immutable staging evidence.

`wb.card_import_items`:

- session ID;
- stable position;
- raw/exact vendor code;
- source `group_id_raw` lossless string;
- merged typed payload;
- validation status.

`wb.card_import_issues`:

- session/file/item and parse-run references;
- safe code/scope;
- row/column/field;
- structured bounded details;
- blocking.

Incomplete/unactivated parse runs and staging rows удаляются только retention GC,
если они не являются `active_parse_run_id` и session не выполняет activation.
Active run не удаляется раньше всего owning batch/import retention graph.
Session aggregate ссылается на набор active runs non-removed files; constraint
«at most one active run» применяется per file, не per session.

`wb.card_batches` и `wb.card_batch_items`:

- immutable header/items;
- source session and revision;
- nullable author reference plus snapshot;
- schema/normalization version;
- checksum;
- stable positions.

`wb.card_import_finalizations`:

- session ID;
- finalized revision;
- idempotency key;
- batch ID;
- actor snapshot;
- permission code и immutable `authorized_at` snapshot;
- created time.

`wb.card_import_handoffs`:

- batch/purpose;
- status;
- attempts/next attempt;
- lease/fence;
- safe error code.

### 18.2. Delete policy

Ни одна active/import/transfer table не использует `ON DELETE CASCADE` от user.
User delete:

- ставит nullable FK в NULL;
- сохраняет actor/Telegram snapshot;
- не удаляет file, batch, transfer, intent, observation или notification.

Internal child tables могут cascade только при отдельной audited retention delete
корневого terminal aggregate. Runtime API такого delete не предоставляет.

### 18.3. Legacy schema cutover

Legacy `wb.card_imports` из migration `000001` удаляется без backfill и без
переноса старого JSON payload. Новая реализация использует только session/file/
batch schema этого плана.

Cutover выполняется отдельной forward migration:

1. deployment остаётся с `TRANSFER_MODE=disabled`;
2. migration явно `DROP TABLE wb.card_imports`;
3. создаёт новые cardimport tables;
4. не пытается восстановить legacy rows при rollback;
5. исправляет migration policy с учётом того, что текущий `000001.down.sql`
   ссылается на не создаваемую `wb.access_requests`.

Потеря legacy rows является принятым product decision. Перед применением в
production-like DB migration выводит только safe row count для операционного
подтверждения, без payload dump.

---

## 19. Cardimport application contracts

```go
type Service interface {
	Begin(ctx context.Context, cmd BeginCommand) (SessionView, error)
	ReserveFile(ctx context.Context, cmd ReserveFileCommand) (FileTicket, SessionView, error)
	RemoveFile(ctx context.Context, cmd RemoveFileCommand) (SessionView, error)
	Finalize(ctx context.Context, cmd FinalizeCommand) (BatchID, error)
	Cancel(ctx context.Context, cmd CancelCommand) error
}

type ValidationQueue interface {
	EnqueueStoredFile(ctx context.Context, fileID FileID) error
}

type BatchReader interface {
	GetBatch(ctx context.Context, batchID BatchID) (BatchHeader, error)
	ListBatchItems(
		ctx context.Context,
		batchID BatchID,
		afterPosition int,
		limit int,
	) ([]BatchItem, error)
}

type BatchConsumer interface {
	Start(ctx context.Context, batchID BatchID) (TransferID, error)
}
```

`ProcessFile(ticket, io.Reader)` отсутствует из application service. Live reader
заканчивается на durable ingestion adapter; domain worker всегда обрабатывает
persisted file ID.

---

## 20. Transfer domain и target snapshot

### 20.1. Start

`BatchConsumer.Start(batchID)`:

1. Сначала ищет existing transfer по batchID и немедленно возвращает его, не
   читая текущий registry/cohort.
2. Для отсутствующего transfer читает immutable Finalize authorization grant.
   Отсутствующий/повреждённый grant является contract error; текущие user rows и
   permissions повторно не читаются.
3. Только затем получает complete
   `MutationTargetSnapshot`.
4. Если cohort не ready, не создаёт никакой transfer row: handoff получает
   `retry_wait/targets_unavailable`.
5. В короткой transaction повторно проверяет existing и выполняет
   `INSERT transfer ... ON CONFLICT(batch_id) DO NOTHING RETURNING id` со
   state `initializing`.
6. Для нового row в той же transaction сохраняет immutable target snapshot,
   Finalize authorization grant,
   batch checksum/schema/normalization/catalog/target-set versions,
   expected item/target/work-unit counts, initialization checksum и unique
   initializer job.
7. При conflict читает победивший aggregate и гарантирует наличие его
   initializer job; новый cohort никогда не читает и не подставляет.
8. Initializer идемпотентно читает immutable batch по stable-position chunks,
   upsert-ит transfer items, item-target rows, source groups/members и двигает
   persisted checkpoint с lease/fence.
9. После полной expansion он сверяет expected counts, contiguous positions,
   target snapshot и rolling checksum.
10. Одной короткой activation transaction меняет `initializing → created`,
   создаёт initial work jobs и делает aggregate доступным mutation workers/UI.

Transient DB/process failure оставляет `expanding` и возобновляется с checkpoint.
Deterministic count/checksum/schema mismatch одной transaction переводит
initialization и transfer в `initialization_failed`, ставит
`attention_required`, создаёт operator notification/rollout alert и zero work
jobs. Duplicate Start возвращает тот же failed TransferID/status.

Automatic rebuild failed-contract запрещён. После исправления binary/schema
отдельная audited `RestartInitialization` допустима только если не было
activation/dispatch: она сохраняет тот же immutable batch/target snapshot,
увеличивает initialization generation и начинает checkpoint с нуля. Старые
incomplete rows остаются generation-scoped и удаляются retention GC; activation
видит только current generation.

Crash оставляет либо отсутствующий transfer, либо durable `initializing`
aggregate, который продолжит тот же snapshot/checkpoint. До activation:

- mutation/preparation jobs не claim-ятся;
- progress/final counters не публикуются как полный transfer;
- operator видит только безопасный статус «инициализация»;
- incomplete rows не считаются нарушением active-aggregate cardinality.

Duplicate Start всегда возвращает тот же TransferID, в том числе во время
initialization, и никогда не «достраивает» его по новому target snapshot.

Snapshot target содержит:

- CabinetID;
- display name snapshot;
- stable ordinal из `WB_TRANSFER_CABINETS`;
- target-set revision;
- capability snapshot `ContentWrite`.

Он не содержит token, raw `sid` или SellerKey.

Изменение ENV после Start не меняет cohort существующего transfer. Если target
временно недоступен, его work ждёт восстановления; target не удаляется из
denominator и не заменяется другим.

### 20.2. Transfer status

```text
initializing
  ├→ initialization_failed ── audited same-snapshot restart → initializing
  └→ created
       ├→ preparing
       ├→ running
       ├→ reconciling
       ├→ blocked_dependencies ── readiness restored → previous runnable phase
       ├→ completed
       ├→ completed_with_errors
       └→ manual_review
```

Status является authoritative projection, а не свободной последовательностью:

| Условие | Transfer status |
|---|---|
| header/snapshot созданы, chunk expansion ещё не активирован | `initializing` |
| deterministic initialization contract/checksum failure | `initialization_failed` |
| expansion активирован, initial jobs materialized | `created` |
| есть runnable/in-flight preparation, mutation ещё не началась | `preparing` |
| есть runnable/in-flight dispatch/media local work | `running` |
| есть upload/media reconciliation | `reconciling` |
| нет runnable work, есть recoverable target/dependency block до следующего dispatch | `blocked_dependencies` |
| все item-target terminal success/already/media-structural | `completed` |
| все item-target доказанно terminal, есть prep/rejection/preflight-conflict/partial-with-full-evidence/media-rejected | `completed_with_errors` |
| нет safe automatic work/recoverable blocks и есть upload/media unresolved, ambiguous correlation или post-mutation inconsistent remote state | `manual_review` |

Приоритет projection:

`initializing → reconciling → running → preparing → blocked_dependencies → manual_review → completed_with_errors → completed`.

Target-wide problem может возникнуть из любой nonterminal pre-dispatch phase.
Другие targets продолжают работу; `blocked_dependencies` показывается только
когда operation больше не имеет runnable/in-flight work. Reasons typed:
target `401`/capability/catalog, PostgreSQL admission/Error Feed DB, media
quota/store/public endpoint и другие явно recoverable pre-dispatch dependency
gates. Восстановление конкретного dependency возвращает только затронутую work
в прежнюю safe phase.

`attention_required` — отдельный orthogonal flag с bounded safe reason codes.
Он может быть true одновременно с `running`, `reconciling` или
`blocked_dependencies`. `manual_review` выбирается только когда нет runnable/in-flight
работы и нет recoverable blocked target; это quiescent, но не финально
`completed` состояние. Late read-only resolver или audited operator command
может изменить projection, не стирая историческую uncertainty.

Сочетание «одна unit unresolved + другой target/resource recoverably blocked»
даёт `blocked_dependencies` с `attention_required=true`. Оно не создаёт terminal
notification и после восстановления target продолжает прежнюю safe phase.

`completed`/`completed_with_errors` требуют terminal outcome для каждой
item×target и успешную доставку или durable постановку final notification.
`manual_review` получает отдельное durable attention notification, но не
замораживает final counters как terminal.

### 20.3. Per-transfer dispatch authorization

Transfer сохраняет immutable `execution_mode_at_start` и
`dispatch_authorized_at/by`.

- Start в global `live` создаёт row с явной persisted authorization из immutable
  Finalize grant. Текущая permission пользователя при Start не проверяется.
- Start в `dry-run` создаёт навсегда non-dispatchable row до отдельной команды.
- Переключение ENV `dry-run → live` не обновляет существующие rows и само по себе
  не делает ни один job runnable.
- Dry-run сохраняет только non-owning proposals: он не ставит
  `active_group_claim_id`/`active_intent_id`, не получает remote-group claim и
  не создаёт upload/media intent. Поэтому abandoned dry-run не блокирует live
  transfers.

Для подготовленного dry-run существует audited command:

```go
type AuthorizeDispatchCommand struct {
	TransferID          TransferID
	ExpectedPayloadRoot string
	ExpectedTargetSet   string
	ExpectedRevision    int64
	IdempotencyKey      string
	ActorID             int64
}
```

Command только durable ставит idempotent `authorization_requested`. Затем
singleton process выполняет authorization job:

1. требует `cards.transfer_all_cabinets`;
2. lock-ит transfer;
3. idempotency-first возвращает прежний authorization;
4. требует dry-run state без dispatched intents;
5. сверяет submitted Merkle/root digest всех proposals, target-set revision и
   transfer revision;
6. повторно проверяет current TransferMutationReady;
7. persistent claim-ит product identities; для add получает remote-group claim;
8. уже под claims повторяет Cards List, Trash/capacity и другие dynamic reads;
9. заново строит exact canonical requests и proposal root;
10. если remote state/version/digest изменились, безопасно освобождает
    pre-dispatch claims, сохраняет `proposal_stale` с новой revision и требует
    нового operator approval;
11. если всё совпало, одной transaction создаёт mutation intents, сохраняет
    authorization и dispatch jobs.

Worker claim для create/add/media всегда требует persisted
`dispatch_authorized_at IS NOT NULL`. Global live mode является дополнительным
kill switch, а не authorization старых rows.

---

## 21. Global и per-target preparation

### 21.1. Global preparation

Один раз на batch item:

- проверить BatchSchemaV1;
- перевести exact decimal/units во внутренние typed values;
- определить source group;
- собрать ordered characteristics и sizes;
- построить media source manifest;
- вычислить `desired_card_digest` без WB IDs.

Global preparation не вызывает WB и не создаёт wire DTO.
Media source manifest на этом шаге содержит только validated source descriptors;
content digests появляются после bounded asset import.

### 21.2. Per-target preparation

Для каждого target отдельно:

1. Получить/обновить catalog snapshot через этот target.
2. Однозначно разрешить category → subjectID.
3. Получить characteristic schema.
4. Получить только реально используемые directories.
5. Проверить brand для subject с полной pagination.
6. Привести named characteristics к exact IDs/types.
7. Проверить required/maxCount/unit/value rules.
8. Получить Cards Limits, полностью пагинировать normal Cards List и Trash Cards
   List и вычислить seller creation capacity по frozen official formula.
9. Построить target-specific feature command.
10. Через WB adapter построить exact catalog wire DTO.
11. Serialize/check bounds и сохранить immutable payload digest.

Даже если WB metadata кажется глобальной, результат сохраняется на
`item×target` или `group×target` гранулярности.

`OperationCatalogVersion` описывает wire contract и не является версией remote
metadata. Metadata cache хранит отдельные:

- `MetadataSnapshotID`;
- `FetchedAt`;
- response digest;
- TTL не более `15m` для subjects/characteristics/directories/brands.

Cache может переиспользовать response только по key:

`(CabinetID, OperationCatalogVersion, OperationID, ParametersDigest)`

Cache miss/error одного seller не делает payload других sellers универсальным.
Использованный MetadataSnapshotID/digest сохраняется с preparation. Dynamic
capacity и existing-card reads не cache-ятся между transfers.

### 21.3. Preparation outcomes

- `prepared`;
- `subject_not_found`;
- `subject_ambiguous`;
- `characteristic_missing`;
- `characteristic_invalid`;
- `brand_not_found`;
- `directory_value_invalid`;
- `card_limit_exceeded`;
- `payload_too_large`;
- `target_temporarily_unavailable`;
- `catalog_contract_error`.

Data errors terminal для item×target. Operational read errors получают bounded
`retry_wait`. Catalog contract error блокирует rollout/target, а не маскируется
как пользовательская XLSX issue.

### 21.4. Price

Stage 2 фиксирует наличие и точный wire type актуального поля
`sizes[].price` create/add DTO. Способ чтения и представления цены в XLSX/
BatchSchemaV1 решается позднее в Gate XLSX-0 перед parser code. Отдельный
discounts-prices client не создаётся.

Если official snapshot больше не принимает price в create/add, catalog contract
фиксирует это без runtime guessing, а Gate XLSX-0 принимает соответствующее
product/schema решение. Runtime «попробовать, если поле поддерживается» запрещено.

---

## 22. Persistent product identity и global mutation ownership

### 22.1. Product identity

```sql
wb.product_identities (
    cabinet_id            text not null,
    vendor_code_key       text collate "C" not null,
    first_normalization_version integer not null,
    last_normalization_version  integer not null,
    vendor_code_hash      bytea not null
                          check (octet_length(vendor_code_hash) = 32),
    state                 text not null,
    active_group_claim_id bigint null,
    active_intent_id      bigint null,
    nm_id                 bigint null,
    imt_id                bigint null,
    subject_id            bigint null,
    generation            bigint not null,
    version               bigint not null,
    primary key (cabinet_id, vendor_code_key)
)
```

Строка переживает завершение transfer. Она не позволяет новому transfer забыть
предыдущий unresolved outcome.

States:

- `reserved`;
- `remote_present`;
- `mutation_pending`;
- `created`;
- `rejected`;
- `blocked_uncertain`;
- `remote_missing`;
- `remote_conflict`.

`blocked_uncertain` сохраняет global ownership и запрещает новую mutation
generation.

State gate действует даже при `active_intent_id IS NULL`:

- `created` и `remote_present` никогда не переходят в create/add только по одному
  absent Cards List read;
- сначала persisted nmID/vendor проверяются bounded повторными normal и Trash
  Cards List reads;
- card в trash даёт `remote_conflict/card_in_trash`;
- stable absence даёт `remote_missing` и operator action;
- `remote_missing`, `remote_conflict` и `blocked_uncertain` блокируют новую
  automatic mutation до audited resolution;
- `rejected` разрешает новую generation только в новом явно запущенном transfer.

### 22.2. Atomic group claim

Все identities source group блокируются в порядке:

`CabinetID → vendor_code_key COLLATE "C"`.

До persistent mutation claim разрешён advisory Cards List read. Если он
показывает возможный create/add, requested media assets materialize-ятся,
quota-reserve-ятся и получают immutable content digests; только затем
вычисляется `desired_group_digest` и выполняется atomic claim. Media failure
поэтому не удерживает product ownership и не допускает upload. Уже под claim
Cards List/Trash read обязательно повторяется и только он authoritative.
Advisory all-present path всё равно lock-ит identities перед записью
`remote_present` и уступает существующему owner claim.

В одной short transaction:

- upsert identity rows;
- проверить active/unresolved intent и persistent state gate;
- создать или присоединиться к persistent `product_group_claim`;
- увеличить fencing/version;
- записать полную source-group membership.

Network под row locks не выполняется.

Если второй transfer встречает group claim:

- с тем же `desired_group_digest` — становится follower и ждёт его outcome;
- с другим digest — ждёт resolution и затем получает existing/conflict;
- при `blocked_uncertain` — получает `global_identity_blocked`;
- сам WB mutation не вызывает.

После claim выполняется authoritative Cards List preflight. Только затем:

- `group_membership` продолжает содержать все source variants;
- `mutation_intent_members` содержит только variants, реально отправляемые;
- для create это все отсутствующие variants;
- для add это только отсутствующие variants;
- existing/`remote_present` identities никогда не получают active_intent_id add;
- follower связан с group claim/outcome, но не становится mutation member.

`desired_group_digest` не равен wire request digest. Он вычисляется как
domain-separated `SHA-256` над versioned, length-prefixed canonical value,
которое включает:

- digest algorithm/schema и normalization versions;
- CabinetID и ordered exact `vendor_code_key` membership;
- весь target-specific desired semantic card payload без remote IDs;
- ordered media manifest digest, включая явное «media не запрошено».

В claim хранятся digest, canonical byte length и bounded canonical desired
value. Follower разрешён только после byte-for-byte equality canonical value,
а не по одному hash. Create/add request digest хранится отдельно, потому что
operation kind и target imtID определяются более поздним remote preflight.
Разные media manifests поэтому не становятся followers: второй transfer ждёт
outcome и затем получает existing/conflict без неявного media replacement.

Follower — durable relation, а не только ожидание в памяти:

- row identity — claim + follower transfer/group; nullable owner intent
  заполняется только если authoritative decision реально создаёт intent;
  desired digest, `last_applied_outcome_version` и state хранятся всегда;
- каждый authoritative owner outcome увеличивает monotonic outcome version;
- owner transaction сохраняет outcome/per-member identity projection и ровно
  один unique paged fan-out job/checkpoint, но не обновляет unbounded followers;
- fan-out worker читает followers stable-ID pages не больше
  `TRANSFER_FOLLOWER_FANOUT_PAGE` и каждой bounded transaction применяет
  projection/wake events, двигая fenced checkpoint;
- replay применяет только version больше `last_applied_outcome_version`;
- full rejection, created, per-member partial, remote conflict и unresolved
  fan-out-ятся явно;
- follower никогда не создаёт upload или media intent и не вызывает WB;
- после owner media outcome follower получает тот же media projection; до этого
  остаётся `follower_waiting_media`.

Recovery sweeper materialize-ит пропущенный follower job/event из durable
owner outcome version. Он не выполняет mutation.

Active followers per claim ограничены
`TRANSFER_MAX_FOLLOWERS_PER_CLAIM`. При заполнении новый transfer получает
recoverable `follower_queue_full` и повторяет authoritative read после owner
resolution, не создавая mutation или unbounded waiting row.

### 22.3. Mutation intent

```text
intent ID
CabinetID
product group claim ID
generation
operation kind: create | add
catalog/request schema versions
desired group digest
request body digest
exact ordered membership
target imtID for add
delivery state
state
dispatch/reconcile timestamps
deadline
safe terminal code
```

Media не является третьим видом этого intent. После получения nmID media
использует отдельный authoritative `media_intents` state machine из раздела 27.
Две модели не дублируют ownership друг друга.

После `dispatching` request schema, body digest, membership и target imtID
immutable.

Intent generation — одно logical desired group mutation решение. Один physical
create request может упаковать несколько group intents, поэтому
`mutation_dispatch_attempts` принадлежит request, а не одному intent:

- upload attempt имеет ровно один `transfer_upload_request_id` и одну или
  несколько явных child intent/unit links;
- media attempt имеет ровно один `media_intent_id`;
- attempt не может одновременно быть upload и media.

Каждый request-level attempt хранит:

- AttemptID и ordinal;
- exact request/body/operation/rate-policy binding;
- Error List baseline либо media pre-state evidence reference;
- permit/ticket ID и validity;
- process-epoch/lease/fence snapshot;
- `egress_started_at` и one-shot egress token;
- typed delivery/response class/non-applied evidence;
- immutable terminal state.

Один attempt выполняет максимум один `RoundTrip`. Новый upload attempt создаётся
только если для всех связанных child intents предыдущий shared attempt:

- доказанно `NotDispatched` до `RoundTrip`; либо
- получил catalog-proven `ResponseReceived/non_applied` с конкретным evidence
  code (`429` и только явно listed `401/403` cases), persisted retry directive и
  не исчерпанным durable budget.

`401`/catalog-proven non-applied `403` сначала блокирует этот target. После
authenticated token/capability recovery новая попытка использует новый
AttemptID, permit, evidence baseline и egress claim. Generic `403`, accepted
response, unknown delivery, decode failure after response и любой response без
non-applied proof запрещают новый dispatch и идут в reconciliation/manual
policy.

Exact correlated business rejection терминален для этого transfer и не
повторяется. Audited operator resolution создаёт новую generation только после
remote inspection и явного release старой ownership, а не переиспользует
attempt.

Окончание reconciliation timeout не является доказательством отсутствия эффекта.

Инварианты:

```text
round_trips_per_attempt <= 1
possibly_applied_attempts_per_generation <= 1
new_attempt_after_response => catalog_proven_non_applied
upload_attempt_intent_count == upload_request_unit_count
```

### 22.4. Release policy

| Outcome upload intent | Product identity |
|---|---|
| `NotDispatched` | intent остаётся active; новый immutable attempt может безопасно вернуться в prepared |
| catalog-proven non-applied 401/403/429 | старый attempt terminal; target/backoff восстанавливается, затем возможен новый attempt той же generation |
| applied/created | active intent очищается, identity хранит nmID/imtID и `created` |
| already present до mutation | mutation intent не создаётся; identity `remote_present` |
| exact correlated full rejection | active intent очищается, identity `rejected`; тот же transfer не retry |
| partial remote с полным evidence | каждая identity получает фактический created/rejected state; автоматического retry нет |
| remote conflict | active intent очищается только если HTTP mutation не была unknown; identity `remote_conflict` продолжает блокировать новую mutation |
| unresolved/unknown/ambiguous | active intent сохраняется, identity `blocked_uncertain` |

«Transfer terminal» само по себе не является условием release. Решение принимается
только по этой таблице.

---

## 23. Group-level existing-card policy

### 23.1. Reconciliation unit

Основная единица — `(transfer target, source group)`. Per-item preflight не может
самостоятельно решить create/already-present.

Normal и Trash Cards List проходят полную pagination и дают exact match каждого
vendor code.

### 23.2. Decision table

| Наблюдаемое состояние | Решение |
|---|---|
| Все variants отсутствуют и identities не имеют prior remote/blocked state | `CREATE_GROUP` через UploadCards |
| Хотя бы один exact vendor code найден в trash | `CARD_IN_TRASH_CONFLICT`, без mutation |
| Remote read пуст, но identity ранее `created/remote_present` | `REMOTE_MISSING`, без mutation |
| Все существуют, один `imtID`, ожидаемый subject | `ALREADY_PRESENT` |
| Часть существует, существующие имеют один `imtID` и ожидаемый subject, итог ≤ 30 | `ADD_TO_GROUP` для отсутствующих |
| Все/часть существуют в разных `imtID` | `GROUP_CONFLICT` |
| Subject хотя бы одной существующей карточки отличается | `SUBJECT_CONFLICT` |
| Add превысит 30 variants | `GROUP_CAPACITY_CONFLICT` |
| Несколько remote cards соответствуют одному exact key | `REMOTE_IDENTITY_CONFLICT` |

`MoveCards` автоматически не вызывается.

Для `ALREADY_PRESENT`:

- nmID/imtID сохраняются;
- existing payload/media не перезаписываются;
- outcome явно говорит, что equality полного content не доказана;
- global identity становится `remote_present`.

Для `ADD_TO_GROUP`:

- target imtID сохраняется до dispatch;
- request membership содержит только отсутствующие variants;
- existing variants остаются untouched.
- до final add decision worker получает persistent
  `remote_group_claim(CabinetID, imtID)`;
- после получения claim Cards List по imtID повторяется и capacity/subject
  пересчитываются;
- один imtID имеет максимум один active/unresolved add intent, даже если vendor
  sets двух transfers не пересекаются;
- claim удерживается до resolved add outcome; unresolved продолжает блокировку.

### 23.3. Batching

```text
target
→ operation kind
→ subject
→ immutable group unit
→ bounded HTTP request
```

- Одна source group не режется между create requests.
- Один add request относится к одному target imtID.
- Create request может содержать несколько complete groups.
- Учитываются одновременно group count, variants/group, serialized bytes и
  core ceiling.
- Gateway никогда не принимает slice больше documented feature bound.
- До dispatch сохраняются request-level row и дочерние group units.

---

## 24. Upload journal и state machine

### 24.1. Group-target states

```text
pending
→ preparing
→ waiting_intent
→ preflight
→ already_present                         terminal
→ conflict                                terminal
→ prepared_create | prepared_add
→ attempt_evidence_ready
→ admission_wait
   ├── NotDispatched/stale evidence ─> retry_wait ─> new attempt
   └── admitted ─> dispatching/egress_started
                    ├── proven non-applied ─> retry_wait|target_blocked ─> new attempt
                    ├── explicit terminal rejection ────────────────> rejected
                    ├── accepted response ──────────────────────────> reconciling_upload
                    ├── unknown delivery ───────────────────────────> reconciling_upload
                    └── crash/lease loss ───────────────────────────> reconciling_upload

reconciling_upload
→ created
→ rejected
→ partial_remote
→ remote_conflict
→ unresolved
```

`partial_remote`, `remote_conflict` и `unresolved` не запускают автоматический
повтор.

### 24.2. Dispatch barrier

Перед каждым create/add request:

1. Проверить current target dispatch-readiness, authorization, singleton process
   epoch/lease/fence.
2. Materialize через catalog Prepare и сохранить exact bounded canonical body,
   digest и versions.
3. Одной transaction создать immutable AttemptID/ordinal и связать с ним request,
   все child group units и соответствующие intents.
4. Довести seller Error List feed до tail; сохранить на attempt
   `baseline_feed_seq`, opaque cursor, DB tail-observed time, expected membership
   и immutable `EvidenceSeal`.
5. Вызвать `BeginPreparedMutation`. Только core маппит CabinetID в SellerKey и
   ровно один раз вызывает PostgreSQL mutation admission.
6. Admission failure завершает attempt как `NotDispatched`. Успех возвращает
   body/attempt-bound opaque ticket с коротким `NotAfter`.
7. Сразу после admission предварительно проверить freshness baseline, fresh
   process epoch,
   work fence и overall deadline. При stale evidence/permit/fence вызвать
   `CancelBeforeStart`: одна transaction проверяет отсутствие egress, помечает
   permit cancelled и attempt `NotDispatched`. Только успешный cancel разрешает
   новый attempt; permit не refund-ится и HTTP отсутствует.
8. Вызвать `ExecutePreparedMutation(ticket,evidence,fence)`. Core восстанавливает
   exact bytes и через `MutationEgressStore.ClaimAndStart` одной PostgreSQL
   transaction:
   - под DB clock повторно проверяет evidence max-age,
     ticket/digest/authorization/fences;
   - атомарно переводит attempt, request, все child units и intents в
     `egress_started/dispatching`;
   - помечает permit used;
   - записывает `egress_claim_token`, `egress_started_at` и
     `start_not_after`.
9. Только caller с `newly_started=true` немедленно выполняет один sealed
   `RoundTrip`. Недостаточный validity margin отклоняется внутри
   `ClaimAndStart` до commit как `NotDispatched`. После commit даже expiry до
   transport классифицируется `UnknownDelivery`; existing claim никогда не
   dispatch-ится повторно.
10. Одной conditional transaction применить typed delivery/result ко всем child
    units. Catalog-proven non-applied response завершает старый attempt и может
    создать новый после durable backoff; accepted/unknown/other response
    допускают только reconciliation/terminal policy.
11. Failure сохранения rate observation после response сохраняет исходную
    delivery, блокирует новые calls и не запускает новый attempt.

Network между admission и RoundTrip отсутствует. Если evidence устарел за время
ожидания permit, attempt и permit сознательно теряются, feed/pre-state
обновляются, и только новый AttemptID проходит новый cycle.

Crash до `egress_started` становится `NotDispatched` только после проверки
durable permit state/expiry и невозможности zombie вызвать `ticket.Do`. Crash
после `egress_started` считается unknown и recovery выполняет только
reconciliation, даже если фактически HTTP не успел начаться.

Expiry sweeper выполняет тот же fenced `CancelBeforeStart`. Он никогда не
переписывает attempt, где egress token уже существует.

Инварианты:

```text
round_trips_per_attempt <= 1
possibly_applied_attempts_per_generation <= 1
all_request_children_share_attempt_id
```

### 24.3. Item-target projection

Item-target states являются projection group outcome:

- `already_present`;
- `submitted`;
- `created`;
- `upload_rejected`;
- `group_conflict`;
- `partial_remote`;
- `upload_unresolved`;
- `media_pending`;
- `media_not_requested`;
- `media_not_applicable`;
- `media_reconciling`;
- `done`;
- `done_no_media_requested`;
- `media_preparation_rejected`;
- `media_preparation_failed`;
- `group_blocked_by_media_preparation`;
- `media_quota_blocked`;
- `media_rejected`;
- `media_unresolved`;
- `created_late_without_media`.

Для group `partial_remote` projection выполняется per member в одной transaction:

- найденный Cards List variant → `created → media_pending`;
- variant с exact correlated error → `upload_rejected`;
- variant без полного evidence → `upload_unresolved`.

Group-level `partial_remote` не копируется слепо во все member rows.

Projection обновляется в той же transaction, что authoritative group/intent
observation. Counters никогда не считаются из памяти worker.

---

## 25. Durable CardsErrorList feed

### 25.1. Контракт

Gateway предоставляет cursor pages:

```go
type ErrorCursor struct {
	UpdatedAt time.Time
	BatchUUID string
}

type ErrorBatchPage struct {
	Batches []ErrorBatch
	Next    bool
	Cursor  ErrorCursor
}

type ErrorBatch struct {
	BatchUUID          string
	UpdatedAt          time.Time
	SubjectID          int64
	BatchVendorCodes   []VendorCode
	RejectedVendorCodes []VendorCode
	MembershipEvidence MembershipEvidence
	Violations         []SafeViolation
}
```

Request содержит real WB cursor/order. Server-side filter по vendor codes не
существует.

### 25.2. Feed storage

`wb.error_feed_checkpoints`:

- CabinetID primary key;
- opaque WB cursor `updated_at + batch_uuid`, используемый только для следующего
  API request;
- `next_feed_seq`, увеличиваемый под lock checkpoint row;
- lease/fence;
- status;
- last successful poll.

`wb.error_batches`:

- logical CabinetID + batchUUID identity;
- first-seen local `feed_seq`;
- latest updatedAt/content digest;
- full `batch_vendor_codes` membership;
- `rejected_vendor_codes` derived only from error entries;
- membership evidence level `complete|rejected_only|unknown`;
- first-seen time;
- normalized subject;
- bounded normalized violations;
- original raw body отсутствует.

`wb.error_batch_versions` сохраняет append-only versions:

- CabinetID + batchUUID + updatedAt + content digest unique;
- version feed sequence;
- full batch membership и rejected subset;
- first-seen time.

Изменение existing batchUUID после baseline не превращает старую identity в
новую request correlation.

Poller:

- читает ascending;
- использует bounded page;
- проходит до `next=false`;
- применяет небольшой cursor overlap;
- deduplicate по batchUUID;
- назначает новые `feed_seq` в порядке serialized per-CabinetID ingestion;
- сохраняет batches и новый checkpoint одной transaction;
- сериализуется per CabinetID.

Raw WB cursor является opaque continuation token. Код копирует пару
`(updatedAt, batchUUID)` в следующий request, но не сравнивает UUID
лексикографически и не выводит из неё локальный causal order.

### 25.3. Baseline

Перед mutation admission feed обязан быть `ready`. Под короткой seller-scoped
coordination:

1. Дренировать feed до текущего tail.
2. Сохранить `baseline_feed_seq = max(feed_seq)`, raw tail cursor,
   `tail_observed_at` и expected membership в новый attempt.
3. Commit immutable EvidenceSeal.

Coordination не удерживается весь период reconciliation.
После admission evidence обязан быть не старше
`TRANSFER_ERROR_BASELINE_MAX_AGE`. Иначе HTTP не выполняется, attempt становится
`NotDispatched/evidence_stale` и цикл начинается заново.

### 25.4. Correlation

Error batch является candidate только если:

- `first_seen_feed_seq > baseline_feed_seq`;
- batchUUID не существовал в локальном feed на момент baseline;
- batch updatedAt строго позже baseline cursor updatedAt;
- с учётом frozen clock uncertainty доказуемо
  `event_lower_bound(batch.updatedAt, TRANSFER_ERROR_FEED_CLOCK_SKEW) > attempt.egress_started_at`;
- subject совпадает;
- `rejected_vendor_codes` является subset expected mutation membership, а при
  `complete` — также subset batch membership;
- для add оба sets относятся только к добавляемым variants;
- batchUUID ещё не связан с другой group unit.

Membership gate имеет две допустимые формы:

- `complete`: `batch_vendor_codes == expected_mutation_vendor_set`;
- `rejected_only`: batch сам по себе не strong; он может стать strong только
  вместе с post-egress Cards List observation, когда rejected subset и remote
  found set образуют точное непересекающееся partition всего expected set.

`unknown` никогда не даёт strong correlation.

Equal timestamp с baseline, existing/pre-baseline batchUUID или changed version
старого UUID никогда не являются strong evidence: они дают stale/ambiguous
observation. Stage 0 фиксирует UUID lifecycle по official fixture; если WB
переиспользует UUID между requests, такая выдача остаётся `unresolved`.

Stage 0 фиксирует, позволяет ли response восстановить полную
`batch_vendor_codes` membership отдельно от rejected subset. Если snapshot
содержит только rejected entries, adapter ставит `rejected_only` и использует
только full-partition rule выше. Если даже rejected identity нельзя получить
однозначно, evidence `unknown` остаётся только диагностическим.

После Cards List observation strong correlation требует:

```text
remote_found_vendor_set
UNION
rejected_vendor_codes
==
expected_mutation_vendor_set
```

и sets не пересекаются.

Для полностью rejected group rejected set должен точно совпадать со всей mutation
membership.

Результат:

- 0 candidates — продолжить polling;
- 1 strong candidate — сохранить batchUUID и normalized violations;
- больше 1 — `correlation_ambiguous → unresolved`;
- только partial/intersection evidence — продолжить до deadline, затем
  `unresolved`.

Один upload HTTP request может содержать несколько groups; correlation ведётся
на group unit, потому что WB возвращает error batch для соответствующего
`variants` array.

### 25.5. External writer condition

Deployment обязан быть единственным automated writer для target vendor codes на
период transfer. Если другой сервис или человек создаёт те же vendor codes после
baseline, WB не даёт универсального request ID для абсолютной корреляции.

При обнаруженной неоднозначности система выбирает `unresolved`, а не ложный
success/rejection.

---

## 26. Upload reconciliation

### 26.1. Sources of evidence

Reconciler объединяет:

- paginated Cards List exact matches;
- persisted Error List feed после baseline;
- исходный remote preflight;
- immutable membership/subject/target imtID;
- exact attempt `egress_started_at`, catalog и rate-policy versions.

### 26.2. Decision table

| Cards List | Correlated errors | Outcome |
|---|---|---|
| Все expected найдены в одном ожидаемом `imtID` | любые stale игнорируются | `created/applied` |
| Ни одной нет | exact errors для всех | `rejected` |
| Часть найдена | exact errors ровно для остальных | `partial_remote` |
| Часть найдена | нет полного evidence | `unresolved` после deadline |
| Найдены в другом subject/imtID | любое | `remote_conflict` |
| Ничего и errors нет | нет | продолжать polling |
| Несколько strong error candidates | ambiguous | `unresolved` |

Для add «ожидаемый imtID» — persisted target imtID. Для create все variants
должны получить один новый imtID.

### 26.3. Timing

WB snapshot документирует async visibility до 30 минут. Defaults:

- initial delay: `3s`;
- exponential read polling с jitter до `30s`;
- normal reconciliation deadline: `35m`;
- late resolver interval: `5m`;
- late resolver retention: `24h`.

Deadline не разрешает mutation retry. Он переводит transfer unit в `unresolved`
и оставляет global identity `blocked_uncertain`.

Late resolver может записать `applied_late` или `rejected_late` и отправить
correction notification. Первоначальный uncertain event остаётся в audit log.

### 26.4. Late-created media policy

Если upload становится `applied_late` уже после перехода transfer в
`manual_review`:

- item получает `created_late_without_media` с фактическими nmID/imtID;
- automatic media intent не создаётся;
- отправляется correction notification;
- transfer history остаётся manual-review/late-resolved;
- media добавляется только будущей отдельной audited repair/edit operation,
  которой нет в v1.

Late resolver является read-only и никогда незаметно возобновляет mutation flow.

---

## 27. Media preparation, submission и reconciliation

### 27.1. Immutable media assets

Внешняя XLSX URL недостаточно надёжна: содержимое может измениться или исчезнуть
до того, как WB его скачает. Поэтому v1 вводит `MediaAssetStore`:

```go
type MediaAssetStore interface {
	Import(ctx context.Context, sourceURL string, limits AssetLimits) (Asset, error)
	PublicURL(asset Asset, retainUntil time.Time) string
	RetainUntil(ctx context.Context, asset Asset, until time.Time) error
}
```

V1 использует PostgreSQL-backed content-addressed blob и public streaming
endpoint:

`GET /public/wb-media/v1/{asset-id}/{delivery-token}`

- delivery token — base64url HMAC-SHA256 над version, asset ID и retention
  deadline;
- DB хранит asset ID, deadline и signing-key version, но не raw token;
- signing key rotation сохраняет старую verify key до окончания retention;
- route и token полностью redacted в access logs/traces;
- directory listing отсутствует;
- endpoint поддерживает bounded GET/HEAD и Range для video;
- Content-Type берётся из validated magic MIME, а не user input;
- response запрещает content sniffing;
- asset доступен только до retention deadline;
- endpoint включается только на exact `MEDIA_PUBLIC_BASE_URL`.

Public handler не держит DB lock во время network stream. До отправки headers
он получает bounded process slot/byte budget, в короткой transaction проверяет
HMAC/deadline и `SELECT ... FOR SHARE` полностью materialize-ит immutable blob
(либо exact bounded Range) в process-owned buffer, затем commit и только после
этого пишет response. GC `FOR UPDATE` не может удалить row до завершения этого
snapshot read; после materialization delete безопасен для уже открытого stream.
HEAD читает metadata без blob. Поэтому durable stream lease/refcount не нужен.

Transfer preparation:

1. До fetch атомарно резервирует per-card/per-transfer/global asset count и
   byte quota. `Content-Length` является только hint; streamed hard limit
   применяется независимо от него.
2. Проверяет только HTTPS URL без credentials.
3. Выполняет SSRF-safe fetch:
   - allowlisted scheme;
   - DNS/IP private, loopback, link-local и metadata ranges запрещены;
   - каждый redirect повторно валидируется;
   - redirect count bounded;
   - DNS rebinding защищается pinned resolved public address;
   - size/time bounded.
4. Проверяет magic MIME, dimensions, format и video/image limits.
5. Сохраняет immutable content-addressed blob и transactionally переводит
   reservation в committed physical/logical usage с учётом deduplication.
6. Получает stable unauthenticated HTTPS URL, доступный WB.

Fetch/write concurrency и суммарные in-flight bytes bounded. Reservation
release идемпотентен при validation failure/cancel/crash; sweeper освобождает
только expired reservation с fencing token. Global store high watermark
отключает новые reservations и делает media readiness false, но не удаляет
referenced blobs.

Все non-empty media manifests source group должны быть materialized до того,
как upload intent группы становится dispatchable. Поэтому quota/store outage не
создаёт новую WB card без ожидаемого media:

| Preparation result | Durable action до upload |
|---|---|
| media не запрошено | pre-upload `media_not_requested`; media intent не создаётся |
| все assets materialized | upload может стать dispatchable |
| deterministic unsafe URL/type/dimensions/limit error | `media_preparation_rejected`, group upload не выполняется |
| transient fetch/store error | bounded `media_preparation_retry_wait`, zero WB mutation |
| quota/high-watermark unavailable | recoverable `media_quota_blocked`, zero WB mutation до capacity/operator action |

После исчерпания transient retry budget результат становится явным
`media_preparation_failed` и требует operator action; worker не переводит job в
безымянный `dead` и не выполняет upload.

Create/add group атомарен: deterministic failure одного requested member
переводит его в `media_preparation_rejected`, остальные mutation members — в
`group_blocked_by_media_preparation`, а group — в terminal
`media_preparation_failed`. Transient/quota block удерживает всю group в
recoverable blocked state. Ни один partial group upload не выполняется.

Public asset name unguessable/content-addressed, directory listing запрещён.
Asset сохраняется минимум до media terminal outcome плюс семь суток.

Один content-addressed asset может использоваться многими manifests/targets.
`RetainUntil` выполняет:

`retain_until = GREATEST(current_retain_until, requested_retain_until)`

GC удаляет blob только когда одновременно:

- retention deadline плюс safety window прошёл;
- нет manifest reference из initializing/nonterminal transfer group,
  pre-upload preparation, dry-run proposal, pending authorization, follower,
  media intent или undelivered notification/outbox;
- conditional delete version совпал.

Удаление blob/storage state идемпотентно. Завершение первого target никогда не
сокращает retention для остальных.
Создание/продление любой такой reference transactionally применяет
`retain_until = GREATEST(...)`. Dry-run, ожидающий authorization дольше исходной
retention, сохраняет assets до terminal/stale proposal retention.

Reference release и quota accounting:

- logical per-card/per-transfer usage уменьшается ровно один раз при release
  последней owning logical reference, через unique usage-ledger key;
- shared physical bytes остаются charged global store, пока существует хотя бы
  одна reference или retention/public stream;
- PostgreSQL GC transaction lock-ит asset/version, повторно проверяет zero live
  references/streams и deadline, удаляет `bytea`, записывает unique physical
  release ledger event и уменьшает global physical usage атомарно;
- crash/replay до или после GC commit не double-decrement-ит usage;
- high-watermark readiness восстанавливается только из committed usage counters,
  а не из предварительной оценки filesystem/row count.

Reference lifetime всегда bounded:

- pre-dispatch/media-preparation reference истекает через
  `MEDIA_PREUPLOAD_REFERENCE_TTL`; если work всё ещё blocked, manifest становится
  `media_preparation_stale`, refs release-ятся, а перед будущим dispatch assets
  импортируются/проверяются заново;
- dry-run proposal и pending authorization имеют
  `TRANSFER_PROPOSAL_TTL`; expiry делает proposal `stale_expired`, освобождает
  refs и требует нового prepare + operator approval;
- follower после durable attach хранит semantic/content digests, но освобождает
  собственные duplicate asset refs; lifetime обеспечивает owner;
- upload reconciliation держит refs до normal deadline. При `unresolved` refs
  release-ятся, потому late-created policy запрещает automatic media;
- media intent держит refs до reconcile deadline плюс
  `MEDIA_ASSET_RETENTION_AFTER_TERMINAL`, затем release независимо от
  manual-review lifetime;
- audit/history навсегда может хранить digests, sizes, manifest и outcome, но не
  обязан хранить blob bytes.

Expiry/release jobs fenced и idempotent. Никакой `manual_review` или abandoned
proposal не pin-ит physical blob бессрочно.

Если infrastructure не может предоставить stable public asset URL, media gate
не проходит; использование произвольных изменяемых source URL в live mode
запрещено.

### 27.2. Manifest

Immutable manifest хранит:

- ordered asset IDs; delivery URLs детерминированно материализуются при request
  build из asset ID, deadline и signing-key version;
- ordered asset digests;
- image count;
- video presence/position;
- schema version;
- manifest digest.

Media применяется только к карточке, созданной/добавленной этим transfer.
`already_present` карточка не получает неявную замену media.

Поскольку `media/save` заменяет весь media set, request всегда содержит полный
desired set.

Перед admission `PrepareMedia` материализует delivery URLs, canonical body,
digest, catalog/rate versions и URL expiry в immutable
`media_upload_requests` row. `Restore` перед каждым attempt проверяет exact
bytes/digest. Новый signed-body version допустим только после доказанного
`NotDispatched/non_applied` outcome; unknown delivery навсегда фиксирует
использованный body и разрешает только reconciliation.

### 27.3. Atomic upload-to-media handoff

Authoritative upload outcome и media continuation фиксируются одной conditional
transaction:

1. сохранить nmID/imtID и upload outcome;
2. применить per-member product identity/release policy;
3. обновить owner projection/outcome version и создать unique bounded follower
   fan-out checkpoint/job;
4. для каждого созданного member с non-empty materialized manifest создать
   unique `media_intent(cabinet_id,nm_id,generation)` и media job;
5. для empty manifest записать `done_no_media_requested` без intent, увеличить
   media-outcome version и создать media fan-out checkpoint;
6. commit upload counters и outbox events.

Для terminal upload member без созданной карточки та же projection transaction
ставит `media_not_applicable`; `done_no_media_requested` никогда не появляется
до наличия nmID/imtID.

Ветка late resolver отличается и находится в этой же transaction boundary:
`applied_late` после unresolved/manual-history записывает
`created_late_without_media`, обновляет identities, создаёт follower fan-out
checkpoint и correction outbox, но создаёт zero media intents/jobs.

Crash не может оставить `created` item без одного из
`media_intent`/`done_no_media_requested`/явного pre-upload media error.
Recovery invariant repair создаёт отсутствующий job только из уже существующего
durable `media_pending` intent; он не синтезирует новый intent из догадки и не
вызывает второй upload.

Любой terminal media outcome атомарно фиксирует monotonic media-outcome version
и unique fan-out checkpoint. Follower projections применяются последующими
bounded fenced page transactions из раздела 22.

### 27.4. State machine

```text
media_pending
→ media_preflight
→ media_evidence_ready
→ media_admission_wait
   ├── NotDispatched/stale pre-state ─> media_retry_wait/new attempt
   └── admitted ─> media_dispatching/egress_started
                    ├── proven non-applied ─> media_retry_wait|target_blocked
                    ├── any other HTTP response ─> media_reconciling
                    ├── unknown delivery ────────> media_reconciling
                    └── crash/lease loss ────────> media_reconciling

media_reconciling
→ media_verified_structural
→ media_rejected
→ media_unresolved
```

Даже `200` всегда означает submitted, потому что WB может вернуть success и не
загрузить ни одного файла.

Media использует тот же `BeginPreparedMutation → opaque ticket →
ExecutePreparedMutation` contract и тот же append-only attempt journal, что
upload. До admission сохраняется fresh Cards List media pre-state EvidenceSeal;
после admission он обязан быть не старше
`TRANSFER_MEDIA_PRESTATE_MAX_AGE`. Один media attempt имеет один permit,
один egress token и максимум один RoundTrip. Внутренних mutation retries нет.

### 27.5. Structural verification

До dispatch сохраняется Cards List media pre-state. После dispatch нужны минимум
два одинаковых observations через stability interval.

`media_verified_structural` требует:

- expected image count;
- expected video presence;
- stable observed order/slots;
- изменение относительно pre-state для новой карточки;
- observation после dispatch.

Cards List возвращает WB CDN derivatives. Равенство source URL или content hash
после WB transformation не проверяется и не заявляется.

В outcome сохраняется:

`verification_level = structural`

Если product требует byte-exact proof, текущий API не позволяет поставить
`verified`: state остаётся manual/unresolved.

Media reconcile defaults:

- poll interval с jitter: `10s..60s`;
- deadline: `15m`;
- stability interval: `30s`.

`media_unresolved` не повторяет save автоматически и не откатывает созданную
карточку. Transfer заканчивается `completed_with_errors` или `manual_review`.
`media_rejected` ставится только после сочетания explicit rejection evidence и
наблюдаемого stable pre-state; один HTTP status без observation недостаточен.

---

## 28. Worker lifecycle, leases и process model

### 28.1. Общая job model

```text
queued
→ leased
→ done

leased
→ retry_wait
→ queued

expired leased
→ state-aware recovery

retry budget exhausted
→ dead | manual_review
```

Job fields:

- kind/target ID;
- status;
- priority;
- next_run_at;
- attempt;
- lease_owner;
- lease_token UUID;
- lease_until;
- fencing version;
- last safe error code;
- created/updated timestamps.

Claim выполняется `FOR UPDATE SKIP LOCKED` и не берёт больше rows, чем есть
свободных worker slots.

Каждый commit:

```sql
UPDATE ...
SET ...
WHERE id = $1
  AND lease_token = $2
  AND version = $3
```

Ноль rows означает lost lease; результат старого worker отбрасывается.

`lease_until` и `next_run_at` рассчитываются по PostgreSQL clock. Network под DB
row lock не выполняется.

### 28.2. Heartbeat

Инварианты:

- `heartbeat_interval <= lease_duration / 3`;
- `lease_duration >= max_single_step_timeout + db_commit_margin`;
- `WB_API_TIMEOUT + db_commit_margin < transfer lease`;
- worker прекращает новый network step после failed heartbeat;
- heartbeat и business commit используют тот же lease token;
- worker не возвращает post-dispatch mutation в pending.

Long reconciliation реализуется серией коротких poll jobs. Lease не удерживается
35 минут целиком.

### 28.3. State-aware recovery

| Durable state | Recovery |
|---|---|
| file fetching/parsing | safe requeue appropriate local stage |
| transfer initializing | resume fenced chunk checkpoint |
| transfer initialization_failed | no automatic work; audited same-snapshot restart only |
| transfer preparing/read preflight | safe requeue |
| upload prepared | safe requeue |
| upload attempt evidence/admission failed | new attempt after typed NotDispatched |
| upload admitted без egress start | дождаться permit expiry; durable check → NotDispatched |
| upload egress started/submitted | reconciliation only |
| upload reconciling | resume reads |
| media prepared | safe requeue |
| media admitted без egress start | дождаться permit expiry; durable check → NotDispatched |
| media egress started/submitted | media reconciliation only |
| unresolved/manual | late reads only; no mutation |
| notification pending | idempotent outbox delivery |
| terminal | no job |

### 28.4. Singleton process guard

Production v1 использует ровно один `wb-service` process:

- до Telegram Long Polling, HTTP listener и workers startup dedicated
  PostgreSQL connection получает advisory singleton lock;
- lock holder записывает новый monotonic `process_epoch` в DB;
- если lock занят, второй процесс fail-fast завершается; follower mode нет;
- только singleton process выполняет любые WB Content calls: catalog
  preparation, preflight, Error List feed, upload/add, media и reconciliation;
- потеря lock connection отменяет process root context, запрещает новые claims
  и admission и начинает shutdown;
- upload/media используют opaque short-lived ticket protocol раздела 24;
- request с `egress_started` после restart выполняет только reconciliation;
- paused goroutine до `ClaimAndStart` не проходит fresh process epoch/fence;
- `ticket.Do` создаёт request context с deadline `start_not_after` внутри
  sealed executor; goroutine, возобновившаяся после expiry до входа в transport,
  получает zero HTTP, но после committed egress start durable outcome всё равно
  остаётся unknown/reconciliation;
- все старые commits отсекаются process epoch и work fencing tokens.

Deployment использует stop-before-start/Recreate. Rolling overlap,
горизонтальные replicas и leader failover в v1 не поддерживаются. PostgreSQL
admission всё равно обязателен, чтобы rate state переживал clean restart; ENV не
может включить memory fallback в dry-run/live.

### 28.5. Fairness и concurrency

Scheduler:

- имеет bounded global in-flight;
- имеет bounded per-target in-flight;
- round-robin выбирает targets;
- не позволяет одному rate-limited target занять все workers;
- read reconciliation и mutation admission имеют отдельные bounded queues;
- соблюдает global product intent независимо от goroutine count.

Feature не оборачивает read `DoJSON` вторым HTTP retry loop и не отправляет
mutation через него. Новый job attempt означает
новый безопасный read step либо новый durable mutation AttemptID после
`NotDispatched/catalog_proven_non_applied`, но не слепой repeat.

### 28.6. Shutdown

1. Прекратить Telegram intake readiness для новых transfers.
2. Остановить handoff и work claims.
3. Снять WB readiness и прекратить новые admissions.
4. Дождаться или отменить in-flight contexts typed cause.
5. Любой dispatch-started mutation сохранить для reconciliation.
6. Выполнить bounded DB commits.
7. Освободить singleton advisory lock только после stop admissions, Telegram
   Long Polling и HTTP intake.
8. Закрыть idle HTTP connections.

Нельзя выполнять bulk transition `running → pending`.

---

## 29. PostgreSQL schema transfer

### 29.1. Aggregate tables

`wb.transfers`:

- unique batch ID;
- source checksum/schema/normalization versions;
- status, attention flag и safe blocked reasons;
- target-set/catalog versions;
- execution mode at start;
- dispatch authorization timestamp/actor/idempotency;
- nullable author FK `ON DELETE SET NULL`;
- actor/chat snapshots;
- authoritative counters cache;
- timestamps.

`wb.transfer_initializations`:

- transfer ID primary key;
- immutable batch/target snapshot checksums;
- expected item, target, item-target, group-member и initial-job counts;
- initialization generation и eventual active generation;
- next stable batch position и rolling expansion checksum;
- status `expanding|verifying|activated|failed_contract`;
- lease token/until, fence, version и activation revision;
- timestamps по DB clock.

Каждая chunk transaction upsert-ит deterministic rows и только затем CAS-ом
двигает checkpoint/checksum под тем же fence. Activation transaction сверяет
все expected counts/checksums, переводит transfer `initializing → created` и
создаёт initial jobs. Duplicate Start/reclaimer используют эту же row.
Все expansion rows имеют `initialization_generation`; uniqueness и activation
counts scoped этой version. Failed old generation не виден active queries и
удаляется retention GC.

`wb.transfer_targets`:

- transfer ID;
- CabinetID;
- display name snapshot;
- ordinal;
- capability/target-set revision;
- status;
- unique `(transfer_id, cabinet_id)` and ordinal.

`wb.transfer_items`:

- transfer ID + batch item ID unique;
- stable position;
- exact vendor code;
- normalization version;
- source `group_id_raw`; transfer grouping semantics применяются только по
  решению Gate GROUP-0;
- global prepared payload/digest/status.

`wb.transfer_item_targets`:

- item + target unique;
- target-specific subject/nmID/imtID;
- prepared payload/digest;
- status/safe error;
- media status/verification level;
- version/timestamps.

`wb.transfer_target_groups`:

- target;
- source group key;
- stable ordinal;
- decision/state;
- expected subject;
- target imtID for add;
- desired/request digest;
- unique `(target_id, source_group_key)`.

`wb.transfer_target_group_members`:

- group + item-target;
- stable ordinal;
- membership kind existing/create/add;
- one group per item-target.

`wb.transfer_proposals`:

- dry-run transfer/target/request kind;
- non-owning observed remote state;
- target/metadata/catalog versions;
- canonical request digest and proposal-root membership;
- revision/status `current|stale|authorized`;
- не содержит active product/remote-group claim.

`wb.transfer_dispatch_authorizations`:

- transfer/revision;
- expected proposal root и target-set revision;
- actor/idempotency key;
- requested/processing/authorized/stale;
- unique idempotency key;
- resulting intent/job references.

### 29.2. Mutation journal

`wb.product_identities` — persistent global identity из раздела 22.

`wb.product_group_claims` и `wb.product_group_claim_members`:

- global claim одной source group;
- desired digest/canonical desired value/owner references;
- полная membership всех variants;
- state/fence и monotonic outcome/media-outcome versions;
- active claim unique на product identity.

`wb.product_group_claim_followers`:

- claim + follower transfer-group unique;
- nullable owner intent reference, заполняемый после create/add decision;
- canonical desired digest/value reference;
- state;
- `last_applied_outcome_version` и `last_applied_media_version`;
- idempotent wake job/outbox key;
- timestamps/retention FK к audit graph.

Index по pending follower/outcome version позволяет recovery fan-out без scan.

`wb.product_group_fanout_checkpoints`:

- claim + outcome kind + outcome version unique;
- next follower stable ID;
- expected/fanned-out counts;
- status/lease/fence/version;
- one unique fan-out job.

Owner transaction создаёт checkpoint/job; bounded page transactions обновляют
followers и checkpoint. Owner outcome commit не зависит от follower count.

`wb.mutation_intents`:

- product group claim reference;
- operation kind/generation;
- catalog/request version;
- desired/request/manifest digest;
- target imtID;
- state/delivery;
- dispatch/reconcile deadlines;
- safe terminal data;
- immutable after dispatch.

`wb.mutation_dispatch_attempts`:

- exactly one owner: upload-request reference XOR media-intent reference;
- exact prepared request reference: owning upload request либо versioned media
  upload request;
- unique ordinal внутри owner request/intent;
- globally unique AttemptID;
- exact operation/body/catalog/rate-policy digest binding;
- evidence kind/reference, baseline feed sequence/raw cursor либо media
  pre-state, tail-observed time;
- permit/ticket ID, admitted/not-after timestamps;
- process epoch/lease/fence;
- one-shot egress token, `egress_started_at`/`start_not_after`;
- state/delivery/response class/non-applied evidence/retry-not-before;
- immutable terminal result.

`wb.mutation_dispatch_attempt_intents`:

- upload AttemptID + mutation intent unique;
- unit/group reference;
- все intents/units одного upload request присутствуют;
- media attempts не имеют rows в этой table.

`wb.mutation_intent_members`:

- intent + product identity;
- stable ordinal;
- unique membership;
- только реально отправляемые variants.

`wb.remote_group_claims`:

- CabinetID + imtID primary key;
- active add intent/claim;
- state/fence;
- unresolved claim не steal-ится по одному timeout.

`wb.transfer_upload_requests`:

- target;
- operation create/add;
- exact bounded canonical request body `bytea`;
- body digest, byte size and catalog/request version;
- current/last attempt reference;
- state/delivery;
- baseline feed sequence и opaque WB cursor;
- dispatch result metadata;
- lease/fence.

`wb.transfer_upload_units`:

- request + group;
- expected mutation membership;
- correlated batchUUID;
- outcome.

`wb.transfer_observations`:

- append-only;
- observation kind/time;
- normalized bounded values;
- source cursor/page;
- no raw response body.

### 29.3. Error feed

`wb.error_feed_checkpoints`, `wb.error_batches` и
`wb.error_batch_versions` имеют unique:

- checkpoint per CabinetID;
- `(cabinet_id, batch_uuid)`;
- `(cabinet_id, feed_seq)`;
- `(cabinet_id, batch_uuid, updated_at, content_digest)` для versions;
- один batchUUID может быть strongly bound максимум к одной upload unit.

`feed_seq` является единственным локальным ordering primitive для
«увидено до/после baseline». Пара `(updated_at, batch_uuid)` хранится opaque и
используется только как continuation в WB request.

### 29.4. Media

`wb.media_assets`:

- SHA-256 primary identity;
- size/MIME/dimensions/kind;
- PostgreSQL bounded blob;
- public delivery path metadata;
- retention deadline и signing-key version;
- validation status;
- retention.

`wb.media_asset_references`:

- asset + owner kind/owner ID unique;
- owner kinds включают transfer preparation/group, dry-run proposal,
  authorization, follower, media intent и outbox;
- required-retain-until;
- terminal/released timestamps;
- index для reference-aware GC.

`wb.media_manifests` и `wb.media_manifest_items`:

- schema/digest;
- ordered assets;
- immutable after dispatch.

`wb.media_intents`:

- unique active `(cabinet_id, nm_id)`;
- item-target;
- generation;
- current/last dispatch-attempt reference;
- pre-state;
- manifest digest;
- state/delivery;
- verification level;
- deadline.

`wb.media_upload_requests`:

- media intent + monotonically increasing prepared-request version unique;
- exact bounded canonical body `bytea`;
- body digest/byte size;
- operation/catalog/rate-policy versions;
- asset manifest digest, delivery-key ID и URL expiry;
- immutable state/current attempt reference.

Каждый media dispatch attempt ссылается на exact
`media_upload_request_id`. URL/key refresh разрешён только пока предыдущий
attempt доказанно `NotDispatched` или catalog-proven non-applied; unknown/other
response запрещает новый prepared request.

`wb.media_quota_usage` и `wb.media_quota_reservations`:

- scope `card|transfer|global` и owner ID;
- logical asset count/bytes, physical bytes и configured quota version;
- reservation UUID/attempt owner, worst-case reserved bytes, expiry;
- state `reserved|committed|released`;
- lease/fence/version;
- unique logical asset reference для dedup-safe commit.

`wb.media_quota_usage_events`:

- unique event/idempotency key;
- scope/asset/reference/transfer;
- reserve/commit/logical-release/physical-release delta;
- before/after version и DB timestamp.

До неизвестного stream size резервируется worst-case per-asset allowance.
Commit под одной transaction заменяет reservation фактическим usage, учитывает
уже существующий content hash и возвращает остаток. Concurrent transfers не
могут oversubscribe quota; sweeper освобождает только expired unfenced
reservation.

### 29.5. Jobs, events и notifications

`wb.transfer_jobs`:

- kind/target;
- lease/fence/retry;
- unique active job per `(kind, target_type, target_id)`.

`wb.transfer_events`:

- append-only safe audit events;
- operation/group/item/target references;
- code, state transition, timestamps;
- no secrets/raw payload.

`wb.transfer_notifications`:

- transfer/event type;
- chat/message snapshot;
- rendered-safe template data;
- pending/leased/retry/delivered;
- idempotency key unique.

### 29.6. PostgreSQL admission

`wb.process_singleton_epochs`:

- singleton lock key primary key;
- monotonic current process epoch;
- process instance UUID и acquired timestamp;
- новый clean startup увеличивает epoch только после advisory lock acquisition;
- admission/egress rows ссылаются на exact epoch, поэтому stale process не
  dispatch-ит и не commit-ит после restart.

`wb.rate_limit_buckets`:

- core-private seller realm key + BucketID primary key;
- active RatePolicyEpoch, independent от catalog version;
- tokens/last refill;
- blocked until;
- optimistic version;
- timestamps по DB clock.

`wb.rate_limit_waiters`:

- waiter UUID/realm/BucketID/owner/deadline;
- per-bucket monotonic FIFO sequence unique;
- status;
- bounded active waiters per bucket;
- crash/cancel cleanup index.

`wb.rate_limit_permits`:

- random permit ID и unique AttemptID;
- core-private realm + BucketID + RatePolicyEpoch;
- operation/body digest, process epoch/work fence;
- admitted-at/not-after reserved send window и derived start-not-after;
- state `admitted|used|expired|cancelled`;
- egress token reference when used.

Grant, consumption одного bucket, waiter dequeue и bucket update выполняются
одной transaction. DB adapter не хранит raw `sid` или token.

### 29.7. DB invariants

- `UNIQUE(transfers.batch_id)`.
- Пока transfer `initializing`, cardinality constraints проверяет initialization
  checkpoint. После activation полный target/item/work-unit count равен
  immutable expected counts.
- Для activated transfer каждый item имеет ровно одну row на target.
- Для activated transfer каждый item-target принадлежит одной group-target.
- Stale initializer fence не двигает checkpoint и не активирует transfer.
- `initialization_failed` имеет zero runnable jobs; только audited
  same-batch/same-target new generation может вернуться в initializing.
- Один active product-group claim на identity.
- Follower outcome/media versions не превышают owner claim versions.
- Fan-out checkpoint advances only under fence and no page exceeds configured
  bound.
- Один active/unresolved intent на product identity.
- Один active/unresolved add claim на `(cabinet_id, imt_id)`.
- Payload/digest/membership immutable после dispatch.
- Один AttemptID имеет один permit/evidence/egress token и максимум один
  `egress_started` transition.
- `cancelled/not_dispatched` permit и `egress_started` mutually exclusive.
- Attempt owner является upload request XOR media intent; upload attempt links
  exactly все child intents/units своего immutable request.
- Каждый attempt ссылается на persisted exact canonical body bytes/digest
  подходящего upload/media prepared request.
- Новый attempt после response разрешён только из persisted
  `catalog_proven_non_applied`.
- `created/done` требует nmID и imtID.
- Media state требует nmID.
- Normal applied upload transaction создаёт ровно один media intent/job либо
  `done_no_media_requested`; late applied создаёт zero media intents.
- Не более одного active complete parse run на каждый non-removed file;
  session aggregate ссылается ровно на набор полностью activated fenced runs
  всех non-removed files.
- `rejected` требует normalized rejection evidence.
- `unresolved` не освобождает product identity.
- Final counters пересчитываются из item-target rows.
- Terminal transition и final notification outbox создаются одной transaction.
- Runtime repositories не предоставляют hard delete active/history records.

---

## 30. Live statistics и Telegram UX

### 30.1. Read model

```go
type CurrentOperation struct {
	ID                   TransferID
	Status               OperationStatus
	AttentionRequired    bool
	BlockedReasons       []SafeCode
	InitializationDone   int
	InitializationTotal  int
	CardsTotal           int
	TargetsTotal         int
	WorkUnitsTotal       int
	Pending              int
	Running              int
	Done                 int
	AlreadyPresent       int
	Rejected             int
	Conflicts            int
	Unresolved           int
	MediaPending         int
	MediaVerified        int
	MediaErrors          int
	Targets              []TargetProgress
	Errors               []GroupedSafeError
}
```

Statistics:

- читает authoritative repository projection;
- проверяет viewer permission;
- показывает current transfer/manual-review operations;
- для `initializing` показывает только verified chunk progress; обычные
  item-target counters публикуются после activation;
- не показывает raw card payload;
- не вызывает WB;
- не инициирует reconciliation;
- не меняет leases/intents.

### 30.2. Telegram rules

- одно обновляемое status message на flow;
- callback содержит session/transfer ID, expected revision и short action;
- callback MAC/signature либо server-side lookup исключает подмену;
- stale callback получает safe refresh;
- `message is not modified` считается idempotent success;
- все dynamic strings HTML-escape;
- message budget ограничивает rendered details, но сохраняет точный total;
- показываются первые N grouped issues и `ещё K`;
- filename/vendor/cabinet display name проходят safe length/redaction.

`manual_review` остаётся видимым до acknowledge. Acknowledge не разрешает новую
mutation и не снимает global blocked identity.

### 30.3. Notification outbox

Уведомления:

- progress throttled/coalesced;
- final;
- manual review;
- late correction.

Telegram network не вызывается внутри business transaction. Duplicate delivery
использует stable idempotency key и обновляет существующее message, где возможно.

---

## 31. Logging, security и privacy

Разрешённые поля логов:

- event;
- operation ID;
- CabinetID;
- session/file/batch/transfer/group/item IDs;
- safe state/error code;
- attempt;
- HTTP status;
- duration/count/byte size;
- catalog version;
- trace/request ID после allowlist;
- delivery state.

Запрещены:

- token и Authorization;
- raw `sid` и SellerKey;
- XLSX/blob bytes;
- full card/request payload;
- raw WB success/error body;
- media source credentials/query secrets;
- arbitrary nested error text;
- user PII сверх технически необходимого ID;
- unknown ENV key name, если он credential-like.

Дополнительные требования:

- logs проходят redaction tests с canary secrets;
- public errors не unwrap-ят raw `url.Error` с URL/query;
- API error preview private и bounded;
- DB JSONB payload доступен только service role;
- backups и blob table шифруются platform controls;
- media public URLs не содержат source URL/filename/user ID;
- asset endpoint не предоставляет listing;
- XLSX ZIP-bomb limits применяются до allocation;
- HTTP origin и redirects fail closed;
- metrics labels не содержат vendorCode, filename, CabinetID display name или
  unbounded WB codes;
- trace propagation использует generated correlation ID, не credentials.

---

## 32. Feature/runtime configuration

### 32.1. Process

| Environment | Default |
|---|---:|
| `APP_INSTANCE_MODE` | `single` |
| `APP_SINGLETON_LOCK_KEY` | required stable int64 |
| `APP_SHUTDOWN_GRACE` | `75s` |
| `TELEGRAM_INTAKE_MODE` | `long_poll` |

V1 принимает только `APP_INSTANCE_MODE=single` и
`TELEGRAM_INTAKE_MODE=long_poll`. Значения для replica/follower/webhook mode
являются config error, а не неявным experimental режимом.

### 32.2. Cardimport

| Environment | Default |
|---|---:|
| `CARDIMPORT_MAX_FILES` | `10` |
| `CARDIMPORT_MAX_FILE_SIZE` | `20MiB` |
| `CARDIMPORT_MAX_DECOMPRESSED_SIZE` | `128MiB` |
| `CARDIMPORT_MAX_ZIP_ENTRIES` | `2048` |
| `CARDIMPORT_MAX_SHEETS` | `16` |
| `CARDIMPORT_MAX_ROWS` | `100000` |
| `CARDIMPORT_MAX_CELLS` | `2000000` |
| `CARDIMPORT_MAX_CARDS` | `50000` |
| `CARDIMPORT_MAX_ISSUES` | `10000` |
| `CARDIMPORT_TELEGRAM_ERRORS_LIMIT` | `20` |
| `CARDIMPORT_PROCESSING_LEASE` | `2m` |
| `CARDIMPORT_HEARTBEAT` | `20s` |
| `CARDIMPORT_BLOB_RETENTION` | `168h` |

### 32.3. Transfer

| Environment | Default |
|---|---:|
| `TRANSFER_WORKERS` | `8` |
| `TRANSFER_MAX_IN_FLIGHT` | `8` |
| `TRANSFER_MAX_IN_FLIGHT_PER_TARGET` | `1` |
| `TRANSFER_CLAIM_SIZE` | `8` |
| `TRANSFER_INITIALIZE_CHUNK_SIZE` | `1000` |
| `TRANSFER_FOLLOWER_FANOUT_PAGE` | `500` |
| `TRANSFER_MAX_FOLLOWERS_PER_CLAIM` | `10000` |
| `TRANSFER_WORK_LEASE` | `2m` |
| `TRANSFER_HEARTBEAT` | `20s` |
| `TRANSFER_RECONCILE_INITIAL_DELAY` | `3s` |
| `TRANSFER_RECONCILE_MAX_INTERVAL` | `30s` |
| `TRANSFER_RECONCILE_TIMEOUT` | `35m` |
| `TRANSFER_ERROR_FEED_CLOCK_SKEW` | `2m` |
| `TRANSFER_ERROR_BASELINE_MAX_AGE` | `45s` |
| `TRANSFER_MEDIA_PRESTATE_MAX_AGE` | `45s` |
| `TRANSFER_MUTATION_MAX_NON_APPLIED_ATTEMPTS` | `5` |
| `TRANSFER_LATE_RECONCILE_INTERVAL` | `5m` |
| `TRANSFER_LATE_RECONCILE_RETENTION` | `24h` |
| `TRANSFER_MEDIA_RECONCILE_TIMEOUT` | `15m` |
| `TRANSFER_MEDIA_STABILITY_INTERVAL` | `30s` |
| `TRANSFER_PROPOSAL_TTL` | `24h` |
| `TRANSFER_NOTIFICATION_RETRY_MAX` | `10` |
| `TRANSFER_MODE` | `disabled` |

`TRANSFER_MODE`:

- `disabled` — no new handoff/worker mutation;
- `dry-run` — import, preparation и reads, но dispatch forbidden;
- `live` — mutations разрешены после всех readiness gates.

### 32.4. Media

| Environment | Default |
|---|---:|
| `MEDIA_FETCH_TIMEOUT` | `30s` |
| `MEDIA_MAX_IMAGE_SIZE` | `32MiB` |
| `MEDIA_MAX_VIDEO_SIZE` | `50MiB` |
| `MEDIA_MAX_REDIRECTS` | `3` |
| `MEDIA_MAX_ASSETS_PER_CARD` | `31` |
| `MEDIA_MAX_BYTES_PER_CARD` | `128MiB` |
| `MEDIA_MAX_ASSETS_PER_TRANSFER` | `100000` |
| `MEDIA_MAX_BYTES_PER_TRANSFER` | `5GiB` |
| `MEDIA_STORE_MAX_BYTES` | required |
| `MEDIA_STORE_HIGH_WATERMARK_PERCENT` | `80` |
| `MEDIA_FETCH_WORKERS` | `4` |
| `MEDIA_MAX_FETCH_IN_FLIGHT_BYTES` | `128MiB` |
| `MEDIA_PUBLIC_MAX_IN_FLIGHT` | `16` |
| `MEDIA_PUBLIC_MAX_IN_FLIGHT_BYTES` | `256MiB` |
| `MEDIA_HTTP_LISTEN_ADDR` | required when media HTTP is enabled |
| `MEDIA_HTTP_READ_HEADER_TIMEOUT` | `5s` |
| `MEDIA_HTTP_IDLE_TIMEOUT` | `30s` |
| `MEDIA_QUOTA_RESERVATION_TTL` | `2m` |
| `MEDIA_PREUPLOAD_REFERENCE_TTL` | `24h` |
| `MEDIA_ASSET_RETENTION_AFTER_TERMINAL` | `168h` |
| `MEDIA_PUBLIC_BASE_URL` | required in live media mode |
| `MEDIA_DELIVERY_KEYRING_REF` | required in live media mode |
| `MEDIA_DELIVERY_ACTIVE_KEY_ID` | required in live media mode |

### 32.5. Cross-field validation

- heartbeat ≤ lease / 3;
- singleton lock получается до Telegram/HTTP/WB startup;
- потеря singleton connection отменяет root context;
- deployment replica count равен `1`, strategy stop-before-start/Recreate;
- app shutdown grace ≥ WB overall timeout + 15s;
- transfer lease > WB overall timeout + 10s DB margin;
- workers/max in-flight/claim positive and under hard ceilings;
- follower fan-out page positive and ≤ max followers per claim;
- per-target in-flight ≤ global;
- reconcile timeout ≥ official async horizon + 5m;
- late retention ≥ normal reconcile timeout;
- blob retention covers unfinished handoff;
- media retention covers media reconciliation plus seven days;
- mutation claim timeout + start margin < mutation ticket TTL;
- evidence max-age ≥ rate-limit wait timeout + mutation claim timeout +
  scheduler jitter;
- baseline/pre-state max age проверяется после admission; stale evidence
  расходует permit, но не вызывает HTTP;
- per-card media count/bytes ≤ per-transfer limits;
- max in-flight media bytes ≤ global store capacity;
- public stream slots/bytes positive, bounded and ≤ memory safety ceiling;
- transfer media bytes ≤ store high-watermark capacity;
- quota reservation TTL > max fetch timeout + DB margin;
- pre-upload reference TTL > one preparation/admission retry horizon;
- proposal TTL ≤ pre-upload reference TTL; expired approval always reprepares;
- asset base URL exact HTTPS public origin;
- active signing key присутствует в injected keyring; retired verify keys
  сохраняются до максимального outstanding asset retention;
- dry-run and live both require singleton process guard for every WB read and
  `WB_API_ADMISSION_MODE=postgres`;
- new Start/Authorize in live additionally requires complete target cohort,
  binding store, migrations and current catalog/rate versions; media
  store/quota/signing обязательны для batch с non-empty media;
- an existing dispatch checks only its current target plus required dependencies;
- reconciliation checks target read readiness and persisted-version support,
  but not full-cohort/write/media-store readiness;
- local projection/follower/outbox jobs remain runnable without WB readiness;
- dry-run transfer without persisted authorization must fail any attempted
  mutation locally as `MutationDisabledError`;
- ENV mode change never backfills `dispatch_authorized_at`.

---

## 33. Целевая структура кода

```text
internal/
  core/
    transport/
      wb/
        client.go
        config.go
        errors.go
        delivery.go
        registry/
          builder.go
          env_loader.go
          token_parser.go
          claims.go
          binding.go
          issues.go
        policy/
          operation.go
          request_plan.go
          response_plan.go
          bucket.go
          content/
            catalog_v1.go
            manifest.go
            dto_categories.go
            dto_directories.go
            dto_cards.go
            dto_errors.go
            dto_media.go
            bounds.go
            snapshot_test.go
        pipeline/
          operation.go
          attempt.go
          mutation_ticket.go
          egress.go
          oneshot.go
          timeout.go
          auth.go
          trace.go
          terminal.go
          decode.go
          classify.go
          retry.go
          logging.go
        ratelimit/
          registry.go
          limiter.go
          admission.go
          response.go
        internaltest/
          fake_clock.go
          scripted_transport.go

  feature/
    cardimport/
      domain/
        session.go
        file.go
        batch.go
        issue.go
        normalization.go
      application/
        service.go
        finalize.go
        validation_worker.go
        handoff_worker.go
        ports.go
      adapter/
        telegram_file_source.go
        postgres_blob_store.go
        xlsx/
          schema_v1.go
          parser.go
          limits.go
          aggregate.go
      repository/
        postgres/
      transport/
        telegram/

    transfer/
      domain/
        transfer.go
        target.go
        product_identity.go
        group.go
        intent.go
        dispatch_attempt.go
        observation.go
        media.go
        errors.go
      application/
        start.go
        prepare.go
        preflight.go
        dispatch.go
        reconcile_upload.go
        reconcile_media.go
        late_resolver.go
        ports.go
      gateway/
        wb/
          gateway.go
          mapper_catalog.go
          mapper_cards.go
          mapper_errors.go
          mapper_media.go
      asset/
        fetcher.go
        ssrf_guard.go
        store.go
        quota.go
        signer.go
        public_handler.go
      worker/
        claim.go
        heartbeat.go
        scheduler.go
        recovery.go
      repository/
        postgres/
      transport/
        telegram/

    statistics/
      domain/
      application/
      source/
        transfer/
      transport/
        telegram/

  platform/
    authorization/
    outbox/
    clock/
    runtime/
      singleton.go
      process_epoch.go
    httpserver/
      server.go
      health.go
    wbexecution/
      postgres_attempt_store.go
      postgres_admission.go
      transaction.go

cmd/
  wb-service/
    main.go
```

Rules:

- canonical constructor/wire type name существует только в `policy/content`;
- business application не импортирует `net/http`;
- Telegram не импортирует repositories напрямую;
- PostgreSQL SQL изолирован adapter-ом;
- shared Client создаётся только composition root;
- no package-level mutable session maps;
- no generic `map[string]any` на business/core boundary;
- generated/mock code не становится источником operation semantics.

---

## 34. Единая последовательность реализации

Каждый stage завершается отдельным reviewable change set. Следующий mutation
stage не начинается до прохождения указанного gate.

### Gate D0. Разрешить deployment

Deliverables:

- ADR о self/on-premise Personal-token deployment;
- подтверждение владельца всех seller accounts;
- список `WB_TRANSFER_CABINETS`;
- single-container/process topology и stop-before-start/Recreate deployment;
- один Telegram bot/Long Poller в singleton process;
- rotation/revocation/shutdown runbook;
- запрет параллельного внешнего writer тех же vendor codes.

Exit:

- security/product/operations owners подписали ADR;
- third-party cloud scenario отсутствует;
- production secrets поступают platform mechanism;
- `TRANSFER_MODE=disabled` по умолчанию.

### Stage 0. Baseline и joint contract freeze

Deliverables:

- сохранить текущий dirty worktree, не теряя user changes;
- инвентаризировать current transport/Telegram/XLSX/schema;
- вынести PostgreSQL runtime data из Go module tree, чтобы `go test ./...` не
  падал на unreadable `out/pgdata`;
- зафиксировать удаление legacy `wb.card_imports` без backfill;
- зафиксировать single-process/Long-Polling topology;
- зафиксировать official API snapshot с URL и `verified_at`;
- заморозить operation manifests, DTO, bounds, statuses, rate buckets;
- принять structural media verification как product contract;
- добавить traceability matrices.

Exit — Gate C0:

- ни в одном safety-critical разделе нет `TBD` или «если API позволяет»;
- exact auth header подтверждён smoke test;
- Cards List/Error List cursor fixtures сохранены;
- ErrorBatch UUID lifecycle, full batch membership vs rejected subset и
  `updatedAt` clock semantics зафиксированы; rejected-only response разрешает
  только full CardsList+error partition correlation;
- upload/media async semantics покрыты fixtures;
- `go test ./...` может обойти всё дерево репозитория.

### Stage 1. Token parser и binding foundation

Deliverables:

- bounded structural JWT parser;
- Personal/capability/expiry validation;
- provisional SellerKey derivation;
- duplicate token/name/seller detection;
- PostgreSQL transaction/dedicated-connection foundation;
- startup singleton advisory lock и monotonic process epoch до любых probes;
- foundation migration `wb.cabinet_seller_bindings` и admission tables;
- `SellerBindingVerifier` port/PostgreSQL primitives и unit fakes;
- pure complete mutation cohort resolver для уже authenticated registry;
- safe diagnostics.

Exit — Gate I0:

- duplicate `sid` отклоняет все conflicts;
- structural read-only target помечается;
- fake verifier проверяет same-seller/rebind/concurrent insert semantics;
- raw token/`sid`/SellerKey не видны feature/log/error;
- 60 structural candidates сортируются deterministic;
- production binding/registry ещё не публикуются без Stages 2–4.

### Stage 2. Catalog types и exact DTO

Deliverables:

- immutable Operation manifest;
- catalog v1 operations из раздела 10;
- exact query/request/response DTO;
- `CardsLimits` помечен как единственный CredentialProbeOperation;
- RequestPlan/ResponsePlan;
- operation bounds и snapshot tests;
- canonical naming без aliases.

Exit — Gate K0:

- gateway-required operation inventory полон;
- Cards Limits, Trash Cards List, Brands, directories и UploadAdd присутствуют;
- arbitrary method/path/DTO невозможны;
- boundary/golden/property tests проходят;
- request byte/count bounds проверены до network.

### Stage 3. Pipeline primitives и HTTP ownership

Deliverables:

- PreparedOperation/AttemptState/AttemptResult;
- sealed middleware stack и shared one-shot guard;
- exact origin validation;
- request recreation;
- реализуемый `http.Client.Do` body contract;
- bounded response/error handling;
- attempt-local decode;
- typed context causes.

Exit:

- body closed exactly once во всех достижимых paths;
- redirect/origin escape невозможен;
- caller output не частично мутируется;
- simultaneous deadline tests deterministic;
- adversarial middleware не может скрыть double-next.

### Stage 4. Limiter, classification и retry

Deliverables:

- per-(SellerKey,BucketID) admission, ровно один bucket на operation;
- PostgreSQL token buckets/FIFO waiters per bucket;
- persisted mutation permits с AttemptID/send-window;
- fake/in-memory `MutationEgressStore` for core contract tests only;
- independent RatePolicyEpoch migration/rolling compatibility;
- memory backend только test/dev;
- response-header observations;
- typed DeliveryState;
- evidence-based retry;
- mutation uncertainty;
- safe operation/attempt logs.

Exit:

- retry reads и mutation matrix полностью покрыта;
- mutation post-dispatch network error никогда не retry;
- explicit non-applied 401/403/429 возвращает durable-attempt directive, без
  скрытого mutation retry;
- DB loss при сохранении rate observation останавливает next attempt;
- retry exhausted/interrupted сохраняет delivery;
- rate policy shared для duplicate-seller rotation realm;
- race/fuzz/security log tests проходят.

### Stage 5. Atomic Client switch и composition core

Deliverables:

- unpublished provisional registry/client;
- production authenticated probe через `CardsLimits`;
- probe-before-first-binding transaction и current-token verification;
- registry-backed published `Client` только после probe/binding;
- `ForCabinet` и complete `MutationTargets`;
- read `DoJSON` и prepared-mutation ticket API используют один sealed HTTP
  executor без двойного admission;
- production mutation path остаётся fail-closed, пока Stage 6 не подключит
  PostgreSQL `MutationEgressStore`;
- один shared injected Client;
- CloseIdleConnections/shutdown;
- старый arbitrary API удалён после migration всех callers.

Exit — Gate WB-0:

- opaque credentials;
- invalid-signature/`401` probe не оставляет binding;
- concurrent first boot создаёт одну authenticated binding;
- rotation same seller проходит probe, rebind отклоняется;
- complete target cohort;
- closed catalog;
- one pipeline;
- typed errors/delivery;
- unit/integration/race/vet green;
- feature adapter может компилироваться только через catalog DTO mapper.

Gate WB-0 доказывает core/read/probe contracts, но не разрешает production
mutation.

### Stage 6. Authorization и remaining forward DB migrations

Deliverables:

- permission `cards.transfer_all_cabinets`;
- actor snapshot и immutable Finalize authorization-grant policy;
- forward drop legacy `wb.card_imports` без backfill;
- cardimport/transfer tables;
- все feature tables после уже введённого seller binding;
- jobs/leases/fencing;
- product identities/intents;
- request-level mutation attempts/evidence/attempt-intent links;
- PostgreSQL `MutationEgressStore` и общий admission/egress transaction manager;
- error feed/media/outbox tables;
- no user cascade;
- `TRANSFER_MODE=disabled`.

Exit:

- migration up на пустой и production-like DB;
- rollback policy задокументирована;
- user hard delete не удаляет operation history;
- DB constraints ловят invalid states;
- repositories не обходят fencing.

Exit — Gate WB-1:

- production mutation executor не стартует без real PostgreSQL egress store;
- permit/evidence/request/child-intent `ClaimAndStart` commit атомарен;
- fake store запрещён в dry-run/live wiring;
- `TRANSFER_MODE=disabled` всё ещё обязателен до worker/media/upload gates.

### Stage 7. Durable file ingestion

Deliverables:

- Telegram FileSource;
- PostgreSQL BlobStore;
- receive/store queue;
- fenced RemoveFile + aggregate rebuild;
- file leases/reclaimer;
- retention GC;
- bounded stream/hash/signature.

Exit:

- kill в каждой fetch точке не теряет файл;
- duplicate Telegram delivery создаёт один blob;
- validation после restart открывает persisted blob;
- GC не удаляет referenced data.
- removed in-flight file cannot reactivate after worker completion.

### Stage 8. XLSX parser и deterministic aggregate

Deliverables:

- Gate XLSX-0 до parser code: заморозить BatchSchemaV1, price representation,
  header aliases, units, requiredness, `Wholesale`/`kizMarked` policy и
  normalization v1;
- BatchSchemaV1 parser после Gate XLSX-0;
- ZIP/workbook limits;
- exact typed parsing;
- VendorCodeIdentityV1;
- lossless raw GroupID preservation без transfer grouping semantics;
- barcode rules;
- immutable parse versions;
- stable multi-file aggregate;
- structured issues/Telegram view.

Exit:

- Gate XLSX-0 записан в checked-in schema fixtures и mapping table;
- parsing order не влияет на aggregate/checksum;
- locale/case/Unicode vectors фиксированы;
- leading-zero barcode сохраняется;
- silent coercion отсутствует;
- conflicts содержат обе source locations;
- fuzz corpus не вызывает unbounded allocation/panic.

### Stage 9. Finalize, immutable batch и handoff

Deliverables:

- authoritative readiness;
- idempotency-first Finalize;
- immutable checksum;
- finalization records;
- handoff outbox;
- dispatcher against `BatchConsumer` contract;
- deterministic fake consumer для cardimport/outbox acceptance;
- production `Transfer.Start` ещё не wired.

Exit — Gate CI-0:

- concurrent Finalize создаёт один batch;
- authorized retry старого successful callback возвращает batch;
- stale different key отклоняется;
- crash после fake consumer success не теряет handoff и повторяет ту же
  idempotency identity;
- batch items нельзя изменить/удалить runtime API.

### Stage 10. Worker foundation до mutations

Deliverables:

- transfer jobs;
- fenced chunk initializer;
- scheduler/claim/heartbeat/fence;
- state-aware recovery;
- singleton process lifecycle/process epoch и stop-before-start recovery;
- graceful shutdown;
- bounded target fairness;
- worker metrics.

Exit — Gate RUN-0:

- старый worker после lease loss не commit-ит;
- post-dispatch state recovery никогда не делает upload;
- второй service process fail-fast не запускает Telegram/HTTP/workers;
- Recreate restart сохраняет limiter state и не создаёт новый burst;
- paused ticket после `NotAfter` не начинает HTTP;
- DB loss fail closed для новых WB calls.

### Stage 11. Transfer aggregate, product identities и preparation

Deliverables:

- Gate GROUP-0 до transfer aggregate: зафиксировать empty/raw GroupID semantics,
  duplicate vendor/group conflicts, category compatibility, size bound и stable
  grouping key;
- production idempotent `Transfer.Start`;
- durable initialization checkpoint/count/checksum и atomic activation;
- complete target snapshot;
- global/per-target preparation;
- catalog cache keys;
- product identity claims/followers;
- version-independent exact vendor ownership и durable follower fan-out;
- group-target model;
- Cards Limits/Brand/directory validation;
- persisted target-specific payload.

До Gate MEDIA-PREP-0 non-empty media groups остаются
`blocked_dependencies/media_pipeline_unavailable` и не получают mutation claim
или upload job.

Exit — Gate TR-0:

- Gate GROUP-0 закрыт и покрыт fixtures/tests;
- partial cohort не создаёт transfer;
- duplicate Start existing transfer не зависит от текущего cohort;
- per-target resolved payloads независимы;
- два concurrent transfers одного target/vendor сериализуются одним ownership
  claim/follower relation, но mutation intent ещё не создаётся;
- different targets независимы;
- unresolved old intent блокирует новую generation.

### Stage 12. Existing-card preflight и group decisions

Deliverables:

- Cards List pagination/exact matching;
- authoritative normal/trash preflight под product/group claims;
- complete group decision table;
- create/add/already/conflict decisions;
- atomic mutation intent creation only after final decision;
- bounded request packing;
- request/group digests.

Exit:

- all absent/all present/mixed/different imtID cases проходят;
- два concurrent transfers одного target/vendor создают максимум один mutation
  intent после authoritative decision;
- add использует только missing variants и persisted imtID;
- subject/capacity/remote identity conflicts terminal;
- MoveCards не вызывается;
- existing cards/media не перезаписываются.

### Stage 13. Durable Error List feed

Deliverables:

- checkpoint poller;
- overlap/dedup;
- normalized error batches;
- complete/rejected-only/unknown membership evidence modes;
- per-target serialization;
- pre-dispatch tail baseline;
- correlation engine.

Exit:

- stale pre-baseline error не связывается;
- >100 pages и equal timestamp не теряются;
- duplicate pages idempotent;
- one/multiple group request correlation верна;
- multiple strong candidates дают unresolved;
- server filter по vendorCode нигде не предполагается.

### Stage 14. Media asset preparation foundation

Deliverables:

- SSRF-safe fetcher;
- PostgreSQL immutable asset store и redacted public streaming handler;
- HTTP server lifecycle, health routes и `/public/wb-media/v1/...` route;
- TLS/reverse-proxy deployment contract и полная redaction path token в proxy
  access logs;
- transactional quota reservations/high-watermark;
- signing/keyring rotation;
- source-to-immutable media manifest;
- reference-aware retention/GC;
- explicit no-media/rejected/transient/quota preparation outcomes.

Exit — Gate MEDIA-PREP-0:

- non-empty manifest полностью materialized до product mutation claim;
- invalid member блокирует whole group с zero WB mutation;
- concurrent quota reservations не oversubscribe store;
- dry-run/proposal/follower references защищены от GC;
- public URLs проходят external GET/HEAD/Range smoke test без раскрытия token;
- unavailable public asset/quota blocks dispatch readiness.

### Stage 15. Upload dispatch и reconciliation

Deliverables:

- immutable mutation journal;
- append-only attempts, opaque ticket и one-admission dispatch barrier;
- core error-action mapping;
- upload/create/add worker;
- Cards List + Error List reconciliation;
- late resolver;
- media intent/job schema;
- atomic normal upload→media handoff и explicit late/no-media branches;
- item/group/counter projection.

Exit — Gate REC-0:

- каждый crash-window покрыт fault injection;
- `round_trips_per_attempt <= 1` и
  `possibly_applied_attempts_per_generation <= 1`;
- 200 никогда не означает created без observation;
- old batch error не принимается за current;
- timeout создаёт blocked unresolved, не retry;
- partial remote сохраняет фактические nmID/imtID.

### Stage 16. Media submission и reconciliation

Deliverables:

- full-set save;
- persisted exact versioned media request bytes/digest/URL expiry;
- media append-only attempts through the same ticket barrier;
- every-response reconciliation;
- structural double observation;
- follower media fan-out.

Exit — Gate MEDIA-0:

- `200` без media не становится done;
- CDN URL не сравнивается с source URL;
- already-present card не изменяется;
- crash после media dispatch не повторяет save;
- verification level виден в DB/UI;
- unavailable public asset blocks live media readiness.

### Stage 17. Statistics, notifications и Telegram integration

Deliverables:

- current read model;
- safe grouped errors;
- progress/final/manual/late notifications;
- callback revisions and permission checks;
- message coalescing/limits;
- production menu flow;
- ровно один Telegram Long Poller в singleton process; webhook и multi-replica
  update intake отсутствуют.

Exit:

- statistics не вызывает WB;
- unauthorized user не запускает и не читает details;
- Telegram duplicate/stale callbacks idempotent;
- terminal notification durable;
- user deletion не ломает delivery snapshot.

### Stage 18. Rollout и cleanup

1. Deploy migrations и core с transfer disabled.
2. Запустить registry/catalog/read smoke tests.
3. Включить dry-run на одном internal batch.
4. Сравнить prepared payload/target cohort без mutation.
   Этот dry-run остаётся non-dispatchable или получает отдельную audited
   `AuthorizeDispatch`; смена ENV его не выпускает.
5. Последовательно, при остановленном production deployment, запустить один
   canary deployment с отдельным `WB_TRANSFER_CABINETS` и изолированным seller;
   одновременная работа двух экземпляров запрещена, production cohort не
   сокращать.
6. Выполнить create/add/error/media/restart сценарии.
7. В production deployment включить заранее утверждённый полный cohort.
8. Проверить, что удалённый на Stage 5 старый execution path не вернулся;
   удалить оставшийся process-local Telegram state и dead compatibility shims.
9. Зафиксировать dashboards/alerts/runbooks.
10. Пометить прежние планы superseded.

Exit — Final Gate описан в разделе 37.

---

## 35. Обязательная тестовая матрица

Все state-machine tests проверяют не только returned error, но и durable DB state,
delivery classification, next job и отсутствие лишнего HTTP.

### 35.1. Core config, identity и registry

- empty/malformed/duplicate cabinet lists;
- ID case/suffix collisions;
- missing/orphan name/token;
- credential-like unknown ENV не раскрывает key/value;
- excessive token/JWT segment/payload;
- malformed base64/JSON/duplicate claims/wrong claim types;
- unsupported token type;
- missing Content bit;
- read-only in read registry;
- read-only in target cohort;
- expired/near-expiry;
- duplicate raw token;
- два CabinetID с одним `sid`;
- два names после normalization;
- 60 valid unique sellers;
- deterministic registry ordering;
- first persistent binding;
- forged/invalid-signature first token fails authenticated probe and leaves no
  binding;
- `401`/probe crash before binding transaction leaves no binding;
- same-seller rotation;
- different-seller rebind;
- concurrent binding builders;
- incomplete target cohort;
- immutable returned snapshots;
- redaction via `fmt`, wrapping, JSON and logs.

### 35.2. Catalog и request/response plans

Для каждой operation:

- canonical ID/name/method/path/origin;
- required capability;
- exact request/response type;
- wrong type, typed nil и nil target;
- min/max/max+1 counts;
- max-1/max/max+1 serialized bytes;
- path/query escaping;
- invalid subject/nmID/imtID/cursor;
- allowed status set;
- empty vs JSON response;
- truncated/oversized/multiple JSON values;
- unknown fields;
- no arbitrary operation constructor.
- CredentialProbe uses exactly the `CardsLimits` manifest, without an alias.
- Mixed legacy/current catalog versions use one active RatePolicyEpoch.
- Rate epoch migration never increases tokens or shortens `blocked_until`.

Golden fixtures:

- Categories/Subjects/Characteristics;
- all directories;
- Brands pagination;
- Cards Limits;
- Cards List cursor and media fields;
- Trash Cards List cursor and full inventory count;
- Cards Error List cursor/batches;
- Upload/Create;
- UploadAdd;
- MediaSave.

Snapshot test выводит meaningful diff при изменении manifest version.

### 35.3. Pipeline/body/timeouts/retry

- middleware order;
- zero/one/two `next` calls;
- buggy middleware ignores double-next error;
- request body recreated per attempt;
- auth added once and only final origin;
- redirect forbidden;
- path/query/userinfo/port/fragment/RawPath/ForceQuery attacks;
- normal/error/empty/oversized body closed once;
- read/close/drain failures;
- caller output unchanged on failed attempt;
- caller cancel, shutdown, overall, admission, attempt, backoff causes;
- simultaneous deadline priority with fake clock;
- read transport retry;
- read 429/5xx retry;
- mutation `429` returns non-applied directive but no hidden retry;
- mutation timeout/DNS/TLS/EOF after dispatch → unknown, no retry;
- decode failure after mutation response → unknown;
- exhausted/interrupted preserves delivery;
- malformed Retry-After/rate headers;
- limiter key shared by same seller realm;
- fairness/cancellation/waiter/key ceilings;
- retry attempt повторно проходит admission;
- Recreate restart одного seller+bucket сохраняет общий FIFO/burst state;
- common Content bucket разделяется разными operations;
- каждая operation manifest ссылается ровно на один BucketID;
- documented exception использует свой bucket вместо `content_common`;
- different sellers do not share a bucket;
- crashed/cancelled waiter cleanup;
- concurrent `429` observations do not shorten the longest block;
- DB loss between response and Observe preserves delivery and blocks next attempt;
- same mutation AttemptID never consumes a second permit;
- expired ticket before `ClaimAndStart`, digest/fence mismatch and second
  `ticket.Do` make zero HTTP;
- concurrent `CancelBeforeStart` vs `ClaimAndStart` has exactly one winner;
- evidence expires between outer check and ClaimAndStart → atomic rejection,
  zero HTTP;
- max admission wait does not deterministically exceed configured evidence age;
- fake-clock pause after admission before claim beyond `NotAfter` makes zero
  HTTP/NotDispatched;
- expiry/cancel after committed egress start remains Unknown even when fake
  transport proves zero bytes;
- log canary secrets absent;
- race test under concurrent cabinets/operations.

### 35.4. Durable cardimport

- duplicate Begin and one active session;
- duplicate Telegram file/message;
- crash after reserve before download;
- crash mid-download;
- blob stored but file-state commit lost;
- retry opens FileSource after restart;
- source permanently unavailable;
- blob hash/size mismatch;
- parse result staged but not activated;
- two validators one file;
- lease loss during parse;
- Cancel during fetch/parse;
- Remove invalid/valid/in-flight file rebuilds aggregate, increments revision and
  fences late worker commit;
- all active parse runs coexist one per non-removed file;
- processing error remains operational, not validation issue;
- referenced blob survives GC;
- terminal retained blob deleted after retention only.

XLSX:

- invalid signature/corrupt/empty workbook;
- missing/duplicate headers;
- huge ZIP/shared strings/rows/cells/formulas;
- missing vendorCode;
- invalid integer/decimal/bool;
- exact decimal and unit conversions;
- deterministic reverse worker completion;
- scalar/characteristic/media conflicts;
- duplicate barcode;
- issue ceiling;
- safe Telegram escaping/truncation;
- fuzz parser/normalizer.

### 35.5. Normalization и immutable batch

- composed/decomposed Unicode remain distinct under lossless v1;
- uppercase/lowercase remain distinct;
- leading/trailing whitespace rejected;
- internal whitespace retained;
- control/NUL rejected;
- DB locale does not alter `COLLATE "C"` equality;
- raw/wire/key propagation;
- numeric/scientific barcode rejected when exact text cannot be proven;
- leading-zero barcode retained;
- raw empty/non-empty GroupID сохраняется lossless без numeric coercion;
- normalization version mismatch;
- stable canonical checksum;
- concurrent two Finalize;
- same idempotency key retry after session finalized;
- known idempotency key from another/revoked actor reveals no BatchID;
- stale revision with different key;
- crash after batch commit/before outbox delivery;
- duplicate outbox delivery creates one transfer;
- runtime batch UPDATE/DELETE rejected.
- duplicate Start during `initializing` returns the same ID and snapshot;
- initializer crash/reclaim resumes the same checkpoint without duplicate rows;
- stale initializer fence cannot move checkpoint or activate;
- deterministic checksum mismatch yields `initialization_failed`, alert and
  zero jobs;
- audited restart uses a new generation with the same immutable batch/targets;
- 50k×target expansion is chunked and activation verifies expected
  counts/checksum without one multi-million-row transaction.

### 35.6. Product identities, concurrency и groups

- Gate GROUP-0 fixtures фиксируют empty/raw GroupID, один vendor в разных
  группах, несовместимые category/subject, size bound и stable grouping key;
- two transfers same CabinetID/vendor;
- dry-run proposal не занимает product/remote-group claim;
- ENV dry-run→live не dispatch-ит старый transfer;
- AuthorizeDispatch повторяет preflight под claims;
- stale proposal digest требует нового approval;
- same digest becomes follower;
- same cards with a different media manifest do not become followers;
- owner created/rejected/partial/conflict/unresolved outcomes fan out
  idempotently to every follower;
- follower created before preflight supports already-present/conflict owner
  outcome with nullable owner-intent;
- follower never creates its own upload/media intent;
- missed follower wake is recovered from monotonic outcome version;
- >fan-out-page followers project through fenced bounded checkpoints;
- follower cap returns recoverable queue-full without creating mutation;
- different digest waits/conflicts;
- previous blocked_uncertain blocks new transfer;
- active v1 unresolved and an equivalent future-version input address the same
  version-independent identity and cannot create another intent;
- same vendor in different CabinetID independent;
- atomic multi-member claim in reversed orders does not deadlock;
- all absent → create;
- vendor exists only in trash → conflict, zero upload;
- all present same imtID/subject → existing;
- mixed same imtID → add;
- two disjoint add transfers same imtID serialize by remote-group claim;
- present/mixed multiple imtID → conflict;
- subject mismatch;
- capacity 29/30/31;
- duplicate remote exact key;
- create request packing group/variant/byte boundaries;
- persisted canonical body restore/digest mismatch;
- add request contains only absent variants;
- existing card media untouched.

### 35.7. Upload crash/fault matrix

Inject process kill or lost lease:

- before product claim;
- after claim;
- after payload save;
- multi-group request transitions all child units atomically;
- one request-level AttemptID links every and only its child intents/units;
- after Error List baseline;
- after EvidenceSeal, before admission;
- after admission, before `ClaimAndStart`;
- after `ClaimAndStart`, before sealed RoundTrip;
- after permit expiry while stale old process is paused and a new singleton
  process epoch is active;
- during request write;
- after WB response, before DB result;
- after first Cards List observation;
- after error batch storage, before binding;
- after outcome, before projection;
- after terminal transition, before notification delivery.

Для каждой generation:

```text
round_trips_per_attempt <= 1
possibly_applied_attempts_per_generation <= 1
stale_fenced_commits == 0
blind_mutation_retries == 0
```

Outcomes:

- NotDispatched admission/config;
- explicit 400/401/402/403/413/429;
- catalog-proven non-applied response creates a new AttemptID only after
  persisted backoff/readiness recovery;
- generic `403` never creates a new attempt;
- expected 200;
- EOF/timeout/shutdown unknown;
- card visibility delayed;
- full success;
- full correlated rejection;
- partial creation;
- unexpected imtID/subject;
- normal deadline unresolved;
- late applied/rejected correction.

### 35.8. Error feed

- empty feed initialization;
- more than 100 batches;
- equal `updatedAt` with different batchUUID;
- UUID lexical order, отличный от ingestion order;
- local feed sequence остаётся monotonic после restart;
- duplicate/overlapping pages;
- poller restart between page insert/checkpoint;
- stale pre-baseline same vendor;
- pre-baseline UUID с post-baseline changed version;
- batch membership отдельно от rejected subset;
- membership unknown never gives strong correlation;
- rejected-only evidence becomes strong only with an exact disjoint Cards List
  partition of the expected set;
- equal/older-than-baseline updatedAt остаётся ambiguous;
- server timestamp interval does not prove occurrence after exact
  `egress_started_at`;
- new full rejection;
- new partial error subset plus found cards;
- partial intersection without complete evidence;
- two strong candidates;
- batch already bound another intent;
- one HTTP request with several groups;
- add membership;
- malformed/oversized violations;
- external writer ambiguity;
- checkpoint lease loss;
- no vendor-code server filter assumption.

### 35.9. Media

- safe HTTPS source;
- HTTP/userinfo/private/loopback/link-local/metadata host rejected;
- DNS rebinding and redirect to private rejected;
- oversized/wrong magic/invalid dimensions;
- immutable asset and stable public URL;
- HMAC expiry/tamper and active/retired signing-key rotation;
- concurrent GC vs GET/Range snapshot materialization never truncates stream;
- public stream slot/byte budget rejects overload before DB blob read;
- concurrent quota reservations cannot oversubscribe the store;
- unknown Content-Length and oversized stream hit the hard cap;
- per-card/per-transfer/global count and byte quotas;
- high watermark blocks before upload and recovers after release;
- asset retention through delayed WB fetch;
- shared asset у двух targets сохраняется до более позднего retention;
- reference release/GC crash replay decrements logical/physical quota exactly
  once and restores high-watermark only after commit;
- dry-run/pending authorization/follower asset survives beyond initial
  retention;
- expired dry-run/proposal requires reprepare/reapproval and releases blob refs;
- long manual-review/unresolved state retains digests but eventually releases
  physical bytes/quota;
- empty media records pre-upload `media_not_requested` and skips only media
  mutation, not card upload;
- created empty-media item atomically becomes `done_no_media_requested`; rejected
  upload becomes `media_not_applicable`;
- deterministic invalid/unsafe asset records
  `media_preparation_rejected` and zero upload;
- one invalid member blocks the whole create/add group with explicit sibling
  projections and zero partial upload;
- transient store/fetch and quota outcomes follow the explicit preparation
  table;
- new card media;
- already-present card skips media;
- pre-state persisted;
- media canonical body restore/digest and signed-URL expiry;
- request refresh allowed after NotDispatched but forbidden after unknown;
- 200 then media appears late;
- 200 and media never appears;
- explicit 409/422 с обязательной reconciliation;
- timeout/EOF after dispatch;
- crash/lost lease after dispatch;
- source URL differs from CDN URL;
- structural two-observation success;
- same count but unchanged pre-state;
- video only;
- 30/31 images;
- media unresolved does not undo card;
- no automatic resubmit.
- crash in atomic upload-outcome→media handoff neither loses nor duplicates
  intent/job;
- `applied_late` creates zero media intents and a correction outbox event.

### 35.10. Workers, singleton process и shutdown

- bounded claims/goroutines/in-flight;
- fairness across 60 targets;
- one blocked target does not occupy all slots;
- lease expiry local/read stage;
- lease expiry upload/media stage;
- heartbeat pause;
- PostgreSQL clock differs from host;
- stale worker conditional update;
- second service process cannot acquire singleton lock and starts no intake/work;
- singleton connection loss cancels process root context;
- stale process epoch cannot claim/dispatch/commit after clean restart;
- stop-before-start/Recreate restart;
- shutdown in each dispatch phase;
- DB unavailable before admission → zero new HTTP;
- restart for one seller does not reset PostgreSQL rate debt/burst;
- sequential old/new binary restart uses one RatePolicyEpoch;
- mixed unresolved plus recoverable target/resource outage remains
  `blocked_dependencies` with attention and resumes safely;
- healthy-target reconciliation continues while the global cohort is unready;
- local follower/projection/outbox jobs work without WB readiness;
- process-local admission is never enabled in dry-run/live.

### 35.11. Authorization, deletion и security

- unauthorized Begin/Reserve/Finalize/details;
- internal Start без valid immutable Finalize grant отклоняется как contract
  error;
- permission revoked before Finalize;
- permission revoked/deleted after successful Finalize does not cancel its
  durable grant/handoff;
- user deleted during collecting;
- user deleted during active/uncertain transfer;
- actor snapshot remains usable for notification/audit;
- callback tampering/stale revision;
- HTML injection in filename/vendor/cabinet/WB code;
- token/`sid`/SellerKey/request body absent in log/error/metric/Telegram;
- raw WB body never persisted;
- unsafe URL not reachable through unwrap;
- DB role cannot read secrets it does not require;
- media asset listing denied.

### 35.12. End-to-end

Primary E2E:

```text
Telegram XLSX
→ durable blob
→ restart
→ validation
→ immutable batch
→ duplicate handoff
→ full target snapshot
→ per-target preparation
→ concurrent same-vendor transfer follower
→ create/add
→ injected crash
→ upload reconciliation
→ media 200/no-op
→ media reconciliation error
→ completed_with_errors
→ statistics
→ durable final notification
```

Дополнительные E2E:

- 60 targets с независимыми rate buckets;
- one read-only configured target blocks full cohort before transfer;
- duplicate seller blocks readiness;
- token rotation same seller resumes;
- WB outage before and after dispatch;
- old Error List history не влияет;
- mixed existing group;
- service restart на каждом persisted state;
- late correction notification.

---

## 36. Verification

### 36.1. После каждого stage

Минимум:

```bash
gofmt -w <changed-go-files>
go test ./...
go vet ./...
git diff --check
```

Для concurrency/core/worker changes:

```bash
go test -race ./...
go test -count=50 ./internal/core/transport/wb/...
go test -count=20 ./internal/feature/cardimport/... ./internal/feature/transfer/...
```

Для parser/normalization:

```bash
go test ./internal/feature/cardimport/...
go test -run=Fuzz -fuzztime=30s ./internal/feature/cardimport/...
```

Fuzz command адаптируется к реальным fuzz target names; CI хранит regression
corpus.

Для migrations:

- apply на empty DB;
- apply на production-like snapshot;
- verify constraints/indexes;
- restart application между migration и feature enable;
- rollback только пока новая feature disabled и до появления live rows.

### 36.2. Contract verification

- checked-in WB fixtures не содержат credentials;
- operation manifest version совпадает с payload/DB catalog version;
- opt-in smoke tests выполняют только safe reads;
- live mutation test запускается вручную в выделенном seller/test cohort;
- никакой CI не создаёт production WB cards автоматически;
- official source URL и verification date обновляются при contract bump.

### 36.3. Static/security verification

- repository secret scan;
- dependency vulnerability scan, если tool закреплён CI;
- redaction/canary tests;
- SQL migration lint;
- Markdown link/heading check;
- architecture dependency test;
- no imports feature→raw HTTP/core credentials;
- no package-level mutable Telegram session state;
- no `TODO/TBD` в contracts, reconciliation и error-action matrices.

### 36.4. Observability acceptance

Перед live:

- dashboard rate admission/wait/429;
- jobs queued/leased/retry/dead;
- singleton process epoch/readiness;
- upload/media dispatch/reconcile/unresolved;
- Error List feed lag;
- blob/media asset retention;
- target readiness/token expiry;
- notification backlog.

Alerts:

- singleton process lock absent;
- target cohort unready;
- credential expiry threshold;
- Error List feed stalled;
- unresolved/partial remote;
- lease/fence conflict spike;
- repeated 401/403;
- media asset/public origin unavailable;
- outbox backlog;
- repeated second-process startup rejection.

---

## 37. Финальный gate и Definition of Done

### 37.1. Architecture

- этот файл является единственным source of truth;
- старые два плана помечены superseded;
- dependency direction соблюдён;
- один composition root создаёт Client/workers/gateways;
- feature не видит token/raw `sid`;
- exact DTO mapper boundary реализован;
- operation catalog полон для flow.

### 37.2. Identity и targets

- one CabinetID ↔ one persistent seller binding;
- duplicate seller отклоняется;
- CabinetID нельзя rebind автоматически;
- complete ordered mutation cohort сохраняется;
- read-only не входит в cohort;
- transfer никогда не обрабатывает молча partial target set.

### 37.3. Import

- каждый принятый файл durable до validation;
- restart не требует старого `io.Reader`;
- parsing bounded/deterministic;
- normalization versioned/lossless;
- Finalize idempotent до проверки stale revision;
- batch immutable;
- outbox delivery duplicate-safe.

### 37.4. Transfer

- per-target preparation сохранена;
- large aggregate initialization chunked, checksummed и atomically activated;
- global product identities сериализуют concurrent transfers;
- identity key не обходится новой normalization version;
- follower outcomes/media fan-out durable и idempotent;
- group-level existing policy реализована полностью;
- create/add packing bounded;
- каждый mutation имеет append-only AttemptID, один permit и максимум один
  RoundTrip;
- evidence/egress state commit предшествует HTTP;
- Error List baseline/cursor/correlation durable;
- upload 200 проходит reconciliation;
- unknown никогда не blind-retry;
- unresolved продолжает блокировать identity;
- late resolution не переписывает audit history.

### 37.5. Media

- source assets immutable и доступны WB;
- pre-upload quota/reservation/high-watermark bounded;
- SSRF/size/type rules enforce;
- media применяется только новым cards;
- каждый media response проходит reconciliation;
- success только structural и так называется;
- 200/no-op не становится done;
- CDN/source URL equality не заявляется;
- unresolved не запускает автоматический repeat.

### 37.6. Runtime

- workers bounded;
- leases/heartbeat/fences до первого mutation;
- state-aware recovery;
- exactly one singleton process/container;
- stop-before-start restart/shutdown tested;
- PostgreSQL admission обязателен и переживает restart;
- один seller bucket использует один active RatePolicyEpoch при mixed catalog
  versions;
- readiness gates не останавливают healthy reconciliation/local jobs;
- PostgreSQL/user deletion не уничтожает history;
- statistics read-only;
- notifications durable.

### 37.7. Quality

- meaningful unit/integration/fault/race tests существуют;
- `go test ./...`, `go test -race ./...` и `go vet ./...` проходят;
- current `out/pgdata` permission obstacle устранён инфраструктурно;
- migration tests проходят;
- security/redaction tests проходят;
- dry-run и canary завершены;
- dashboards, alerts и runbooks готовы;
- live mode включается отдельным audited config change.

### 37.8. Допустимые ограничения

Следующие ограничения считаются осознанными, а не незакрытыми вопросами:

1. WB не предоставляет универсальный upload idempotency/request ID. Поэтому
   exclusive writer, cursor evidence и `unresolved` обязательны.
2. Cards List не доказывает byte equality media после WB transformation.
   Гарантируется только явно обозначенная structural verification.
3. V1 поддерживает Personal token только для разрешённого own/on-premise режима.
4. V1 использует ровно один process/container, один Telegram Long Poller и
   stop-before-start deployment. Horizontal replicas и multi-active scheduler
   требуют отдельного review intake, ownership/claims и egress fencing.
5. Existing cards не редактируются и не получают media replacement.
6. Unresolved outcome может потребовать ручного решения и навсегда блокировать
   автоматическую mutation данного identity до audited resolution.

---

## 38. Матрица закрытия найденных проблем

| Найденная проблема | Решение в этом плане |
|---|---|
| duplicate `sid` создаёт duplicate target | строгая биекция и binding, разделы 5 и 9 |
| read-only попадает в mutation | complete writable cohort, разделы 5 и 9 |
| Personal token deployment не подтверждён | Gate D0, раздел 4 |
| legacy `wb.card_imports` несовместима с новой schema | forward DROP без backfill, раздел 18.3 и Stage 6 |
| revoke между Finalize и async Start неоднозначен | immutable authorization grant создаётся при Finalize; Start не recheck-ит user, разделы 4, 17 и 20 |
| несколько Telegram replicas конфликтуют с Long Polling | ровно один singleton process и один Long Poller, разделы 4, 28 и Stage 17 |
| media требует публичный HTTP endpoint | HTTP/health/media server вводится на Stage 14, раздел 27 |
| rate operation допускает неоднозначный набор buckets | ровно один BucketID на manifest; overlays вне v1, разделы 10 и 12 |
| feature DTO не проходит exact core type | adapter mapper, раздел 11 |
| core catalog не содержит limits/brands/directories | полный catalog v1, раздел 10 |
| naming/manifest неполны | mandatory manifest fields, раздел 10 |
| Stage registry раньше token parser | corrected build order, раздел 9 и Stage 1 |
| timeout cause неразличим | `WithTimeoutCause` и typed priority, раздел 12 |
| body ownership требует недостижимый response+error | реализуемый Client.Do contract, раздел 12 |
| pre-wire mutation safety не определена | DeliveryState fail-closed после RoundTrip, разделы 10–13 |
| restart сбрасывает process-local rate-limit | PostgreSQL admission по SellerKey/BucketID + singleton process, разделы 12 и 28 |
| upload worker появляется раньше lease/media preparation | Stages 10 и 14 до upload Stage 15 |
| нет core-error → feature-action table | раздел 13 |
| живой XLSX reader нельзя восстановить | FileSource + BlobStore, раздел 15 |
| normalized vendorCode не определён | VendorCodeIdentityV1, раздел 16 |
| Finalize retry конфликтует с revision | idempotency lookup first, раздел 17 |
| user hard delete удаляет history | nullable FK/snapshots/no cascade, разделы 18 и 29 |
| preparation cabinet-dependent, payload один | per-target payload, раздел 21 |
| два transfers одновременно upload один vendor | persistent identity/intent/follower, раздел 22 |
| partial existing group ломает GroupID | group decision + UploadAdd, раздел 23 |
| dispatch/crash может дать duplicate upload | append-only attempt + opaque ticket + egress invariant, раздел 24 |
| CardsErrorList ошибочно фильтруется vendorCodes | durable cursor feed, раздел 25 |
| старая error batch связывается новой операции | pre-dispatch baseline + strong correlation, раздел 25 |
| upload 200 принят за business success | обязательный reconciliation, раздел 26 |
| media 200 может ничего не загрузить | every-response structural reconciliation, раздел 27 |
| source media URL может исчезнуть/измениться | immutable MediaAssetStore, раздел 27 |
| media URL нельзя сравнить с WB CDN | explicit structural verification, раздел 27 |
| lease истекает во время WB call | heartbeat/config fence/state-aware recovery, раздел 28 |
| случайно запущен второй process | startup singleton lock fail-fast; intake/work не запускаются, разделы 4 и 28 |
| first-boot fake token отравляет seller binding | authenticated probe до binding, раздел 9 и Stage 5 |
| большие batch×targets требуют giant transaction | chunk initializer + atomic activation, разделы 20 и 29 |
| follower остаётся без terminal projection/media | versioned atomic fan-out, разделы 22 и 29 |
| normalization v2 обходит unresolved identity | version-independent exact wire key, разделы 16 и 22 |
| upload outcome теряет media job при crash | atomic upload-to-media handoff, раздел 27 |
| media assets исчерпывают PostgreSQL | transactional quotas/high-watermark, разделы 27, 29 и 32 |
| asset GC удаляет dry-run/pre-upload media | reference graph + monotonic retention, раздел 27 |
| global readiness замораживает reconciliation | per-job readiness gates, разделы 4 и 32 |
| право мутировать все кабинеты не определено | explicit permission, раздел 4 |
| старые планы продолжают расходиться | canonical status и superseded headers |
| `go test ./...` падает на runtime pgdata | вынести volume из module tree на Stage 0 |

---

## 39. Итоговый контракт

После выполнения плана система представляет собой один связный pipeline:

```text
authorized Telegram command
→ durable bounded XLSX
→ deterministic versioned aggregate
→ idempotent immutable batch
→ complete mutation target snapshot
→ chunked checksummed activation
→ target-specific preparation
→ globally serialized product/group intent
→ per-attempt evidence seal
→ PostgreSQL single-bucket admission and opaque short-lived ticket
→ durable egress start
→ one sealed WB RoundTrip
→ cursor-correlated upload reconciliation
→ immutable media delivery
→ structural media reconciliation
→ authoritative statistics
→ durable notification
```

Ни один слой не может превратить отсутствие доказательства в разрешение повторить
mutation. Это главный end-to-end invariant всего решения.
