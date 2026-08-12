# Архитектура WB Core

## Статус

Документ описывает фактически реализованный пакет:

```text
internal/core/transport/wb
```

Это описание текущего кода, а не план, roadmap или backlog. WB Core собран,
подключён в composition root и покрыт автоматическими тестами. Feature-адаптеры
для cardimport/transfer находятся за границей этого документа.

## Назначение

WB Core предоставляет один process-wide клиент для Content API Wildberries:

```text
Config
→ Clientset
→ CabinetClient
→ typed ContentV1 API
→ APIClient executor
→ request/response helpers
→ flowcontrol
→ HTTP transport
```

Ядро отвечает за:

- загрузку нескольких кабинетов из environment;
- безопасное хранение opaque token каждого кабинета;
- общий HTTP connection pool;
- per-cabinet Authorization;
- закрытый catalog Content API операций;
- подготовку URL, query и JSON body;
- bounded чтение и декодирование response;
- rate admission и observation response headers;
- retry только безопасных read-операций;
- классификацию delivery state и ошибок;
- один итоговый структурированный лог логического запроса.

Feature-код не получает token, `*http.Client`, произвольный method/path или
низкоуровневый executor.

## Структура пакетов

Структура следует client-go-подобному разделению соседних пакетов. Вложенный
`wb/internal` не используется.

```text
internal/core/transport/wb/
├── clientset.go
├── cabinet.go
├── types.go
├── errors.go
├── config/
├── api/content/v1/
├── typed/content/v1/
├── client/
│   ├── request/
│   └── response/
├── flowcontrol/
├── policy/
└── transport/
```

Направление зависимостей:

```text
wb Clientset
  → config
  → client
  → typed/content/v1

typed/content/v1
  → client.Executor
  → api/content/v1

client
  → client/request
  → client/response
  → flowcontrol
  → policy
  → transport

client/request !→ client/response
client/response !→ client/request
client/response !→ flowcontrol
client/response !→ policy
```

## Конфигурация

Environment schema:

```text
WB_API_BASE_URL=https://content-api.wildberries.ru
WB_API_TIMEOUT=20s
WB_API_CABINETS=main,backup

WB_API_CABINET_MAIN_NAME=Основной
WB_API_CABINET_MAIN_TOKEN=<secret>

WB_API_CABINET_BACKUP_NAME=Резервный
WB_API_CABINET_BACKUP_TOKEN=<secret>
```

`Config` содержит base URL, общий timeout и список `CabinetConfig`. Конструктор
Clientset работает с копией конфигурации и не изменяет caller-owned значения.

Проверяются:

- непустой список кабинетов;
- уникальные `CabinetID`;
- уникальные нормализованные display names;
- bounded name и token;
- отсутствие surrounding whitespace у token;
- положительный timeout;
- production HTTPS URL;
- allowlisted host `content-api.wildberries.ru`;
- отсутствие port, userinfo, path, query и fragment.

Token не декодируется и не нормализуется. Поле исключено из JSON, а `String` и
`GoString` заменяют непустое значение на `--- REDACTED ---`.

## Clientset и кабинеты

Публичный composition API:

```go
func NewForConfig(
	config *config.Config,
	logger *zap.Logger,
) (*Clientset, error)

func NewForConfigAndHTTPClient(
	config *config.Config,
	httpClient *http.Client,
	logger *zap.Logger,
) (*Clientset, error)

func (c *Clientset) Cabinets() []CabinetInfo
func (c *Clientset) ForCabinet(id config.CabinetID) (*CabinetClient, error)
func (c *Clientset) CloseIdleConnections()
```

Свойства:

- registry строится по принципу all-or-error;
- кабинеты сортируются по `CabinetID`;
- `Cabinets()` возвращает независимый deterministic snapshot;
- `ForCabinet()` возвращает immutable logical client;
- неизвестный ID классифицируется через `ErrCabinetNotFound`;
- token нельзя получить из `CabinetClient`;
- один `Clientset` владеет одним shared transport.

`CabinetClient` предоставляет только:

```go
func (c *CabinetClient) ID() config.CabinetID
func (c *CabinetClient) Name() string
func (c *CabinetClient) ContentV1() contentv1.ContentV1Interface
```

## HTTP transport

Один clone базового `*http.Transport` обслуживает connection pool всех
кабинетов. Для каждого кабинета создаётся shallow copy `http.Client` со своей
wrapper chain:

```text
http.Client copy
→ Authorization wrapper for one cabinet
→ AttemptTrace wrapper
→ shared base RoundTripper
```

Wrappers не выполняют retry, не читают response body и не пишут terminal log.

Authorization wrapper:

- клонирует request перед изменением header;
- устанавливает exact `Authorization: <token>`;
- не добавляет `Bearer`;
- не изменяет token;
- не записывает credentials в context или logs.

Redirects запрещены через `http.ErrUseLastResponse`.

## Catalog операций и typed API

`policy.Operation` содержит закрытые поля:

- стабильный operation ID;
- HTTP method и relative path;
- один rate-limit `BucketID`;
- read/mutation kind;
- retry mode;
- разрешённые success statuses;
- request/response body mode;
- request/response byte bounds.

Конкретные операции и wire DTO находятся в `api/content/v1`. Typed-клиенты
находятся в `typed/content/v1` и сами выбирают operation manifest.

Content V1 предоставляет группы:

- Categories: parent categories, subjects, characteristics, brands;
- Directories: colors, kinds, countries, seasons, VAT, TNVED;
- Cards: limits, list, trash list, error list, upload, upload-add;
- Media: save by links.

Публичные typed signatures принимают конкретные query/request DTO и возвращают
конкретные response DTO. Arbitrary method/path API отсутствует.

## Executor

Typed-клиенты зависят от узкого контракта:

```go
type Executor interface {
	Execute(
		ctx context.Context,
		operation policy.Operation,
		query any,
		body any,
		target any,
	) error
}
```

Один вызов `APIClient.Execute` владеет полным lifecycle:

```text
create logical trace
→ validate target
→ prepare immutable request
→ apply overall timeout
→ wait for rate admission
→ create a fresh http.Request
→ execute wrapper chain
→ observe allowed rate headers once
→ bounded read and close response body
→ classify status/transport result
→ retry safe read when allowed
→ decode final JSON into a temporary value
→ publish decoded result
→ write one final log
```

## Request и response helpers

`client/request` не владеет HTTP client, limiter, retry, trace или logger.

`request.Prepare` один раз:

- кодирует typed query;
- сериализует JSON body;
- проверяет request byte bound;
- собирает URL из validated base URL и catalog path;
- возвращает immutable `Prepared`.

`Prepared.NewHTTPRequest` создаёт новый request и новый `bytes.Reader` для
каждой physical attempt.

`client/response` — stateless пакет. Он отвечает за:

- bounded read и обязательный close body;
- validation decode target;
- JSON decode через временное значение;
- retryable HTTP status allowlist;
- retryable transport error allowlist.

Stateful публичные `Request`, builder и `Result` отсутствуют.

## Flowcontrol и retry

Registry хранит immutable bucket policies и лениво создаёт limiter для пары:

```text
(CabinetID, BucketID)
```

Admission выполняется перед каждой physical attempt. Очередь ожидания bounded и
учитывает cancellation.

Response observation разбирает разрешённые rate headers один раз. Полученный
server delay одновременно используется limiter-ом и executor backoff. Ошибка
observation учитывается в trace, но не заменяет успешный или уже
классифицированный business result.

Автоматический retry разрешён только operation manifest для read-операций и
ограничен следующими результатами:

- HTTP `429`, `500`, `502`, `503`, `504`;
- allowlisted timeout/EOF/closed/reset transport errors.

Cancellation и deadline не retry-ятся. Mutation всегда выполняет не более
одной physical attempt.

Clientset использует максимум три read attempts и exponential backoff с base
delay `500 ms`. Если сервер передал больший `Retry-After`, используется он;
фактическое ожидание ограничивается context deadline.

## Delivery state и ошибки

Корневой пакет экспортирует программный контракт:

```go
type ClassifiedError interface {
	error
	Code() string
	Delivery() DeliveryState
	RetryAfter() time.Duration
}
```

Delivery states:

- `NotDispatched` — HTTP-отправка не начиналась;
- `ResponseReceived` — получен явный HTTP response;
- `UnknownDelivery` — отправка началась, но response не получен.

Per-attempt recorder создаётся отдельно для каждого `http.Client.Do`, поэтому
delivery текущей попытки не наследует dispatch state предыдущего retry.

Основные error codes:

- `invalid_request`;
- `invalid_result_target`;
- `invalid_response`;
- `rate_limited`;
- `retry_interrupted`;
- `unexpected_status`;
- `transport_error`.

Business control flow не должен анализировать текст `error.Error()`.

## Tracing и logging

Один logical request создаёт один закрытый `requestTrace` и ровно одну запись:

```text
event=wb_request_completed
```

Trace содержит безопасные metadata:

- локальный request ID;
- cabinet ID и display name;
- operation, method, path и bucket ID;
- attempt count и statuses;
- dispatch count;
- accumulated limiter wait;
- request/response sizes;
- total duration;
- final status, delivery state и error code;
- количество rate observation errors.

Decode входит в lifecycle до final log. Token, Authorization, query, request
body и response body не логируются.

## Composition root и lifecycle

В `cmd/wb-service/main.go` после logger, PostgreSQL и Telegram server создаётся
один process-wide `Clientset`:

```go
wbConfig := wbconfig.NewConfigMust()

wbClientset, err := wb.NewForConfig(&wbConfig, logger.Logger)
if err != nil {
	panic(fmt.Errorf("create WB clientset: %w", err))
}
defer wbClientset.CloseIdleConnections()
```

Clientset создаётся до регистрации Telegram handlers. Ошибка конфигурации или
сборки любого кабинета останавливает startup. При shutdown root context
отменяется, limiter waits и HTTP calls завершаются по context, после чего
закрываются idle connections общего pool.

## Проверки

Автоматические тесты покрывают:

- config validation и credential redaction;
- operation manifest и catalog integrity;
- immutable request preparation;
- bounded response ownership и safe decode;
- limiter queue, cancellation, observation и backoff;
- Authorization и AttemptTrace wrappers;
- safe-read retry и mutation one-shot;
- `UnknownDelivery` и retry interruption;
- один final log и decode error logging;
- сохранение success при observation error;
- Clientset snapshots и cabinet isolation;
- выбор operations всеми typed Content V1 methods;
- публичный `ClassifiedError` contract.

Проверочный набор:

```text
go test ./cmd/... ./internal/...
go test -race ./internal/core/transport/wb/...
go vet ./cmd/... ./internal/...
go mod verify
git diff --check
```

Локальный `httptest.Server` smoke автоматически пропускается только в sandbox,
где запрещён TCP `listen`. Основные HTTP-сценарии дополнительно выполняются
через deterministic in-memory `RoundTripper`.

## Текущая граница интеграции

WB Core подключён в composition root, но пока не передан feature-адаптерам.
Реальный WB smoke не выполняется без явно предоставленных безопасных test
credentials. Эти ограничения относятся к интеграции приложения, а не к
внутренней архитектуре ядра.
