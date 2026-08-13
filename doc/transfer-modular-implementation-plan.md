# Модульный план реализации переноса карточек

## 1. Статус документа

Это единственный канонический implementation plan для product flow переноса
карточек. Он разделяет слишком большую feature `transfer` на несколько bounded
modules внутри одного приложения.

Документ:

- фиксирует product semantics, границы модулей и порядок реализации;
- не предлагает микросервисы: v1 остаётся одним Go-процессом и одной
  PostgreSQL;
- не возвращает XLSX parsing или batch creation из `cardimport` обратно в
  transfer flow;
- является единственным актуальным источником требований к реализации переноса
  карточек.

Дата первого среза: 2026-08-13.

Решения ревизии 2026-08-13:

- исходное разделение на `transfer`, `cardprepare`, `cardpublication`, `media`,
  `statistics` и `platform` сохраняется; это разные state machines, а не
  кандидаты на обратное объединение;
- repository находится до первого production release, поэтому полная целевая
  schema этой feature собирается в `migration/000001_init.*.sql`; новая
  migration для transfer flow в рамках этого плана не создаётся;
- после первого production применения `000001` она замораживается, но этот
  post-release migration policy находится за границей текущей реализации;
- баркоды в import payload опциональны: если они не заданы, `Upload`/`UploadAdd`
  не передают пользовательские SKU и WB генерирует их автоматически;
- v1 не создаёт global barcode identity/claim и не сериализует transfers по
  barcode; указанные пользователем barcodes проверяются только внутри immutable
  batch, а remote collision становится обычным publication rejection;
- Cards Limits в v1 являются preparation-time estimate. Повторный read прямо
  перед dispatch отложен в `FUTURE-LIMITS-REFRESH` и не является gate текущего
  rollout.

Короткая карта v1:

| Gate | Владелец | Что должно стать durable | Что запрещено до выхода |
|---|---|---|---|
| 0. Foundation | `platform` + `cardimport` | UoW/outbox/runtime и immutable batch | создавать transfer |
| 1. Dry-run | `transfer` + `cardprepare` + `media` + `cardpublication` | target snapshot, proposals, manifests, publication plans, `ExecutionRoot` | любые WB mutations |
| 2. Live approval | `transfer` | exact persisted authorization trusted actor-а | полагаться только на ENV |
| 3. Product dispatch | `cardpublication` | journal, один call, reconciliation/attribution | retry unknown delivery |
| 4. Media dispatch | `media` | direct attribution + та же live authorization + journal | media для existing/unattributed card |

Каждый следующий gate зависит только от immutable IDs/digests предыдущего.
Изменение ещё не отправленных plan inputs создаёт новую execution generation и
supersedes старую; существующий mutation attempt не переписывается. Изменение
immutable target/seller binding того же transfer не replans operation, а
fail-closed переводит её в manual review и требует нового batch/transfer.

## 2. Причина разделения

Монолитный `transfer` одновременно содержал:

- operation orchestration;
- target snapshot и initialization;
- WB catalog preparation;
- dry-run proposals;
- global product ownership;
- existing-card policy;
- Cards Error List feed;
- create/add mutation journal;
- upload reconciliation;
- media asset ingestion;
- media publication/reconciliation;
- statistics и Telegram UX;
- generic jobs, leases, outbox и process lifecycle.

Это несколько разных state machines с разными правилами безопасности и
разными владельцами данных. Их реализация в одном service/repository привела бы
к большой общей schema, неявным зависимостям и сложному тестированию crash
windows.

Поэтому пользовательское название операции остаётся `transfer`, а реализация
делится на вертикальные модули:

```text
cardimport
    ↓ immutable batch
transfer
    ↓ preparation request
cardprepare
    ↓ immutable proposal
cardpublication
    ↓ attributed newly-created nmID + manifest
media

statistics ← transfer query model

platform → workqueue, outbox, singleton runtime, HTTP server
```

## 3. Общие product-инварианты

- Перенос является копированием во все mutation-ready кабинеты выбранного
  immutable named cohort; пользователь не выбирает отдельные targets.
- Existing card не обновляется, не перемещается и не получает media replacement.
- `MoveCards` не вызывается.
- HTTP `200` от async WB mutation не означает created/accepted: сначала
  проверяется application envelope; успешный envelope означает только submission.
- Unknown или ambiguous mutation delivery не разрешает слепой повтор.
- Все длительные процессы восстанавливаются из PostgreSQL.
- Tokens, raw Authorization и signed media URLs не сохраняются в business
  tables и не выводятся в application logs.
- `TRANSFER_MODE=disabled|dry-run|live`, default `disabled`.
- `MEDIA_AUTO_DISPATCH=false` by default; startup не разрешает `true`, пока WB
  gateway contract capability не подтверждает direct member→`nmID` correlation.
- Смена ENV `dry-run → live` не авторизует существующие operations.
- Любой WB dispatch требует отдельной persisted authorization, связанной с exact
  `ExecutionRoot`/`TargetSetRoot`, seller bindings и revisions; ENV является
  capability/kill gate, но не authorization.
- Target snapshot содержит stable seller binding и write capability. Один
  `CabinetID` нельзя незаметно перепривязать к другому WB seller.
- Неоднозначно атрибутированная новая remote card не получает media mutation.
- HTTP status и transport delivery недостаточны для business outcome: typed
  response envelope проверяется отдельно.
- Один container/process и один Telegram Long Poller защищены singleton
  PostgreSQL advisory lock.
- Удаление пользователя не удаляет batch, transfer, publication или media
  history.

## 4. Gate `CARDIMPORT-DONE`

Модульный flow начинается только с immutable batch. `cardimport` должен
предоставлять:

```go
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
	Start(ctx context.Context, batchID BatchID) error
}
```

Минимальный frozen contract:

```go
type BatchHeader struct {
	ID                   BatchID
	Purpose              Purpose
	ItemsCount           int
	GroupsCount          int
	SchemaVersion        int
	NormalizationVersion int
	Checksum             Digest
	AuthorSnapshot       AuthorSnapshot
}

type AuthorSnapshot struct {
	UserID         *UserID
	TelegramUserID int64
	DisplayName    string // bounded audit value at Finalize time
}

type BatchItem struct {
	ID             BatchItemID
	BatchID        BatchID
	Position       int // global contiguous 1..ItemsCount
	SourceFileID   FileID
	SourceRows     []int
	SourceGroupKey SourceGroupKey // digest of a typed canonical tuple
	VendorCode     string // original normalized value used by v1 identity policy
	Payload        CardPayload
}
```

`Finalize` формирует global order по `(source_file_id, local_position)`, сохраняет
его как contiguous `position` и считает version-tagged checksum по canonical
`BatchSemanticV1{Purpose, ItemsCount, GroupsCount, SchemaVersion,
NormalizationVersion, OrderedItems}`. `BatchID`, `Checksum`, author/audit fields
и timestamps в hash не входят: checksum не цикличен и deterministic для одних
frozen source identities/rows. Он не объявляется content-address двух независимо
загруженных sessions, потому что source file identity входит в batch semantics.
Локальная position, начинающаяся с 1 в каждом файле, не является batch position.

Barcodes в `CardPayload` и каждом size опциональны. Пустой набор не является
ошибкой readiness, не получает локальную генерацию и сериализуется в WB request
в соответствии с typed DTO как отсутствие пользовательского barcode. Непустые
значения остаются частью immutable payload и проверяются на дубликаты внутри
batch.

Concrete v1 contract:

- XLSX barcode column и отдельные cells могут отсутствовать/быть пустыми;
- `ParsedSize.SKUs` допускает empty slice, но `sizes` остаётся обязательным;
- `card_import_items.barcodes` имеет `NOT NULL DEFAULT '{}'` без
  `cardinality(barcodes) > 0`;
- WB DTO использует `SKUs []string` с tag `json:"skus,omitempty"`, чтобы empty
  value действительно отсутствовал в JSON, а не уходил как `null`/`[]`;
- после успешной read-only reconciliation WB-generated `skus/chrtID` можно
  сохранить только как remote observation; immutable batch/request не меняются;
- предоставленный пользователем barcode нельзя молча отбросить: он отправляется
  exact и может соответствовать физической маркировке товара.

Физический contract итоговой `000001`:

- `card_batches`: unique `source_session_id`, purpose, schema/normalization
  versions, item/group counts, checksum и `author_snapshot JSONB`; прежнее имя
  `authorization` не используется, чтобы audit автора не путался с live gate;
- `card_batch_items`: unique `(batch_id, position)`, `source_file_id`,
  `source_rows`, `source_group_key`, exact vendor code и typed payload;
- finalized header/items immutable для application role; исправление import
  требует новой session/batch, а не update frozen rows; `000001` устанавливает
  DB triggers, отклоняющие `UPDATE/DELETE` frozen batch/header items обычной
  application role, а repository не предоставляет mutation methods.

Stable source-group key вычисляется только при `Finalize`:

```text
non-empty Group = FileID + "group" + exact normalized Group value
empty Group     = FileID + "singleton" + vendor_code_key
```

Поэтому пустая группа создаёт отдельную singleton group для каждого vendor code,
а одинаковый непустой Group объединяет items только внутри одного source file.
Это typed length-prefixed canonical encoding с version tag, а не конкатенация
строк с разделителем; digest и normalization version сохраняются в batch.

Gate закрыт, когда:

- `Finalize` создаёт ровно один immutable batch;
- batch имеет checksum и frozen schema/normalization versions;
- `BatchItem` содержит typed payload, stable position, source location, exact
  vendor code и stable source-group key;
- pagination сохраняет строгий порядок;
- transactional `BatchFinalized` outbox доставляет тот же batch ID at-least-once;
- concurrent Finalize и `BatchFinalized` delivery recovery покрыты тестами.

Gate сейчас считается открытым. До его закрытия нельзя merge-ить Stage 1
`transfer` code. Изменения `card_batches`, `card_batch_items`, optional barcodes,
Finalize и outbox вносятся в текущую pre-release `000001_init` migration.
Соответствие реально развёрнутой локальной schema этой версии проверяется
отдельным migration fixture; новый numbered migration не создаётся.

`Finalize` является единственной границей создания batch:

```go
type FinalizeCommand struct {
	SessionID        SessionID
	ExpectedRevision int64
	IdempotencyKey   string
}

func (s *Service) Finalize(
	ctx context.Context,
	actor TrustedActor,
	command FinalizeCommand,
) (BatchHeader, error)
```

Одна transaction:

1. Lock-ит import session `FOR UPDATE`.
2. Проверяет trusted actor и ownership до раскрытия/возврата batch.
3. Для уже finalized session возвращает batch по unique `source_session_id`
   только когда `IdempotencyKey`, `ExpectedRevision` и stored
   `finalized_from_revision` совпадают с исходной командой. Повтор exact command
   идемпотентен; reused key с другим payload и любой другой stale command дают
   typed conflict.
4. Проверяет expected revision, `collecting`, non-empty items, zero
   blocking issues и status `valid` всех active files.
5. Формирует frozen global positions, source locations и source-group keys.
6. Вставляет batch/items и проверяет фактические counts/canonical checksum.
7. Переводит session в `finalized` и append-ит ровно одно `BatchFinalized`
   событие в общий platform outbox с unique business key `card-batch:<BatchID>`.

Ошибка любого шага откатывает batch, items, session transition и outbox event.
Telegram callback «Готово» вызывает только `Finalize`, но не `transfer.Start`
напрямую. Outbox handler вызывает `BatchConsumer.Start(batchID)` at-least-once и
помечает delivery только после `nil`; crash до отметки повторно доставляет тот же
BatchID. Отдельная `card_import_handoffs` state machine в итоговую `000001` не
входит. `BatchReader` возвращает только finalized frozen rows.

`TrustedActor` создаётся Telegram/auth adapter-ом из проверенного transport
context. Actor IDs никогда не берутся из callback payload; payload содержит
только opaque command ID и expected revision. `AuthorSnapshot` является только
audit evidence автора batch и никогда не разрешает WB mutation.

Все use cases, изменяющие import session/files/items (`SaveParsed`, replace,
delete, issue resolution и `Finalize`), сначала lock-ят ту же session row
`FOR UPDATE` и после lock требуют state `collecting`; они не полагаются на ранее
прочитанный status. Поэтому parse transaction, начавшаяся до Finalize, либо
commit-ится первой и попадает в batch, либо после Finalize просыпается и
отклоняется. `finalized_from_revision`, finalize key/digest и batch ID
сохраняются атомарно с session transition.

Ни один новый модуль не читает `card_import_sessions`, `card_import_files` или
`card_import_items` напрямую.

## 5. Модули и владение данными

| Модуль | Ответственность | Чем не занимается |
|---|---|---|
| `transfer` | operation, target snapshot, initialization, orchestration, общий status | WB catalog, create/add, media |
| `cardprepare` | read-only catalog resolution, validation, payload, dry-run proposal | remote ownership и mutations |
| `cardpublication` | product identities, preflight, create/add, Error List, reconciliation | XLSX, catalog mapping, media |
| `media` | assets, manifests, public delivery, media mutation/reconciliation | product create/add |
| `statistics` | current read model и Telegram presentation | WB calls и business mutations |
| `platform` | workqueue, outbox, singleton runtime, clock, HTTP lifecycle | business decisions |

Правило владения: только модуль-владелец изменяет свои таблицы. Другие модули
используют typed commands, query ports и durable events.

## 6. Модуль `transfer`

### 6.1. Ответственность

`transfer` является небольшим orchestrator-ом пользовательской операции:

- принимает `batchID`;
- идемпотентно создаёт operation;
- сохраняет полный ordered cabinet snapshot;
- chunk-ами разворачивает batch в items/groups/targets;
- создаёт downstream work;
- принимает typed результаты других модулей;
- обновляет item-target projection и counters;
- вычисляет общий operation status;
- создаёт final/manual-review notification events.

`transfer` не вызывает Content API напрямую.

### 6.2. Данные

```text
wb.transfers
wb.transfer_targets
wb.transfer_items
wb.transfer_groups
wb.transfer_group_members
wb.transfer_group_targets
wb.transfer_item_targets
wb.transfer_initializations
wb.transfer_dispatch_slots
wb.transfer_live_authorizations
wb.transfer_live_authorization_events
wb.transfer_events
wb.transfer_notifications
```

Ключевые ограничения:

- `UNIQUE(batch_id)`;
- `UNIQUE(transfer_id, cabinet_id)`;
- `UNIQUE(transfer_id, source_group_id, target_id)` для
  `transfer_group_targets`;
- composite FKs гарантируют same-transfer ownership:
  `(transfer_id, source_group_id) → transfer_groups(transfer_id, id)`,
  `(transfer_id, target_id) → transfer_targets(transfer_id, id)` и
  `(transfer_id, group_target_id) → transfer_group_targets(transfer_id, id)`;
- immutable target snapshot после create;
- immutable batch-derived rows после activation;
- delivery deduplicated по `(event_id, consumer)`, logical business event имеет
  producer-side `UNIQUE(event_type, aggregate_id, aggregate_revision)`, а
  применение результата требует expected monotonic revision aggregate;
- одна active live authorization на execution generation; её exact binding
  immutable, lifecycle пишется append-only events и проецируется владельцем;
- user foreign key использует `ON DELETE SET NULL`, history не каскадит.

`wb.transfer_group_targets` — canonical aggregate межмодульного flow:

```text
GroupTargetID
TransferID
SourceGroupID
TargetID
Revision
PreparationStatus
AssetStatus
PublicationStatus
MediaStatus
ResultCode
```

Group и target принадлежат тому же transfer. Каждый `transfer_item_target`
ссылается на свой `GroupTargetID`. После activation существуют ровно
`groups_count × targets_count` group-target rows; все команды и результаты
используют persisted `GroupTargetID`, а не независимо собранную пару IDs.

Для composite FK parent tables имеют `UNIQUE(transfer_id, id)`.
`transfer_item_targets` хранит также `source_group_id`; FK
`(transfer_id, source_group_id, transfer_item_id)` ведёт в
`transfer_group_members`, а `(transfer_id, source_group_id, group_target_id)` —
в `transfer_group_targets`. Так membership того же source group обеспечивается
БД, не application-only проверкой.

Каждая ось `PreparationStatus|AssetStatus|PublicationStatus|MediaStatus` имеет
bounded форму `not_started|running|succeeded|rejected|unresolved|skipped`.
Allowed transition задаётся versioned reducer-ом:

```text
not_started → running → succeeded | rejected | unresolved | skipped
unresolved → succeeded | rejected   // только append-only evidence correction
```

Terminal state не возвращается в `running`, correction не создаёт mutation.
Каждый result несёт expected `GroupTargetRevision`; reordered future result
сохраняется в inbox до требуемой revision, stale/duplicate становится no-op.
Operation projection вычисляется reducer-ом из этих осей, а не произвольным
status update worker-а.

`transfer_targets` замораживает cohort name/position, `CabinetID`, `SellerKey`,
binding/capability revisions и required capabilities; token/token fingerprint
туда не попадает. `transfer_live_authorizations` хранит execution generation/root,
component roots, execution revision, expiry и current projected state, а
`transfer_live_authorization_events` — append-only actor/state audit. Attempts
ссылаются на authorization ID/revision внешним typed ID, не изменяя эти tables.

### 6.3. `Start`

```go
func (s *Service) Start(
	ctx context.Context,
	batchID cardimport.BatchID,
) (TransferID, error)
```

Порядок:

1. Найти existing transfer по batch ID и сразу вернуть его.
2. Для нового operation прочитать immutable BatchHeader.
3. Проверить purpose/schema/checksum и audit author snapshot.
4. Получить полный ordered mutation-ready snapshot выбранного named cohort через
   target-registry port. `Clientset.Cabinets()` недостаточен для этого контракта.
5. Если snapshot пуст, partial, содержит duplicate seller или недоступный target,
   transfer row не создавать.
6. Проверить overflow-safe capacity gates до materialization и вычислить
   `TargetSetRoot`.
7. Одной transaction создать transfer в `initializing`, targets и unique
   initializer job.
8. При concurrent conflict вернуть победивший transfer, не подставляя новый
   snapshot.

Target-registry port возвращает:

```go
type MutationTargetSnapshot struct {
	CohortName string
	Revision   Digest
	Targets    []MutationTarget
}

type MutationTarget struct {
	Position           int
	CabinetID          CabinetID
	SellerKey          Digest // H("wb-seller-key:v1", canonical sid)
	BindingRevision    int64
	CapabilityRevision int64
	ContentRead        bool
	ContentWrite       bool
	CredentialExpiresAt time.Time // registry-only validation metadata
}
```

WB Core валидирует JWT claims `sid`, Content capability, read-only bit и expiry,
затем подтверждает token безопасным authenticated WB read. Persistent
`CabinetID ↔ SellerKey` binding создаётся/обновляется только после успешного
probe; raw `sid` и token не покидают WB Core. Registry не возвращает partial cohort.
Ротация token того же seller сохраняет binding; другой seller под тем же
`CabinetID` делает cohort unavailable. Display name и raw token в snapshot/digest
не входят.

`SellerKey` — versioned canonical digest, одинаковый для одного WB seller после
restart/token rotation. `BindingRevision` меняется только при explicit accepted
seller rebind, `CapabilityRevision` — при verified capability/read-only change;
обычная ротация token с тем же seller/capabilities не меняет их. `TargetSetRoot`
считается по canonical ordered cohort, CabinetID, SellerKey, обеим revisions и
required capabilities.

Registry владеет technical `wb.mutation_target_bindings` с unique `CabinetID`,
`SellerKey`, binding/capability revisions, capability snapshot и
verified-at/status. Mismatch никогда не перезаписывает binding автоматически:
он переводит запись в
`identity_mismatch` до явного administrative resolution. Это не transfer
business state и credentials там не сохраняются.

Snapshot сохраняет ordered `Position`, `ContentRead=true`, `ContentWrite=true` и
expiry/probe evidence revision; `CredentialExpiresAt` используется для readiness
и dispatch validation, но не считается seller identity. Duplicate CabinetID или
SellerKey и любой non-readable/non-writable/expired target делают весь cohort
unavailable, targets не фильтруются молча.

Для обычного production flow `CohortName=production` содержит все утверждённые
mutation targets. Isolated canary запускается с отдельным deployment/config
`CohortName=canary`, а не пользовательским исключением одного target из уже
созданного production transfer. Canary и production process не работают
одновременно против одной DB/advisory-lock key: это последовательная смена
configured cohort либо полностью отдельное окружение с отдельной DB. Promotion
использует новую import session и новый immutable batch/TransferID для полного
production cohort; finalized canary batch не переиспользуется и не клонируется
неявно.

Adapter `transfer.Consumer` реализует `cardimport.BatchConsumer` и отбрасывает
возвращаемый TransferID. Поэтому `cardimport` не импортирует concrete transfer.

Capacity policy задаётся конфигурацией и как минимум ограничивает
`ItemsCount`, `GroupsCount`, `TargetsCount`, `ItemsCount×TargetsCount` и
`GroupsCount×TargetsCount`. Произведения считаются с overflow check по значениям
BatchHeader и snapshot до создания transfer; превышение возвращает typed
`transfer_capacity_exceeded`, не оставляя operation/jobs. Это не WB Cards
Limits, а локальная защита PostgreSQL/workqueue от неконтролируемой матрицы.

### 6.4. Initialization

Initializer stable-position chunks:

```text
ListBatchItems
→ validate contiguous positions
→ upsert transfer items
→ upsert source groups/members
→ upsert group-target rows for every group × target
→ create item-target rows referencing group-target
→ update checkpoint/count/checksum
```

После последнего chunk проверяются expected counts, group invariants, target
snapshot и rolling checksum. Успешная activation одной transaction выполняет:

```text
phase initializing → preparing; outcome remains running
→ PreparationRequested events/jobs
```

Deterministic mismatch даёт `initialization_failed`, attention event и zero
downstream jobs.

### 6.5. Status projection

Status хранится ортогональными projections, потому что phase, terminal outcome,
operator attention и live gate не образуют одну линейную цепочку:

```text
phase:
  initializing | preparing | assets | awaiting_authorization
  | publishing | reconciling | media

outcome:
  running | initialization_failed | completed
  | completed_with_errors | manual_review | cancelled

attention_code:
  nullable bounded safe code

live_gate:
  unavailable | requested | authorized | revoked | expired | superseded | closed
```

`awaiting_authorization` означает завершённый reproducible dry-run proposal и
zero dispatchable mutations. `manual_review` допускает late read-only evidence;
оно может создать correction event, но не новый mutation attempt. Status
вычисляется из durable work/results и не изменяется worker-ом произвольно.

### 6.6. Live authorization

Read-only planning, approval и live dispatch входят в одну execution generation
одного `TransferID`: `UNIQUE(batch_id)` не позволяет создавать второй transfer
для того же batch. Generation увеличивается только при явном replan после
изменения ещё не отправленных planning artifacts; old generation становится
`superseded` и никогда не dispatch-ится. Immutable target/seller binding mismatch
всегда fail-closed/manual. После готовности всех proposals, manifests и
publication plans
`transfer` создаёт `requested` authorization для exact generation; только
trusted user action может перевести её в `authorized`.

```text
ExecutionRoot = hash(
  contract version
  + execution generation
  + ProposalSetRoot
  + PublicationPlanSetRoot
  + ManifestSetRoot
  + TargetSetRoot
)
```

```go
type RequestLiveCommand struct {
	TransferID                    TransferID
	ExpectedExecutionRevision     int64
	ExpectedExecutionGeneration   int64
	ExpectedExecutionRoot         Digest
	ExpectedAuthorizedSlotSetRoot Digest
	IdempotencyKey                string
}

type ApproveLiveCommand struct {
	TransferID                    TransferID
	LiveAuthorizationID           LiveAuthorizationID
	ExpectedAuthorizationRevision int64
	ExpectedExecutionRevision     int64
	ExpectedExecutionGeneration   int64
	ExpectedExecutionRoot         Digest
	ExpectedAuthorizedSlotSetRoot Digest
	IdempotencyKey                string
}

type RevokeLiveCommand struct {
	TransferID                    TransferID
	LiveAuthorizationID           LiveAuthorizationID
	ExpectedAuthorizationRevision int64
	SafeReasonCode                string
	IdempotencyKey                string
}
```

Actor передаётся отдельным `TrustedActor` из проверенного Telegram/auth context,
не из callback payload. Persisted authorization содержит `LiveAuthorizationID`,
command roots, exact cohort/seller binding revisions, actor/permission snapshot,
approval timestamp, expiry и state:

```text
requested → authorized → revoked | expired | superseded | closed
```

Lifecycle принадлежит явным commands/events:

- `RequestLive` создаёт конкретную requested row с exact execution/slot roots;
- `ApproveLive` и `RevokeLive` принимают trusted actor и exact authorization ID;
- clock worker вызывает internal `ExpireLive(id, expectedRevision, now)`;
- replan вызывает internal `SupersedeLive(id, expectedRevision,
  newExecutionRevision)` до публикации новой generation;
- `CloseLive` допустим только когда все bound slots begun/terminal либо явно
  исключены без mutation.

Каждая команда append-ит одно versioned authorization event и обновляет
projection в той же transaction. Unique `(command_type, idempotency_key)` хранит
canonical command digest и trusted actor digest: exact replay возвращает прежний
result, тот же key с другим payload или actor даёт `idempotency_conflict`. Старый
callback не может выбрать «текущую» authorization или новый remaining slot set,
потому что несёт exact ID, revision и `ExpectedAuthorizedSlotSetRoot`.

У aggregate три независимых monotonic counters:

- `ExecutionRevision` замораживает planning artifacts одной generation и входит
  в authorization;
- `AuthorizationRevision` сериализует approve/revoke/expiry lifecycle;
- `ProjectionRevision` защищает status/counters/query model и не входит в
  authorization.

`consumed` не используется: одна authorization может покрывать несколько
заранее зафиксированных publication/media attempts одной generation. Изменение
proposal/manifest/publication plan set до первого mutation создаёт новую
generation и переводит прежнюю authorization в `superseded`. Status, counters и
result projection используют отдельную `ProjectionRevision` и не инвалидируют
authorization. Target set/seller binding immutable: их mismatch даёт
fail-closed/manual, а не новую generation того же transfer.

После первого begun slot replan не пересобирает весь transfer. Новая generation
может materialize только never-begun actions: begun/committed/unknown slots
переносятся как sealed references с исходными IDs/generation/outcomes и никогда
не получают новый DispatchSlotID. ExecutionRoot новой generation включает
`SealedSlotSetRoot + RemainingPlanSetRoot`. Если изменение невозможно изолировать
от begun group/request membership, affected group-target остаётся manual review,
а не получает повторный mutation.

Execution plan materializes `transfer_dispatch_slots` и заранее присваивает
deterministic, kind-tagged `DispatchSlotID` каждому
mutating publication action и каждому потенциальному media member/manifest.
Read-only `ALREADY_PRESENT_*`/conflict plans не имеют dispatch slot и не требуют
live approval. Authorization дополнительно связывает `AuthorizedSlotSetRoot`.
В `publication_attempts` и `media_attempts` действует собственный
`UNIQUE(dispatch_slot_id)`, а type tag запрещает cross-kind collision: смена
authorization никогда не создаёт второй attempt для уже committed/unknown slot.
Transfer projection помечает slot begun/terminal только по idempotent module
events; lag projection не опасен благодаря module-side unique constraint.

`transfer_dispatch_slots` имеет kind-specific `CHECK` constraints и два partial
unique indexes, чтобы PostgreSQL `NULL` semantics не пропустила duplicate:

```text
publication: UNIQUE(transfer_id, execution_generation, group_target_id,
                    member_key) WHERE slot_kind='publication'
media:       UNIQUE(transfer_id, execution_generation, group_target_id,
                    member_key, manifest_id) WHERE slot_kind='media'
```

Publication slot требует `manifest_id IS NULL`, media — `manifest_id IS NOT
NULL`; оба требуют non-empty member key. Также есть `UNIQUE(id, slot_kind)`.
Обе attempt tables хранят checked constant `slot_kind`:
`publication_attempts` использует composite FK `(dispatch_slot_id, slot_kind)`
с `CHECK(slot_kind='publication')`, `media_attempts` — такой же FK с
`CHECK(slot_kind='media')`; в каждой таблице `dispatch_slot_id UNIQUE NOT NULL`.
Это физически реализуемая cross-module exclusivity, а не предполагаемый «global
UNIQUE» в двух независимых tables.

Если authorization истекла или отозвана до части slots, `transfer` может создать
новую `requested` authorization для той же ExecutionGeneration/ExecutionRoot,
но только с root ещё не начатых slots. Требуется новое explicit trusted approval;
revoke/expiry не approve-ит продолжение автоматически. Уже committed attempts
только reconciles. Для direct-attributed media eligibility используется тот же
механизм: действующая authorization выпускает `MediaDispatchRequested`, а при её
expiry operation возвращается в `awaiting_authorization` до нового approval
оставшегося media slot.

Authorization job:

1. Требует current `TRANSFER_MODE=live`; `disabled`/`dry-run` fail closed.
2. Проверяет permission trusted actor и сохраняет immutable actor snapshot.
3. Lock-ит execution plan/authorization и проверяет expected execution и
   authorization revisions/generation.
4. Повторно получает named cohort и сравнивает `TargetSetRoot`, SellerKey и
   binding revisions с transfer snapshot.
5. Пересчитывает exact component roots/ExecutionRoot и проверяет отсутствие
   stale preparations/publication plans.
6. Idempotently возвращает прежний result по command key.
7. Одной transaction append-ит `authorized` event и создаёт exact
   `PublicationDispatchRequested` только для remaining `CREATE_GROUP`/
   `ADD_TO_GROUP` slots.

Для сохранения module ownership `cardpublication` и `media` используют typed
transfer-owned port, работающий внутри caller-owned transaction:

```go
type LiveAuthorizationVerifier interface {
	LockValid(
		ctx context.Context,
		tx DBTX,
		check LiveAuthorizationCheck,
	) (LiveAuthorizationEvidence, error)
}
```

`LiveAuthorizationCheck` содержит authorization ID/revision, execution
generation/root, approved и current `TargetSetRoot`, component roots,
CabinetID/SellerKey и pinned `ClientGeneration`. Перед каждой attempt transaction
registry пересчитывает полный current named-cohort root, а не проверяет только
выбранный target; добавление/удаление/перестановка другого cabinet тоже даёт zero
call и manual review. В одной
transaction verifier lock-ит authorization row, проверяет `authorized`, TTL и
exact binding, после чего caller создаёт immutable attempt со ссылкой на
`LiveAuthorizationID` и его revision. Revoke и создание attempt сериализуются
этой блокировкой: победивший revoke запрещает call, а committed attempt уже
безотзывно авторизован и может только dispatch/reconcile.

Каждый dispatch непосредственно перед созданием/claim mutation attempt снова
проверяет persisted authorization state/expiry, exact revisions и runtime
`TRANSFER_MODE=live`, а current target — через seller-bound registry. Потеря
write capability или SellerKey mismatch даёт zero call. Revoke запрещает ещё не
созданные attempts; уже committed или unknown attempt только reconciles. ENV
остаётся kill/capability gate и никогда не выпускает старую operation
самостоятельно.

## 7. Модуль `cardprepare`

### 7.1. Ответственность

Модуль выполняет только безопасные WB reads и строит immutable target-specific
proposal:

- global semantic preparation исходной карточки;
- category → subject resolution;
- characteristics schema;
- реально используемые directories;
- brand validation;
- required/type/unit/maxCount/value validation;
- Cards Limits read;
- target-specific typed `UploadCard`;
- serialized-size/bounds validation;
- payload/semantic/proposal digests;
- dry-run result.

Модуль не читает normal/trash Cards List для mutation decision, не получает
product ownership и не вызывает mutations.

Cards Limits snapshot фиксируется в proposal как observation time/value и в v1
читается ровно один раз для target-specific preparation; между transfers этот
read не кэшируется. Изменение лимита между preparation и dispatch разрешается
через обычный typed WB rejection и reconciliation. Повторный just-in-time read
может быть добавлен только после измерения реальной частоты этой гонки
(`FUTURE-LIMITS-REFRESH`); у `cardpublication` зависимости от Cards Limits нет.

### 7.2. Данные

```text
wb.card_preparations
wb.card_preparation_groups
wb.card_preparation_items
wb.card_preparation_payloads
wb.card_metadata_snapshots
wb.card_metadata_cache
```

Каждый preparation result привязан к:

```text
GroupTargetID
TransferID
TargetID/CabinetID
SourceGroupID
Batch schema/normalization version
WB operation/catalog version
MetadataSnapshotID
Input revision
```

### 7.3. Cache policy

Metadata cache key:

```text
CabinetID
+ OperationCatalogVersion
+ OperationID
+ ParametersDigest
```

Subjects/characteristics/directories/brands могут иметь bounded TTL. Cards
Limits, normal Cards List, Trash List и mutation preflight между transfers не
cache-ятся.

### 7.4. Outcomes

```text
prepared
subject_not_found
subject_ambiguous
characteristic_missing
characteristic_invalid
brand_not_found
directory_value_invalid
card_limit_exceeded
payload_too_large
target_temporarily_unavailable
catalog_contract_error
```

Data errors терминальны для конкретного group-target. Recoverable dependency
errors получают bounded retry через workqueue.

### 7.5. Выходной контракт

```go
type PreparationCompleted struct {
	EventID       EventID
	TransferID    TransferID
	GroupTargetID GroupTargetID
	PreparationID PreparationID
	ProposalRoot  Digest
	Revision      int64
}
```

Large payload в событие не помещается. Следующий модуль получает immutable
PreparationID и читает proposal через query port.

## 8. Модуль `cardpublication`

### 8.1. Ответственность

Только этот модуль может создавать карточки WB. Он владеет:

- persistent product identity;
- group claims/followers;
- remote group claim для add;
- full normal/trash preflight;
- create/add/already/conflict decision;
- request packing;
- Cards Error List feed;
- mutation intents и attempts;
- `Upload`/`UploadAdd` dispatch;
- upload reconciliation и late resolver.

### 8.2. Product identity

Global identity key:

```text
CabinetID + exact vendor_code_key COLLATE "C"
```

`vendor_code_key` строится versioned normalization policy: текущая v1 сохраняет
original normalized importer value, не меняет регистр и не выполняет Unicode
folding. Точные bytes сравниваются под `COLLATE "C"`; normalization version
входит в batch/proposal/intent evidence.

Barcode/SKU не входит в global identity v1. Если source не передал barcode, WB
генерирует его автоматически. Если передал, значение входит в request/evidence,
но remote collision не получает отдельный global claim и классифицируется как
terminal member rejection только при exact structured correlation; ambiguous
mapping остаётся unresolved. Это осознанное упрощение не разрешает retry после
possibly applied mutation.

Identity переживает transfer и хранит:

- known `nmID`/`imtID`/subjectID;
- active group claim;
- active mutation intent;
- generation/version;
- resolved или unresolved remote state.

States:

```text
reserved
mutation_pending
remote_present
rejected
blocked_uncertain
remote_missing
remote_conflict
```

Identity state описывает remote presence/claim, но не ownership. Доказательство
создания хранится отдельно в immutable attribution evidence. Одна identity имеет
максимум одного active/unresolved mutation owner.

### 8.3. Group claim и followers

Один cabinet-scoped normal/trash observation generation может обслужить много
group claims в пределах bounded freshness window. Full pagination не запускается
после каждого claim. Непосредственно перед intent выполняется targeted exact
recheck всех decision inputs группы: normal/trash presence exact vendor-code
keys, `imtID`, subject, remote membership/capacity и exact packed request bytes.
Это уменьшает нагрузку, но остаётся observation, а не внешней блокировкой WB.

```text
recheck matches approved PublicationPlan/ExecutionRoot
→ authorization + claims + attempt transaction

recheck changes decision, membership, target imtID/subject/capacity or bytes
→ PublicationPlanStale
→ zero claim, zero attempt, zero call
→ supersede authorization/generation
→ rebuild read-only plan
```

Recheck `ObservationID` и digest сохраняются в attempt evidence и повторно
сравниваются с approved plan внутри dispatch transaction. Внешняя запись после
observation всё равно возможна, поэтому этот шаг не ослабляет attribution rules.

Dry-run/preflight только строит immutable `PublicationPlan`; active identity и
group claims он не захватывает. После exact live authorization и targeted recheck
короткая dispatch transaction:

1. Через `LiveAuthorizationVerifier` lock-ит exact authorization.
2. Lock-ит identities в deterministic order и проверяет active/unresolved state.
3. Проверяет, что PublicationPlan/ExecutionRoot не stale.
4. Создаёт или находит group claim/followers.
5. Сохраняет exact desired semantic digest, membership, intent и attempt.
6. Commit-ит до единственного WB call.

WB API не выдаёт snapshot token, поэтому full read не называется атомарным или
полностью authoritative: pagination использует overlap, deduplication и
stable-pass rules, а observation generation/time сохраняются как evidence.

Если второй publication встречает claim:

- тот же exact desired value → durable follower;
- другой desired value → ждёт owner outcome, затем получает existing/conflict;
- unresolved owner → `global_identity_blocked`;
- собственный WB mutation не выполняется.

### 8.4. Existing-card decision table

| Remote state | Outcome |
|---|---|
| Все variants отсутствуют | `CREATE_GROUP` |
| Часть существует в одном `imtID` и ожидаемом subject | `ADD_TO_GROUP` |
| Все существуют в одном `imtID`, ожидаемом subject и совместимы по доступным полям | `ALREADY_PRESENT_COMPATIBLE` |
| Все существуют, но доступные semantic fields отличаются | `ALREADY_PRESENT_DIFFERENT_UNTOUCHED` |
| Хотя бы один exact vendor code найден в trash | `CARD_IN_TRASH_CONFLICT` |
| Существующие variants имеют разные `imtID` | `GROUP_CONFLICT` |
| Existing subject отличается | `SUBJECT_CONFLICT` |
| Итоговая group превышает 30 variants | `GROUP_CAPACITY_CONFLICT` |
| Один exact key соответствует нескольким remote cards | `REMOTE_IDENTITY_CONFLICT` |

Оба `ALREADY_PRESENT_*` outcomes ничего не обновляют и сохраняют bounded diff по
полям, реально возвращаемым Cards List. Не наблюдаемые этим API поля, включая
цену, не объявляются совпавшими. Для `ADD_TO_GROUP` request содержит только
missing variants и получает durable remote-group claim по `(CabinetID, imtID)`.

### 8.5. Request packing

```text
target
→ operation kind
→ subject
→ complete source group
→ bounded request
```

- Source group не режется между create requests.
- Один add request относится к одному target `imtID`.
- Create request может содержать несколько complete groups.
- Учитываются documented group/variant bounds и serialized bytes.
- Exact membership сохраняется до dispatch.

### 8.6. Error List feed

Error List остаётся внутренним submodule `cardpublication/errorfeed`, потому что
его semantics связана с mutation baseline и request membership.

Он владеет:

- per-target cursor/checkpoint;
- overlap pagination;
- normalized error batches;
- deduplication;
- pre-dispatch baseline;
- correlation evidence.

Ошибка до baseline не может коррелироваться с новым request. Multiple strong
candidates дают unresolved, а не произвольный выбор.

### 8.7. Mutation journal

Error List baseline сначала получается безопасным read и сохраняется как
immutable evidence. Затем до WB call одна transaction через
`LiveAuthorizationVerifier` lock-ит действующую exact authorization и создаёт:

- immutable publication intent;
- exact request row;
- request-level attempt;
- child intent links;
- ссылку на Error List baseline;
- dispatch recheck `ObservationID`/digest;
- `LiveAuthorizationID`/revision;
- attempt в `dispatching`.

После commit выполняется ровно один typed call:

```go
Cards().Upload(...)
Cards().UploadAdd(...)
```

WB gateway возвращает не только transport error, а typed mutation submission:

```go
type SubmissionDisposition string // accepted | rejected_proven | uncertain

type MemberDisposition struct {
	PublicationMemberID PublicationMemberID
	RequestMemberIndex  int
	VendorCode          string
	Disposition         SubmissionDisposition
	SafeCodes           []string // bounded, normalized, sorted
}

type SubmissionResult struct {
	Delivery       DeliveryState
	HTTPStatus     int
	SafeRequestID  string
	EnvelopeVersion string
	Disposition    SubmissionDisposition
	SafeCode       string
	MemberResults  []MemberDisposition
	UnmatchedCount int
}
```

- successful HTTP status + `ResponseMeta.Error=false` означает только accepted
  submission;
- successful HTTP status + `ResponseMeta.Error=true` никогда не является
  accepted submission и возвращается как typed application problem;
- non-success response body bounded-decode-ится в safe WB error envelope;
  documented `additionalErrors` с exact unique vendor codes могут дать bounded
  per-member `rejected_proven`;
- request/member становится terminal `rejected_proven` только когда versioned
  WB operation contract однозначно доказывает, что mutation не принята;
  неизвестный/general `ResponseMeta.Error`, неполный или duplicate member mapping
  получает `uncertain` и reconciliation, но не retry;
- raw `errorText`, arbitrary details и credentials не сохраняются в business
  result/logs; gateway маппит их на versioned safe codes;
- malformed envelope при начавшемся round trip остаётся uncertain evidence, а не
  разрешением повторить mutation.

Versioned classifier matrix является fixture-ом по WB operation/HTTP/envelope
shape: только документированная synchronous rejection с доказанным no-acceptance
получает `rejected_proven`; generic `5xx`, unknown `4xx`, request-wide error без
такого contract proof, duplicate/unmatched vendor, mixed incomplete member set и
oversized/malformed body дают `uncertain`. Per-member correlation идёт сначала по
persisted request index/member ID, vendor code служит проверяемым evidence, а не
единственным map key. Unknown fields сохраняются только bounded counts/safe code.

Attempt states различают фактическое знание, а не только HTTP status:

```text
planned → dispatching → response_accepted | response_rejected_proven
                     ↘ submission_unknown
response_accepted | submission_unknown → reconciling
```

`submission_unknown` включает и неизвестную delivery, и полученный, но
malformed/unmapped application response; `SubmissionResult.Delivery` сохраняет
разницу как evidence.

Инварианты:

```text
round_trips_per_attempt <= 1
possibly_applied_attempts_per_publication_member_identity_generation <= 1
unknown outcome => no automatic mutation retry
```

### 8.8. Reconciliation

Evidence:

- normal Cards List;
- Trash List;
- Cards Error List после baseline.

Outcomes:

```text
already_present
created_attributed
created_observed_unattributed
rejected
partial_remote
remote_conflict
unresolved
```

`response_accepted`, `submission_unknown`, `UnknownDelivery` и crash после
`dispatching` переходят в reconciliation. Proven exact `response_rejected_proven`
терминален только для однозначно mapped members; partial/unmapped members
остаются unresolved. Reconciliation deadline не доказывает отсутствие remote
effect; operation остаётся unresolved/manual.

Для manual resolution существует только evidence-based `ResolveUnknown`:

- `mark_remote_present` требует persisted exact remote observation;
- `mark_rejected` требует correlated terminal Error List/application evidence;
- `close_unresolved_no_retry` закрывает operator attention, но сохраняет identity
  `blocked_uncertain`.

Команды «повторить тот же request» нет. Crash между commit journal и socket мог
произойти до отправки, но отсутствие card после deadline всё равно не доказывает
no-effect асинхронного WB call.

### 8.9. Attribution boundary

Upload/Create response текущего WB contract не возвращает `nmID` и universal
idempotency/correlation key. Поэтому появление exact vendor code после baseline
само по себе не доказывает, что карточку создал именно этот intent: между reads её
мог создать внешний writer.

Для v1 действуют строгие уровни evidence:

| Evidence | Publication outcome | Automatic media |
|---|---|---|
| exact card существовала до intent | `already_present` | запрещена |
| WB в будущем вернул direct request/member → `nmID` correlation | `created_attributed` | разрешена |
| card появилась после dispatch, но direct correlation отсутствует | `created_observed_unattributed` + `manual_review` | запрещена |
| correlated Error List rejection | `rejected` | запрещена |
| conflicting/partial/unknown evidence | `unresolved`/`partial_remote` | запрещена |

Immutable attribution record содержит:

```text
AttributionEvidenceID
TransferID
GroupTargetID
PublicationMemberID
CabinetID
NMID
ExecutionGeneration
ExecutionRoot
PreflightObservationID
AttemptID
PostObservationID
ErrorBaselineID
DirectCorrelationID // nullable safe protocol identifier
Level = direct | observed_after_attempt | ambiguous
SafeReasonCode
```

`direct` допустим только при protocol-level correlation exact request member с
immutable `nmID`. Временная последовательность observations сама по себе даёт не
выше `observed_after_attempt`. Unique `(publication_member_id, attempt_id,
nmID, level)` и same-transfer/group-target FKs не позволяют комбинировать
evidence одной карточки с event другой; media query port сравнивает весь tuple.

Стабильные повторные Cards List observations повышают confidence remote state,
но не превращаются в ownership proof. `MediaEligible` разрешён исключительно
для `created_attributed`. До появления direct WB correlation текущий Upload flow
может создавать карточки, но автоматическая media publication для них остаётся
fail-closed. Ручное решение не вызывает media через этот service.

### 8.10. Данные

```text
wb.product_identities
wb.publication_group_claims
wb.publication_group_members
wb.publication_followers
wb.publication_remote_group_claims
wb.publication_units
wb.publication_intents
wb.publication_requests
wb.publication_attempts
wb.publication_attempt_members
wb.publication_observations
wb.publication_attributions
wb.publication_error_cursors
wb.publication_error_batches
```

`publication_attempts` обязательно (`NOT NULL`) связывает `DispatchSlotID`,
`LiveAuthorizationID`/revision, ExecutionGeneration/ExecutionRoot, request digest,
baseline и dispatch-recheck observation. Unique slot запрещает второй mutation
attempt даже после reauthorization/replay.

Attempt дополнительно хранит delivery state, HTTP status, envelope/classifier
version, safe request ID/code и unmatched count. `publication_attempt_members`
хранит immutable request index, PublicationMemberID, vendor code, disposition и
bounded normalized codes; unique `(attempt_id, request_member_index)` и
`(attempt_id, publication_member_id)` запрещают ambiguous persistence.

## 9. Модуль `media`

Media является самостоятельным модулем с двумя фазами.

### 9.1. Asset preparation до product mutation

- URL scheme allowlist;
- DNS/IP validation до connect и после redirect;
- запрет loopback/private/link-local/metadata targets;
- bounded redirects/time/bytes;
- content type и magic-byte validation;
- SHA-256 и immutable asset storage;
- atomic quota reservation;
- source-to-asset manifest;
- signed public URL infrastructure;
- reference-aware retention.

Manifest root связывает ordered asset IDs/hashes, а не expiring signed URL.
Public link выпускается непосредственно для media request с достаточным TTL и
может ротироваться без изменения immutable manifest. Asset backend обязан быть
durable across process/container restart; container-local temporary file не
является допустимым production backend.

Если хотя бы один requested asset source group не подготовлен, publication claim
и product mutation для группы не создаются.

### 9.2. Media publication после нового `nmID`

`cardpublication` создаёт `MediaEligible` только для outcome
`created_attributed`, то есть карточки, чья связь с publication доказана direct
WB evidence. Existing и `created_observed_unattributed` cards не получают media
eligibility. `transfer` применяет событие, проверяет remaining dispatch slot и
выпускает `MediaDispatchRequested` только с текущей exact live authorization.

```go
type MediaEligible struct {
	EventID                 EventID
	TransferID              TransferID
	GroupTargetID           GroupTargetID
	PublicationID           PublicationID
	PublicationMemberID     PublicationMemberID
	CabinetID               CabinetID
	NMID                    int64
	ManifestID              ManifestID
	AttributionEvidenceID   AttributionEvidenceID
	ExecutionGeneration     int64
	ExecutionRoot           Digest
}

type MediaDispatchRequested struct {
	MediaEligible
	DispatchSlotID           DispatchSlotID
	LiveAuthorizationID      LiveAuthorizationID
	LiveAuthorizationRevision int64
}
```

Media module:

1. Открывает caller-owned transaction.
2. Через typed publication query port проверяет immutable
   `AttributionEvidenceID.level=direct` и exact publication member/`nmID`
   binding.
3. Через `LiveAuthorizationVerifier` lock-ит authorization и проверяет exact
   manifest/execution root, seller binding, TTL/revoke.
4. В той же transaction idempotently создаёт media intent, фиксирует
   manifest/request schema/digest и append-only attempt в `dispatching`.
5. После commit вызывает `Media().SaveByLinks` один раз.
6. После любого accepted/unknown outcome выполняет read-only reconciliation и
   публикует typed outcome.

Ошибка шагов 2–4 откатывает и intent, и attempt. Revoke/expiry до commit даёт
zero intent/attempt/call; committed media attempt уже
авторизован и только reconciles. Event без direct attribution или с другим
manifest/authorization binding отвергается fail closed и не создаёт intent.

WB CDN URL не сравнивается с source URL. Проверяется структурное remote
состояние и стабильность повторного наблюдения.

### 9.3. States

```text
preparing_assets
→ assets_ready
→ waiting_card
→ dispatching
→ reconciling
→ completed
→ rejected
→ unresolved
```

### 9.4. Данные

```text
wb.media_assets
wb.media_asset_references
wb.media_quota_reservations
wb.media_manifests
wb.media_manifest_items
wb.media_intents
wb.media_attempts
wb.media_observations
```

`media_attempts` обязательно (`NOT NULL`) связывает `DispatchSlotID`, exact
PublicationMemberID/`nmID`, ManifestID/root, AttributionEvidenceID,
LiveAuthorizationID/revision и ExecutionGeneration/ExecutionRoot. Ни одно из
этих полей не восстанавливается постфактум по «текущему» transfer state.
`media_intents.dispatch_slot_id` также `UNIQUE NOT NULL`; replay/concurrency
возвращает тот же intent, а несовпадающий tuple для того же slot даёт conflict.

## 10. Модуль `statistics`

`statistics` является отдельной read feature и не вызывает WB.

Она получает через transfer query port:

- current operations;
- aggregate status;
- target/group/item counters;
- safe grouped error codes;
- manual-review details;
- notification state.

Telegram renderer не читает repositories напрямую. В v1 основным read model
владельцем остаётся `transfer`; module results применяются к его item-target
projection через idempotent events.

## 11. `platform`: только технические примитивы

```text
internal/platform/
├── workqueue/
├── outbox/
├── transaction/
├── runtime/
├── clock/
├── httpserver/
└── actorauth/
```

`platform/actorauth` только устанавливает trusted actor/permissions. Business
решение live authorization и его tables принадлежат `transfer`.

`internal/platform` становится единственным владельцем generic transaction,
queue, outbox и lifecycle primitives. Существующие generic PostgreSQL/runtime
adapters из `internal/core` переносятся или оборачиваются один раз в Stage 0;
параллельные Pool/UoW/outbox implementations запрещены. WB HTTP/DTO/clientset
остаются в `internal/core/transport/wb`, а feature gateway является тонким
adapter-ом над ним, без второго HTTP client/retry layer.

### 11.1. Workqueue

Предоставляет только:

- job claim;
- lease;
- heartbeat;
- monotonic fence;
- `available_at`;
- bounded retries;
- graceful shutdown.

Business state и retry policy принадлежат модулю-обработчику. Generic queue не
решает, можно ли повторять WB mutation.

Canonical `000001` primitives:

```text
wb.platform_jobs
  id PK, business_key UNIQUE, kind, payload_ref, status,
  available_at, lease_owner, lease_until, fence, process_epoch, attempts

wb.platform_runtime_epoch
  singleton_key PK, epoch, leader_id, lease_until, revision
```

Claim — один `UPDATE ... WHERE available_at<=now AND lease expired RETURNING`,
который увеличивает monotonic fence и связывает current process epoch. Complete,
reschedule и owner business commit требуют exact `(job_id, fence,
process_epoch)`; stale owner обновляет zero rows. Partial indexes обслуживают
только claimable rows, payload bounded и содержит reference, а не credentials.

### 11.2. Outbox/inbox

Передача между модулями:

```text
change module state
+ insert outbox event in the same transaction
→ dispatcher
→ idempotent consumer/inbox
→ consumer state + next outbox event
```

At-least-once delivery не создаёт повторную business operation благодаря unique
delivery ID, deterministic business key
`(event_type, aggregate_id, aggregate_revision)` и expected consumer revision.

```text
wb.platform_outbox
  event_id PK,
  UNIQUE(event_type, aggregate_id, aggregate_revision),
  schema_version, bounded payload/ref, available_at, lease/fence, process_epoch

wb.platform_inbox
  PRIMARY KEY(consumer, event_id),
  UNIQUE(consumer, event_type, aggregate_id, aggregate_revision),
  applied_revision, applied_at
```

Dispatcher claim/ack использует те же lease/fence/epoch правила, что workqueue.
Inbox row, consumer state и следующий outbox event commit-ятся одним DBTX;
business-key collision с другим payload digest является contract error, не
успешным dedupe.

Для атомарности используется общий transaction contract:

```go
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) Row
}

type UnitOfWork interface {
	WithinTransaction(ctx context.Context, fn func(DBTX) error) error
}
```

Business repository methods, inbox apply, outbox insert и enqueue принимают один
caller-owned `DBTX`; repository не открывает вложенную independent transaction.
Изменение owner state и исходящее событие/job commit-ятся вместе.

`WithinTransaction` обязан rollback-нуть на returned error, panic, context
cancellation и commit error; panic после rollback rethrow-ится. Commit error
никогда не считается успехом и не запускает post-commit side effect. WB/HTTP,
asset fetch и любой другой external I/O внутри transaction callback запрещены;
callback только фиксирует local intent/evidence, внешний call выполняется после
успешного commit. Nested/independent transactions из repositories запрещены.

Правило «только owner изменяет свои таблицы» не запрещает модулю вставлять
technical row в platform outbox/workqueue через этот contract: payload и
business decision принадлежат producer, lifecycle delivery — `platform`.

### 11.3. Runtime

```go
type ConnectionAcquirer interface {
	Acquire(ctx context.Context) (SessionConnection, error)
}

type SessionConnection interface {
	Exec(ctx context.Context, sql string, args ...any) (CommandTag, error)
	Ping(ctx context.Context) error
	Release()
}
```

- singleton session advisory lock на dedicated `*pgxpool.Conn`, полученной через
  отдельный `ConnectionAcquirer` port; lock никогда не берётся через случайную
  connection обычного Pool;
- process epoch;
- root context cancellation при потере lock;
- worker start/stop order;
- readiness;
- recovery scheduling;
- один Telegram Long Poller.

Advisory key — versioned constant hash от service identity + database identity,
не hostname/PID/configured cohort. Startup вызывает bounded
`pg_try_advisory_lock` и проверяет exact boolean; heartbeat `Ping`/ownership probe
выполняется через ту же SessionConnection. `Release` до successful unlock
запрещён, а connection никогда не возвращается business Pool в течение runtime.

Dedicated connection удерживается весь process lifetime. Runtime получает lock
до запуска Telegram, HTTP и workers, периодически проверяет connection и при её
потере fail-closed отменяет root context. Второй process не становится follower,
а завершает startup. Graceful shutdown сначала запрещает новые claims, ждёт
bounded drain workers, вызывает `pg_advisory_unlock` на той же physical
connection, проверяет result и только затем release-ит connection.

Advisory connection сама по себе не fence-ит уже выданные pooled DB
transactions. При успешном singleton startup runtime атомарно увеличивает
persisted `process_epoch`; каждый work/job claim получает epoch+lease fence, а
каждый business commit, outbox append и attempt creation проверяет current epoch
в SQL. После lock/connection loss отдельный watchdog transaction сначала
инвалидирует epoch (или новый leader увеличивает его), поэтому старый process с
отложенным pool call не может commit-ить state/mutation intent. WB call
разрешается только после committed attempt с current epoch; потеря lock после
commit означает reconciliation, не повтор.

Mutation-target registry/client binding имеет собственный immutable
`ClientGeneration`. Dispatch transaction проверяет SellerKey/capabilities и
сохраняет client generation в attempt; post-commit call выполняется тем же
pinned immutable client handle. Hot reload не заменяет handle in-place: старый
handle живёт до окончания call, новый применяется только к новым attempts. Если
пинning невозможно, hot reload credentials в v1 запрещён и требует controlled
process restart. Так token/client нельзя сменить между verification и socket.

### 11.4. HTTP server

- health/readiness;
- signed media route;
- Range/HEAD support;
- reverse-proxy/TLS contract;
- полная redaction path token в logs.

## 12. Межмодульные contracts

Минимальные стабильные IDs/messages располагаются в отдельном contracts
package, но repositories, WB DTO и business logic туда не попадают:

```text
internal/contract/
├── transfer.go
├── preparation.go
├── publication.go
└── media.go
```

Основной flow событий:

```text
transfer
  → PreparationRequested

cardprepare
  → PreparationCompleted | PreparationRejected

transfer
  → AssetPreparationRequested

media
  → AssetsPrepared | AssetPreparationRejected

transfer
  → PublicationPreflightRequested

cardpublication
  → PublicationPlanReady | PublicationPlanRejected

transfer
  → awaits exact live authorization
  → PublicationDispatchRequested (только с LiveAuthorizationID/ExecutionRoot)

cardpublication
  → AlreadyPresentCompatible | AlreadyPresentDifferentUntouched
    | CreatedAttributed | CreatedObservedUnattributed
    | Rejected | Partial | Unresolved

cardpublication
  → MediaEligible (условно: только future direct-correlated CreatedAttributed)

transfer
  → applies MediaEligible
  → MediaDispatchRequested (только с current exact authorization)

media
  → MediaCompleted | MediaRejected | MediaUnresolved

transfer
  → applies results
  → recalculates operation projection
  → creates notification event
```

События содержат IDs, revisions и bounded safe result codes. Large payloads
читаются по immutable reference через query port.

Все preparation/publication/media commands и results конкретной пары group ×
target содержат persisted `TransferID` + `GroupTargetID`; publication member
events дополнительно содержат `PublicationMemberID`. Consumer проверяет
same-transfer composite identity, а не восстанавливает пару по CabinetID/group
value.

Publication fact и media fact имеют независимые aggregate revisions. Consumer
должен допускать перестановку доставки: ранний media result сохраняется
идемпотентно и projection пересчитывается после появления publication fact.

## 13. Package structure

```text
internal/feature/
├── cardimport/
├── transfer/
│   ├── service/
│   ├── repository/postgres/
│   ├── worker/
│   └── transport/telegram/
├── cardprepare/
│   ├── service/
│   ├── repository/postgres/
│   ├── gateway/wb/
│   └── worker/
├── cardpublication/
│   ├── service/
│   ├── repository/postgres/
│   ├── gateway/wb/
│   ├── errorfeed/
│   └── worker/
├── media/
│   ├── service/
│   ├── repository/postgres/
│   ├── gateway/wb/
│   ├── asset/
│   ├── publichttp/
│   └── worker/
└── statistics/
    ├── service/
    ├── source/transfer/
    └── transport/telegram/

internal/contract/
internal/platform/
```

### 13.1. Pre-release schema policy

- Все новые tables, columns, indexes и constraints этого плана входят в
  `migration/000001_init.up.sql`.
- `migration/000001_init.down.sql` удаляет их в обратном dependency order.
- `000002` до первого production release не создаётся.
- CI проверяет fresh-database цикл `000001 up → down → up` и соответствие
  repository contract tests.
- Локальная БД, где была применена промежуточная версия `000001`, не изменяется
  скрыто: разработчик явно пересоздаёт disposable database/volume. Production
  data этим процессом не удаляются.

## 14. Последовательность реализации

### Stage 0. Contracts и platform foundation

Deliverables:

- Gate `CARDIMPORT-DONE`;
- optional barcode schema/DTO и exact Upload JSON fixtures;
- module/event IDs и schemas;
- `DBTX`/UnitOfWork для atomic owner-state + outbox/job;
- workqueue lease/fence primitives;
- outbox/inbox;
- singleton runtime на dedicated PostgreSQL connection;
- WB Core generic bounded `ResponseMeta`/non-2xx decode, delivery state and
  redacted safe classification (feature member mapping остаётся Stage 6);
- validated config/docs для mode, named cohort, authorization TTL, capacity и
  public media origin;
- `TRANSFER_MODE`, default `disabled`.

Exit:

- duplicate event idempotent;
- concurrent Finalize создаёт один versioned immutable batch и один
  `BatchFinalized` outbox event;
- multi-file order/pagination/checksum stable, BatchReader возвращает только
  frozen finalized rows;
- SaveParsed/Finalize race и DB immutability constraints пройдены;
- empty barcode реально omits `skus` в Upload/UploadAdd JSON, supplied barcode
  сохраняется exact;
- business commit без соответствующего outbox/job и обратное невозможны;
- panic/commit error rollback-ит state/outbox, external I/O внутри UoW отсутствует;
- stale fence не commit-ит;
- второй process fail-fast;
- singleton connection loss снимает readiness/cancels workers, invalidated epoch
  запрещает late pooled commit;
- graceful shutdown unlock/release выполняется на той же dedicated connection;
- invalid/missing live config fail-fast, secrets не выводятся;
- ни один WB mutation ещё недоступен.

### Stage 1. `transfer` operation foundation

- domain/statuses;
- named mutation cohort и seller-bound target snapshot;
- idempotent `Start`;
- chunk initializer;
- overflow-safe transfer capacity gate;
- atomic activation;
- operation query skeleton.

Exit:

- concurrent Start создаёт один transfer;
- existing-first не читает новый cohort;
- token rotation того же seller допустима, CabinetID rebinding к другому seller
  fail-closed;
- immutable client generation закрывает verify→post-commit-call credential race;
- restart продолжает initialization;
- activation имеет exact stable `groups×targets` и `items×targets` cardinality;
- composite FKs отклоняют cross-transfer/group membership references;
- capacity overflow/exceed не создаёт transfer или matrix rows;
- checksum mismatch не создаёт downstream work.

### Stage 2. `cardprepare` read-only pipeline

- global preparation;
- catalog gateway;
- per-target preparation;
- metadata snapshots/cache;
- immutable proposals;
- dry-run result.

Exit:

- proposals reproducible;
- targets независимы;
- Cards Limits observation сохранён в proposal и выполнен ровно одним read на
  target-specific preparation;
- data/recoverable errors typed;
- zero WB mutations.

### Stage 3. `media` asset preparation

- SSRF-safe fetcher;
- immutable store;
- quota;
- manifest;
- signed public route;
- durable asset backend и deploy/public-origin wiring;
- retention.

Exit:

- invalid member блокирует whole group;
- quota не oversubscribe-ится;
- public URLs проходят external smoke test;
- media mutation ещё недоступна.

### Stage 4. `cardpublication` ownership и preflight

- product identities;
- read-only identity/remote preflight;
- normal/trash pagination;
- decision table;
- bounded request packing;
- immutable `PublicationPlan`/root;
- immutable attribution evidence model.

Exit:

- concurrent preflights дают deterministic plans;
- existing cards untouched;
- create/add/already/conflicts покрыты;
- active mutation claims ещё не захватываются;
- zero WB mutations.

### Stage 5. Live authorization, Error feed и journal foundation

- persisted live authorization state/events;
- exact proposal/manifest/target binding, TTL, revoke/supersede;
- `LiveAuthorizationVerifier` с shared DBTX/row lock;
- Error List cursor/overlap/dedup;
- baseline;
- immutable intent/request/attempt schemas;
- child links;
- zero mutation calls.

Exit:

- stale error не коррелируется;
- duplicate pages idempotent;
- ENV live сам не authorizes dry-run;
- request/approve/revoke/expiry verifier-level races сериализованы без attempt;
- ни один attempt ещё не переводится в `dispatching`.

### Stage 6. Card publication dispatch/reconciliation

- `Upload`/`UploadAdd` gateway;
- mapping generic WB classification/`additionalErrors` на exact
  publication/member dispositions;
- authorization-bound journal commit и один post-commit call;
- dispatch-time identity/group/remote-group claims и followers;
- recovery from `dispatching` into reconciliation;
- normal/error reconciliation;
- explicit attributed/unattributed evidence boundary;
- partial member projection;
- late resolver/follower fan-out.

Exit:

- `200` не означает created;
- `ResponseMeta.Error=true` не означает accepted;
- UnknownDelivery не retry-ится;
- approve/revoke/expiry ↔ real attempt boundary сериализован;
- каждый attempt содержит exact `LiveAuthorizationID`/revision;
- unattributed observed card получает manual review и zero
  `MediaEligible`/`MediaDispatchRequested`;
- all fault windows покрыты;
- outcomes возвращаются transfer через outbox.

### Stage 7. Media dispatch/reconciliation (capability-gated)

- media intents;
- authorization/attribution verification;
- `SaveMediaByLinks` journal;
- structural observation;
- follower result propagation;
- reference-aware GC.

Exit:

- при текущем Upload contract `MEDIA_AUTO_DISPATCH` остаётся false, worker не
  dispatch-ит media и Stage 7 не входит в live rollout path;
- включение возможно только после contract fixture direct member→`nmID`, startup
  capability check и direct-attribution E2E;
- existing card не получает media;
- `created_observed_unattributed` не получает media;
- revoked/expired authorization до media attempt даёт zero call;
- `200` без observation не даёт completed;
- crash после dispatch не повторяет mutation.

### Stage 8. Statistics, Telegram и notifications

- current read model;
- progress/grouped errors/manual details;
- notification outbox;
- callback revision/idempotency/permissions;
- composition-root wiring.

Exit:

- statistics не вызывает WB;
- stale callbacks безопасны;
- terminal/manual notification durable.

### Stage 9. Rollout

1. Зафиксировать dashboards, alerts, capacity limits и recovery runbook.
2. Deploy pre-release `000001` schema/binary с `TRANSFER_MODE=disabled`.
3. Проверить dedicated singleton loss/restart и safe-read smoke tests.
4. Запустить internal `dry-run` batch.
5. Сверить seller-bound targets/groups/proposals/media manifests.
6. В canary-configured singleton (или отдельном DB environment) явно
   авторизовать live operation; второй process к той же DB не запускать.
7. Проверить create/add/error/unattributed/restart; media проверять только при
   наличии direct attributable evidence.
8. Включить заранее утверждённый полный production cohort.

## 15. Reviewable PR sequence

1. `platform/uow-workqueue-outbox-runtime` — сначала единая transaction/outbox
   boundary и dedicated singleton.
2. `cardimport/finalize-immutable-batch` — закрывает `CARDIMPORT-DONE` через
   общий outbox и optional barcode contract.
3. `wb/seller-bound-cohorts-and-safe-errors` — SellerKey/capabilities,
   authenticated probe и structured mutation envelopes.
4. `transfer/operation-start-capacity`.
5. `transfer/operation-initializer-and-group-targets`.
6. `cardprepare/catalog-limits-and-dry-run`.
7. `media/asset-foundation`.
8. `cardpublication/identity-preflight-attribution-model`.
9. `transfer/live-authorization`.
10. `cardpublication/errorfeed-journal-foundation`.
11. `cardpublication/authorization-dispatch-reconcile`.
12. `media/authorization-dispatch-reconcile`.
13. `statistics/telegram-outbox`.
14. `transfer/rollout-hardening`.

Каждый PR должен завершаться runnable tests и сохранять mutations disabled,
пока не закрыты все предыдущие safety gates.

Все schema changes этих PR до первого release синхронно обновляют только
`000001_init.up.sql` и `000001_init.down.sql`; отдельный numbered migration не
создаётся.

## 16. Тестовая стратегия

### 16.1. Contracts/outbox

- duplicate delivery;
- stale revision;
- event schema validation;
- two EventIDs for one aggregate revision collapse by deterministic business key;
- crash до/после outbox dispatch;
- consumer commit + next event atomicity;
- repository method внутри caller-owned DBTX;
- owner state/outbox atomic rollback в обе стороны;
- independent aggregate revisions при reordered publication/media events.

### 16.2. Cardimport gate

- duplicate/concurrent Finalize;
- deterministic global order across multiple files;
- schema/normalization/checksum fixture;
- checksum excludes itself/audit/timestamps and detects ordered frozen-row change;
- source location и source-group key survive batch freeze;
- stale callback revision and actor spoof from payload rejected;
- exact finalized command replay returns same batch; stale revision or reused key
  with changed payload conflicts;
- чужой actor с известным finalized SessionID не получает BatchHeader;
- SaveParsed vs Finalize lock ordering yields either included rows or typed
  finalized conflict, never post-finalize mutation;
- fault after every Finalize step rolls back batch/session/outbox together;
- outbox crash/recovery and duplicate `BatchFinalized` delivery;
- finalized session cannot be reparsed and BatchReader never reads mutable rows;
- DB rejects application-role UPDATE/DELETE of frozen batch rows;
- missing barcode accepted and `skus` omitted in exact Upload/UploadAdd JSON;
- supplied barcodes preserved and duplicates rejected inside batch.

### 16.3. Transfer

- duplicate/concurrent Start;
- empty/changed cohort;
- initializer restart;
- exact stable `groups×targets` and `items×targets` cardinality;
- DB rejects cross-transfer group/target/item membership FKs;
- capacity limit/exact-boundary/integer-overflow;
- count/checksum mismatch;
- idempotent result application;
- aggregate status priority;
- seller binding rotation/rebinding;
- SellerKey стабилен across restart/same-seller token rotation; unchanged
  capabilities не меняют binding/capability revisions;
- invalid/read-only/expired probe and duplicate SellerKey;
- canary/production cohort separation;
- authorization request/idempotency/expiry/revoke/supersede/stale
  proposal/manifest/target/generation;
- concurrent approve/revoke/attempt locking and trusted actor context;
- reused authorization idempotency key with changed payload/actor conflicts;
- old callback cannot approve a different authorization revision/remaining slot root;
- projection result revision не инвалидирует authorization remaining slots;
- expiry/revoke допускает только explicit reauthorization remaining slots той же
  execution root; begun product slot не повторяется;
- ENV dry-run→live does not authorize existing operation.

### 16.4. Cardprepare

- category/subject ambiguity;
- required/type/unit/maxCount;
- directory/brand validation;
- cache isolation by CabinetID;
- exact payload/digest fixtures;
- serialization bounds;
- Cards Limits exceeded и ровно один read на target preparation.

### 16.5. WB Core

- stable versioned SellerKey across restart/token rotation;
- capability/read-only/expiry change и authenticated probe;
- HTTP 200 с `ResponseMeta.Error=true` не возвращает success;
- bounded non-2xx/malformed/oversized response classification;
- raw body, token, sid и arbitrary error text отсутствуют в logs/business rows.

### 16.6. Cardpublication

- all absent/all present/mixed;
- compatible/different existing cards remain untouched with bounded diff;
- trash/different imtID/subject/capacity conflicts;
- concurrent identity/group claims;
- follower same/different desired value;
- Error List overlap/dedup/baseline;
- request packing boundaries;
- every dispatch crash window;
- success/rejection/partial/unknown/late evidence;
- every attempt stores exact live authorization ID/revision;
- HTTP 200 with `error=true`;
- bounded HTTP 400 `additionalErrors` per-member mapping;
- malformed response after dispatch;
- external writer between baseline and observation gives unattributed/manual;
- cabinet observation generation reused; targeted recheck occurs before intent;
- recheck transitions create→add/create→already/normal→trash/capacity-changed
  дают PlanStale, zero claim/attempt/call и новый read-only plan;
- PlanStale after partial progress seals begun/unknown slots and replans only
  never-begun actions;
- recheck ObservationID/digest входит в attempt;
- expiry до delayed command + reauthorization не повторяет begun slot;
- no direct correlation means zero `MediaEligible`/`MediaDispatchRequested`;
- `ResolveUnknown` cannot retry and accepts only matching persisted evidence;
- supplied barcode remote rejection is terminal and not retried.

### 16.7. Media

- DNS rebinding/redirect/private IP vectors;
- type/size/quota validation;
- asset/reference/GC races;
- signed URL expiry/rotation/redaction;
- response without remote effect;
- crash after media dispatch;
- existing card never submitted;
- observed-but-unattributed card never submitted;
- revoked/expired/wrong-manifest authorization never creates media attempt;
- expiry между CreatedAttributed и media требует explicit remaining-slot
  authorization, не повторяя product attempt;
- replay with same attribution evidence creates one intent.

### 16.8. Runtime/E2E

- lease loss/stale fence;
- singleton process conflict;
- singleton dedicated connection loss before/after mutation dispatch;
- same-connection advisory unlock then release;
- stale process epoch cannot commit through an old pooled transaction;
- pinned client generation survives rotation without seller/client TOCTOU;
- graceful shutdown;
- DB loss fail-closed;
- fresh `000001 up → down → up` schema fixture;
- schema fixture не содержит legacy `card_import_handoffs` и
  `card_batches.authorization`;
- one/multiple cabinets;
- duplicate `BatchFinalized` delivery/callback;
- dry-run → explicit live authorization;
- full restart scenarios;
- durable final/manual notifications.

После каждого change set:

```bash
go test ./internal/feature/cardimport/...
go test ./internal/feature/transfer/...
go test ./internal/feature/cardprepare/...
go test ./internal/feature/cardpublication/...
go test ./internal/feature/media/...
go test ./internal/feature/statistics/...
go test ./internal/platform/...
go test ./...
go vet ./...
```

Repository stages дополнительно требуют PostgreSQL integration tests,
fresh-database `000001 up/down/up` fixture, concurrent execution и fault
injection.

## 17. Definition of Done

- Modules имеют однозначное владение tables/state machines.
- Ни один module не изменяет таблицы другого module напрямую.
- Межмодульная передача crash-safe и idempotent.
- Transfer остаётся небольшим orchestrator-ом.
- Cardprepare не имеет mutation imports/calls.
- Только cardpublication вызывает create/add.
- Только media вызывает media mutation.
- Statistics не вызывает WB.
- Unknown mutation outcome не получает автоматический повтор.
- Каждый mutation attempt связан с exact non-revoked-at-commit live
  authorization и seller binding.
- Каждый dispatch slot имеет максимум один committed attempt независимо от
  expiry/revoke/reauthorization.
- New remote card без direct ownership evidence не получает media.
- При текущем Upload contract обычный created observation остаётся unattributed;
  автоматический media dispatch остаётся выключен до direct WB correlation.
- Empty barcode omits `skus`; supplied barcode сохраняется и отправляется exact.
- `CARDIMPORT-DONE` закрыт immutable batch + transactional `BatchFinalized`.
- `transfer_group_targets` материализован с exact group×target cardinality.
- State/outbox/job используют один caller-owned transaction.
- Singleton advisory lock удерживается dedicated connection до shutdown.
- Fresh `000001 up/down/up` проходит без `000002`.
- Every item × target имеет persisted explainable result.
- Restart восстанавливает все nonterminal workflows.
- Existing cards и media остаются untouched.
- Dry-run и canary gates пройдены до production live.
