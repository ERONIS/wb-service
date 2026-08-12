# Архивный план card features

> **Статус: superseded.** Канонический объединённый план:
> [`wb-cards-transfer-master-plan.md`](./wb-cards-transfer-master-plan.md).
> Этот файл сохранён только для истории и traceability.

# Master-план `cardimport`, `transfer` и live `statistics`

## 1. Статус документа

Этот документ является целевым планом реализации следующих независимых features:

```text
internal/feature/cardimport
internal/feature/transfer
internal/feature/edit
internal/feature/statistics
```

Дата архитектурного решения:

```text
2026-08-03
```

Обязательный prerequisite — готовый WB Core, описанный в
[`wb-core-architecture.md`](./wb-core-architecture.md).

Feature должна опираться на публичный typed API:

```go
clientset.Cabinets()
clientset.ForCabinet(id)
cabinet.ContentV1()
```

---

## 2. Цель

Построить полный пользовательский процесс:

```text
выбор transfer/edit
→ загрузка нескольких XLSX
→ немедленная валидация
→ кнопка «Готово»
→ immutable batch
→ transfer/edit
→ текущий статус операции
```

Первая реализуемая ветка после `cardimport` — `transfer`.

`edit` получает только архитектурную точку расширения. Полное редактирование карточек пока не реализуется.

---

## 3. Текущее состояние

В репозитории уже существуют:

- модель карточки в [`internal/core/domain/cards.go`](/home/Eronis/IT/wb-service/internal/core/domain/cards.go);
- однофайловый [`CardImport`](/home/Eronis/IT/wb-service/internal/core/domain/card_import.go);
- XLSX-парсер в [`internal/feature/cards/transport/xlsx`](/home/Eronis/IT/wb-service/internal/feature/cards/transport/xlsx);
- Telegram-заготовка в [`internal/feature/cards/transport/telegram/transport.go`](/home/Eronis/IT/wb-service/internal/feature/cards/transport/telegram/transport.go);
- однофайловая таблица `wb.card_imports`;
- кнопки `Перенос карточек` и `Редактирование карточек`.

Текущие ограничения:

- `CardImport` представляет один файл, а не сессию из нескольких файлов;
- purpose хранится в process-local `SelectedPurpose map`;
- состояние теряется после рестарта;
- нет service и repository реализации `cards`;
- Telegram callbacks и document handler не зарегистрированы;
- `parseInt` и `parseFloat` молча превращают ошибки в `0`;
- строки без `vendorCode` молча пропускаются;
- конфликты одинаковых карточек не диагностируются;
- дубли проверяются только внутри одного файла;
- нет immutable batch;
- нет transfer;
- нет live statistics;
- нет автоматических тестов.

Текущий baseline:

```text
go test ./cmd/... ./internal/...  — проходит
go vet ./cmd/... ./internal/...   — проходит
git diff --check                  — проходит
```

`go test ./...` сейчас упирается в недоступный `out/pgdata`; это необходимо устранить до финального gate.

---

## 4. Граница плана

### 4.1. Входит

- выделение `cardimport` из текущей `cards`;
- выбор `transfer|edit`;
- одна активная сессия загрузки на пользователя;
- загрузка нескольких XLSX;
- немедленная обработка каждого файла;
- строгая XLSX-валидация;
- проверка общего набора между файлами;
- одно Telegram-окно приёма;
- кнопки `Готово` и `Отмена`;
- immutable batch;
- надёжная передача `batchID`;
- transfer во все валидные кабинеты WB;
- подготовка WB DTO;
- создание карточек;
- reconciliation асинхронного результата;
- получение `nmID`;
- медиа по ссылкам;
- группировка карточек;
- результаты для каждой пары `карточка × кабинет`;
- partial failure;
- bounded workers;
- восстановление после рестарта;
- live statistics текущих transfer/edit;
- Telegram-уведомление о финальном результате;
- контракты для будущего edit;
- migrations, tests, wiring и документация.

### 4.2. Не входит

- завершение `core/transport/wb`;
- ручной выбор кабинетов;
- привязка кабинетов к пользователям;
- удаление отдельного XLSX;
- передача невалидных карточек;
- изменение purpose открытой сессии;
- отмена уже запущенного transfer;
- rollback созданных в WB карточек;
- удаление карточек из исходного кабинета;
- чтение карточек из исходного кабинета через API;
- полная реализация edit;
- edit-specific поля и patch mask;
- историческая статистика;
- фильтры статистики;
- отчёты за день/неделю/месяц;
- статистика продаж, заказов и остатков WB;
- отдельный prices/discounts API;
- multipart `/content/v3/media/file`;
- самостоятельный retry или rate limiting поверх WB core.

---

## 5. Зафиксированные решения

### 5.1. Features одного уровня

```text
internal/feature/
├── cardimport/
├── transfer/
├── edit/
└── statistics/
```

Вложенных features внутри `cards` не будет.

### 5.2. Окно приёма принадлежит `cardimport`

`cardimport` отвечает за:

- выбор действия;
- приём файлов;
- количество карточек;
- процесс валидации;
- ошибки валидации;
- `Готово`;
- `Отмена`.

Это состояние не отображается в `statistics`.

### 5.3. Валидация выполняется при загрузке

Каждый XLSX немедленно:

1. резервируется в сессии;
2. скачивается;
3. разбирается;
4. валидируется;
5. объединяется с предыдущими файлами;
6. запускает общую межфайловую проверку;
7. обновляет Telegram-окно.

`Готово` ничего повторно не валидирует.

### 5.4. Любая ошибка блокирует `Готово`

Если хотя бы одна карточка или один файл содержит blocking error:

```text
Готово недоступно
```

Поскольку удаления отдельного файла нет, пользователь нажимает `Отмена`, исправляет XLSX и начинает новую сессию.

### 5.5. Передача только через `batchID`

Между features нельзя передавать `[]Card` в памяти.

```text
cardimport → batchID → transfer/edit
```

Batch неизменяем после создания.

### 5.6. Transfer работает со всеми кабинетами

Ручного выбора кабинетов нет.

Под «всеми кабинетами» понимается snapshot результата:

```go
wbClient.Cabinets()
```

Это все валидные ENV-кабинеты глобального WB registry. Rejected кабинеты в этот список не входят.

### 5.7. Transfer является копированием

Transfer не использует `/cards/moveNm` для переноса между кабинетами.

```text
XLSX → подготовка → создание новой карточки в каждом кабинете
```

Исходная карточка не удаляется.

### 5.8. Statistics показывает только текущие операции

```text
transfer ──┐
           ├──→ statistics
edit ──────┘
```

`cardimport` не является источником statistics.

Истории, периодов и фильтров нет.

### 5.9. Один WB Client на процесс

Composition root создаёт один общий `*wb.Client`.

Запрещается создавать отдельный client:

- для transfer;
- для worker;
- для кабинета;
- для одной карточки.

Иначе будут разделены limiter и HTTP connection pool.

---

## 6. Целевая схема

```text
Telegram
   |
   v
cardimport
   |
   | immutable batchID
   v
handoff/outbox
   |
   +------------------+
   |                  |
   v                  v
transfer             edit
   |                  |
   |                  └─ future
   |
   v
snapshot Client.Cabinets()
   |
   v
карточка × кабинет
   |
   v
prepare → upload → reconcile → media → done/error
   |
   v
transfer current read model
   |
   v
statistics
```

---

## 7. Направление зависимостей

```text
cardimport/xlsx       → cardimport/domain
cardimport/telegram   → cardimport/service
cardimport/postgres   → cardimport/domain

transfer/service      → cardimport BatchReader interface
transfer/wb adapter   → core/transport/wb
transfer/postgres     → transfer/domain

statistics/service    → statistics CurrentOperationsSource
statistics/transfer adapter → transfer/service

edit                  → cardimport BatchReader, в будущем

cmd/wb-service        → все concrete features и adapters
```

`cardimport` не импортирует concrete `transfer`, `edit` или `statistics`.

`transfer` не импортирует `statistics`.

`statistics` получает transfer через adapter и не читает transport-логи WB.

---

# Feature `cardimport`

## 8. Ответственность `cardimport`

Feature должна:

1. создать сессию после выбора purpose;
2. принять несколько XLSX;
3. проверить каждый файл;
4. сохранить структурированные ошибки;
5. пересобрать общий набор карточек;
6. определить готовность сессии;
7. сформировать immutable batch;
8. надёжно передать `batchID`.

Feature не должна:

- выбирать кабинеты;
- создавать карточки в WB;
- выполнять WB-dependent validation;
- отображаться в statistics;
- хранить transfer state.

`Готово` означает, что пройдена XLSX- и общая WB-независимая валидация. WB-dependent проверка предметов, характеристик и существующих карточек выполняется уже внутри transfer.

---

## 9. Пользовательский flow `cardimport`

```text
📂 Карточки
→ 📤 Перенос карточек / ✏️ Редактирование карточек
→ создать collecting session
→ принять XLSX №1
→ валидировать
→ принять XLSX №2
→ валидировать и проверить общий набор
→ ...
→ все файлы terminal
→ cards > 0
→ errors = 0
→ показать «✅ Готово»
→ создать batch
→ отправить batchID consumer-у
```

У пользователя может быть только одна незавершённая сессия.

Если пользователь повторно нажал то же действие, открывается существующее окно.

Если выбрано другое действие при открытой сессии, сервис просит сначала нажать `Отмена`.

До реализации edit кнопка редактирования может отображаться, но должна отвечать:

```text
Редактирование карточек пока недоступно
```

Она не должна создавать недоставляемый batch.

---

## 10. Telegram-окно приёма

Во время проверки:

```text
📥 Приём карточек

Действие: Перенос
Карточек отправлено: 127
Проверяется: 24
Ошибок валидации: 0

⏳ Выполняется проверка...

[❌ Отмена]
```

При ошибках:

```text
📥 Приём карточек

Действие: Перенос
Карточек отправлено: 127
Ошибок валидации: 2

❌ cards-2.xlsx · строка 14 · Цена:
ожидается положительное целое число

❌ cards-3.xlsx · строка 37 · ABC-100:
поле «Бренд» конфликтует с cards-1.xlsx

[❌ Отмена]
```

После успешной проверки:

```text
✅ Все карточки провалидированы

Действие: Перенос
Карточек отправлено: 127
Ошибок валидации: 0

[✅ Готово]
[❌ Отмена]
```

Правила:

- кнопки удаления файла нет;
- ошибки находятся в том же сообщении;
- показывается точное общее количество ошибок;
- из-за Telegram limit отображаются первые N ошибок и `…ещё K`;
- filenames, sheets, vendor codes и значения HTML-escape-ятся;
- stale callback не может завершить изменившуюся сессию;
- `message is not modified` считается успешным idempotent результатом.

---

## 11. State machines `cardimport`

### 11.1. Session

```text
collecting ──Finalize──> ready ──handoff──> dispatched
     |
     └──Cancel──> cancelled
```

Cancel разрешён только до `Finalize`.

### 11.2. File

```text
received → validating → valid
                     └→ invalid

received/validating ──Cancel──> abandoned
```

### 11.3. Readiness predicate

`Готово` отображается и принимается только если:

```text
session.status == collecting
files_count > 0
inflight_files_count == 0
cards_count > 0
blocking_issues_count == 0
все файлы находятся в valid
все aggregate items валидны
```

Predicate проверяется не только Telegram renderer-ом, но и внутри `Finalize` под DB lock.

---

## 12. XLSX pipeline

Для каждого файла:

1. Проверить активную сессию и владельца.
2. Проверить лимиты.
3. Идемпотентно зарезервировать Telegram-файл.
4. Скрыть `Готово`.
5. Проверить extension, MIME, размер и XLSX signature.
6. Ограниченно скачать файл.
7. Открыть workbook.
8. Прочитать поддерживаемый worksheet.
9. Найти header row.
10. Проверить обязательные и дублирующиеся headers.
11. Разобрать строки с координатами.
12. Выполнить строгую типизацию.
13. Объединить размеры внутри файла.
14. Сохранить parsed result.
15. Пересобрать aggregate всех файлов.
16. Проверить межфайловые конфликты.
17. Пересчитать counters.
18. Обновить Telegram-окно.

DB-транзакция не удерживается во время скачивания и parsing.

---

## 13. Правила валидации

Минимальные группы ошибок:

- unsupported file;
- file too large;
- corrupt workbook;
- empty workbook;
- worksheet missing;
- header row missing;
- required header missing;
- duplicate header;
- required value missing;
- invalid integer;
- invalid decimal;
- invalid boolean;
- value out of range;
- invalid media URL;
- missing vendor code;
- conflicting card field;
- conflicting characteristic;
- duplicate barcode;
- files/cards/rows limit exceeded;
- too many validation issues;
- download/processing interrupted.

`parseInt`, `parseFloat` и `parseBool` должны возвращать typed error. Нельзя молча заменять ошибку на `0` или `false`.

Строка без `vendorCode` больше не пропускается — создаётся blocking issue.

---

## 14. Объединение нескольких XLSX

Пользовательский счётчик карточек — количество уникальных нормализованных `vendorCode`.

Строки размеров одной карточки не увеличивают счётчик.

Для одинакового `vendorCode`:

- одинаковые scalar values совместимы;
- непустое значение может дополнить пустое;
- два разных непустых значения создают conflict;
- размеры объединяются с dedup;
- медиа объединяются со стабильным порядком;
- одинаковые характеристики deduplicate;
- разные значения одной характеристики создают conflict;
- ошибка содержит обе source locations.

Одинаковый barcode у разных карточек является blocking error.

Aggregate строится в стабильном порядке:

```text
file ID → worksheet → row
```

Результат не должен зависеть от того, какой handler завершился раньше.

---

## 15. Structured validation issues

```go
type ValidationIssue struct {
	Code       IssueCode
	Scope      IssueScope
	FileID     int64
	Filename   string
	Sheet      string
	Row        int
	Column     int
	Field      string
	VendorCode string
	Details    map[string]string
	Blocking   bool
}
```

Основой является `Code + Details`, а не готовая произвольная строка.

Это необходимо для:

- детерминированных тестов;
- безопасного Telegram rendering;
- будущей локализации;
- группировки одинаковых ошибок;
- ограничения раскрываемых данных.

В первой версии все validation issues являются blocking.

---

## 16. Immutable batch

`Finalize`:

1. блокирует session row;
2. проверяет `expectedRevision`;
3. проверяет readiness predicate;
4. создаёт `card_batches`;
5. копирует aggregate в `card_batch_items`;
6. вычисляет checksum;
7. создаёт outbox handoff;
8. переводит session в `ready`;
9. commit-ит всё одной транзакцией.

Новые файлы после этого отклоняются.

Повторный `Finalize` возвращает тот же `batchID`.

Batch repository не предоставляет update/delete методов.

---

## 17. Контракты `cardimport`

```go
type BatchID int64
type SessionID int64

type Service interface {
	Begin(
		ctx context.Context,
		telegramID int64,
		purpose Purpose,
		window WindowRef,
	) (SessionView, error)

	ReserveFile(
		ctx context.Context,
		telegramID int64,
		sessionID SessionID,
		file FileMeta,
	) (FileTicket, SessionView, error)

	ProcessFile(
		ctx context.Context,
		ticket FileTicket,
		reader io.Reader,
	) (SessionView, error)

	Finalize(
		ctx context.Context,
		telegramID int64,
		sessionID SessionID,
		expectedRevision int64,
	) (BatchID, error)

	Cancel(
		ctx context.Context,
		telegramID int64,
		sessionID SessionID,
	) error
}
```

Consumer-facing:

```go
type BatchReader interface {
	GetBatch(
		ctx context.Context,
		batchID BatchID,
	) (BatchHeader, error)

	ListBatchItems(
		ctx context.Context,
		batchID BatchID,
		afterPosition int,
		limit int,
	) ([]BatchItem, error)
}
```

```go
type BatchConsumer interface {
	Start(
		ctx context.Context,
		batchID BatchID,
	) error
}
```

`Start(batchID)` обязан быть идемпотентным.

---

## 18. Надёжная передача batch

Передача реализуется transactional outbox.

```text
Finalize transaction
→ batch
→ batch items
→ outbox pending
→ session ready
→ commit
```

Отдельный dispatcher:

```text
claim pending handoff
→ выбрать consumer по purpose
→ Start(batchID)
→ отметить delivered
→ session dispatched
```

Delivery — at-least-once.

Consumer создаёт operation с:

```text
UNIQUE(batch_id)
```

Поэтому повторная доставка не создаёт второй transfer.

До появления полноценного edit его handler должен возвращать typed `FeatureUnavailable`, а Telegram не должен разрешать начать edit session.

---

## 19. PostgreSQL `cardimport`

Новая forward migration создаёт:

### `wb.card_import_sessions`

- author;
- purpose;
- status;
- Telegram chat/message;
- revision;
- counters;
- timestamps.

Partial unique index запрещает две активные сессии пользователя.

### `wb.card_import_files`

- session ID;
- Telegram file/file_unique/message IDs;
- filename/MIME/size;
- status;
- rows/cards/issues counters;
- attempts;
- timestamps.

Unique:

```text
(session_id, telegram_file_unique_id)
(session_id, telegram_message_id)
```

### `wb.card_import_file_cards`

- parsed cards конкретного файла;
- normalized vendor code;
- payload JSONB;
- source locations;
- stable ordinal;
- schema version.

### `wb.card_import_items`

- mutable aggregate collecting session;
- vendor code;
- normalized payload;
- stable position;
- validation status.

Unique:

```text
(session_id, vendor_code)
```

### `wb.card_import_issues`

- scope/code;
- file/card reference;
- row/column/field;
- structured details;
- blocking flag.

### `wb.card_batches`

- source session;
- author;
- purpose;
- schema version;
- cards count;
- checksum;
- creation time.

### `wb.card_batch_items`

- batch;
- stable position;
- vendor code;
- immutable payload.

### `wb.card_import_handoffs`

- batch;
- purpose;
- pending/processing/retry_wait/delivered;
- attempt count;
- lease;
- next attempt;
- safe error code.

Существующая `wb.card_imports` не переписывается задним числом. Новая модель вводится forward migration. Старую таблицу можно удалить только отдельной миграцией после подтверждения отсутствия consumers и нужных данных.

---

# Feature `transfer`

## 20. Ответственность `transfer`

`transfer` получает готовый `batchID` и:

1. идемпотентно создаёт Transfer;
2. читает immutable cards постранично;
3. получает snapshot всех валидных кабинетов;
4. создаёт задачи `карточка × кабинет`;
5. выполняет WB-dependent preparation;
6. создаёт bounded upload batches;
7. отправляет карточки;
8. выполняет reconciliation;
9. получает `nmID`;
10. сохраняет медиа;
11. рассчитывает общий результат;
12. отправляет финальное Telegram-сообщение;
13. предоставляет current read model для statistics.

`transfer` не читает XLSX и не изменяет batch.

---

## 21. Значение «все кабинеты»

В начале transfer:

```go
cabinetSnapshot := wbClient.Cabinets()
```

Snapshot содержит только valid entries immutable WB registry.

Для каждого кабинета сохраняется только:

```text
CabinetID
порядок в snapshot
```

Token, `sid`, auth headers и credentials feature не получает.

Добавление, удаление или переименование ENV-кабинета после рестарта не изменяет уже созданную transfer operation.

Если snapshot пуст, transfer завершается с `no_available_cabinets`. При выполненном WB core это возможно только при ошибке wiring, потому что zero-valid registry должен блокировать startup.

---

## 22. Transfer state machines

### 22.1. Operation

```text
created
→ preparing
→ queued
→ running
→ completed | partial | failed
```

Результаты:

- `completed` — все пары `card × cabinet` завершились успешно либо `already_present`;
- `partial` — существует хотя бы один успех и хотя бы одна ошибка/uncertain;
- `failed` — нет ни одного успешного результата.

### 22.2. Card × cabinet

```text
pending
→ preflight
→ prepared
→ uploading
→ submitted
→ reconciling
→ created
→ media
→ done
```

Дополнительные terminal outcomes:

```text
already_present
failed
uncertain
```

После достижения `uploading` item никогда не возвращается напрямую в `pending`: после restart или неизвестного результата он сначала проходит reconciliation.

---

## 23. Feature-level WB gateway

Business service зависит от собственного интерфейса:

```go
type ContentGateway interface {
	Cabinets() []Cabinet

	ResolveSubject(
		ctx context.Context,
		cabinetID CabinetID,
		category string,
	) (Subject, error)

	GetCharacteristics(
		ctx context.Context,
		cabinetID CabinetID,
		subjectID int,
	) ([]CharacteristicSpec, error)

	FindCards(
		ctx context.Context,
		cabinetID CabinetID,
		vendorCodes []string,
	) (map[string]RemoteCard, error)

	UploadCards(
		ctx context.Context,
		cabinetID CabinetID,
		groups []CreateCardGroup,
	) error

	FindUploadErrors(
		ctx context.Context,
		cabinetID CabinetID,
		vendorCodes []string,
	) (map[string][]UploadViolation, error)

	SaveMediaByLinks(
		ctx context.Context,
		cabinetID CabinetID,
		nmID int64,
		links []string,
	) error
}
```

WB adapter реализует его через единственный shared `*wb.Client`:

```text
ForCabinet(id)
→ CabinetClient.DoJSON
→ opaque content operation
```

Используемые core operations:

- `ParentCategoriesOperation`;
- `SubjectsOperation`;
- `SubjectCharacteristicsOperation`;
- `CardsListOperation`;
- `UploadCardsOperation`;
- `CardsErrorListOperation`;
- `SaveMediaByLinksOperation`.

`MoveCardsOperation` не является переносом между кабинетами.

`UpdateCardsOperation` принадлежит будущему edit.

`UploadCardsAddOperation` не нужен для первой версии transfer, если создаются новые группы.

---

## 24. Подготовка карточек

Для каждой карточки transfer:

1. нормализует category;
2. разрешает `category → subjectID`;
3. получает schema характеристик;
4. преобразует raw characteristic names в IDs;
5. приводит значения к ожидаемым типам;
6. проверяет required characteristics;
7. проверяет размеры и barcodes;
8. формирует feature-owned WB DTO;
9. группирует карточки по исходному `GroupID`;
10. сохраняет подготовленный payload или его schema version.

Исходный `GroupID` используется только как логический ключ группировки. Он не передаётся как target `imtID`.

Feature DTO нельзя объединять с `domain.Card` через общие JSON tags. XLSX/domain и WB request DTO должны быть разными структурами.

---

## 25. Existing-card policy

Перед upload transfer вызывает `CardsList` для соответствующих `vendorCode`.

Если карточка уже существует:

- transfer не вызывает update;
- не создаёт дубликат;
- отмечает результат `already_present`;
- edit остаётся отдельной feature.

`already_present` считается идемпотентным успешным no-op.

Это позволяет безопасно повторно обработать batch и не отправлять mutation второй раз.

---

## 26. Grouping и batching

Подготовленные карточки группируются:

```text
cabinet
→ subject
→ source GroupID
→ bounded upload batch
```

Правила:

- одна карточка без GroupID создаётся отдельно;
- карточки одинакового GroupID образуют новую target-группу;
- target `imtID` создаётся WB и не копируется из source;
- лимит variants/groups проверяется до вызова WB;
- batch ограничен operation contract и request body bounds core;
- один request никогда не содержит карточки разных кабинетов;
- один огромный запрос для всех карточек запрещён.

Точный batch size фиксируется на этапе 0 по актуальному operation snapshot и тестируется boundary-тестами.

---

## 27. Upload и mutation uncertainty

HTTP `2xx` от `UploadCards` означает только, что запрос принят.

После любого принятого upload:

```text
submitted → reconciling
```

Если core возвращает `UncertainOutcomeError`:

- не повторять upload;
- сохранить uncertain delivery state;
- перейти в reconciliation;
- проверить CardsList и CardsErrorList;
- только доказанное отсутствие результата может разрешить новый upload.

Обычный retry, `429`, backoff и rate limiting остаются ответственностью WB core. Transfer не строит второй transport retry loop.

---

## 28. Reconciliation

Для submitted/uncertain batch:

1. запрашивать `CardsList` по vendor codes;
2. искать созданные карточки;
3. сохранять `nmID` и `imtID`;
4. запрашивать `CardsErrorList`;
5. сопоставлять WB validation errors;
6. повторять только safe read polling;
7. завершить по success/error/deadline.

Outcomes:

```text
card found       → created
explicit WB error → failed
deadline reached → uncertain
```

Reconciliation имеет:

- configurable interval;
- configurable overall deadline;
- bounded page size;
- restart-safe persisted state;
- отсутствие busy loop;
- safe normalized errors.

---

## 29. Media

После получения `nmID`:

```text
created → media → done
```

В первой версии поддерживается только:

```text
POST /content/v3/media/save
```

То есть медиа передаются по URL из XLSX.

Multipart `/content/v3/media/file` отсутствует в реализованном WB Core и не
используется.

При `UncertainOutcomeError` media mutation нельзя повторять вслепую. Сначала требуется проверить карточку через `CardsList` и сравнить ожидаемое состояние медиа.

Карточка без media сразу переходит `created → done`.

---

## 30. Цена

Реализованный WB Core ограничен Content API и не предоставляет отдельный
`discounts-prices-api`.

Поэтому:

- отдельная установка цены после создания не входит;
- отдельный `PriceGateway` сейчас не реализуется;
- если актуальный `UploadCards` contract допускает price внутри create DTO, `Card.Price` переносится этим запросом;
- это проверяется на этапе 0 по operation snapshot;
- если contract этого не допускает, price сохраняется в batch, но отдельная отправка требует нового transport-плана.

Transfer не должен создавать ad-hoc HTTP client для другого WB host.

---

## 31. PostgreSQL `transfer`

### `wb.transfers`

- `id`;
- unique `batch_id`;
- author;
- status;
- cards/cabinets counts;
- timestamps;
- safe terminal error code;
- final notification status.

### `wb.transfer_cabinets`

- transfer ID;
- cabinet ID;
- stable ordinal;
- status;
- timestamps.

Unique:

```text
(transfer_id, cabinet_id)
```

### `wb.transfer_items`

- transfer ID;
- batch item ID;
- stable position;
- vendor code;
- group ID;
- prepared payload/schema version;
- preparation status/error.

### `wb.transfer_item_cabinets`

- transfer item;
- cabinet;
- current stage/status;
- `nmID`;
- `imtID`;
- attempt/lease data;
- safe error code/details;
- timestamps.

Unique:

```text
(transfer_item_id, transfer_cabinet_id)
```

### `wb.transfer_upload_batches`

- transfer/cabinet;
- stable batch key;
- status;
- vendor-code membership;
- submitted/reconcile timestamps;
- lease;
- safe outcome.

### `wb.transfer_notifications`

Transactional outbox финальных Telegram-уведомлений. Terminal operation может исчезнуть из live statistics только после надёжного создания финального notification.

Statistics не получает собственные historical tables.

---

## 32. Worker, concurrency и recovery

Используется bounded DB-backed worker pool.

```text
claim work with FOR UPDATE SKIP LOCKED
→ lease
→ process outside transaction
→ conditional commit by lease/version
```

Правила:

- глобальный лимит workers;
- ограниченный in-flight;
- отсутствие goroutine на каждую пару;
- fairness между кабинетами;
- core limiter остаётся единственным rate limiter;
- shutdown прекращает admission новых jobs;
- in-flight context отменяется;
- lease позволяет другому процессу восстановить работу.

Recovery:

- `pending|prepared` можно продолжить;
- `uploading|submitted` всегда идут в reconciliation;
- `media` сначала проверяет фактическое состояние;
- terminal results не выполняются повторно;
- один сбойный кабинет не блокирует остальные.

---

# Feature `statistics`

## 33. Граница `statistics`

Feature показывает только текущие transfer/edit operations.

Она не показывает:

- cardimport sessions;
- завершённую историю;
- периоды;
- фильтры;
- продажи;
- заказы;
- остатки.

`statistics` ничего не изменяет:

- не запускает retry;
- не отменяет transfer;
- не удаляет данные;
- не очищает карточки;
- не вызывает WB.

---

## 34. Current read model

```go
type CurrentOperation struct {
	ID             int64
	Type           OperationType
	Status         OperationStatus
	CardsTotal     int
	CabinetsTotal  int
	Pending        int
	Running        int
	Done           int
	AlreadyPresent int
	Failed         int
	Uncertain      int
	Stages         []StageCount
	Cabinets       []CabinetProgress
	Errors         []CurrentError
}
```

Source interface:

```go
type CurrentOperationsSource interface {
	ListCurrent(
		ctx context.Context,
		viewerID int64,
	) ([]CurrentOperation, error)
}
```

Adapter `statistics/source/transfer` получает данные через transfer service/repository и преобразует их в statistics model.

Transfer не импортирует statistics.

В будущем добавляется `statistics/source/edit`.

---

## 35. Telegram `statistics`

Главное состояние:

```text
📊 Текущие операции

Перенос #42
Карточек: 127
Кабинетов: 60
Всего задач: 7620

🟡 Ожидает: 1200
🔵 В работе: 40
🟢 Готово: 6300
⚪ Уже существовало: 60
🔴 Ошибки: 20

Текущий этап:
• upload: 12
• ожидание nmID: 18
• media: 10

[🔄 Обновить]
[⬅️ Назад]
```

Если операций нет:

```text
📊 Текущие операции

Активных операций нет.

[⬅️ Назад]
```

Правила:

- никаких фильтров;
- никаких периодов;
- cardimport не показывается;
- ошибки безопасно группируются по stage/code;
- длинный список ограничивается Telegram message budget;
- pagination допустима только как техническое ограничение сообщения, но не как фильтр;
- terminal transfer исчезает после подготовки финального уведомления пользователю.

---

# Feature `edit`

## 36. Extension seam

Сейчас фиксируются только:

```go
type BatchConsumer interface {
	Start(ctx context.Context, batchID cardimport.BatchID) error
}
```

И future statistics source.

Не добавляются:

- WB article;
- patch mask;
- absent/empty/value semantics;
- update DTO;
- update worker;
- edit database tables;
- edit validation.

Production wiring не направляет batch в несуществующий edit consumer.

---

## 37. Ошибки

Минимальные feature-level error codes:

### Cardimport

```text
unsupported_file
file_too_large
workbook_invalid
header_missing
required_value_missing
invalid_number
invalid_url
duplicate_conflict
duplicate_barcode
limit_exceeded
processing_interrupted
```

### Transfer

```text
no_available_cabinets
cabinet_unavailable
credentials_rotation_due
read_only_credentials
subject_not_found
characteristic_not_found
required_characteristic_missing
already_present
upload_rejected
upload_uncertain
reconciliation_timeout
card_creation_failed
media_rejected
media_uncertain
internal_state_conflict
```

Feature хранит stable `error_code` и безопасные details.

Raw token, claims, headers, query, JSON request/response и unsafe transport errors не сохраняются и не показываются пользователю.

---

## 38. Logging и безопасность

Разрешённые поля feature-логов:

- event;
- session ID;
- file ID;
- batch ID;
- transfer ID;
- item ID;
- cabinet ID;
- stage;
- status;
- error code;
- attempts;
- counts;
- durations.

Не логируются:

- WB token;
- `sid`;
- auth headers;
- XLSX binary;
- полный payload карточки;
- raw WB body;
- произвольный nested error;
- персональные данные из Excel;
- cabinet display name без необходимости.

Telegram text обязательно HTML-escape-ится.

XLSX reader имеет hard limits против ZIP bombs и чрезмерных строк/ячеек.

---

## 39. Конфигурация

Потребуются validated settings:

### Cardimport

```text
CARDIMPORT_MAX_FILES
CARDIMPORT_MAX_FILE_SIZE
CARDIMPORT_MAX_DECOMPRESSED_SIZE
CARDIMPORT_MAX_ROWS
CARDIMPORT_MAX_CELLS
CARDIMPORT_MAX_CARDS
CARDIMPORT_MAX_ISSUES
CARDIMPORT_TELEGRAM_ERRORS_LIMIT
CARDIMPORT_PROCESSING_LEASE
```

### Transfer

```text
TRANSFER_WORKERS
TRANSFER_MAX_IN_FLIGHT
TRANSFER_CLAIM_SIZE
TRANSFER_WORK_LEASE
TRANSFER_RECONCILE_INTERVAL
TRANSFER_RECONCILE_TIMEOUT
TRANSFER_BATCH_SIZE
TRANSFER_NOTIFICATION_RETRY
```

Точные defaults фиксируются на этапе 0 по реальным XLSX fixtures и актуальному WB operation snapshot.

Каждое значение имеет compile-time ceiling и проверяется при startup.

---

## 40. Целевая структура файлов

```text
internal/feature/
├── cardimport/
│   ├── feature.go
│   ├── domain/
│   │   ├── session.go
│   │   ├── file.go
│   │   ├── card.go
│   │   ├── batch.go
│   │   └── validation.go
│   ├── service/
│   ├── repository/postgres/
│   ├── transport/xlsx/
│   └── transport/telegram/
│
├── transfer/
│   ├── feature.go
│   ├── config.go
│   ├── domain/
│   │   ├── transfer.go
│   │   ├── item.go
│   │   ├── cabinet.go
│   │   ├── result.go
│   │   └── errors.go
│   ├── service/
│   ├── repository/postgres/
│   ├── gateway/wb/
│   │   ├── gateway.go
│   │   ├── dto.go
│   │   └── mapper.go
│   ├── worker/
│   └── transport/telegram/
│
├── statistics/
│   ├── feature.go
│   ├── service/
│   ├── source/transfer/
│   └── transport/telegram/
│
└── edit/
    └── contracts.go
```

---

# Последовательность реализации

## 41. Gate WB-0. Завершить WB core

До начала feature integration должны выполняться критерии WB master-плана:

- один новый execution path;
- `ForCabinet`;
- `Cabinets`;
- opaque credentials;
- Content operation catalog;
- rate limit/retry/mutation uncertainty;
- один process-wide client;
- tests/race/vet проходят.

Feature нельзя реализовывать против временного `ForCredentials`.

---

## 42. Этап 0. Зафиксировать feature contracts

Действия:

1. Зафиксировать UX cardimport.
2. Зафиксировать validation matrix.
3. Зафиксировать merge rules.
4. Выбрать hard limits.
5. Зафиксировать batch schema v1.
6. Проверить актуальные UploadCards limits.
7. Проверить price внутри create DTO.
8. Зафиксировать media-by-links contract.
9. Зафиксировать `already_present`.
10. Зафиксировать live statistics projection.

Gate:

- все понятия имеют одно значение;
- нет ручного выбора кабинетов;
- cardimport не входит в statistics;
- `Готово` не запускает validation;
- отдельный prices API исключён.

---

## 43. Этап 1. Выделить `cardimport`

Действия:

1. Переименовать `feature/cards`.
2. Перенести feature-specific domain из `core/domain`.
3. Создать facade `Feature` по стилю users.
4. Ввести IDs, Purpose и state machines.
5. Удалить `SelectedPurpose map`.

Gate:

- packages компилируются без cycles;
- состояние не хранится в process-local map;
- `cardimport` не импортирует concrete consumers.

---

## 44. Этап 2. Создать cardimport schema/repository

Действия:

1. Добавить forward migration.
2. Реализовать session/file/items/issues.
3. Реализовать immutable batch.
4. Реализовать outbox.
5. Добавить row locking и idempotency.

Gate:

- одна active session;
- concurrent Finalize создаёт один batch;
- Cancel не оставляет dispatch;
- batch immutable.

---

## 45. Этап 3. Harden XLSX parser

Действия:

1. Ввести source coordinates.
2. Заменить silent coercion typed errors.
3. Проверять headers.
4. Добавить limits.
5. Добавить within-file merge.
6. Добавить fixtures/fuzz.

Gate:

- invalid данные не превращаются в zero values;
- parser bounded;
- результат детерминирован.

---

## 46. Этап 4. Реализовать multi-file aggregate

Действия:

1. Reserve/Process pipeline.
2. Межфайловый merge.
3. Конфликты scalar/characteristics/barcodes.
4. Summary counters.
5. Revision checks.
6. Recovery незавершённых файлов.

Gate:

- каждый новый файл немедленно проверяется;
- Ready появляется только при zero errors;
- completion order не влияет на aggregate.

---

## 47. Этап 5. Telegram cardimport

Действия:

1. Выбор transfer/edit.
2. Единое control message.
3. Document handler.
4. Renderer counts/errors.
5. `Готово`.
6. Общий `Отмена`.
7. Stale callback protection.
8. Role/ownership middleware.

Gate:

- UX точно соответствует согласованному;
- delete-file отсутствует;
- cardimport не появляется в statistics.

---

## 48. Этап 6. Batch и handoff

Действия:

1. Finalize transaction.
2. Checksum/schema version.
3. BatchReader.
4. Outbox dispatcher.
5. Consumer router.
6. Idempotent delivery.

Gate:

- restart не теряет batch;
- duplicate delivery создаёт один consumer operation;
- consumer получает только batchID.

---

## 49. Этап 7. Transfer domain и repository

Действия:

1. Transfer/item/cabinet models.
2. State machines.
3. Migration.
4. Unique batch idempotency.
5. Work leases.
6. Current read queries.

Gate:

- snapshot всех cabinets сохраняется;
- item × cabinet уникальны;
- failure одного кабинета не блокирует другие.

---

## 50. Этап 8. Feature WB adapter

Действия:

1. ContentGateway.
2. Feature-owned DTO.
3. Mapping subjects/characteristics.
4. CardsList.
5. UploadCards.
6. CardsErrorList.
7. SaveMediaByLinks.
8. Typed core error mapping.

Gate:

- используется один injected Client;
- tokens недоступны feature;
- generic retry отсутствует;
- contract tests проходят.

---

## 51. Этап 9. Transfer preparation

Действия:

1. Read batch pages.
2. Resolve subject.
3. Map characteristics.
4. Required validation.
5. Existing-card preflight.
6. Group/chunk planning.
7. Persist prepared state.

Gate:

- до mutation все локальные ошибки известны;
- already-present не обновляется;
- request batches bounded.

---

## 52. Этап 10. Upload и reconciliation

Действия:

1. DB-backed worker.
2. Upload batches.
3. Submitted state.
4. Uncertain outcome handling.
5. CardsList polling.
6. ErrorList polling.
7. nmID/imtID persistence.
8. Reconcile deadlines.

Gate:

- `2xx` не считается final creation;
- mutation не повторяется вслепую;
- restart после upload идёт в reconciliation.

---

## 53. Этап 11. Media и завершение

Действия:

1. Save media links.
2. Проверка uncertain media.
3. Финальные item/cabinet statuses.
4. completed/partial/failed.
5. Final notification outbox.

Gate:

- карточка без media завершается;
- media failure не стирает созданную карточку;
- итоговые counters совпадают с authoritative rows.

---

## 54. Этап 12. Worker lifecycle

Действия:

1. Bounded worker pool.
2. Fair scheduling.
3. Leases.
4. Restart recovery.
5. Graceful shutdown.
6. Safe logs/metrics.

Gate:

- нет unbounded goroutines;
- нет duplicate mutation;
- race tests проходят;
- один плохой кабинет изолирован.

---

## 55. Этап 13. Live statistics

Действия:

1. CurrentOperationsSource.
2. Transfer adapter.
3. Telegram renderer.
4. Refresh/back callbacks.
5. Error grouping.
6. Message bounds.

Gate:

- отображаются только текущие transfer;
- cardimport отсутствует;
- history/filters отсутствуют;
- statistics ничего не мутирует.

---

## 56. Этап 14. Wiring и edit seam

Действия:

1. Загрузить WB registry.
2. Создать один Client.
3. Создать cardimport.
4. Создать transfer и worker.
5. Зарегистрировать handoff consumer.
6. Создать statistics.
7. Зарегистрировать Telegram.
8. Добавить unavailable edit handler.
9. Graceful shutdown и CloseIdleConnections.

Gate:

- production имеет один WB Client;
- edit не создаёт недоставляемых jobs;
- весь flow доступен из Telegram.

---

## 57. Этап 15. Документация и hardening

Действия:

1. Обновить README.
2. Сослаться на WB core master-plan.
3. Добавить package GoDoc.
4. Описать recovery/runbook.
5. Описать error codes.
6. Удалить старые cards consumers.
7. Проверить migration up/down.
8. Выполнить security review.

Gate:

- code и docs описывают один flow;
- старый `feature/cards` отсутствует;
- все acceptance criteria выполнены.

---

# Тестовая матрица

## 58. Cardimport

- begin transfer;
- повторный begin;
- попытка сменить purpose;
- N корректных файлов;
- duplicate Telegram update;
- новый файл после появления `Готово`;
- corrupt/empty/oversized XLSX;
- missing/duplicate headers;
- invalid numbers;
- missing vendor code;
- compatible duplicate;
- conflicting duplicate;
- duplicate barcode;
- deterministic order;
- Cancel во время parsing;
- Finalize одновременно с новым файлом;
- двойной Finalize;
- restart validation;
- restart handoff;
- batch checksum/immutability;
- invalid session никогда не dispatch-ится.

## 59. Transfer

- exact cabinet snapshot;
- 1, 60 и 128 valid cabinets;
- rejected cabinet отсутствует;
- zero cabinets;
- one card × all cabinets;
- multiple cards/groups;
- bounded batches;
- already present;
- unknown subject;
- missing characteristic;
- explicit upload error;
- upload `2xx` → reconciliation;
- `UncertainOutcomeError`;
- card appears after polling;
- CardsErrorList failure;
- reconcile deadline;
- media success/failure/uncertain;
- restart before upload;
- restart during upload;
- restart during reconciliation;
- restart during media;
- one cabinet credentials failure;
- partial transfer;
- complete failure;
- duplicate handoff;
- no blind mutation retry;
- no second WB Client.

## 60. Statistics

- no current operations;
- one transfer;
- multiple transfers;
- exact counters;
- stages;
- cabinet progress;
- safe grouped errors;
- cardimport session absent;
- completed notified operation absent;
- no filters;
- no historical query;
- Telegram message bounds;
- refresh idempotent.

## 61. Security

- token отсутствует в feature structs;
- token отсутствует в БД;
- token отсутствует в errors/logs;
- raw WB body не сохраняется;
- XLSX values не логируются;
- Telegram HTML escaped;
- user не может открыть чужую session;
- stale callback безопасен;
- XLSX ZIP bomb bounded;
- workers и queues bounded.

## 62. End-to-end

```text
transfer selected
→ XLSX №1
→ validation
→ XLSX №2
→ validation
→ Ready
→ immutable batch
→ handoff
→ transfer
→ all valid cabinets
→ upload
→ reconciliation
→ media
→ final notification
→ operation disappears from statistics
```

Отдельные E2E:

- invalid file → errors → no Ready → Cancel;
- crash after batch commit → handoff restored;
- crash after upload → reconciliation without duplicate upload;
- one cabinet fails → others complete → partial;
- duplicate Telegram callbacks → one batch and one transfer.

---

# Verification

## 63. После каждого этапа

```bash
gofmt -w <changed-go-files>

go test ./internal/feature/cardimport/...
go test ./internal/feature/transfer/...
go test ./internal/feature/statistics/...

go vet ./internal/feature/cardimport/...
go vet ./internal/feature/transfer/...
go vet ./internal/feature/statistics/...

git diff --check
```

## 64. Финальный gate

```bash
go test ./cmd/... ./internal/...
go test -race ./cmd/... ./internal/...
go vet ./cmd/... ./internal/...
git diff --check
```

После исправления проблемы с `out/pgdata`:

```bash
go test ./...
go test -race ./...
```

Проверки архитектурных запретов:

```bash
rg 'ForCredentials\\(' cmd internal/feature
rg 'SelectedPurpose' internal
rg 'TOKEN|Token string' internal/feature
rg 'feature/cardimport' internal/feature/statistics
```

Ожидания:

- `ForCredentials` не найден;
- `Select1

# Критерии завершения

## 65. Общий Definition of Done

План считается выполненным, когда одновременно:

1. `cardimport`, `transfer` и `statistics` являются отдельными features.
2. `feature/cards` больше не существует.
3. Пользователь выбирает transfer или доступный consumer.
4. Purpose хранится в PostgreSQL.
5. Пользователь может отправить несколько XLSX.
6. Каждый файл валидируется сразу.
7. Общий набор проверяется после каждого файла.
8. Ошибки отображаются в одном Telegram-окне.
9. При ошибках `Готово` отсутствует.
10. Отдельного удаления файла нет.
11. `Отмена` отменяет всю сессию.
12. `Готово` не запускает валидацию.
13. Создаётся один immutable batch.
14. Между features передаётся только `batchID`.
15. Handoff переживает restart.
16. Transfer автоматически получает все valid cabinets.
17. Пользователь не выбирает кабинеты.
18. Feature не получает WB credentials.
19. Используется один process-wide WB Client.
20. Каждая пара `card × cabinet` имеет отдельный результат.
21. HTTP `2xx` upload не считается окончательным успехом.
22. Mutation uncertainty проходит reconciliation.
23. Blind retry mutation отсутствует.
24. Один кабинет не блокирует остальные.
25. Медиа передаётся только по ссылкам.
26. Отдельный prices API не вызывается.
27. Transfer завершается как completed/partial/failed.
28. Statistics показывает только текущие transfer/edit.
29. Cardimport в statistics отсутствует.
30. История и фильтры отсутствуют.
31. Edit можно подключить через существующий consumer contract.
32. Restart не создаёт duplicate batch/upload.
33. Unit, integration, E2E и race tests проходят.
34. Токены отсутствуют в feature DB/errors/logs.
35. Code и документация описывают один и тот же flow.

---

## 66. Итоговый контракт

```text
Telegram purpose
→ durable cardimport session
→ N bounded XLSX
→ immediate parse/validation
→ deterministic aggregate
→ zero blocking errors
→ Ready
→ immutable batch
→ transactional handoff
→ idempotent transfer.Start(batchID)
→ snapshot Client.Cabinets()
→ bounded card × cabinet work
→ feature-owned WB DTO
→ shared WB Client
→ upload
→ business reconciliation
→ nmID/imtID
→ media by links
→ per-cabinet outcome
→ completed/partial/failed
→ final Telegram notification

transfer/edit current state
→ statistics
```

`cardimport` заканчивает ответственность на immutable batch.  
`transfer` владеет WB-dependent подготовкой и выполнением.  
`statistics` только отображает текущие операции.  
`edit` подключается позднее без изменения уже реализованных границ.
