# Архитектура WB Core

## Статус

Документ описывает фактически реализованный пакет:

```text
internal/core/transport/wb
```

Это описание текущего кода, а не план, roadmap или backlog. WB Core собран и
подключён в composition root. Общая проверка credential, WB seller identity и
постоянных cabinet bindings находится внутри Core; feature-адаптеры получают
только его проверенный immutable snapshot.

## Назначение

WB Core предоставляет один process-wide клиент для Content и General API
Wildberries:

```text
Config
→ Clientset
→ identity.Registry
→ CabinetClient
→ APIClient executor
→ request/response helpers
→ flowcontrol
→ HTTP transport
```

Ядро отвечает за:

- загрузку нескольких кабинетов из environment;
- безопасное хранение opaque token каждого кабинета;
- локальную проверку JWT claims;
- удалённую проверку token через Content `/ping` и General
  `/api/v1/seller-info` при startup;
- сравнение seller из JWT с seller, подтверждённым WB;
- уникальность seller и постоянную связь `CabinetID → SellerKey`;
- версии identity binding и capability snapshot;
- общий HTTP connection pool;
- per-cabinet Authorization;
- закрытый catalog Content API операций;
- подготовку URL, query и JSON body;
- bounded чтение и декодирование response;
- rate admission и observation response headers;
- retry только безопасных read-операций;
- классификацию delivery state и ошибок;
- один итоговый структурированный лог логического запроса.

Feature-код не получает token, `*http.Client` или произвольный method/path. Его
`transport/wb` получает только credential-bound generic executor и выбирает
закрытый `Operation` вместе с raw query/request/response DTO.

## Структура пакетов

Структура следует client-go-подобному разделению соседних пакетов. Вложенный
`wb/internal` не используется.

```text
internal/core/transport/wb/
├── clientset.go
├── cabinet.go
├── credentials.go
├── types.go
├── errors.go
├── config/
├── api/content/v1/
├── api/general/v1/
├── identity/
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
  → identity
  → client
  → api/content/v1
  → api/general/v1

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

PostgreSQL-адаптер порта `identity.Store` расположен отдельно от transport:

```text
internal/core/repository/postgres/wbidentity
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

Token не покидает Clientset. Для seller-bound target safety WB Core декодирует
документированные JWT claims, подтверждает token через WB и сравнивает `sid` из
JWT с `sid` из General API. После синхронизации binding Core отдаёт feature
только безопасные digests, revisions, capability flags, expiry и client
generation. Raw token и raw seller ID не возвращаются. В config поле исключено
из JSON, а `String` и `GoString` заменяют непустое значение на
`--- REDACTED ---`.

## Clientset и кабинеты

Публичный composition API:

```go
func NewForConfig(
	ctx context.Context,
	config *config.Config,
	identityStore identity.Store,
	logger *zap.Logger,
) (*Clientset, error)

func NewForConfigAndHTTPClient(
	ctx context.Context,
	config *config.Config,
	httpClient *http.Client,
	identityStore identity.Store,
	logger *zap.Logger,
) (*Clientset, error)

func (c *Clientset) Cabinets() []CabinetInfo
func (c *Clientset) ForCabinet(id config.CabinetID) (*CabinetClient, error)
func (c *Clientset) ExecutorForCabinet(id config.CabinetID) (client.Executor, error)
func (c *Clientset) CredentialSnapshot() ([]CredentialIdentity, error)
func (c *Clientset) PinnedExecutor(id config.CabinetID, generation ClientGeneration) (client.Executor, error)
func (c *Clientset) CloseIdleConnections()

func client.ExecuteResponse[T any](
    ctx context.Context,
    executor client.Executor,
    operation policy.Operation,
    query any,
    body any,
) (T, error)
```

Свойства:

- registry строится по принципу all-or-error;
- Clientset публикуется только после локальной проверки claims, успешных
  Content ping и General seller-info запросов, проверки совпадения seller и
  синхронизации всех bindings;
- один seller нельзя настроить под двумя `CabinetID`;
- прежний `CabinetID` нельзя автоматически перепривязать к другому seller;
- `CredentialSnapshot()` возвращает копию единственного startup snapshot и не
  выполняет повторные HTTP/DB обращения;
- кабинеты сортируются по `CabinetID`;
- `Cabinets()` возвращает независимый deterministic snapshot;
- `ForCabinet()` возвращает immutable logical client;
- неизвестный ID классифицируется через `ErrCabinetNotFound`;
- token нельзя получить из `CabinetClient`;
- один `Clientset` владеет одним shared transport.

`CabinetClient` предоставляет identity и generic execution закрытой операции:

```go
func (c *CabinetClient) ID() config.CabinetID
func (c *CabinetClient) Name() string
func (c *CabinetClient) Execute(
    ctx context.Context,
    operation policy.Operation,
    query any,
    body any,
    target any,
) error
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

## Catalog операций и raw DTO

`policy.Operation` содержит закрытые поля:

- стабильный operation ID;
- HTTP method и relative path;
- один rate-limit `BucketID`;
- read/mutation kind;
- retry mode;
- разрешённые success statuses;
- request/response body mode;
- request/response byte bounds.

Конкретные операции и wire DTO находятся в `api/content/v1` и
`api/general/v1`. General seller-info и Content ping используются внутренней
проверкой identity. Для прикладных Content-операций endpoint-specific методов в
Core нет: operation manifest выбирает `feature/*/transport/wb`, он же вызывает
`client.ExecuteResponse[T]` и получает полный raw response DTO.

`ExecuteResponse[T]` является единственным generic convenience helper. Он не
выбирает operation, не интерпретирует WB envelope и не реализует workflow.
Пакет `typed` удалён намеренно, потому что его endpoint methods дублировали
feature transports.

Content V1 предоставляет группы:

- Categories: parent categories, subjects, characteristics, brands;
- Directories: colors, kinds, countries, seasons, VAT, TNVED;
- Cards: limits, list, trash list, error list, upload, upload-add;
- Media: save by links.

Arbitrary method/path API отсутствует: executor принимает только закрытый
`policy.Operation`, полученный из catalog package.

## Executor

Feature transport зависит от узкого контракта:

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
wbIdentityStore := wbidentitypostgres.New(uow)

wbClientset, err := wb.NewForConfig(
	ctx,
	&wbConfig,
	wbIdentityStore,
	logger.Logger,
)
if err != nil {
	panic(fmt.Errorf("create WB clientset: %w", err))
}
defer wbClientset.CloseIdleConnections()
```

Clientset создаётся до регистрации Telegram handlers. Ошибка конфигурации,
сборки кабинета, локальной проверки claims, Content ping, General seller-info,
сравнения seller или синхронизации bindings останавливает startup. При shutdown
root context отменяется, limiter waits и HTTP calls завершаются по context,
после чего закрываются idle connections общего pool.

## Текущая граница интеграции

WB Core подключён в composition root. Пакет `identity` владеет общей проверкой
credential, seller identity, уникальностью sellers, bindings и их revisions.
`transfer/transport/wb` только преобразует проверенный Core snapshot в свой
порт. Затем `transfer` добавляет feature-правило: mutation cohort требует
одновременно `ContentRead` и `ContentWrite`, и вычисляет собственную revision
набора целей.
