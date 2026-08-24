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
Текущая ревизия: 2026-08-24.

Текущий реализованный срез: Stages 0–7 — полный transfer flow от immutable batch
до product/media publication, reconciliation и manual evidence resolver без
повторного WB mutation. Stage 8 `statistics` отложен отдельным решением и в
текущей итерации не реализуется:

- startup один раз проверяет все configured WB cabinets и замораживает target
  snapshot до остановки процесса;
- `transfer` сразу после startup и затем каждые 3 секунды читает finalized
  batches; batch другого purpose пропускается, а временная ошибка не сдвигает
  cursor и повторяется;
- `Start` идемпотентно создаёт один transfer и immutable target snapshot;
- `Initialize` одной PostgreSQL transaction создаёт groups, items, membership,
  group-target и item-target matrices, сверяет checksum/cardinality и только
  после этого переводит transfer из `initializing` в `preparing`;
- после rollback следующий polling tick повторяет initialization целиком, а
  уже committed transfer не инициализируется повторно;
- тот же polling loop после `transfer` запускает `cardprepare`: для каждого
  transfer в `preparing` создаётся ровно одна preparation и work rows для exact
  matrix `source group × target cabinet`;
- `cardprepare/transport/wb` выполняет только одиночные safe reads и возвращает
  raw DTO, а service владеет pagination, проверкой `ResponseMeta`, resolution
  catalog metadata, валидацией и сборкой exact Upload payload;
- successful preparation одной transaction сохраняет metadata snapshot, exact
  request bytes, ordered members и `ProposalRoot`; data rejection сохраняется
  bounded outcome без proposal artifacts;
- typed result отдельно применяется владельцем `transfer`; после terminal
  result всех group-target transfer переходит из `preparing` в
  `awaiting_authorization`;
- временная ошибка одного target оставляет только его work в `pending` и не
  мешает подготовить остальные targets; следующий polling tick повторяет этот
  pending work;
- `transfers` разделяет `phase`, `outcome` и `attention_code`; initialization
  failure завершается как `finished/failed`, а active phases имеют только
  `outcome=running`;
- `transfer_group_targets` является stage projection, а
  `transfer_item_targets` хранит current item×target state, bounded result и
  nullable source action;
- в `000001` определён единый publication journal
  `publication_plans/actions/action_members/attempts` для create/add/media;
- `cardpublication` после `cardprepare` читает immutable proposal только через
  typed `ProposalReader`, а подготовленные matrix IDs/media links — только через
  typed transfer source;
- `cardpublication/transport/wb` выполняет одну raw Cards List или Trash List
  operation; service владеет полной cursor pagination, проверкой изменившихся
  страниц и canonical observation;
- normal/trash observation сохраняется immutable, после чего deterministic
  decision различает create, add, compatible/different existing, trash,
  subject/group/capacity и duplicate identity conflicts;
- один product action v1 содержит ровно одну complete source group; exact
  Upload/UploadAdd bytes, ordered members, request/member digests и ActionKey
  сохраняются до authorization;
- для каждого реально создаваемого member с photo/video URLs заранее создаётся
  potential media action: сохраняются исходный порядок links и
  `MediaLinkSetRoot`; Stage 7 dispatcher допускает его только после persisted
  attribution нового `nmID` по уникальной паре `CabinetID + vendorCode`;
- `PublicationPlan`, product/media actions, action members, observations и
  product identity projection сохраняются одной transaction с typed update
  transfer current projection;
- read-only existing/conflict results сразу завершают item-target; если mutation
  actions нет вообще, fixed reducer завершает transfer без authorization;
- statistics foundation состоит из read-only transfer/item/action/attempt views
  без summary counters и собственных business tables;
- planning по-прежнему выполняет только WB reads; product и media mutations
  вызываются отдельными dispatchers только после exact live authorization;
- immutable target snapshot теперь фиксирует также exact
  `ClientGeneration` и expiration credentials; authorization verifier
  fail-closed сверяет их в caller-owned transaction;
- admin Telegram UI показывает только планы автора batch,
  создаёт request и требует отдельную кнопку «Отправить в WB»;
  callback не содержит actor или `PlanDigest`, они читаются из
  trusted context и PostgreSQL;
- authorization привязана к exact `PlanDigest`/`TargetSetRoot`, имеет
  revision и TTL, может быть отозвана из Telegram; expiry, revoke,
  supersede и close сохраняются в append-only command journal;
- истёкшая authorization не скрывает plan: новое открытие
  атомарно фиксирует `expired` и создаёт новую authorization
  на тот же неизменный plan;
- `cardpublication` читает Cards Error List для frozen targets не
  чаще одного раза в 30 секунд: service владеет pagination,
  five-minute overlap, normalization и deduplication, а repository —
  per-cabinet watermark/cursor и immutable safe evidence; первый poll
  начинается с current time minus overlap, а не со всей истории WB;
- polling вызывает каждый feature processor на каждом tick: ошибка
  одного processor логируется, но не лишает Error List и остальные
  независимые processors своего цикла;
- непосредственно перед attempt в одной DB transaction фиксируются targeted
  recheck, exact Error List baseline, identity binding, authorization evidence
  и единственный `publication_attempts` row;
- statistics foundation дополнена authorization и Error List facts, а
  attempt facts ссылаются на exact baseline и recheck observation;
- после commit attempt dispatcher выполняет ровно один raw `Upload` либо
  `UploadAdd`; success envelope считается только принятым submission, а не
  доказательством созданной карточки;
- process interruption после committed attempt переводится в
  `unknown_delivery` и никогда не создаёт второй WB call;
- accepted/uncertain submission проходит normal/trash/Error List
  reconciliation; если после подтверждённого отсутствия до attempt появилась
  ровно одна normal card с тем же уникальным `vendorCode` и ожидаемой группой,
  она завершается как `CREATED_CONFIRMED_BY_VENDOR_CODE`, сохраняет `nmID` и
  разрешает media action;
- незавершённая reconciliation сохраняет immutable post-submission observation
  и повторяется не чаще `CARDPUBLICATION_RECONCILIATION_DELAY`; по
  `CARDPUBLICATION_RECONCILIATION_TIMEOUT` результат становится unresolved;
- terminal product result одной transaction обновляет owner journal, identity,
  typed transfer item/group/operation projections и закрывает ещё активную live
  authorization после terminal всего plan;
- manual resolver принимает только exact persisted post-submission observation
  либо Error List batch после attempt baseline; третья команда закрывает
  attention без изменения unresolved result и без повторного WB call;
- каждая manual command использует trusted actor, idempotency key, expected
  action/item revisions и append-only `publication_manual_resolutions`; owner
  action/plan, product identity и typed transfer projection меняются одной
  caller-owned transaction;
- закрытый unresolved item хранит `attention_closed_at`; появившееся позже
  persisted evidence может отдельной revision исправить его в success/rejected,
  но никогда не создаёт новый attempt;
- Stage 7 media dispatcher использует тот же live authorization и common
  action/member/attempt journal, сохраняет exact final request с attributed
  `nmID` и выполняет не более одного `SaveMediaByLinks`;
- `CARDPUBLICATION_MEDIA_AUTO_DISPATCH=true` включает готовый media flow по
  умолчанию; флаг можно выставить в `false` как kill gate. Без точной
  vendor-code attribution media action завершается объяснимым skipped result,
  а unknown media delivery становится unresolved и не повторяется;
- automatic product/media results и исходная attempt evidence не
  переписываются retry.

### 1.1. Оценка готовности без `statistics`

На 2026-08-24 функциональный кодовый scope transfer flow без Stage 8
`statistics` реализован. Готовность к production оценивается примерно в
**95%**: это release-readiness, а не процент недописанных use cases.

Реализована полная цепочка:

```text
Finalize → Start → Initialize → cardprepare → immutable publication plan
→ Telegram live authorization → Upload/UploadAdd → reconciliation
→ unique vendor-code attribution → SaveMediaByLinks → terminal projection
```

Новых обязательных business use cases до Stage 8 не запланировано. Оставшиеся
примерно **5%** — проверка уже написанной реализации:

- ручной fresh PostgreSQL цикл `000001 up → down → up`;
- canary с реальными WB envelopes для create, add, Error List и media;
- подтверждение фактического появления фотографий после
  `MEDIA_REQUEST_ACCEPTED`;
- restart/unknown-delivery/authorization-expiry и финальный single-instance
  integration audit;
- rollout checklist, monitoring и recovery runbook.

Stage 8 `statistics`, statistics Telegram presentation, notification UI и
Telegram callbacks manual resolver намеренно отложены. Они не входят в текущий
scope и не уменьшают указанную готовность transfer flow.

Завершённый кодовый срез Stage 6–7 дополнительно фиксирует:

- не более одного dispatchable product action на один target в одном polling
  проходе; следующий action того же кабинета ждёт terminal предыдущего;
- pinned `ClientGeneration` для mutation call после authorization-bound commit;
- full raw `Upload`/`UploadAdd` DTO в feature transport и versioned
  classification только в service;
- terminal `rejected_proven` только при полном уникальном member mapping;
  неполный `additionalErrors`, non-2xx/malformed response и transport ambiguity
  становятся `uncertain`;
- immutable Error List evidence хранит rejected vendor codes и только bounded
  SHA-256 error codes без raw WB error text;
- exact Error List correlation связывает member, attempt, baseline и batch;
- повторная незавершённая reconciliation throttled через action timestamp и
  сохраняет post-submission observation;
- terminal всего publication plan закрывает ещё active authorization; поздний
  revoke/expiry не откатывает уже полученный WB result;
- manual correction не принимает произвольные `nmID`/ошибки от оператора:
  remote IDs извлекаются из immutable observation, rejection — из exact Error
  List batch, выбранного после baseline;
- exact replay одного manual idempotency key возвращает прежний результат, а
  другой actor/payload, stale action revision, item revision или identity
  binding дают conflict;
- `close_unresolved_no_retry` не меняет product identity
  `blocked_uncertain` и оставляет возможность применить позднее evidence;
- media action остаётся planned только при exact attribution того же
  plan/member/item по уникальному `CabinetID + vendorCode`; template ссылок
  остаётся immutable, а attempt хранит exact
  финальный `SaveMediaByLinks{nmID,data}` body/digest и AttributionID;
- перед media attempt выполняется catalog recheck exact vendor→`nmID`, identity
  CAS и повторная live authorization verification; один committed attempt
  физически блокирует второй WB call;
- выключенный media capability gate завершает eligible action как skipped без
  attempt; expired authorization можно запросить заново для remaining planned
  actions того же immutable plan;
- `core/transport/wb` не расширялся для Stage 6; ранее созданные test files
  внутри `core/transport/wb` удалены согласно project rule.

Текущий verification status:

- `go build ./...` проходит;
- `git diff --check` проходит;
- новые test files не создавались, тесты не запускались;
- новая numbered migration не создавалась: изменяется только pre-release
  `000001_init.up.sql`/`000001_init.down.sql`;
- official WB OpenAPI на 2026-08-21 сверена для `Upload`, `UploadAdd`,
  `Cards Error List` и `SaveMediaByLinks`; request/response DTO и актуальная
  cursor pagination совпадают, а media `200/error=false` теперь явно означает
  только `MEDIA_REQUEST_ACCEPTED`;
- выполнение migration на реальной fresh PostgreSQL ещё не подтверждено.

Актуальные архитектурные решения:

- business flow разделён на `transfer`, `cardprepare`, `cardpublication` и
  `statistics`; отдельного `media` feature и инфраструктурного business module
  нет;
- общий PostgreSQL `DBTX`/UnitOfWork остаётся в `internal/core`; singleton
  runtime и transactional outbox полностью исключены из текущей реализации;
- до полного завершения `transfer` и `statistics` не создаются outbox/event
  adapters, dispatcher, runtime guard, process epoch, advisory lock и фоновые
  очереди межмодульной доставки;
- текущая межмодульная orchestration выполняется явными прямыми service calls.
  Она намеренно не обещает recovery после process crash и защиту от двух
  одновременно запущенных экземпляров; product dispatcher доступен только при
  `TRANSFER_MODE=live` плюс persisted Telegram approval, а production rollout
  до возврата к singleton/runtime решению запрещён runbook-ом;
- `cardimport` заканчивает работу на `Finalize`: кнопка «Готово» только
  валидирует данные и атомарно сохраняет immutable batch. Она не вызывает и не
  импортирует `transfer`;
- `transfer` имеет простой feature-local polling loop: раз в 3 секунды он через
  read-only `cardimport.BatchReader` находит новые finalized batches, создаёт по
  одному transfer на batch и выполняет одну атомарную initialization. Это не
  generic runtime, outbox или очередь;
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
- Все configured WB cabinets проверяются один раз при startup до запуска
  Telegram. Startup строит immutable process-lifetime target snapshot и
  fail-fast завершается, если хотя бы один cabinet недоступен, read-only,
  просрочен или принадлежит другому seller. `transfer.Start` не выполняет WB
  probe и только копирует уже проверенный startup snapshot; смена credentials
  требует restart приложения.
- Media asset preparation в текущем v1 отсутствует. Исходные photo URLs не
  скачиваются, не проверяются и не сохраняются локально: после подтверждённого
  создания карточки внутренняя media-подсистема `cardpublication` передаёт их в
  исходном порядке непосредственно в WB `SaveMediaByLinks`.
  SSRF/MIME/hash/store/quota/manifest/signed-public-route полностью отложены.
- Media не является отдельной feature: ownership, attribution, authorization,
  journal, `SaveMediaByLinks` и reconciliation находятся внутри
  `cardpublication`. Это исключает отдельный handoff из `cardpublication` в
  feature `media` и сохраняет одного владельца всех WB product mutations.
- `core/transport/wb` содержит только WB DTO, `OperationSpec`, credential-safe
  cabinet executor, единственный generic helper `client.ExecuteResponse[T]` и
  общие transport guarantees. Feature-specific transport выбирает операции и
  через helper возвращает полный raw response DTO своему service; pagination,
  envelope interpretation, catalog mapping, target verification и mutation
  workflow остаются внутри feature. После добавления helper WB Core заморожен:
  `typed` не возвращается, новые функции, DTO, operations и файлы не добавляются,
  существующие файлы больше не редактируются.

Короткая карта v1:

| Gate | Владелец | Что должно стать durable | Что запрещено до выхода |
|---|---|---|---|
| 0. Foundation | `core` + `cardimport` | UoW и immutable batch | создавать transfer |
| 1. Dry-run | `transfer` + `cardprepare` + `cardpublication` | startup target snapshot, proposals, immutable publication actions и `PlanDigest` | любые WB mutations |
| 2. Live approval | `transfer` | exact persisted authorization trusted actor-а | полагаться только на ENV |
| 3. Product dispatch | `cardpublication` | journal, один call, reconciliation/attribution | retry unknown delivery |
| 4. Media dispatch | `cardpublication` | unique vendor-code attribution + та же live authorization + common action journal | media для existing/неоднозначной card |

Каждый следующий gate зависит только от immutable IDs/digests предыдущего.
Изменение ещё не отправленного plan создаёт новый immutable plan и требует нового
trusted approval. Начатое или неизвестно доставленное action никогда не
пересоздаётся и не получает автоматический повтор. Изменение immutable
target/seller binding того же transfer не replans operation, а fail-closed
закрывает её с operator attention и требует нового batch/transfer.

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
- media link dispatch;
- media publication/reconciliation;
- statistics и Telegram UX;
- общие transaction и process lifecycle.

Это несколько разных state machines с разными правилами безопасности и
разными владельцами данных. Их реализация в одном service/repository привела бы
к большой общей schema, неявным зависимостям и сложному разбору crash
windows.

Поэтому пользовательское название операции остаётся `transfer`, а реализация
делится на вертикальные модули:

```text
cardimport
    ↓ «Готово»: immutable batch в PostgreSQL
transfer polling раз в 3 секунды
    ↓ Start + одна атомарная initialization
transfer
    ↓ preparation request
cardprepare
    ↓ immutable proposal
cardpublication
    ├── Upload / UploadAdd
    └── attributed newly-created nmID + original photo URLs
        → SaveMediaByLinks

statistics ← transfer projections + cardpublication durable facts

core → transaction и shared transports
```

## 3. Общие product-инварианты

- Перенос является копированием во все mutation-ready кабинеты выбранного
  immutable named cohort; пользователь не выбирает отдельные targets.
- Existing card не обновляется, не перемещается и не получает media replacement.
- `MoveCards` не вызывается.
- HTTP `200` от async WB mutation не означает created/accepted: сначала
  проверяется application envelope; успешный envelope означает только submission.
- Unknown или ambiguous mutation delivery не разрешает слепой повтор.
- В текущем development scope нет durable redelivery worker. После restart
  committed незавершённый attempt консервативно становится `unknown_delivery`
  и идёт только в reconciliation; второй WB mutation call не выполняется.
  Полностью откатившаяся initialization повторяется на следующем polling tick.
- Tokens и raw Authorization не сохраняются в business tables и не выводятся в
  application logs.
- `TRANSFER_MODE=disabled|dry-run|live`, default `disabled`.
- `CARDPUBLICATION_MEDIA_AUTO_DISPATCH=true` by default; `false` служит kill
  gate. Dispatcher всегда требует persisted member→`nmID` attribution по
  уникальной паре `CabinetID + vendorCode` и без неё выполняет zero media calls.
- Смена ENV `dry-run → live` не авторизует существующие operations.
- Любой WB dispatch требует persisted authorization, связанной с exact
  `PlanDigest`, `TargetSetRoot` и seller bindings; ENV является
  capability/kill gate, но не authorization.
- Target snapshot содержит stable seller binding и write capability. Один
  `CabinetID` нельзя незаметно перепривязать к другому WB seller.
- Неоднозначно атрибутированная новая remote card не получает media mutation.
- HTTP status и transport delivery недостаточны для business outcome: typed
  response envelope проверяется отдельно.
- До возврата к singleton/runtime deployment вручную запускает не более одного
  application instance и одного Telegram Long Poller.
- Удаление пользователя не удаляет batch, transfer или publication/media
  history.

## 4. Gate `CARDIMPORT-DONE`

Модульный flow начинается только с immutable batch. `cardimport` должен
предоставлять:

```go
type BatchReader interface {
	ListFinalizedBatches(
		ctx context.Context,
		after *BatchCursor,
		limit int,
	) ([]BatchHeader, error)
	GetBatch(ctx context.Context, batchID BatchID) (BatchHeader, error)
	ListBatchItems(
		ctx context.Context,
		batchID BatchID,
		afterPosition int,
		limit int,
	) ([]BatchItem, error)
}

type BatchCursor struct {
	FinalizedAt time.Time
	BatchID     BatchID
}
```

`cardimport` знает только о `BatchReader` и своих таблицах. Обратного
`BatchConsumer`, callback-а или импорта package `transfer` нет. Cursor задаёт
стабильный порядок `(finalized_at, batch_id)`; после restart transfer начинает
просмотр сначала, а не хранит delivery state внутри `cardimport`.

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
	FinalizedAt          time.Time // polling cursor; не входит в checksum
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
- concurrent Finalize создаёт один batch, а повтор exact команды возвращает его
  идемпотентно.

Gate сейчас считается открытым. До его закрытия нельзя merge-ить Stage 1
`transfer` code. Изменения `card_batches`, `card_batch_items`, optional barcodes
и Finalize вносятся в текущую pre-release `000001_init` migration.
Соответствие реально развёрнутой локальной schema этой версии проверяется
ручным циклом fresh database `000001 up → down → up`; новый numbered migration
не создаётся.

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
7. Переводит session в `finalized` в той же transaction.

Ошибка любого шага откатывает batch, items и session transition. Telegram
callback «Готово» вызывает только `Finalize` и после успеха сообщает
пользователю, что batch сохранён. Он не вызывает `transfer.Start`. Новый batch
подхватывается polling loop-ом модуля `transfer` не позднее следующего обычного
трёхсекундного тика. `BatchReader` возвращает только finalized frozen rows и
предоставляет их в стабильном порядке `(finalized_at, batch_id)`.

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
| `transfer` | operation, target snapshot, initialization, orchestration, current projections | WB catalog, подробный mutation journal и WB mutations |
| `cardprepare` | read-only catalog resolution, validation, payload, dry-run proposal | remote ownership и mutations |
| `cardpublication` | product identities, preflight, create/add, Error List, attribution, прямая передача photo URLs в `SaveMediaByLinks` и reconciliation обеих mutations | XLSX, catalog mapping, download, local assets, manifests и public delivery |
| `statistics` | read-only current/historical statistics и Telegram presentation | WB calls, business mutations и собственные копии business facts |

Правило владения: только модуль-владелец изменяет свои таблицы. Другие модули
используют typed commands и query ports.

`core` не является business module и не владеет transfer/card semantics. Он
предоставляет только общие transaction и transport primitives.

## 6. Модуль `transfer`

### 6.1. Ответственность

`transfer` является небольшим orchestrator-ом пользовательской операции:

- раз в 3 секунды ищет новые finalized batches через typed `BatchReader`;
- идемпотентно создаёт operation;
- сохраняет полный ordered cabinet snapshot;
- одной bounded transaction разворачивает batch в items/groups/targets;
- создаёт downstream work;
- принимает typed результаты других модулей;
- обновляет group/item current projections;
- вычисляет `phase/outcome/attention_code` фиксированным reducer-ом;
- формирует terminal/operator-attention notification state.

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
wb.transfer_live_authorizations
wb.transfer_notification_state
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
- прямой typed result command содержит source aggregate ID/revision и expected
  `GroupTargetRevision`; exact replay является no-op, stale или future revision
  возвращает typed conflict и не сохраняется в промежуточной очереди;
- не более одной active live authorization на один exact `PlanDigest`; новый
  plan всегда получает новую authorization row;
- user foreign key использует `ON DELETE SET NULL`, history не каскадит.

`wb.transfer_group_targets` — canonical aggregate межмодульного flow:

```text
GroupTargetID
TransferID
SourceGroupID
TargetID
Revision
PreparationStatus
PublicationStatus
MediaStatus
OverallOutcome
AttentionCode
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

Каждая ось `PreparationStatus|PublicationStatus|MediaStatus` является только
текущей краткой проекцией и имеет bounded форму
`not_started|running|succeeded|rejected|unresolved|skipped`.
Allowed transition задаётся versioned reducer-ом:

```text
not_started → running → succeeded | rejected | unresolved | skipped
unresolved → succeeded | rejected   // только append-only evidence correction
```

Terminal state не возвращается в `running`, correction не создаёт mutation.
Каждый прямой result command несёт expected `GroupTargetRevision` и source
aggregate ID/revision. Exact duplicate становится no-op; stale/future command
получает typed conflict и вызывающий service перечитывает актуальное состояние.
Отдельные inbox/event rows для этого не создаются. Operation projection
вычисляется reducer-ом из этих осей, а не произвольным status update.

В `transfer_group_targets` не сохраняются raw WB response, HTTP details,
request bytes, reconciliation evidence или список attempts. Эти факты принадлежат
`cardprepare`/`cardpublication`. Таблица остаётся небольшой проекцией для
orchestration, Telegram и текущего progress.

`transfer_item_targets` — единственная current projection результата конкретной
карточки в конкретном кабинете:

```text
TransferItemTargetID
TransferID
GroupTargetID
TransferItemID
Revision
State          // pending | running | terminal
OutcomeClass   // nullable: success | skipped | rejected | unresolved | internal_error
OutcomeCode    // nullable bounded feature code
NMID           // nullable
SourceActionID // nullable typed external reference
AttentionClosedAt // nullable; только manual close unresolved/internal_error
StartedAt      // nullable
FinishedAt     // nullable
```

Detailed immutable member result хранится в `cardpublication`, а
`transfer_item_targets` содержит только последнее подтверждённое состояние.
Typed result command атомарно сохраняет owner fact и обновляет эту projection
через caller-owned `DBTX`. Статистика не должна угадывать item result по
group-level status.

`transfer_targets` замораживает cohort name/position, `CabinetID`, `SellerKey`,
binding/capability revisions и required capabilities; token/token fingerprint
туда не попадает. `transfer_live_authorizations` хранит exact `PlanDigest`,
trusted actor snapshot, lifecycle timestamps, expiry и current state. Mutation
attempt ссылается на authorization ID, но не изменяет authorization table.

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
4. Получить immutable ordered snapshot выбранного named cohort из startup
   target-registry. На этом шаге WB requests не выполняются.
5. Если snapshot пуст, partial, содержит duplicate seller или недоступный target,
   transfer row не создавать.
6. Проверить overflow-safe capacity gates до materialization и вычислить
   `TargetSetRoot`.
7. Одной transaction создать transfer в `initializing` и immutable targets.
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

При startup WB Core безопасно декодирует credential metadata из JWT claims
`sid`, Content capability, read-only bit и expiry. Затем он проверяет exact
token через Content `/ping`, получает authenticated seller identity через
General `/api/v1/seller-info` и требует совпадения обоих `sid`.
`transfer/transport/wb` не делает повторных WB-запросов: он только преобразует
immutable Core snapshot в feature port. Persistent `CabinetID ↔ SellerKey`
binding создаётся/обновляется WB Core только после успешной проверки; raw `sid`
и token не покидают Core. Partial registry запрещён: ошибка любого credential
останавливает startup до запуска Telegram.
Ротация token того же seller сохраняет binding; другой seller под тем же
`CabinetID` делает cohort unavailable. Display name и raw token в snapshot/digest
не входят.

`SellerKey` — versioned canonical digest, одинаковый для одного WB seller после
restart/token rotation. `BindingRevision` меняется только при explicit accepted
seller rebind, `CapabilityRevision` — при verified capability/read-only change;
обычная ротация token с тем же seller/capabilities не меняет их. `TargetSetRoot`
считается по canonical ordered cohort, CabinetID, SellerKey, обеим revisions и
required capabilities.

WB Core identity registry владеет `wb.cabinet_identity_bindings` с unique
`CabinetID`, `SellerKey`, binding/capability revisions, capability snapshot и
verified-at/status. Mismatch никогда не перезаписывает binding автоматически:
он переводит запись в `identity_mismatch` и останавливает startup. Возврат token
исходного seller снова активирует прежний binding; автоматического rebind к
другому seller нет. Это не transfer business state и credentials там не
сохраняются.

Startup snapshot сохраняет ordered `Position`, `ContentRead=true`,
`ContentWrite=true` и expiry/probe evidence revision и остаётся immutable весь
process lifetime. `transfer.Start` только копирует его в operation. Duplicate
CabinetID/SellerKey и любой non-readable/non-writable/expired target не дают
приложению запуститься. Если token или cabinet state меняется после startup,
новый snapshot появляется только после controlled restart.

В v1 WB Core декодирует документированные JWT claims `id`, `sid`, `s`, `exp`,
проверяет их форму/expiry и выдаёт feature только domain-separated `SellerKey`,
capabilities, expiry, revisions и `ClientGeneration`. Декодированные claims без
successful Content ping, seller-info comparison и binding sync не считаются
trusted. Persistent Core registry хранит только digests, capabilities,
revisions, expiry, verified time и `ClientGeneration`; raw token и raw `sid` не
сохраняются. Clientset и verified snapshot immutable на время process lifetime,
hot reload credentials в v1 отсутствует.

Для обычного production flow `CohortName=production` содержит все утверждённые
mutation targets. Isolated canary запускается с отдельным deployment/config
`CohortName=canary`, а не пользовательским исключением одного target из уже
созданного production transfer. Canary и production process не работают
одновременно против одной DB: это последовательная смена
configured cohort либо полностью отдельное окружение с отдельной DB. Promotion
использует новую import session и новый immutable batch/TransferID для полного
production cohort; finalized canary batch не переиспользуется и не клонируется
неявно.

`transfer.Start` не вызывается из `cardimport` или Telegram. Его вызывает только
feature-local polling loop модуля `transfer`. Loop запускается composition root-ом
после успешной startup-проверки кабинетов и останавливается вместе с process
context. Интервал v1 фиксирован конфигурацией со значением по умолчанию
`3s`; concurrent tick не запускается, пока предыдущий ещё выполняется.

Каждый tick работает так:

1. Сначала получает transfers в phase `initializing`, чтобы закончить работу,
   прерванную остановкой процесса.
2. Затем читает finalized batches стабильными bounded pages через
   `cardimport.BatchReader`.
3. Для каждого batch вызывает идемпотентный `Start`, затем `Initialize`.
4. Успешно обработанный или детерминированно отклонённый batch сдвигает локальный
   cursor; transient DB error останавливает текущий проход и повторяется на
   следующем тике.
5. После restart cursor начинается сначала. `UNIQUE(transfers.batch_id)` и
   идемпотентные методы делают повторный просмотр уже обработанных batches
   безопасным.

Transfer foundation processor не создаёт event/outbox/job rows и сам не вызывает
WB mutations. Composition root последовательно запускает в том же polling loop
подготовку и authorization-gated product/media dispatch. В текущем
поддерживаемом one-instance deployment одного последовательного loop достаточно.

Capacity policy задаётся конфигурацией и как минимум ограничивает
`ItemsCount`, `GroupsCount`, `TargetsCount`, `ItemsCount×TargetsCount` и
`GroupsCount×TargetsCount`. Произведения считаются с overflow check по значениям
BatchHeader и snapshot до создания transfer; превышение возвращает typed
`transfer_capacity_exceeded`, не оставляя transfer или matrix rows. Это не
WB Cards Limits, а локальная защита PostgreSQL от неконтролируемой матрицы.

### 6.4. Initialization

Initialization выполняется один раз логически для каждого batch. Отдельной
таблицы `transfer_initializations`, chunk checkpoints, lease, fence, process
epoch и generic worker queue нет. Физический повтор допустим только после
rollback или restart; committed initialization повторно ничего не создаёт.

`Initialize(TransferID)` lock-ит transfer row и проверяет phase:

- `initializing` — выполняет materialization;
- `preparing` или более поздняя phase — возвращает успех как idempotent no-op;
- `phase=finished, outcome=failed, attention_code=initialization_failed` —
  возвращает сохранённый результат без retry.

Сначала `Initialize` читает immutable batch bounded pages, собирает будущую
матрицу в памяти и до записи проверяет contiguous positions, checksum, counts и
group membership. Этот read выполняется через `cardimport.BatchReader`; transfer
не читает mutable import tables и не изменяет таблицы `cardimport`.

После успешной проверки одна transfer-owned bounded transaction lock-ит
operation, повторно проверяет phase и выполняет:

```text
insert transfer items
→ insert source groups/members
→ insert group-target rows for every group × target cabinet
→ create item-target rows referencing group-target
→ validate persisted counts and matrix cardinality
→ phase initializing → preparing
```

Пять materialized tables имеют простое назначение:

- `transfer_items` — копия каждой исходной карточки внутри операции;
- `transfer_groups` — логические группы исходных карточек;
- `transfer_group_members` — какие карточки входят в каждую группу;
- `transfer_group_targets` — одна задача на каждую пару `group × cabinet`;
- `transfer_item_targets` — отдельный результат каждой карточки в каждом
  кабинете.

Capacity gate до начала transaction гарантирует, что вся матрица помещается в
одну bounded transaction. Любая transient/DB ошибка откатывает все вставки и
оставляет transfer в `initializing`; следующий tick повторяет initialization с
начала. Частично созданной матрицы после commit не существует. Несовпадение
checksum/counts является deterministic: матрица откатывается, а transfer
отдельно переводится в `phase=finished`, `outcome=failed`,
`attention_code=initialization_failed`, поэтому polling не повторяет невалидный
batch бесконечно. До `preparing` downstream use cases не вызываются.

### 6.5. Phase, outcome и current projections

У operation нет одного универсального status. Текущее место выполнения,
конечный результат и необходимость участия оператора являются независимыми
измерениями:

```text
phase:
  initializing | preparing | awaiting_authorization
  | publishing | reconciling | media | finished

outcome:
  running | succeeded | partial | rejected
  | unresolved | failed | cancelled

attention_code:
  nullable bounded safe code
```

Правила:

- `phase` отвечает только на вопрос «что сейчас выполняется»;
- `outcome=running` действует до `phase=finished`;
- `manual_review` не является phase или отдельным status: новый unresolved
  result получает `attention_code`, а очистить его можно только append-only
  `close_unresolved_no_retry`; сам outcome при этом остаётся unresolved;
- initialization error даёт `phase=finished, outcome=failed`;
- полностью успешная операция даёт `phase=finished, outcome=succeeded`;
- смешанные terminal item results дают `phase=finished, outcome=partial`;
- correction после reconciliation может изменить `unresolved` на доказанный
  terminal outcome, но не создаёт новую mutation attempt.

Projection вычисляется только из persisted group/item results и action facts
фиксированным reducer-ом. Feature не выставляет произвольную пару
`phase/outcome` напрямую.

Для длительности используются отдельные `started_at` и `finished_at`.
`updated_at` показывает последнее изменение строки и не используется как
начало/окончание этапа или попытки.

### 6.6. Простая live authorization

`cardpublication` сначала создаёт immutable publication plan и его actions.
Одна authorization подтверждает точный набор ещё не начатых действий:

```text
PlanDigest = hash(
  contract version
  + TargetSetRoot
  + ordered(
      ActionKey
      + ActionKind
      + TargetID
      + MemberSetDigest
      + RequestDigest
      + MediaLinkSetRoot
    )
)
```

В digest входят как product actions, так и заранее известные potential media
actions с immutable member/link binding. `nmID` для media может появиться позже,
но action не становится dispatchable без persisted attribution по уникальному
`CabinetID + vendorCode`.

`transfer_live_authorizations` хранит:

```text
LiveAuthorizationID
TransferID
PlanDigest
Revision
State
TrustedActorID
TrustedActorDigest
RequestedAt
ApprovedAt
ExpiresAt
RevokedAt
ClosedAt
SafeReasonCode
```

Каждая `request/approve/revoke/expire/supersede/close` дополнительно
сохраняется как immutable row в
`transfer_live_authorization_commands`: `IdempotencyKey`, actor/command digests,
result state/revision. Одна authorization может иметь несколько
commands; idempotency относится к команде, а не к lifecycle row.

State machine ограничена:

```text
requested → authorized → revoked | expired | superseded | closed
    └→ revoked | expired | superseded
```

Минимальные commands:

```go
type RequestLiveCommand struct {
    TransferID     TransferID
    PlanDigest     Digest
    IdempotencyKey string
}

type ApproveLiveCommand struct {
    TransferID           TransferID
    LiveAuthorizationID  LiveAuthorizationID
    ExpectedRevision     int64
    ExpectedPlanDigest   Digest
    IdempotencyKey       string
}

type RevokeLiveCommand struct {
    TransferID          TransferID
    LiveAuthorizationID LiveAuthorizationID
    ExpectedRevision    int64
    SafeReasonCode      string
    IdempotencyKey      string
}
```

Actor всегда берётся из trusted Telegram/auth context, а не из callback payload.
Exact replay одного command key возвращает прежний result. Тот же key с другим
payload или actor digest даёт `idempotency_conflict`.

Отдельного clock worker нет. Approve, dispatch и query после row lock сравнивают
`expires_at` с текущим временем и синхронно переводят истёкшую authorization в
`expired`. Revoke/expiry запрещают только ещё не начатые actions. Уже
зафиксированная attempt может только получить response/reconciliation result.
Для продолжения того же неизменного plan создаётся новая authorization на тот же
`PlanDigest`; dispatchable остаются только actions без attempt. Отдельный digest
«оставшихся slots» не нужен, потому что `UNIQUE(action_id)` физически запрещает
повтор уже начатого action.

`ActionID` является единственным dispatch identity. Таблица
`publication_actions` имеет unique `(transfer_id, plan_digest, action_key)`, а
`publication_attempts` — `UNIQUE(action_id)`. Поэтому create, add и media
используют одну физическую защиту от второго вызова. Отдельные
`transfer_dispatch_slots`, publication/media attempt tables, execution
generations, component roots и remaining-slot roots в v1 не создаются.

Если plan устарел до первого begun action, старая authorization и старые planned
actions переводятся в `superseded`, после чего создаётся новый immutable plan и
нужен новый approval. После первого begun action автоматический replan в v1
запрещён: begun/unknown action не пересобирается, а оставшаяся операция
закрывается с operator attention. Новый запуск выполняется через новый immutable
batch/TransferID. Это намеренное упрощение вместо частичного переноса slots между
generations.

Для атомарной проверки `cardpublication` использует transfer-owned typed port:

```go
type LiveAuthorizationVerifier interface {
    LockValid(
        ctx context.Context,
        tx DBTX,
        check LiveAuthorizationCheck,
    ) (LiveAuthorizationEvidence, error)
}
```

`LiveAuthorizationCheck` содержит `TransferID`, `ActionID`,
`LiveAuthorizationID`, expected authorization revision, exact `PlanDigest`,
`TargetSetRoot`, `TargetID`, `CabinetID`, `SellerKey` и pinned
`ClientGeneration`.
В одной caller-owned transaction:

1. `cardpublication` lock-ит свой action и identities;
2. verifier lock-ит transfer-owned authorization;
3. verifier проверяет `TRANSFER_MODE=live`, `authorized`, TTL, exact plan и
   startup-frozen target/seller/client binding, а также что `ActionID`
   действительно принадлежит этому plan/target, остаётся `planned`
   и ещё не привязан к authorization;
4. verifier возвращает immutable authorization evidence, не изменяя таблицы
   `cardpublication`;
5. `cardpublication` создаёт единственную attempt и переводит action в
   `dispatching`;
6. transaction commit-ится до внешнего WB call.

После commit выполняется не более одного WB round trip. Revoke и создание
attempt сериализуются row lock: победивший revoke даёт zero call, а committed
attempt уже не повторяется и может только завершиться или reconciliation.

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
```

Cross-transfer metadata cache не входит в v1: сначала сохраняется immutable
snapshot на каждый group-target. Cache можно добавить позже как прозрачную
оптимизацию safe reads, не меняя proposal contract.

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
error возвращается текущему синхронному caller-у без сохранения terminal result;
следующий transfer polling tick может повторить только всё ещё pending
group-target. Отдельного worker, timer и фоновой retry policy нет.

### 7.5. Выходной контракт

```go
type PreparationResult struct {
	TransferID    TransferID
	GroupTargetID GroupTargetID
	PreparationID PreparationID
	ProposalRoot  Digest
	Revision      int64
}
```

Large payload в result command не помещается. Следующий модуль получает
immutable PreparationID и читает proposal через query port.

### 7.6. Первый реализуемый срез и детерминированные правила v1

Первый срез `cardprepare` — чистый planner без БД и фонового lifecycle. Он получает
одну frozen source group и уже прочитанный catalog snapshot, а возвращает typed
`UploadCardsGroup` либо один safe outcome. Срез не получает WB client и поэтому
конструктивно не может выполнить mutation. Leases, очередь и отдельная delivery
инфраструктура в текущем плане не создаются; вопрос рассматривается заново
после завершения `transfer` и `statistics`.

Второй срез разделён на два слоя. `cardprepare/transport/wb` имеет по одному
методу на safe WB read, выбирает закрытый `OperationSpec` и возвращает service
полный raw response DTO без pagination или interpretation. `cardprepare/service`
владеет последовательностью: один Cards Limits read, paginated Subjects,
subject resolution, Characteristics, paginated Brands и только реально
используемые Directories. Service проверяет `ResponseMeta`, не переносит raw
`ErrorText` в outcome и только после полного snapshot вызывает pure planner.
Context cancellation и transport errors возвращаются вызывающему service как
error; data и application-envelope outcomes остаются bounded typed results.

Текущий integration slice остаётся синхронным:

- `transfer` явно вызывает `cardprepare` через typed service contract;
- source group и target cabinet передаются use case-у, WB reads выполняются без
  открытой DB transaction;
- transport/context failure возвращается вызывающему service без фонового retry;
- successful proposal сохраняет immutable metadata snapshot, payload и member
  rows в таблицах `cardprepare`;
- результат явно применяется `transfer` через typed command; `cardprepare`
  никогда напрямую не обновляет transfer-owned tables;
- canonical mutation request хранится exact byte-for-byte в `BYTEA`, а не в
  `JSONB`; PostgreSQL не должен пересериализовать body, участвующий в
  `ProposalRoot`;
- read-only query port выдаёт proposal только по exact
  `TransferID + GroupTargetID + PreparationGroupID + ProposalRoot`, проверяет
  frozen member cardinality/order и не раскрывает SQL следующему feature;
- повторный прямой вызов сравнивает frozen identity/digests, а terminal proposal
  нельзя переписать другим payload.

Правила v1:

- category/subject, brand и имена characteristics сравниваются после trim,
  Unicode lower-case и схлопывания пробелов; в payload сохраняется каноническое
  значение WB из snapshot;
- ноль exact subject matches даёт `subject_not_found`, больше одного —
  `subject_ambiguous`; fuzzy/autocorrect в v1 нет;
- пустой brand допустим, непустой обязан иметь ровно одно exact совпадение в
  subject-specific Brands;
- все variants одной source group обязаны иметь один resolved subject;
- `charcType=1` принимает строку или разделённый `;` список строк и проверяет
  `maxCount`; `charcType=4` принимает ровно одно конечное число;
- неизвестная, дублированная или неподдерживаемая characteristic даёт typed data
  outcome, а не молчаливое удаление;
- обязательные characteristics проверяются после добавления системных значений
  из frozen item (`VATRate`); directory values проверяются только по реально
  использованным справочникам snapshot;
- limits являются observation: `freeLimits + paidLimits` сравнивается с числом
  создаваемых variants, но не является обещанием доступности на dispatch;
- canonical JSON, metadata digest и proposal root вычисляются после полной
  проверки; входные slices предварительно копируются и сортируются, поэтому
  planner не зависит от порядка ответа WB;
- planner проверяет лимит variants, длины title/description и serialized request
  bytes feature-owned validator-ом; byte limit берётся из immutable
  `UploadCardsOperation`, поэтому feature не дублирует transport bound.

## 8. Модуль `cardpublication`

### 8.1. Ответственность

Только этот модуль может изменять карточки WB в transfer flow. Он владеет:

- persistent product identity;
- normal/trash preflight и existing-card decision;
- immutable publication plans;
- единым action/member/attempt journal для create, add и media;
- Cards Error List evidence;
- `Upload`/`UploadAdd` dispatch и reconciliation;
- attribution нового `nmID`;
- прямой передачей исходных photo URLs в `SaveMediaByLinks`;
- media reconciliation.

`cardpublication` не владеет transfer progress tables. После сохранения своего
durable result он обновляет current transfer projection только typed command-ом
в той же caller-owned transaction.

### 8.2. Plan, action, member и attempt

Главная модель модуля:

```text
PublicationPlan
    └── PublicationAction
            ├── PublicationActionMember
            └── PublicationAttempt (0 или 1)
                    └── persisted observations/evidence
```

Назначение сущностей:

- `PublicationPlan` — immutable exact набор действий одного transfer;
- `PublicationAction` — одно потенциальное внешнее изменение WB;
- `PublicationActionMember` — immutable membership и current result карточки;
- `PublicationAttempt` — факт единственного WB round trip;
- observation/attribution rows — доказательства remote результата.

Action kinds:

```text
create_group | add_to_group | upload_media
```

Action state:

```text
planned | authorized | dispatching | reconciling | terminal | superseded
```

Terminal result хранится независимо от state:

```text
outcome_class:
  success | skipped | rejected | partial | unresolved | internal_error

outcome_code:
  nullable bounded feature-specific code
```

`state` отвечает за выполнение, `outcome_class` — за агрегирование
статистики, `outcome_code` — за точную безопасную причину. Raw WB error text не
является outcome code и не используется как статистическое измерение.

### 8.3. Product identity и простая concurrency policy

Global identity key v1:

```text
CabinetID + exact vendor_code_key COLLATE "C"
```

`vendor_code_key` использует versioned importer normalization, не меняет
регистр и не выполняет Unicode folding. Barcode/SKU не входит в global identity.
Пустой barcode не передаётся и генерируется WB; переданный barcode сохраняется в
request/evidence exact.

`product_identities` хранит известные `nmID`/`imtID`/subject ID, current
remote state и nullable active action reference:

```text
unknown | mutation_pending | remote_present | rejected
| blocked_uncertain | remote_missing | remote_conflict
```

В v1 `cardpublication` обрабатывает не более одного mutating action одновременно
на один CabinetID. Это feature-local последовательная lane внутри
поддерживаемого one-process deployment. Persistent
`UNIQUE(cabinet_id, vendor_code_key)` и row locks защищают identity; отдельные
group-claim, follower и remote-group-claim state machines не создаются.

Если другой transfer встречает:

- `remote_present` — получает read-only existing result;
- `mutation_pending` — не вызывает WB и ждёт terminal/reconciliation result
  текущего action;
- `blocked_uncertain` — получает unresolved/operator attention;
- conflicting remote identity — получает bounded conflict code.

Cross-process leases/fencing остаются частью отдельного будущего runtime
решения. До него второй application instance не поддерживается.

### 8.4. Existing-card decision table

| Remote state | Outcome code |
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

Оба `ALREADY_PRESENT_*` ничего не изменяют и сохраняют bounded diff только по
полям, доступным в Cards List. Для них mutation action не создаётся, а
`transfer_item_targets` получает `outcome_class=skipped` и точный
`outcome_code`.

### 8.5. Preflight и построение immutable plan

Один cabinet-scoped normal/trash observation может обслуживать bounded page
group-targets. WB API не выдаёт snapshot token: observation является evidence,
а не внешней блокировкой.

Request packing:

```text
target
→ operation kind
→ subject
→ complete source group
→ bounded serialized request
```

- source group не режется между create requests;
- один add action относится к одному target `imtID`;
- в v1 один create action содержит ровно одну complete source group. Это
  намеренно убирает межгрупповой packing и делает action/result attribution
  прямым; объединение нескольких groups остаётся возможной будущей
  оптимизацией;
- учитываются documented group/variant и serialized-byte limits;
- exact request bytes и ordered membership сохраняются до approval.

Каждый mutating request становится одним `PublicationAction`. Для каждого
prepared member с photo URLs заранее создаётся potential `upload_media` action,
связанный с product member и immutable `MediaLinkSetRoot`. Такой action остаётся
не dispatchable, пока reconciliation не свяжет exact новый `nmID` с member по
уникальной паре `CabinetID + vendorCode` и ожидаемой WB-группе.

До attribution media action хранит не готовый HTTP request, а immutable template
`{links}`: его `RequestDigest` фиксирует exact ordered links, а
`MediaLinkSetRoot` связывает authorization с ними. После persisted attribution
attempt сохраняет отдельный exact digest фактического
`SaveMediaByLinks{nmID, links}` request; template и link root при этом не
изменяются.

`publication_actions` хранит:

```text
ActionID
TransferID
PlanID
PlanDigest
ActionKey
TargetID
Kind
State
OutcomeClass
OutcomeCode
RequestDigest
RequestPayload
MemberSetDigest
MediaLinkSetRoot
AuthorizationID
CreatedAt
StartedAt
FinishedAt
```

`publication_action_members` связывает request с transfer matrix:

```text
ActionMemberID
ActionID
TransferID
GroupTargetID
TransferItemTargetID
RequestMemberIndex
VendorCode
OutcomeClass
OutcomeCode
NMID
```

Один create action может включать несколько complete source groups одного
target; каждый member сохраняет собственный `GroupTargetID`. Add action содержит
members одного group-target/remote `imtID`, media action — ровно один member.

`ActionKey` детерминирован из kind, target, ordered group/member identities и
immutable payload/link digest. Unique
`(transfer_id, plan_digest, action_key)` делает повторное построение того же plan
идемпотентным.

Непосредственно перед attempt выполняется targeted recheck decision inputs.
Если recheck меняет create/add/already/conflict decision или exact request bytes:

- до первого begun action plan становится `superseded`, zero call, строится
  новый plan и требуется новый approval;
- после первого begun action автоматический replan запрещён, affected result
  становится unresolved/operator attention.

### 8.6. Error List feed

Error List остаётся внутренним submodule `cardpublication/errorfeed` и владеет:

- per-target cursor;
- overlap pagination;
- normalized immutable error batches;
- deduplication;
- pre-dispatch baseline;
- correlation evidence.

Ошибка до baseline не коррелируется с новым action. Несколько подходящих
candidates дают unresolved, а не произвольный выбор.

### 8.7. Единый mutation journal

До каждого WB call одна transaction:

1. lock-ит action, identities и exact live authorization;
2. повторно сверяет action с approved `PlanDigest`;
3. сохраняет Error List baseline и targeted recheck observation;
4. создаёт единственный `publication_attempts` row;
5. переводит action/identities в `dispatching`;
6. commit-ит до внешнего I/O.

После commit выполняется ровно одна raw WB operation, соответствующая
`action.kind`:

```text
create_group → Upload
add_to_group → UploadAdd
upload_media → SaveMediaByLinks
```

Feature `transport/wb` возвращает полный raw response DTO. Service выполняет
versioned classification:

```go
type SubmissionDisposition string // accepted | rejected_proven | uncertain

type SubmissionResult struct {
    Delivery        DeliveryState
    HTTPStatus      int
    SafeRequestID   string
    ClassifierVersion string
    Disposition     SubmissionDisposition
    SafeCode        string
    MemberResults   []MemberDisposition
    UnmatchedCount  int
}
```

Правила:

- HTTP success и `ResponseMeta.Error=false` означает только accepted
  submission, но не созданную карточку;
- `ResponseMeta.Error=true` не считается accepted;
- terminal `rejected_proven` допустим только при documented exact proof, что
  member/request не принят;
- generic 5xx, unknown 4xx, malformed/incomplete envelope и ambiguous member
  mapping дают `uncertain`;
- raw `errorText`, credentials и arbitrary details не сохраняются в business
  result/logs;
- unknown/uncertain result не разрешает автоматический повтор.

`publication_attempts` хранит:

```text
AttemptID
ActionID                 // UNIQUE, NOT NULL
AuthorizationID
ErrorBaselineID
RecheckObservationID
RequestDigest
RequestPayload             // exact bytes actually sent
AttributionID              // nullable; required by media attempt
DeliveryState
HTTPStatus
ResponseDisposition
ClassifierVersion
SafeRequestID
SafeErrorCode
UnmatchedCount
StartedAt
FinishedAt
```

Request/member response evidence хранится immutable. Для product attempt payload
совпадает с action request; для media action остаётся immutable `{links}`
template, а attempt фиксирует final body с attributed `nmID`. Common attempt table
используется для product и media, поэтому статистике не нужен `UNION` разных
journal-моделей.

Action transition:

```text
planned → dispatching
dispatching → reconciling | terminal
reconciling → terminal
```

Инварианты:

```text
round_trips_per_action <= 1
possibly_applied_actions_per_identity <= 1
unknown outcome => no automatic mutation retry
```

### 8.8. Reconciliation

Product evidence:

- normal Cards List;
- Trash List;
- Cards Error List после baseline.

Safe result codes включают:

```text
already_present_compatible
already_present_different_untouched
created_confirmed_by_vendor_code
rejected
partial_remote
remote_conflict
unresolved
```

Accepted, uncertain и crash-after-dispatch action переходят в reconciliation.
Exact proven rejection терминален только для однозначно mapped members.
Deadline без доказательства remote effect даёт `outcome_class=unresolved`, а не
повтор mutation.

Manual resolution принимает только persisted evidence:

- `mark_remote_present` требует exact post-submission observation после attempt,
  ровно одну normal card с тем же vendor code, отсутствие такой card в trash и
  совпадение `subjectID` для create либо `imtID` для add;
- `mark_rejected` требует exact Error List batch после зафиксированного baseline
  с тем же vendor code и непустыми bounded error codes;
- `close_unresolved_no_retry` закрывает attention, но сохраняет identity
  `blocked_uncertain`.

Все три команды требуют trusted actor, idempotency key и expected action
revision. Результат сохраняется в append-only
`publication_manual_resolutions`; action/plan current result, product identity и
`transfer_item_targets` меняются в одной transaction. Exact replay возвращает
тот же journal result. Stale revision/binding/evidence отклоняются. После
`close_unresolved_no_retry` позднее доказательство можно применить новой
revision: `attention_closed_at` очищается, но нового attempt/WB call не
появляется. Команды «повторить тот же action» нет.

### 8.9. Attribution boundary

Текущий Upload contract не возвращает universal request/member → `nmID`
correlation. В v1 применяется более простая domain policy: WB не допускает две
карточки с одинаковым `vendorCode` в одном кабинете. Если targeted recheck прямо
перед attempt подтвердил отсутствие карточки, а после единственного committed
attempt появилась ровно одна normal card с тем же `vendorCode` и ожидаемым
`subjectID`/`imtID`, она считается результатом этого action. Это осознанно
принимает небольшой риск конкурентной записи внешним WB-клиентом между recheck
и observation; неоднозначные или конфликтующие evidence остаются fail-closed.

| Evidence | Product result | Automatic media |
|---|---|---|
| exact card существовала до action | `already_present_*` | запрещена |
| protocol-level exact member → `nmID` correlation | `created_attributed` | разрешена |
| absent до attempt + одна matching card после attempt | `created_confirmed_by_vendor_code` | разрешена |
| correlated rejection | `rejected` | запрещена |
| conflicting/partial evidence | `unresolved`/`partial_remote` | запрещена |

Attribution evidence хранит:

```text
AttributionEvidenceID
TransferID
GroupTargetID
PublicationActionID
PublicationActionMemberID
CabinetID
NMID
PlanDigest
AttemptID
PreflightObservationID
PostObservationID
ErrorBaselineID
DirectCorrelationID
Level                 // direct | observed_after_attempt | ambiguous
SafeReasonCode
```

`Level=observed_after_attempt` является достаточным attribution evidence только
при выполнении описанной выше unique-vendor policy и строгой проверке ожидаемой
группы. `Level=direct` также поддерживается, если WB позже даст прямую
correlation. В остальных случаях media action не становится dispatchable.

### 8.10. Данные и ownership

```text
wb.product_identities
wb.publication_plans
wb.publication_actions
wb.publication_action_members
wb.publication_attempts
wb.publication_observations
wb.publication_attributions
wb.publication_error_cursors
wb.publication_error_batches
wb.publication_error_baselines
wb.publication_error_correlations
wb.publication_manual_resolutions
```

Не создаются:

```text
wb.transfer_dispatch_slots
wb.publication_intents
wb.publication_requests
wb.publication_media_intents
wb.publication_media_attempts
wb.publication_group_claims
wb.publication_followers
wb.publication_remote_group_claims
```

Action является intent, exact request и dispatch identity одновременно.
`publication_action_members` хранит immutable ordered request membership и
current member outcome. Evidence-based correction меняет только bounded current
outcome; `publication_manual_resolutions` хранит неизменяемую команду, actor,
revision и ссылку на выбранное доказательство. Исходная
attempt/response/observation evidence остаётся immutable.
`transfer_item_targets` хранит только current межмодульную projection.

## 9. Media-подсистема внутри `cardpublication`

Отдельного feature `media` и отдельных media journal tables нет. Media — это
`PublicationAction{Kind: upload_media}` и обычный
`PublicationAttempt` того же модуля.

### 9.1. Вход и eligibility

Potential media action создаётся вместе с publication plan из frozen prepared
member:

```go
type SaveMediaCommand struct {
    TransferID                 TransferID
    ActionID                   PublicationActionID
    GroupTargetID              GroupTargetID
    PublicationActionMemberID  PublicationActionMemberID
    CabinetID                  CabinetID
    NMID                       int64
    PhotoURLs                  []string
    PlanDigest                 Digest
    LiveAuthorizationID        LiveAuthorizationID
}
```

`PhotoURLs` сохраняют исходный порядок. В v1 они не скачиваются, не
проверяются и не сохраняются локально. Валидность ссылок фактически проверяет WB
при `SaveMediaByLinks`.

Media action требует одновременно:

- exact member/product action binding;
- terminal successful product result и persisted `nmID` attribution уровня
  `direct` либо `observed_after_attempt`;
- matching immutable link-set root;
- действующую authorization exact `PlanDigest`;
- отсутствие attempt для этого ActionID.

Existing, rejected, unresolved и неоднозначные card media action не получают.

### 9.2. Dispatch и result

Dispatch использует общий алгоритм §8.7:

1. verifier lock-ит authorization и common action;
2. проверяется attribution/member/`nmID`/link-set binding;
3. создаётся common `publication_attempts` row;
4. после commit выполняется один `SaveMediaByLinks`;
5. raw response классифицируется и сохраняется common action outcome-ом;
6. accepted envelope завершает media как success с bounded code
   `MEDIA_REQUEST_ACCEPTED`: это подтверждает принятие запроса WB, но без
   отложенной проверки ссылок не доказывает фактическое появление файлов;
   application error даёт rejected, а ambiguous/unknown delivery —
   unresolved/operator attention;
7. unknown delivery не повторяется: Cards List не позволяет доказать применение
   исходных ссылок после преобразования их в WB CDN URLs.

Automatic media dispatch включён по умолчанию, но остаётся capability-gated:
`CARDPUBLICATION_MEDIA_AUTO_DISPATCH=false` выключает его без изменения плана.
Недоступная vendor-code attribution оставляет media action terminal
`outcome_class=skipped`,
`outcome_code=VENDOR_CODE_ATTRIBUTION_UNAVAILABLE`.

### 9.3. Отложено

До отдельного решения после основного transfer flow отложены:

- URL scheme, DNS/IP, redirect и SSRF validation;
- скачивание photo/video;
- MIME, magic bytes, hash и size checks;
- content-addressed storage и deduplication;
- quota reservation;
- immutable asset manifests;
- signed public URLs и HTTP media server;
- retention и garbage collection.

## 10. Модуль `statistics`

Статус на 2026-08-24: **отложен** до отдельной команды на продолжение. В
текущем repository сохранён только foundation из read-only SQL views. Package
`internal/feature/statistics`, repository/service, Telegram presentation,
notifications и composition-root wiring пока не создаются.

`statistics` является отдельной read-only feature:

- не вызывает WB;
- не изменяет business tables;
- не поддерживает собственные вручную увеличиваемые counters;
- не восстанавливает результаты из application logs;
- не использует raw error text как dimension.

### 10.1. Источники

Текущий UI читает transfer projections:

- operation `phase/outcome/attention_code`;
- group-target stage progress;
- item-target current results;
- notification state.

Историческая статистика читает durable facts `cardpublication`:

- immutable plan/action identity и membership вместе с current outcomes;
- immutable реальные WB attempts;
- delivery/response classification;
- observations/attribution;
- action/member outcomes и timestamps.

Для стабильной SQL-границы в `000001` создаются только read-only views, но не
summary tables:

```text
wb.statistics_transfer_facts
wb.statistics_item_facts
wb.statistics_action_facts
wb.statistics_attempt_facts
```

Views выбирают bounded business columns из owner tables и не содержат request
payload, raw response или credentials. `statistics/repository` читает эти views;
только owner feature меняет исходные строки.

### 10.2. Обязательные dimensions и measures

Dimensions:

```text
created date/time bucket
TransferID
CabinetID + frozen target position
ActionKind
Phase
OutcomeClass
OutcomeCode
DeliveryState
ResponseDisposition
requires_attention
```

Measures:

```text
transfers total
items/groups/actions total
terminal/success/skipped/rejected/partial/unresolved counts
real WB round trips
unknown delivery count
preparation duration
action duration
transfer end-to-end duration
```

Правила:

- `started_at`/`finished_at` используются для duration; `updated_at` не
  используется как время начала или конца;
- `OutcomeClass` даёт стабильную крупную группировку, `OutcomeCode` —
  детализацию внутри feature;
- старые операции используют frozen target snapshot, поэтому переименование
  кабинета не переписывает историю;
- reconciliation correction меняет current projection, но исходная attempt и её
  delivery evidence остаются immutable;
- каждый item result должен иметь `SourceActionID` или safe read-only outcome,
  поэтому строка объяснима от общей статистики до конкретной попытки.

### 10.3. Query model

Минимальные queries:

```go
type Reader interface {
    ListOperations(ctx context.Context, filter OperationFilter) ([]OperationRow, error)
    GetOperation(ctx context.Context, id TransferID) (OperationDetails, error)
    AggregateTransfers(ctx context.Context, filter AggregateFilter) (TransferTotals, error)
    AggregateItems(ctx context.Context, filter AggregateFilter) (ItemTotals, error)
    AggregateActions(ctx context.Context, filter AggregateFilter) (ActionTotals, error)
    AggregateErrors(ctx context.Context, filter AggregateFilter) ([]ErrorGroup, error)
}
```

Telegram renderer вызывает только `statistics/service` и не читает repositories
напрямую. Drill-down сохраняет цепочку:

```text
transfer → group target → item target
         → publication action → attempt/evidence
```

На первом release aggregates считаются обычными indexed SQL queries по views.
Materialized views, cached counters и отдельные daily summary tables добавляются
только после измеренного performance bottleneck. Их отсутствие не меняет
business write model.

Prometheus/runtime metrics, если появятся позже, предназначены для health,
latency и process monitoring. Они не являются источником business statistics.

## 11. Общая инфраструктура в `core`

Отдельного инфраструктурного package root нет. Общие технические механизмы
продолжают существующую структуру `internal/core`:

```text
internal/core/
├── repository/postgres/
│   ├── pool/
│   └── transaction/
└── transport/
    ├── telegram/
    └── wb/
```

`core` не содержит product decisions и не владеет business tables feature.
WB DTO, `OperationSpec`, generic executor/clientset, rate limits, retry policy
для safe reads, delivery state и redaction остаются в
`internal/core/transport/wb`. В core нет endpoint-specific typed client methods,
pagination, `ResponseMeta` business interpretation, target cohort registry,
catalog mapping или payload validation.

Каждый WB-использующий модуль имеет собственный `transport/wb`. Этот transport
получает generic cabinet executor и вызывает `client.ExecuteResponse[T]` с
operation + query/body DTO. Helper возвращает полный raw response DTO
service-слою. Transport не создаёт второй HTTP client и не повторяет core
retry/rate-limit mechanics. Сложные последовательности reads, interpretation и
решения реализует feature service.

Это финальная граница WB Core. Пакет `typed` отсутствует намеренно: его готовые
endpoint methods дублировали бы `feature/*/transport/wb`. После этого этапа
реализация transfer/cardprepare/cardpublication/statistics не изменяет
`internal/core/transport/wb`.

### 11.1. Transaction boundary

Для атомарности используется общий contract из
`internal/core/repository/postgres/transaction`:

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

Business repository methods принимают один caller-owned `DBTX`; repository не
открывает вложенную independent transaction. Связанные изменения owner state
commit-ятся вместе.

`WithinTransaction` обязан rollback-нуть на returned error, panic, context
cancellation и commit error; panic после rollback rethrow-ится. Commit error
никогда не считается успехом и не запускает post-commit side effect. WB/HTTP,
любой external I/O внутри transaction callback запрещён;
callback только фиксирует local intent/evidence, внешний call выполняется после
успешного commit. Nested/independent transactions из repositories запрещены.

### 11.2. Отложенные delivery/runtime механизмы

Transactional outbox, dispatcher, generic worker framework, PostgreSQL advisory
singleton lock, process epoch и fencing в текущей реализации отсутствуют. Для
них нет Go packages, generic transport adapters, environment config, tables или
columns в `000001`. Единственное исключение — простой последовательный
трёхсекундный polling loop внутри `transfer`; он не является общей delivery
инфраструктурой, но последовательно вызывает feature-owned processors, включая
capability-gated product dispatcher `cardpublication`.

До завершения `transfer` и `statistics` composition root запускает обычные
server components и polling loop `transfer`; downstream use cases вызываются
явными service calls. Запуск двух экземпляров приложения против одной БД
считается неподдерживаемым deployment mode, но приложение технически не
блокирует его. Default mode оставляет WB mutations disabled; включение
`TRANSFER_MODE=live` предназначено только для контролируемого single-instance
development/canary, а отсутствие crash-safe delivery и singleton fencing не
принимается как production guarantee.

После завершения двух модулей вопрос рассматривается заново отдельным design
decision. Тогда выбирается конкретный вариант recovery/delivery; старый outbox
или runtime код автоматически не возвращается.

Mutation-target registry/client binding имеет собственный immutable
`ClientGeneration`. Dispatch transaction проверяет SellerKey/capabilities и
сохраняет client generation в attempt; post-commit call выполняется тем же
pinned immutable client handle. Hot reload не заменяет handle in-place: старый
handle живёт до окончания call, новый применяется только к новым attempts. Если
пинning невозможно, hot reload credentials в v1 запрещён и требует controlled
process restart. Так token/client нельзя сменить между verification и socket.

### 11.3. HTTP server

Отдельный media feature и HTTP server в текущем v1 не нужны: WB получает
исходные ссылки напрямую через `cardpublication/transport/wb`. Health/readiness
остаются deployment concern основного приложения.

## 12. Межмодульные contracts

Минимальные стабильные IDs/messages располагаются в отдельном contracts
package, но repositories, WB DTO и business logic туда не попадают:

```text
internal/contract/
├── transfer.go
├── preparation.go
└── publication.go
```

Основной flow прямых typed commands:

```text
transfer → cardprepare.Prepare
cardprepare → Prepared | PreparationRejected
transfer → applies preparation current projection

transfer → cardpublication.BuildPlan
cardpublication → PublicationPlanReady(PlanID, PlanDigest, ActionIDs)
                | PublicationPlanRejected

transfer → RequestLive(PlanDigest)
trusted actor → ApproveLive(AuthorizationID, PlanDigest)

transfer → cardpublication.DispatchAction(ActionID, AuthorizationID)
cardpublication → saves Action/Member/Attempt facts
cardpublication → PublicationResult(group/item results, SourceActionID)
transfer → applies current group/item projections

cardpublication upload_media action
  → waits for unique vendor-code attribution
  → uses the same DispatchAction/Attempt mechanism

statistics
  → reads transfer current projections
  → reads cardpublication durable fact views
```

Typed commands/results содержат IDs, expected revisions и bounded safe result
codes. Large payloads читаются по immutable reference через query port.

Все preparation/publication commands конкретной пары group × target содержат
persisted `TransferID + GroupTargetID`. Member result дополнительно содержит
`TransferItemTargetID` и `PublicationActionMemberID`; attempt/result содержит
`SourceActionID`. Consumer проверяет same-transfer composite identity, а не
восстанавливает пару по CabinetID/group value.

Product и media — разные `ActionKind`, но один aggregate/journal owner
`cardpublication`. Transfer не знает raw WB response и не создаёт mutation
attempt. Duplicate typed result является idempotent; media action не
dispatch-ится до persisted unique vendor-code attribution.

## 13. Package structure

```text
internal/feature/
├── cardimport/
│   ├── feature.go
│   ├── service/
│   ├── repository/
│   └── transport/
├── transfer/
│   ├── feature.go
│   ├── server/              # process-lifecycle polling, без business rules
│   ├── service/
│   ├── repository/postgres/
│   └── transport/
│       ├── telegram/
│       └── wb/
├── cardprepare/
│   ├── feature.go
│   ├── service/
│   ├── repository/postgres/
│   └── transport/
│       └── wb/
├── cardpublication/
│   ├── feature.go
│   ├── service/              # product + internal media use cases
│   ├── repository/postgres/  # common publication plan/action/attempt tables
│   └── transport/wb/         # Upload, UploadAdd, Error List, SaveMediaByLinks
└── statistics/               # planned, Stage 8 отложен
    ├── feature.go
    ├── service/
    ├── repository/
    └── transport/telegram/

internal/contract/
internal/core/
├── repository/postgres/transaction/
└── transport/
```

### 13.1. Pre-release schema policy

- Все новые tables, columns, indexes и constraints этого плана входят в
  `migration/000001_init.up.sql`.
- `migration/000001_init.down.sql` удаляет их в обратном dependency order.
- `000002` до первого production release не создаётся.
- Fresh-database rollout использует цикл `000001 up → down → up`.
- Локальная БД, где была применена промежуточная версия `000001`, не изменяется
  скрыто: разработчик явно пересоздаёт disposable database/volume. Production
  data этим процессом не удаляются.

## 14. Последовательность реализации

### Stage 0. Contracts и core foundation

Deliverables:

- Gate `CARDIMPORT-DONE`;
- optional barcode schema/DTO и ручная проверка exact Upload JSON;
- module IDs и typed service contracts;
- `DBTX`/UnitOfWork для связанных owner-state изменений;
- WB Core generic bounded JSON/non-2xx decode, delivery state and redacted
  transport classification; `ResponseMeta` interpretation остаётся feature;
- validated config/docs для mode, named cohort, authorization TTL и capacity;
- `TRANSFER_MODE`, default `disabled`.
- `TRANSFER_AUTHORIZATION_TTL`, default `1h`, допустимый диапазон
  `(0, 24h]`.

Exit:

- concurrent Finalize создаёт один versioned immutable batch;
- multi-file order/pagination/checksum stable, BatchReader возвращает только
  frozen finalized rows;
- SaveParsed/Finalize race и DB immutability constraints пройдены;
- empty barcode реально omits `skus` в Upload/UploadAdd JSON, supplied barcode
  сохраняется exact;
- panic/commit error rollback-ит связанные state changes, external I/O внутри
  UoW отсутствует;
- invalid/missing live config fail-fast, secrets не выводятся;
- ни один WB mutation ещё недоступен.

### Stage 1. `transfer` operation foundation

- domain/statuses;
- named mutation cohort и seller-bound target snapshot;
- один startup authenticated probe всех configured cabinets до запуска Telegram;
- idempotent `Start`;
- polling finalized batches каждые 3 секунды;
- одна atomic initialization без chunk/checkpoint table;
- overflow-safe transfer capacity gate;
- atomic activation;
- operation query skeleton.

Exit:

- concurrent Start создаёт один transfer;
- existing-first не читает новый cohort;
- `transfer.Start` не делает WB read и копирует immutable startup snapshot;
- token rotation того же seller допустима, CabinetID rebinding к другому seller
  fail-closed;
- immutable client generation закрывает verify→post-commit-call credential race;
- после rollback/restart polling повторяет initialization целиком, committed
  initialization становится no-op;
- после commit нет частично созданной matrix;
- activation имеет exact stable `groups×targets` и `items×targets` cardinality;
- composite FKs отклоняют cross-transfer/group membership references;
- capacity overflow/exceed не создаёт transfer или matrix rows;
- checksum mismatch не создаёт downstream requests.

### Stage 2. `cardprepare` read-only pipeline

- global preparation;
- feature-owned WB transport returning raw catalog DTOs;
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

### Stage 3. Result model и statistics foundation

- заменить смешанный `transfers.status` на `phase/outcome/attention_code`;
- определить explicit `started_at/finished_at`;
- сделать `transfer_group_targets` небольшой current stage projection;
- сделать `transfer_item_targets` authoritative current item×target projection;
- добавить common `publication_plans/actions/action_members/attempts` schema;
- определить bounded `OutcomeClass/OutcomeCode`;
- добавить read-only statistics views без summary tables;
- удалить из плана/schema dispatch slots и отдельные media attempt tables.

Exit:

- phase никогда не содержит terminal outcome;
- manual review выражается `unresolved + attention_code`;
- item result имеет explainable source action/read-only code;
- product/media используют один action/attempt schema;
- ни одного WB mutation call нет.

### Stage 4. `cardpublication` identity, preflight и immutable plan

- product identities;
- sequential per-cabinet mutation lane;
- normal/trash pagination и observations;
- existing-card decision table;
- bounded request packing;
- exact request bytes/member sets;
- ordered photo URL set и stable link-set root без fetch;
- immutable `PublicationPlan/PlanDigest`;
- product и potential media actions;
- attribution evidence model.

Статус: planning slice реализован. Stage 6–7 dispatch использует sequential
mutation lane: один polling loop и feature-level mutex последовательно выполняют
WB calls; до возврата runtime/singleton coordination запуск второго application
instance запрещён.

Exit:

- repeated preflight даёт deterministic plan/action keys;
- existing cards untouched;
- create/add/already/conflicts покрыты;
- group claims/followers/dispatch slots отсутствуют;
- zero WB mutations.

### Stage 5. Simple live authorization, Error feed и journal foundation

Статус: реализован. Этап намеренно заканчивается до
begin-attempt transaction и не выполняет WB mutations.

- одна authorization row на exact `PlanDigest`;
- trusted actor, TTL, revoke/supersede/close;
- `LiveAuthorizationVerifier` с shared DBTX/row lock;
- Error List cursor/overlap/dedup и baseline;
- common immutable action/member repository и attempt schema; единый
  begin-attempt repository command создаётся в Stage 6 вместе с targeted
  recheck, authorization lock, baseline и identity binding, чтобы не
  появился опасный partial API;
- `UNIQUE(publication_attempts.action_id)`;
- authorization/error-batch statistics facts и baseline dimensions в attempt
  facts;
- zero mutation calls.

Exit:

- stale error не коррелируется;
- duplicate pages idempotent;
- ENV live сам не authorizes plan;
- approve/revoke/expiry ↔ create-attempt race сериализован;
- reused command key с другим actor/payload конфликтует;
- ни один attempt ещё не переводится в `dispatching`.

### Stage 6. Product dispatch и reconciliation

Статус: кодовый slice реализован, включая manual evidence resolver без retry.
Осталась ручная проверка `000001 up → down → up` на fresh PostgreSQL и сверка
реальных WB envelopes перед production rollout.

- `Upload`/`UploadAdd` feature transport;
- mapping raw WB envelope/`additionalErrors` на bounded dispositions;
- authorization-bound attempt commit и один post-commit call;
- identity row lock и active action binding;
- targeted pre-dispatch recheck;
- accepted/uncertain/rejected reconciliation;
- unique vendor-code attribution/fail-closed evidence boundary;
- member/item current projection;
- manual evidence resolver, append-only command journal и explicit attention
  close без retry.

Exit:

- `200` не означает created;
- `ResponseMeta.Error=true` не означает accepted;
- UnknownDelivery не retry-ится;
- каждый real call имеет ровно один ActionID/AttemptID/AuthorizationID;
- matching unique card получает persisted `nmID`, а конфликтующее evidence —
  unresolved attention и zero media call;
- outcomes возвращаются transfer typed service command-ом.

### Stage 7. Common media action dispatch (capability-gated)

Статус: кодовый slice реализован и подключён к polling. Default capability gate
включён; WB call возможен только при persisted unique vendor-code attribution и
exact live authorization. Значение `false` остаётся kill gate.

- `upload_media` action использует common plan/action/member/attempt journal;
- authorization/attribution/member/`nmID` verification;
- direct passthrough original photo URLs без download/validation/storage;
- exact final request payload/digest + AttributionID в common attempt;
- один `SaveMediaByLinks` и bounded terminal response classification;
- `200/error=false` сохраняется как `MEDIA_REQUEST_ACCEPTED`, а не как ложное
  доказательство фактической загрузки файлов;
- media group и transfer projection reducer;
- restart после attempt commit даёт unknown delivery без retry.

Exit:

- `CARDPUBLICATION_MEDIA_AUTO_DISPATCH=true` по умолчанию;
- existing, rejected, unresolved и неоднозначные cards media не получают;
- revoked/expired authorization даёт zero call;
- common `UNIQUE(action_id)` запрещает второй media call;
- отсутствие vendor-code attribution сохраняется как объяснимый skipped result.

### Stage 8. Statistics, Telegram и notifications

Статус на 2026-08-24: **отложен**. Не приступать без отдельного решения о
возврате к Stage 8. Он не входит в оценку 95% готовности transfer flow из
раздела 1.1. SQL fact views уже существуют как foundation, а feature package
отсутствует. Manual resolver доступен как service `cardpublication`; Telegram
presentation и callback wiring его команд остаются частью этого этапа.

- current operation/group/item queries;
- transfer/item/action/attempt aggregates;
- progress, grouped `OutcomeClass/OutcomeCode` и attention details;
- duration только по `started_at/finished_at`;
- drill-down до ActionID/AttemptID;
- notification presentation из persisted projections;
- callback revision/idempotency/permissions;
- composition-root wiring.

Exit:

- statistics не вызывает WB и не пишет business facts;
- ни один aggregate не зависит от raw logs/error text;
- counters сходятся с base rows;
- каждая статистическая item/action строка объяснима source fact-ом;
- stale callbacks безопасны.


### Stage 9. Rollout

1. Зафиксировать dashboards, alerts, capacity limits и recovery runbook.
2. Deploy pre-release `000001` schema/binary с `TRANSFER_MODE=disabled`.
3. Проверить safe-read operation вручную.
4. Запустить internal `dry-run` batch.
5. Сверить seller-bound targets/groups/proposals/media link sets.
6. В canary-configured deployment (или отдельном DB environment) явно
   авторизовать live operation; второй process к той же DB не запускать.
7. Проверить create/add/error/conflict/restart и media после unique vendor-code
   attribution.
8. Включить заранее утверждённый полный production cohort.

## 15. Reviewable PR sequence

1. `core/uow` — единая transaction boundary внутри существующего core.
2. `cardimport/finalize-immutable-batch` — закрывает `CARDIMPORT-DONE` и optional
   barcode contract.
3. `wb/seller-bound-cohorts-and-safe-errors` — один startup SellerKey/capability
   probe и structured mutation envelopes.
4. `transfer/operation-start-capacity`.
5. `transfer/polling-atomic-initialization-and-group-targets`.
6. `cardprepare/catalog-limits-and-dry-run`.
7. `transfer/phase-outcome-item-projection`.
8. `cardpublication/common-plan-action-attempt-schema`.
9. `cardpublication/identity-preflight-attribution-model`.
10. `transfer/simple-plan-authorization`.
11. `cardpublication/errorfeed-common-journal`.
12. `cardpublication/product-dispatch-reconcile`.
13. `cardpublication/common-media-action`.
14. `statistics/fact-views-queries-telegram`.
15. `transfer/rollout-hardening`.

Каждый PR сохраняет mutations disabled, пока не закрыты все предыдущие safety
gates.

Все schema changes этих PR до первого release синхронно обновляют только
`000001_init.up.sql` и `000001_init.down.sql`; отдельный numbered migration не
создаётся.

## 16. Ограничения на структуру и проверки

- Новые автоматические тесты в рамках этого плана не создаются.
- Все `*_test.go` из `internal/core/transport/wb` удаляются; новые тестовые
  файлы в замороженном WB Core запрещены.
- В корне каждого `internal/feature/<module>` разрешён только `feature.go` и
  каталоги `server/`, `service/`, `repository/`, `transport/`. `server/` содержит
  только lifecycle drivers вроде polling и не содержит business rules.
  Environment config adapters находятся в transport; delivery/event adapters в
  текущем плане не создаются. Отдельные root-level handler/config/worker/test
  files запрещены.
- HTTP, Telegram и WB adapters хранятся в `transport/`.
- Следующий список является ручным checklist инвариантов, а не планом создания
  test files.

### 16.1. Core contracts — ручная проверка

- stale revision;
- repository method внутри caller-owned DBTX;
- связанные owner-state changes rollback-ятся вместе;
- ручная проверка schema подтверждает отсутствие outbox/runtime tables, process
  epoch и generic jobs/inbox tables.

### 16.2. Cardimport gate

- duplicate/concurrent Finalize;
- deterministic global order across multiple files;
- ручная проверка schema/normalization/checksum;
- checksum excludes itself/audit/timestamps and detects ordered frozen-row change;
- source location и source-group key survive batch freeze;
- stale callback revision and actor spoof from payload rejected;
- exact finalized command replay returns same batch; stale revision or reused key
  with changed payload conflicts;
- чужой actor с известным finalized SessionID не получает BatchHeader;
- SaveParsed vs Finalize lock ordering yields either included rows or typed
  finalized conflict, never post-finalize mutation;
- fault after every Finalize step rolls back batch/session together;
- finalized session cannot be reparsed and BatchReader never reads mutable rows;
- DB rejects application-role UPDATE/DELETE of frozen batch rows;
- missing barcode accepted and `skus` omitted in exact Upload/UploadAdd JSON;
- supplied barcodes preserved and duplicates rejected inside batch.

### 16.3. Transfer

- duplicate/concurrent Start;
- empty/changed cohort;
- polling не создаёт второй transfer для уже обработанного batch;
- transient failure/restart повторяет всю initialization после rollback;
- committed initialization является no-op и не дублирует matrix rows;
- exact stable `groups×targets` and `items×targets` cardinality;
- DB rejects cross-transfer group/target/item membership FKs;
- capacity limit/exact-boundary/integer-overflow;
- count/checksum mismatch;
- idempotent result application;
- phase/outcome/attention reducer не смешивает этап и terminal result;
- duration использует started/finished timestamps, не updated_at;
- every item-target result имеет bounded class/code и explainable source;
- seller binding rotation/rebinding;
- SellerKey стабилен across restart/same-seller token rotation; unchanged
  capabilities не меняют binding/capability revisions;
- startup fails before Telegram on invalid/read-only/expired cabinet or duplicate
  SellerKey; later `transfer.Start` performs zero WB probes;
- canary/production cohort separation;
- authorization request/idempotency/expiry/revoke/supersede и stale PlanDigest;
- concurrent approve/revoke/attempt locking and trusted actor context;
- reused authorization idempotency key with changed payload/actor conflicts;
- old callback cannot approve a different authorization ID/revision/PlanDigest;
- projection result revision не инвалидирует approved PlanDigest;
- expiry/revoke запрещает not-begun actions, но не повторяет begun ActionID;
- stale plan после первого begun action не создаёт partial replan;
- ENV dry-run→live does not authorize existing operation.

### 16.4. Cardprepare

- category/subject ambiguity;
- required/type/unit/maxCount;
- directory/brand validation;
- cache isolation by CabinetID;
- ручная сверка exact payload/digest;
- serialization bounds;
- Cards Limits exceeded и ровно один read на target preparation.

### 16.5. WB Core

- stable versioned SellerKey across restart/token rotation;
- credential claims/capability/read-only/expiry decode без remote workflow;
- feature transport получает raw response DTO через generic executor;
- bounded non-2xx/malformed/oversized response classification;
- raw body, token, sid и arbitrary error text отсутствуют в logs/business rows.

### 16.6. Cardpublication

- all absent/all present/mixed existing-card decisions;
- compatible/different existing cards remain untouched with bounded diff;
- trash/different imtID/subject/capacity conflicts;
- sequential per-cabinet lane и identity row-lock policy;
- second transfer при pending/uncertain identity выполняет zero WB calls;
- deterministic PlanDigest/ActionKey/member order/request bytes;
- common action/member/attempt schema используется для create/add/media;
- DB unique запрещает второй attempt одного ActionID;
- Error List overlap/dedup/baseline;
- request packing boundaries;
- HTTP 200 с `error=true` не считается accepted;
- bounded HTTP 400 `additionalErrors` per-member mapping;
- malformed/unknown response после dispatch не разрешает retry;
- accepted/rejected/partial/unknown/late evidence сохраняют bounded outcomes;
- каждый attempt хранит ActionID, authorization ID и request digest;
- external writer between targeted recheck and observation остаётся принятым
  v1 risk unique-vendor policy; binding mismatch/conflict даёт attention;
- targeted recheck before attempt detects create↔add/already/trash/capacity change;
- stale plan до первого begun action supersedes whole plan с zero call;
- stale plan после begun action не пересобирает begun/unknown actions;
- manual resolution accepts only matching persisted evidence and cannot retry;
- supplied barcode remote rejection is not blindly retried;
- no unique vendor-code attribution means media action is skipped/not
  dispatchable;
- original photo URL order is passed to WB unchanged;
- media action performs zero preliminary HTTP fetches and has no local storage;
- existing, rejected, unresolved и ambiguous cards never receive media;
- revoked/expired/wrong-plan authorization never creates attempt;
- crash после product/media dispatch не создаёт второй attempt.

### 16.7. Statistics

- current transfer/group/item rows сходятся с base projection rows;
- historical action/attempt aggregates сходятся с durable facts и immutable
  attempt evidence;
- OutcomeClass и OutcomeCode агрегируются отдельно;
- raw error text/request payload отсутствуют в statistics views;
- duration использует только started_at/finished_at;
- cabinet dimension использует frozen target snapshot;
- drill-down ведёт от TransferID до SourceActionID/AttemptID;
- reconciliation correction не переписывает исходную attempt evidence;
- statistics repository выполняет только SELECT и не вызывает WB.

### 16.8. Composition/E2E

- pinned client generation survives rotation without seller/client TOCTOU;
- graceful shutdown;
- DB loss fail-closed;
- ручной fresh database цикл `000001 up → down → up`;
- ручная проверка schema подтверждает отсутствие legacy `card_import_handoffs` и
  `card_batches.authorization`;
- one/multiple cabinets;
- dry-run → explicit live authorization;
- cardimport Finalize не вызывает transfer; трёхсекундный polling видит новый
  finalized batch и идемпотентно запускает его;
- deployment/runbook явно запрещает второй application instance до возврата к
  singleton/runtime design.

Проверка change set ограничивается сборкой, форматированием и ручным прохождением
релевантных инвариантов. Новые test files, PostgreSQL integration tests и test
fixtures не создаются.

## 17. Definition of Done

Текущий DoD относится к Stages 0–7. Пункты `statistics` вынесены ниже и не
блокируют текущую итерацию; production rollout по-прежнему требует ручных
проверок из §1.1.

- Modules имеют однозначное владение tables/state machines.
- Ни один module не изменяет таблицы другого module напрямую.
- Межмодульные прямые команды имеют явный typed contract и не меняют чужие
  repositories.
- Transfer остаётся небольшим orchestrator-ом.
- Cardprepare не имеет mutation imports/calls.
- Только `cardpublication` вызывает product и media mutations:
  `Upload`, `UploadAdd`, `SaveMediaByLinks`.
- Unknown mutation outcome не получает автоматический повтор.
- Каждый mutation attempt связан с exact non-revoked-at-commit live
  authorization, PlanDigest, ActionID и seller binding.
- Каждый publication action имеет максимум один committed attempt независимо от
  expiry/revoke/reauthorization; product и media используют один journal.
- New remote card получает media только после targeted pre-attempt absence и
  exact post-attempt match по `CabinetID + vendorCode + subjectID/imtID`.
- Одна matching created observation сохраняется как
  `CREATED_CONFIRMED_BY_VENDOR_CODE`; automatic media dispatch включён по
  умолчанию и всё равно требует live authorization.
- Empty barcode omits `skus`; supplied barcode сохраняется и отправляется exact.
- `CARDIMPORT-DONE` закрыт immutable batch.
- `transfer_group_targets` материализован с exact group×target cardinality.
- `transfers` хранит phase/outcome/attention раздельно.
- `transfer_group_targets` и `transfer_item_targets` являются только current
  projections и не дублируют raw mutation journal.
- Связанные owner-state changes используют caller-owned transaction владельца.
- Fresh `000001 up/down/up` проходит без `000002`.
- Every item × target имеет persisted explainable result.
- Recovery nonterminal workflows и защита от второго instance явно отложены до
  отдельного решения после `transfer` и `statistics`; до него live rollout
  запрещён.
- Existing cards и media остаются untouched.
- Dry-run и canary gates пройдены до production live.

### 17.1. Отложенный DoD Stage 8

- `statistics` не вызывает WB и не изменяет business facts.
- `statistics` читает current projections и immutable action/attempt facts без
  manually maintained counters, raw logs и WB calls.
- Telegram statistics, notifications и manual-resolution callbacks читают
  только typed service/query contracts и корректно отклоняют stale callbacks.
