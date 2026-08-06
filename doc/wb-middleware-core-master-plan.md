# Master-план WB core с middleware и каталогом кабинетов

> **Статус: superseded.** Канонический объединённый план:
> [`wb-cards-transfer-master-plan.md`](./wb-cards-transfer-master-plan.md).
> Этот файл сохранён только для истории и traceability.

## 1. Статус документа

Этот документ является новым целевым планом развития:

```
internal/core/transport/wb
```

Дата архитектурного решения:

```
2026-08-02
```

Предыдущие superseded-планы удалены. Этот файл является единственным целевым
планом реализации. Документ
`wb-transport-architecture.md` продолжает описывать текущее состояние кода до
завершения миграции.

Главное изменение относительно прошлого master-плана: пакет `middleware` не
удаляется. Старый middleware-контракт вокруг `http.RoundTripper` заменяется
типизированным многостадийным WB middleware stack.

## 2. Цель

Построить единое ядро WB-клиента, которое:

- работает с несколькими кабинетами;
- штатно загружает и обслуживает примерно `50..60` кабинетов;
- изолирует ошибочный token одного кабинета от остальных valid кабинетов;
- загружает название кабинета и токен из environment при старте;
- не передаёт токены через feature-код;
- добавляет авторизацию через middleware;
- выполняет rate limit перед каждой наблюдаемой SDK-попыткой;
- реализует Retry как middleware-оркестратор;
- владеет полным жизненным циклом каждой попытки;
- читает, ограничивает и закрывает response body до решения о Retry;
- умеет учитывать transport, body-read и JSON-decode ошибки;
- безопасно работает с GET и изменяющими операциями;
- создаёт отдельный структурированный лог каждой полной попытки;
- использует `http.RoundTripper` только как нижний сетевой адаптер;
- имеет один execution path и детерминированно тестируется.

## 3. Граница плана

### 3.1. Входит в план

- transport config;
- каталог кабинетов из ENV;
- только Personal-токены WB;
- структурное декодирование JWT claims;
- opaque credentials;
- выбор клиента по cabinet ID;
- Content operation catalog;
- custom operation/attempt/finalize middleware;
- Retry middleware с attempt loop;
- RateLimit middleware и response observer;
- Auth middleware;
- AttemptTimeout middleware;
- tracing middleware;
- decode/classification middleware;
- attempt и operation logging middleware;
- terminal executor;
- bounded request/response body;
- mutation uncertainty;
- dependency injection;
- unit, integration, race, fuzz и lifecycle tests;
- миграция со старого `RoundTripper` middleware.

### 3.2. Не входит в план

- хранение токенов в PostgreSQL;
- Telegram UI управления токенами;
- OAuth 2.0;
- любой token, не соответствующий exact Personal contract;
- дополнительные схемы и headers авторизации;
- автоматическая ротация без перезапуска;
- runtime hot reload ENV;
- удалённый secret manager;
- криптографическая проверка подписи WB JWT;
- проверка отзыва токена без обращения к WB;
- distributed rate limiter между процессами;
- circuit breaker;
- автоматическая reconciliation mutation-запросов;
- endpoints вне согласованного Content API;
- streaming и multipart endpoints.

ENV является выбранным источником credentials для первой версии. Архитектура
должна позволять позже заменить ENV loader на secret manager без изменения
middleware и feature-кода.

## 4. Зафиксированные архитектурные решения

### 4.1. Middleware сохраняется

Сохраняется не старый тип:

```go
type Middleware func(next http.RoundTripper) http.RoundTripper
```

Вместо него вводятся типизированные WB middleware трёх уровней:

1. operation middleware;
2. attempt middleware;
3. finalize middleware.

Дополнительно существует синхронный headers observer, который вызывается сразу
после получения response headers.

### 4.2. Retry является middleware-оркестратором

Retry middleware имеет право вызвать attempt chain несколько раз. Это
единственный компонент, способный инициировать новый вызов HTTP client/base
RoundTripper на уровне WB SDK.

Обычное attempt middleware обязано вызвать `next` не более одного раза.

### 4.3. Executor остаётся

Executor не конкурирует с middleware. Он собирает stack, создаёт operation
state, запускает middleware и обеспечивает terminal handler.

Разделение:

```
Executor     = владелец pipeline и lifecycle
Middleware   = политики и наблюдатели
RoundTripper = нижний сетевой адаптер
```

### 4.4. Body имеет одного владельца

Только terminal handler получает сырой `*http.Response` и
`response.Body`. Body не возвращается middleware и feature-коду.

Terminal handler:

1. ограниченно читает body;
2. при необходимости выполняет bounded drain;
3. закрывает body;
4. материализует bounded result;
5. возвращает result в attempt pipeline.

### 4.5. Credentials не передаются в context

Обязательные данные передаются типизированными структурами:

- operation;
- cabinet;
- bucket;
- retry mode;
- attempt index;
- credentials;
- timings;
- response outcome.

`context.Context` используется только для cancellation, deadline и tracing.
Старый `requestmeta` после миграции удаляется.

### 4.6. Stack закрыт для произвольного изменения

Порядок safety-critical middleware собирается конструктором клиента и не
меняется public option-ами. Нельзя:

- удалить RateLimit;
- поставить AttemptTimeout перед RateLimit;
- переместить Auth до создания attempt state;
- добавить middleware, вызывающее transport повторно;
- заменить terminal handler вторым способом отправки.

Расширяемость первой версии разрешена только через безопасные observer seams для
metrics/tests. После стабилизации можно отдельно спроектировать public extension
slots.

## 5. Почему такой подход нормален

Архитектура соответствует направлению зрелых Go SDK:

| Проект | Фактический подход | Что берём |
|---|---|---|
| [AWS SDK Go v2](https://github.com/aws/aws-sdk-go-v2/blob/main/aws/retry/middleware.go) | Retry объявлен Smithy middleware, но внутри управляет attempt loop | Retry middleware может быть оркестратором |
| [Kubernetes client-go](https://github.com/kubernetes/client-go/blob/master/rest/request.go) | Собственный request orchestrator управляет limiter и повторами | Один цикл должен считать все наблюдаемые SDK attempts |
| [Gcore Go](https://github.com/G-Core/gcore-go/blob/main/internal/requestconfig/requestconfig.go) | Retry loop находится выше HTTP middleware | Retry не должен быть скрыт внутри base transport |

Наш вариант строже:

- Retry видит результат чтения body;
- JSON decode выполняется в attempt-local target;
- mutation transport uncertainty представлена отдельной ошибкой;
- RateLimit получает отдельный timeout;
- attempt log создаётся после retry decision;
- secrets не попадают в wire dump.

## 6. Целевая схема

```
CabinetClient.DoJSON
  -> OperationLogging middleware
  -> проверить caller context
  -> создать Overall context
  -> Prepare middleware
       -> validate operation
       -> validate response target
       -> validate/bound query, затем build URL
       -> operation-specific RequestPlan доказывает upper bound
       -> marshal request JSON один раз
       -> defensive exact size check
  -> Retry middleware/orchestrator
       |
       +-> для каждой попытки:
       |     CredentialsPreflight middleware
       |     RateLimit middleware
       |       -> Acquire под admission timeout
       |       -> зарегистрировать headers observer
       |     AttemptTimeout middleware
       |     Auth middleware
       |     Trace middleware
       |     Classify middleware
       |     Decode middleware
       |     Terminal handler
       |       -> fresh request/header/body
       |       -> http.Client.Do
       |       -> base RoundTripper.RoundTrip
       |       -> headers observers
       |       -> bounded read
       |       -> bounded drain при необходимости
       |       -> Close
       |       -> materialized response
       |
       +-> retry decision
       +-> Finalize middleware
       |     -> metrics
       |     -> terminal attempt log
       +-> context-aware backoff
       +-> следующая попытка снова с CredentialsPreflight/RateLimit.Acquire
  -> conditional Commit for JSONRequired/JSONOptional
  -> Operation summary
```

### 6.1. Порядок для `429 -> 200`

```
operation_start
prepare_once
attempt_1_start
credentials_preflight_1
acquire_1
attempt_timeout_1_start
auth_1
send_1
observe_429
read_1
close_1
decode_or_skip_1
classify_1
retry_decision_1
attempt_log_1
backoff
attempt_2_start
credentials_preflight_2
acquire_2
attempt_timeout_2_start
auth_2
send_2
observe_200
read_2
close_2
decode_2_to_temporary_target
classify_2
retry_decision_2
attempt_log_2
commit_decoded_value
operation_summary
```

Response body первой попытки закрывается до backoff и до второго `Acquire`.

## 7. Pipeline contracts

Типы pipeline размещаются в:

```
internal/core/transport/wb/internal/pipeline
```

### 7.1. Operation middleware

```go
type OperationHandler func(
	context.Context,
	*OperationState,
) OperationResult

type OperationMiddleware interface {
	ID() string
	HandleOperation(
		context.Context,
		*OperationState,
		OperationHandler,
	) OperationResult
}
```

Chain builder оборачивает каждый `next` в one-shot guard. Повторный вызов
того же `next` не запускает inner chain и возвращает typed
`MiddlewareContractError` для operation/attempt chain. Это runtime-инвариант, а
не договорённость только в комментарии. Finalize chain тоже имеет guard, но
из-за observer-only void contract второй вызов подавляется и фиксируется
internal diagnostic/metric, не меняя primary result.

Operation middleware выполняется один раз на `DoJSON`.

Цепочка:

```
OperationLogging
  -> Prepare
  -> RetryOrchestrator
  -> Commit
```

Retry middleware вызывает следующий operation handler только после выбора
финального успешного attempt result. Следующий handler коммитит временно
декодированное/optional значение в target caller-а; для `NoContent` commit
является отсутствующим шагом.

### 7.2. Attempt middleware

```go
type AttemptHandler func(
	context.Context,
	*AttemptState,
) AttemptResult

type AttemptMiddleware interface {
	ID() string
	HandleAttempt(
		context.Context,
		*AttemptState,
		AttemptHandler,
	) AttemptResult
}
```

Цепочка:

```
CredentialsPreflight
  -> RateLimit
  -> AttemptTimeout
  -> Auth
  -> Trace
  -> Classify
  -> Decode
  -> Terminal
```

Каждый вызов attempt chain соответствует максимум одному
`http.Client.Do`.

One-shot guard внутри chain builder использует CAS: если middleware ошибочно
вызывает `next` второй раз, transport повторно не запускается, а attempt
завершается `MiddlewareContractError`.

### 7.3. Headers observer

```go
type HeadersObserver interface {
	ID() string
	ObserveHeaders(
		AttemptInfo,
		ResponseHead,
	) error
}
```

`ResponseHead` содержит только:

- status code;
- protocol;
- allowlisted rate-limit headers;
- факт получения response;
- время получения headers.

Он не содержит body, Authorization, query и полный набор headers.

Terminal вызывает все observers синхронно после `Do` и до первого
`response.Body.Read`. Observer не получает request context: deadline попытки
не должен отменить уже полученный server rate-limit feedback. Observer обязан
быть неблокирующим; diagnostic error одного observer сохраняется, но не мешает
вызвать остальные.

### 7.4. Finalize middleware

```go
type FinalizeHandler func(AttemptReport)

type FinalizeMiddleware interface {
	ID() string
	FinalizeAttempt(
		AttemptReport,
		FinalizeHandler,
	)
}
```

Finalize chain вызывается Retry middleware ровно один раз для каждой начатой
попытки после classification и retry decision.

Finalize chain не получает canceled request context и не выполняет сеть. Его
diagnostic lifecycle обязан закончить metrics/logging даже после caller,
overall или attempt cancellation. One-shot guard не допускает двойной вызов
следующего finalizer-а; нарушение фиксируется internal diagnostic, потому что
finalize interface намеренно не возвращает business error.

```
Metrics
  -> AttemptLogger
  -> finalize terminal no-op
```

Finalize middleware является observer-ом и не может:

- изменить primary result;
- начать новый HTTP request;
- изменить retry decision;
- получить raw credentials или body.

## 8. Состояние operation и attempt

### 8.1. PreparedOperation

Public `CabinetClient.DoJSON` принимает opaque `policy/content.Operation`.
Чтобы не создавать import cycle, root package устанавливает в private
`OperationState` trusted resolver closure:

```text
wb root captures content.Operation
  -> closure calls content.Resolve(operation)
  -> returns neutral policy.OperationSpec
  -> Prepare validates and materializes OperationDescriptor
```

Prepare вызывает resolver ровно один раз. `middleware` и
`internal/pipeline` не импортируют `policy/content`; feature-код не может
передать `OperationSpec` или собственный resolver в `DoJSON`. Zero/unknown
opaque operation возвращает safe local error, а внешний OperationLogging всё
равно формирует summary с placeholder `operation=invalid`.

`PreparedOperation` создаётся один раз и после публикации immutable. Его
исполняемые поля закрыты:

```go
type PreparedOperation struct {
	descriptor   OperationDescriptor
	cabinet      CabinetRef
	prototype    *http.Request // body-less, validated
	requestBytes []byte
	responsePlan ResponsePlan
	startedAt    time.Time
}

func (p *PreparedOperation) Descriptor() SafeOperationDescriptor

func (p *PreparedOperation) NewHeaders() http.Header

func (p *PreparedOperation) NewRequest(
	ctx context.Context,
	ownedHeaders http.Header,
) *http.Request
```

`NewHeaders` возвращает fresh clone private validated header template.
`NewRequest` не парсит URL, не валидирует policy и не сериализует JSON после
admission. Оно клонирует проверенный body-less prototype, присваивает именно
fresh `ownedHeaders` текущей попытки и создаёт новый reader/ReadCloser над
private immutable bytes. URL pointer, shared header template и request byte
slice не выдаются middleware или feature-коду.

Для JSON body `NewRequest` выставляет exact `ContentLength`, fresh
`io.NopCloser(bytes.NewReader(...))` и `GetBody=nil`: replay выполняет только
наш attempt orchestrator созданием нового request. Для body-less operation
`Body=nil`, `ContentLength=0`, `GetBody=nil`.

`OperationDescriptor` — закрытая материализация neutral `OperationSpec`: ID,
safe name, method, canonical path, bucket, retry mode, read/mutation kind и
query/request/response plans. Pipeline не импортирует `policy/content` и не
принимает descriptor/spec/resolver от feature-кода.

`RequestPlan` принадлежит trusted Content catalog. Он проверяет exact request
DTO type, operation-specific collection/string limits и overflow-safe
conservative upper bound JSON до сериализации. Request DTO graph первой версии
не может реализовывать `json.Marshaler`/`encoding.TextMarshaler` и не выполняет
I/O: стандартный `encoding/json` сначала строит полный buffer, поэтому один
только bounded writer не является memory bound. Если plan не может доказать
upper bound, Prepare fail-closed не вызывает marshal.

После доказательства bound `json.Marshal` вызывается один раз; actual bytes
дополнительно проверяются на `MaxRequestBodySize`. Catalog property/fuzz tests
гарантируют `len(marshal(v)) <= estimatedBound` для валидных DTO. Это ограничивает
выходную allocation compile-time ceiling-ом с документированным overhead
`encoding/json`, а не обещает невозможный zero-allocation streaming contract.
Overall context проверяется до и сразу после validation/marshal; late
cancellation не публикует PreparedOperation и не доходит до `Acquire`.

Query сначала оценивается без построения строки: bounded число keys/values и
overflow-safe длина percent-encoded representation. Только затем `url.Values`
кодируется один раз; encoded query и полный URL проходят отдельные exact limits.
Catalog-owned `QueryPlan` дополнительно разрешает только известные operation
keys, multiplicity и value grammar; unknown key и query у operation без query
отклоняются до `Acquire`. После Prepare хранится private deep copy encoded
query, а caller `url.Values` больше не используется.

### 8.2. AttemptState

Новая структура создаётся на каждую попытку:

```go
type AttemptState struct {
	Number      int
	MaxAttempts int
	Prepared    *PreparedOperation
	Headers     http.Header
	Timings     AttemptTimings
	Trace       AttemptTrace
}
```

Внутри также находятся opaque credentials и headers observers, но они не
попадают в safe report.

Каждая попытка получает:

- новый `AttemptState`;
- новый attempt context;
- новый `http.Request`;
- новый `http.Header`;
- новый `bytes.Reader`;
- новый `io.ReadCloser` request body;
- новый `httptrace.ClientTrace`.

### 8.3. AttemptResult

```go
type AttemptResult struct {
	StatusCode       int
	ClientDoCalled   bool
	ResponseReceived bool
	ResponseBytes    int64
	DeliveryState    DeliveryState
	FailureClass     FailureClass
	RetryDecision    RetryDecision
}
```

Внутри result могут временно находиться bounded response bytes, attempt-local
decoded value и безопасно классифицированные causes. Эти данные не входят в
logger report автоматически.

### 8.4. RetryDecision

```go
type RetryDecision struct {
	RetryScheduled bool
	Reason         RetryReason
	BackoffDelay   time.Duration
}
```

Используется имя `RetryScheduled`, а не `WillRetry`: cancellation во время
backoff может не позволить запустить следующую попытку.

`BackoffDelay` содержит только local retry backoff. Server rate-limit block
хранится limiter-ом отдельно и отражается в safe attempt report как
`RateLimitBlockDuration`.

Operation summary дополнительно содержит:

```
retry_interrupted=true|false
```

## 9. Обязательные инварианты

1. В package существует ровно один SDK send path.
2. Retry middleware — единственный компонент, вызывающий attempt chain повторно.
3. Обычное attempt middleware вызывает `next` не больше одного раза.
4. Перед каждым `http.Client.Do` выполняется ровно один успешный
   `RateLimit.Acquire`; один admission нельзя использовать для двух send.
5. `Acquire` необратим: полученный локальный token не возвращается даже при
   cancellation, transport error или редкой expiry race до HTTP.
6. Admission failure не вызывает Auth, AttemptTimeout или transport.
7. AttemptTimeout начинается только после успешного admission.
8. Один `http.Client.Do` вызывает base transport не больше одного раза.
9. Redirects не создают дополнительные отправки.
10. `ObserveHeaders` выполняется до первого чтения body.
11. Любой полученный response body закрывается ровно один раз.
12. Body закрывается до явного owned `attemptCancel()` при unwind; caller или
    deadline могут асинхронно отменить context раньше Close.
13. Body закрывается до finalize, backoff и следующего `Acquire`.
14. Request предыдущей попытки не переиспользуется.
15. Request JSON сериализуется один раз и byte-identical между попытками.
16. Decode выполняется в новый attempt-local target.
17. Failed attempt не изменяет target caller-а.
18. Response commit выполняется ровно один раз после финального успеха с
    decoded/optional value и ноль раз для `NoContent` или error response.
19. Finalize вызывается один раз даже для admission и transport errors.
20. Global `MaxAttempts` учитывает каждый запуск attempt chain, включая
    preflight/admission failure; каждый наблюдаемый `http.Client.Do` занимает
    один такой slot.
21. Token, auth headers и raw response никогда не попадают в middleware report.

## 10. Где используется RoundTripper

Клиент владеет одним `http.Client`:

```go
http.Client{
	Transport:     baseRoundTripper,
	Timeout:       0,
	CheckRedirect: denyRedirects,
}
```

`denyRedirects` обязан возвращать `http.ErrUseLastResponse`. Тогда первый
`3xx` response возвращается terminal handler с доступным body, а следующий
request не создаётся:

```go
func denyRedirects(
	_ *http.Request,
	_ []*http.Request,
) error {
	return http.ErrUseLastResponse
}
```

Terminal handler вызывает:

```go
response, err := client.httpClient.Do(request)
```

Внутренняя схема:

```
Terminal handler
  -> http.Client.Do
     -> baseRoundTripper.RoundTrip
```

Base `RoundTripper` отвечает только за:

- proxy;
- DNS;
- TCP;
- TLS;
- HTTP/1.1 и HTTP/2;
- connection pooling;
- keep-alive;
- передачу bytes.

В base transport отсутствуют:

- настроенный WB/application Retry wrapper; остаётся только документированное
  internal behavior стандартного `http.Transport` из раздела 10.1;
- WB RateLimit;
- Auth orchestration;
- WB attempt logging;
- body decode;
- mutation classification.

Тесты подменяют base transport через:

```go
type RoundTripFunc func(
	*http.Request,
) (*http.Response, error)
```

Production transport создаётся клонированием `http.DefaultTransport` без
изменения глобального singleton.

Type assertion всегда проверяется:

```go
base, ok := http.DefaultTransport.(*http.Transport)
if !ok {
	return nil, ErrUnsupportedDefaultTransport
}
owned := base.Clone()
```

Constructor не делает unchecked assertion и не panic-ует, если другой package
заменил global `http.DefaultTransport`. Отдельный test временно подменяет global
и проверяет typed constructor error. Production client затем использует только
owned clone.

### 10.1. Граница счётчика attempts

План различает четыре счётчика:

```text
pipeline attempt (attempt chain started)
  -> 0 или 1 успешный RateLimit Acquire
  -> 0 или 1 http.Client.Do
  -> 0 или 1 вызов внедрённого RoundTripper
```

`MaxAttempts` и `attempts_started` считают pipeline attempts. `http_calls`
считает только `ClientDoCalled`. Limiter списывает token только при успешном
`Acquire`. Поэтому admission failure имеет attempt log, но не является HTTP
call; при этом каждый реальный HTTP call всегда находится внутри одного
pipeline attempt и имеет ровно один предшествующий `Acquire`.

Обычно после успешного `Acquire` сразу следует `http.Client.Do`. Единственное
допустимое исключение — локальная остановка на узкой границе cancellation или
expiry после admission. Такой token намеренно не возвращается: консервативная
потеря одной локальной квоты безопаснее ошибочного refund после конкурентного
`ObserveHeaders`. `ClientDoCalled` означает именно вход в `http.Client.Do`;
вызов base `RoundTripper` может не произойти, если context уже отменён внутри
`net/http`.

Стандартный [`http.Transport`](https://pkg.go.dev/net/http#Transport) может
внутри одного `RoundTrip` автоматически повторить idempotent request при
network error на ранее успешно использованном соединении. Эта внутренняя
деталь не наблюдается внешним middleware и не может входить в `MaxAttempts` или
отдельный rate-limit admission без замены стандартного transport.

Поэтому контракт формулируется честно:

- `MaxAttempts`/logs считают pipeline attempts, limiter — успешные admissions,
  а `http_calls` — входы в `Client.Do`;
- redirects и application Retry всегда учитываются явно;
- mutation methods не помечаются `Idempotency-Key` и не объявляются
  idempotent без отдельного WB contract;
- transport-internal retry допустим только в границах поведения стандартной
  библиотеки для idempotent requests;
- строгая гарантия «один TCP write на один limiter token» потребовала бы
  специального transport или отключения connection reuse и не входит в этот
  план.

Это ограничение должно быть отражено в GoDoc и тестах: fake RoundTripper
доказывает число вызовов SDK, но не количество внутренних TCP writes
production `http.Transport`.

## 11. Response body lifecycle

`RoundTrip` возвращает response после headers. Terminal продолжает attempt:

```
headers
-> ObserveHeaders
-> bounded Read
-> optional bounded drain
-> Close
-> materialize
```

### 11.1. Нормальный response

Body читается до EOF с лимитом:

```
MaxResponseBodySize + 1
```

`limit+1` позволяет отличить exact-limit от oversized response.

### 11.2. Error response

HTTP status имеет приоритет. Для безопасного `APIError` сохраняется только
bounded и sanitized preview:

```
MaxErrorBodySize
```

Raw preview не включается в `Error()` и logger. Получить его можно только через
явный accessor с документацией о потенциальной чувствительности.

### 11.3. Bounded drain

Если body не был полностью дочитан и ошибки чтения ещё не было, executor может
дочитать в `io.Discard` не больше:

```
64 KiB
```

Цель — достичь EOF и позволить transport переиспользовать соединение, не читая
неограниченный ответ. Если EOF не достигнут в пределах bound, body закрывается,
а соединение может быть исключено из reuse.

После read error дополнительный drain не выполняется.

### 11.4. Close

`Close` сообщает transport, что response больше не используется:

- полностью прочитанное соединение может вернуться в pool;
- недочитанное соединение может быть закрыто;
- resources не остаются занятыми следующими запросами.

Close error сохраняется как diagnostic. Она не должна скрывать известный HTTP
status или более раннюю body-read error.

## 12. ENV-модель кабинетов

### 12.1. Выбранный формат

Используются отдельные environment variables, а не строка
`name:token,name:token`.

Сокращённый пример для нескольких кабинетов:

```dotenv
WB_API_CABINETS=shop_001,shop_002,shop_060

WB_API_CABINET_SHOP_001_NAME=Кабинет_001
WB_API_CABINET_SHOP_001_TOKEN=eyJ_REPLACE_WITH_PERSONAL_TOKEN

WB_API_CABINET_SHOP_002_NAME=Кабинет_002
WB_API_CABINET_SHOP_002_TOKEN=eyJ_REPLACE_WITH_PERSONAL_TOKEN

WB_API_CABINET_SHOP_060_NAME=Кабинет_060
WB_API_CABINET_SHOP_060_TOKEN=eyJ_REPLACE_WITH_PERSONAL_TOKEN
```

В реальном `WB_API_CABINETS` перечисляются все фактические ID без `...`.
Порядок списка сохраняется в registry. Целевой штатный объём — `50..60`
Personal-токенов; hard limit оставляет запас до 128 кабинетов.

Mode variable не нужен: Personal является единственным допустимым типом.
Credentials loader принимает только перечисленные ниже cabinet variables;
дополнительные credential keys не используются и формируют safe startup
diagnostic без вывода value.

### 12.2. Зачем нужен технический ID

В `WB_API_CABINETS` находятся стабильные технические ID:

```
shop_001
shop_002
shop_060
```

Это не ID, полученный от WB, и WB API его не требует. Это обязательный только
для нашего клиента стабильный local alias. Использовать вместо него display
name нельзя: имя может измениться или совпасть. Использовать token нельзя: он
является secret и меняется при ротации. Автоматический порядковый ID также
небезопасен — после перестановки 50–60 ENV-записей feature-код может выбрать
другой кабинет. Поэтому alias задаётся один раз и сохраняется при замене name
или token.

ID:

- используется feature-кодом для выбора клиента;
- разрешён в безопасных логах;
- не меняется при переименовании display name;
- однозначно преобразуется в имя ENV;
- не является token или seller ID.

ID считается несекретным operational alias. В нём запрещено размещать ИНН,
юридическое название, WB seller ID, телефон или другие персональные данные.

Формат:

```
[a-z][a-z0-9_]{0,31}
```

Environment suffix получается через uppercase:

```
shop_001 -> WB_API_CABINET_SHOP_001_TOKEN
```

`NAME` — отображаемое название для UI. Оно не является limiter key и не
логируется transport-слоем.

Display name должен быть valid UTF-8, не иметь leading/trailing whitespace,
переводов строк и control characters. Разрешаются Unicode letters/digits,
обычный пробел, `.`, `_` и `-`; символы синтаксиса Make/shell вроде `$`, `#`,
кавычек, backslash и `=` отклоняются. Exact duplicate display names
отклоняются, чтобы UI и operator diagnostics не становились неоднозначными.

### 12.3. Почему не один JSON и не delimiter pairs

Отдельные variables выбраны потому, что они:

- проще ротируются независимо;
- удобно создаются через Docker/Kubernetes secrets;
- не требуют логировать или разбирать один большой secret blob;
- не зависят от разделителей внутри display name;
- позволяют ошибке назвать конкретную отсутствующую переменную без вывода token.

WB token является JWT и хранится raw. Дополнительное Base64-кодирование не
используется: valid WB compact JWT уже состоит из трёх непустых unpadded
Base64URL segments и точек, поэтому не содержит пробелы, `$`, `#`, кавычки или
другой Make/shell syntax. Любое значение вне strict compact-JWT grammar
отклоняется до публикации registry.

### 12.4. Загрузка

ENV читается один раз при старте:

```
EnvSource.LookupEnv + EnvSource.Keys
-> parse transport config
-> parse cabinet IDs
-> load name/token каждой записи
-> независимо validate каждую entry
-> decode structural JWT claims
-> build immutable CabinetRegistry из valid entries
-> return safe CabinetLoadResult с rejected issues
-> build Client
```

Auth middleware никогда не вызывает `os.Getenv`.

Источник объявляется явно, потому что одной функции `LookupEnv` недостаточно
для обнаружения orphan variables и опечаток:

```go
type EnvSource interface {
	LookupEnv(string) (string, bool)
	Keys(prefix string) []string
}
```

Production adapter строит keys через `os.Environ`, но не возвращает и не
логирует values при enumeration. Test adapter использует immutable map.

Ошибки разделяются на два уровня:

- fatal schema/config error останавливает startup: отсутствующий/пустой
  `WB_API_CABINETS`, invalid/duplicate cabinet ID, превышение hard limits,
  неправильный transport config или отсутствие хотя бы одного valid cabinet;
- cabinet-local error отклоняет только соответствующую entry: missing/invalid
  NAME/TOKEN, malformed/expired/unsupported Personal token, неправильные
  permissions или duplicate name/raw token.

Все остальные valid entries публикуются в immutable registry с сохранением их
относительного порядка. Composition root пишет одну `Error` запись на
отклонённый кабинет и итоговую startup summary:

```text
event=wb.cabinet.rejected cabinet_id=shop_017 reason_code=token_rotation_due
event=wb.cabinets.loaded state=degraded requested=60 loaded=59 rejected=1
```

Allowlist лога: `event`, `cabinet_id`, `reason_code`, counts. Token, encoded JWT,
display name, claims, `sid`, ENV value и nested parser error не логируются.
Неизвестные/orphan credential variables также дают safe diagnostic и
игнорируются; если это опечатка ключа listed cabinet, отсутствие правильного
NAME/TOKEN отдельно отклонит именно этот cabinet.

При `loaded > 0` Client создаётся и процесс продолжает работу; наличие rejected
entries отмечается `state=degraded`, но не делает весь process unready. При
`loaded == 0` loader возвращает fatal error и Client не создаётся.

Один и тот же raw token нельзя привязать к двум cabinet ID: точный duplicate
считается ошибкой обеих entries, поэтому обе пропускаются. Diagnostic содержит
только конфликтующие cabinet ID. Два разных Personal-токена с одним `sid`
допустимы: это разные credentials одного limiter realm, поэтому они разделяют
серверную квоту через общий `(sid, BucketID)` key.

### 12.5. Ограничения

- количество кабинетов: `1..128`;
- cabinet ID: `1..32` ASCII characters;
- display name: `1..128` UTF-8 bytes;
- Personal token: `1..8 KiB`;
- total credential bytes: не больше `512 KiB`; сумма всех raw Personal tokens
  и display names.

Граница гарантированно вмещает 60 токенов максимального разрешённого размера и
60 имён по 128 bytes: `60 * (8 KiB + 128 B) < 512 KiB`. Обычно WB JWT заметно
меньше hard limit.

Compile-time ceilings нельзя увеличить через ENV. Низкий общий предел выбран
также потому, что environment всего процесса ограничен OS/container runtime
ещё до старта Go loader; deployment обязан учитывать собственный `ARG_MAX` и
лимит Secret объекта платформы.

## 13. Валидация токенов

### 13.1. Raw token

Loader обязан:

- использовать `LookupEnv` и различать missing/empty;
- не выполнять `TrimSpace` над token;
- отклонять leading/trailing whitespace;
- отклонять CR, LF, NUL и control characters;
- отклонять значение с prefix `Bearer `;
- проверять ровно три непустых unpadded Base64URL segment;
- ограничивать размер header и payload до decode;
- не включать raw или encoded token в error.

Auth middleware само добавляет:

```
Authorization: Bearer <raw-token>
```

### 13.2. Structural JWT decode

Personal-token parser без сетевого запроса читает allowlist:

- `acc`;
- `for`;
- `t`;
- `sid`;
- `exp`;
- `s`.

Parser валидирует bounded JWT header как JSON object, но не доверяет его
`alg`/`kid`. Для payload он использует `json.Decoder.UseNumber`, проверяет exact
JSON field types, canonical lowercase UUIDv4 для `sid`, отклоняет duplicate
keys и trailing data. Неизвестные claims после синтаксической проверки
игнорируются: WB явно
оставляет за собой право менять служебные поля payload.

Использование claims:

- `sid` — локальный Personal limiter scope;
- `exp` — обязательный input для effective admission deadline и fail-fast
  expiry/rotation cutoff;
- `s` — проверка Content category и read-only режима;
- `acc/for/t` — соответствие Personal contract.

Декодирование без проверки подписи не является доказательством подлинности.
Claims используются как operational hints в рамках доверенного ENV: они могут
локально запретить operation и выбрать conservative limiter partition, но не
могут авторизовать запрос. WB остаётся источником истины, проверяет подпись,
статус и полномочия token на каждом запросе.

### 13.3. Personal contract

Все кабинетные токены должны соответствовать:

```
acc = 3
for = self
t   = false
sid = canonical lowercase UUIDv4
exp = future valid Unix timestamp
```

Несоответствие отклоняет только cabinet этой entry и добавляет safe issue в
`CabinetLoadResult`. Остальные valid Personal credentials продолжают
публиковаться. Startup останавливается только если после фильтрации не осталось
ни одного valid cabinet.

### 13.4. Неподдерживаемые токены

Принимается только exact сочетание из раздела 13.3. Любое другое значение или
отсутствие `acc/for/t` возвращает `UnsupportedTokenTypeError`. Расширение типов
требует отдельного review policies, origins и headers.

### 13.5. Категория и read-only

Token должен содержать Content permission bit `1 << 1`. Иначе cabinet не
публикуется.

Read-only bit — `1 << 30`. Read-only token разрешает read operations. Mutation
operation локально отклоняется typed ошибкой `ReadOnlyCredentialsError`.
Отсутствие read-only bit не является локальной авторизацией mutation: это лишь
разрешение дойти до WB, а окончательное решение принимает WB. Неизвестные bits
никогда не трактуются как дополнительные права.

### 13.6. Истечение и отзыв

Согласно [документации WB](https://dev.wildberries.ru/docs/openapi/api-information),
официальный срок token сейчас составляет 180 дней, но клиент использует
консервативную политику 175 дней: compile-time
`personalTokenSafetyMargin = 120h` вычитается из JWT `exp`.

```text
wb_expiry        = time.Unix(exp, 0)
effective_expiry = wb_expiry - 120h
```

В коде не hardcode-ится дата создания и не делается предположение о наличии
`iat`: единственным admission deadline является `effective_expiry`. Если token
был выдан на полный официальный срок 180 дней и подключён сразу, клиент будет
использовать его не более 175 дней. Если token подключён позже, он всё равно
остановится за пять суток до WB `exp`.

Отсутствующий, нецелый, overflow `exp`, underflow при вычитании margin или
`now >= effective_expiry` отклоняет только этот cabinet при старте. Auth
middleware повторно проверяет effective expiry перед отправкой, чтобы
долгоживущий процесс не продолжал futile calls после rotation deadline.

При создании Client формируется одна безопасная startup summary с количеством
credentials и количеством токенов, истекающих в пределах
`CredentialExpiryWarning` до effective expiry. Для каждого такого токена
допускается отдельный
`Warn` только с `cabinet_id` и округлённым `expires_in`; token, display name,
claims и `sid` не логируются. Background goroutine/ticker на кабинет не
создаётся: дальнейшее истечение обнаруживает существующий per-attempt
CredentialsPreflight.

Если effective expiry наступил уже во время работы процесса, запрос только
этого cabinet завершается локальным `CredentialsRotationDueError` до limiter и
HTTP. Terminal operation log пишет `Error` с `cabinet_id` и safe reason code;
остальные cabinets продолжают работать.

Локальное decode не определяет отзыв token. Ответ WB `401` является
non-retryable auth error.

## 14. Cabinet registry и Client API

### 14.1. Типы

```go
type CabinetID string

type CabinetInfo struct {
	ID   CabinetID
	Name string
}

type CabinetRegistry struct {
	// immutable valid lookup + rejected reason map + ordered valid IDs
}

type CabinetLoadIssueCode string

type CabinetLoadIssue struct {
	CabinetID CabinetID
	Code      CabinetLoadIssueCode
}

type CabinetLoadResult struct {
	Registry  *CabinetRegistry
	Requested int
	Rejected  []CabinetLoadIssue
}
```

`CabinetLoadIssueCode` имеет закрытый allowlist: `missing_name`,
`invalid_name`, `missing_token`, `token_invalid`, `token_unsupported`,
`token_wb_expired`, `token_rotation_due`, `content_permission_missing`,
`duplicate_name`, `duplicate_token`. Он не включает исходное parser message.

В private entry находятся:

- cabinet ID;
- display name;
- opaque credentials;
- WB token expiry и effective rotation deadline;
- read-only capability;
- Personal `sid`.

Registry также хранит private immutable `orderedValidIDs []CabinetID`.
`Cabinets()` возвращает defensive copy только valid entries в их относительном
порядке из `WB_API_CABINETS`, поэтому результат не зависит от iteration order
Go map. Registry сохраняет для declared-but-rejected ID только safe issue code,
без NAME/TOKEN/claims. Rejected entries доступны composition root как safe
`CabinetLoadIssue`; raw parser errors туда не попадают.

### 14.2. Opaque credentials

```go
package auth

type Credentials struct {
	// private auth material
}
```

У credentials нет:

- `Token() string`;
- exported fields;
- JSON marshal;
- text marshal.

`String()` и `GoString()` возвращают только:

```
[REDACTED]
```

Logger не использует `zap.Any` для credentials.

Так как Auth middleware является sibling package, credentials предоставляет
не string getters, а узкие capabilities:

```go
func (Credentials) ValidateAt(
	time.Time,
	mutation bool,
) error

func (Credentials) AdmissionDeadline() time.Time

func (Credentials) ApplyHeaders(http.Header)
```

`ApplyHeaders` записывает token прямо в fresh header map и ничего не
возвращает наружу. `ValidateAt` возвращает только typed redacted errors.
Limiter realm вычисляется при construction и передаётся отдельным opaque key,
поэтому RateLimit middleware также не получает raw `sid` string.

### 14.3. Конструкторы

```go
func LoadCabinetRegistryFromEnv(
	source EnvSource,
	clock Clock,
) (CabinetLoadResult, error)

func NewClient(
	config Config,
	cabinets *CabinetRegistry,
	logger *zap.Logger,
	build BuildInfo,
) (*Client, error)
```

Production composition root передаёт process environment adapter. Tests
используют map fake.

Public `ClientOption` в первой версии отсутствует: safety-critical stack и
transport нельзя заменить через произвольную option. Internal
`newClientWithDependencies` доступен только package tests и принимает fakes.

`BuildInfo.Version` приходит из build-time ldflags; deterministic fallback для
local/test build — `dev`. Разрешённая grammar:

```text
[A-Za-z0-9][A-Za-z0-9._-]{0,63}
```

Именно из этого validated значения строится `User-Agent`; hostname, cabinet
data и credentials туда не входят.

### 14.4. Выбор кабинета

```go
func (client *Client) ForCabinet(
	id CabinetID,
) (*CabinetClient, error)

func (client *Client) Cabinets() []CabinetInfo
```

`ForCredentials` удаляется из public execution path. Feature-код не может
обойти registry и передать произвольный token.

Unknown ID возвращает typed `UnknownCabinetError`. Declared, но rejected ID
возвращает typed `CabinetUnavailableError` с safe reason code. Ни одна ошибка не
перечисляет другие cabinets, token или claims.

### 14.5. Пример использования

```go
cabinet, err := wbClient.ForCabinet("shop_001")
if err != nil {
	return err
}

var response content.CardsResponse

err = cabinet.DoJSON(
	ctx,
	content.ListCards(),
	query,
	nil,
	&response,
)
```

Feature-код знает только cabinet ID и operation. Он не знает token или `sid`.

### 14.6. Ротация первой версии

```
обновить ENV token
-> restart / rolling restart
-> loader проверяет новый token
-> valid cabinets публикуются, rejected cabinets логируются
```

Тот же cabinet ID остаётся контрактом feature-кода. Hot reload и одновременная
поддержка двух версий одного Personal token не входят в первую версию.

Можно заменить один или несколько токенов за один restart. Ошибка нового token
временно делает недоступным только соответствующий cabinet; остальные entries
загружаются. Если не осталось ни одного valid cabinet, startup завершается
ошибкой. При rolling restart два процесса имеют независимые Client-local
limiters и могут временно увеличить общий burst; deployment обязан прекратить
admission и дренировать old instance либо принять возможные server `429`.
Distributed coordination остаётся вне scope.

## 15. Auth middleware

Статическая проверка Personal claims, permission bits и initial effective expiry
выполняется в Prepare до Retry. Дополнительно `CredentialsPreflight`
выполняется в начале каждой попытки до admission:

1. проверяет Personal token на текущий момент;
2. запрещает mutation для locally read-only credentials;
3. вычисляет effective credentials expiry;
4. ограничивает admission deadline этой датой.

Auth middleware выполняется после admission и внутри attempt timeout. Оно
делает финальную проверку effective expiry, закрывающую race на границе
ожидания limiter.

Оно:

1. берёт opaque credentials из typed `AttemptState`;
2. повторно проверяет, что effective token deadline не наступил;
3. один раз вызывает `PreparedOperation.NewHeaders` и сохраняет fresh map в
   `AttemptState.Headers`;
4. добавляет exact auth headers в эту owned map;
5. вызывает следующий handler один раз.

Обязательные application headers:

```
Accept: application/json
Authorization: Bearer <raw-token>
Content-Type: application/json    только при наличии JSON body
User-Agent: wb-service/<build-version>
```

Auth middleware не:

- читает ENV;
- декодирует JWT заново;
- логирует headers;
- изменяет shared template;
- выполняет HTTP;
- выполняет Retry.

Header template с заранее установленным `Authorization` или другим неразрешённым
auth header является локальной invariant error и не отправляется: Auth
middleware единолично владеет авторизационными headers.

Если final effective-expiry check не проходит после успешного `Acquire`, HTTP не
вызывается, но локальный token намеренно не возвращается. CredentialsPreflight
и admission deadline, ограниченный expiry, делают это редкой boundary race.
Такой outcome логируется с `client_do_called=false`; server quota не была
израсходована, а локальный limiter остаётся консервативным.

`User-Agent` задаётся request template явно и не зависит от стандартного
значения Go transport. Build version проходит отдельную safe validation и не
содержит hostname, cabinet data или credentials.

## 16. RateLimit middleware

### 16.1. Версионируемый Content operation catalog

Первая версия поддерживает 13 JSON-операций. Каталог opaque: feature package
получает готовую operation через функцию-конструктор и не может изменить
`Method`, `Path`, `BucketID` или `RetryMode`.

| Constructor | Name | Method | Path | BucketID | RetryMode |
|---|---|---|---|---|---|
| `ParentCategoriesOperation` | `content.object.parent-all` | `GET` | `/content/v2/object/parent/all` | `BucketCommon` | `RetrySafe` |
| `SubjectsOperation` | `content.object.all` | `GET` | `/content/v2/object/all` | `BucketCommon` | `RetrySafe` |
| `SubjectCharacteristicsOperation` | `content.object.characteristics` | `GET` | `/content/v2/object/charcs/{subjectId}` | `BucketCommon` | `RetrySafe` |
| `UploadCardsOperation` | `content.cards.upload` | `POST` | `/content/v2/cards/upload` | `BucketCardsUpload` | `RetryRateLimitOnly` |
| `UploadCardsAddOperation` | `content.cards.upload-add` | `POST` | `/content/v2/cards/upload/add` | `BucketCardsUploadAdd` | `RetryRateLimitOnly` |
| `CardsListOperation` | `content.cards.list` | `POST` | `/content/v2/get/cards/list` | `BucketCommon` | `RetrySafe` |
| `CardsErrorListOperation` | `content.cards.error-list` | `POST` | `/content/v2/cards/error/list` | `BucketCardsErrorList` | `RetrySafe` |
| `UpdateCardsOperation` | `content.cards.update` | `POST` | `/content/v2/cards/update` | `BucketCardsUpdate` | `RetryRateLimitOnly` |
| `MoveCardsOperation` | `content.cards.move` | `POST` | `/content/v2/cards/moveNm` | `BucketCommon` | `RetryRateLimitOnly` |
| `DeleteCardsToTrashOperation` | `content.cards.delete-trash` | `POST` | `/content/v2/cards/delete/trash` | `BucketCardsDeleteTrash` | `RetryRateLimitOnly` |
| `RecoverCardsOperation` | `content.cards.recover` | `POST` | `/content/v2/cards/recover` | `BucketCardsRecover` | `RetryRateLimitOnly` |
| `TrashCardsListOperation` | `content.cards.trash-list` | `POST` | `/content/v2/get/cards/trash` | `BucketCommon` | `RetrySafe` |
| `SaveMediaByLinksOperation` | `content.media.save` | `POST` | `/content/v3/media/save` | `BucketCommon` | `RetryRateLimitOnly` |

`SubjectCharacteristicsOperation` принимает `subjectID > 0` и материализует
реальный path; literal `{subjectId}` никогда не попадает в HTTP request.
`POST /content/v3/media/file` не входит в первую версию: это multipart/streaming,
а контракт этого плана ограничен bounded JSON.

`RetrySafe` означает семантически безопасное чтение, даже если WB использует
`POST`. `RetryRateLimitOnly` разрешает повтор только после однозначно полученного
retryable ответа WB, например `429`; transport uncertainty автоматически не
повторяется.

Для mutation строка `RetryRateLimitOnly` является candidate policy до Stage 0.
Snapshot хранит для неё official evidence URL, дату проверки и краткую
машиночитаемую гарантию. Если официальный contract не доказывает rejection до
side effect, resolver материализует `RetryDisabled`, а snapshot test не
позволяет случайно включить retry.

### 16.2. Snapshot bucket policies

| BucketID | String ID | Interval | Burst |
|---|---|---:|---:|
| `BucketCommon` | `content.common` | `600ms` | `5` |
| `BucketCardsUpload` | `content.cards.upload` | `6s` | `5` |
| `BucketCardsUploadAdd` | `content.cards.upload-add` | `6s` | `5` |
| `BucketCardsErrorList` | `content.cards.error-list` | `6s` | `5` |
| `BucketCardsUpdate` | `content.cards.update` | `6s` | `5` |
| `BucketCardsDeleteTrash` | `content.cards.delete-trash` | `20s` | `5` |
| `BucketCardsRecover` | `content.cards.recover` | `20s` | `5` |

Это snapshot `content-2026-08-02`, а не вечная константа протокола. На этапе 0
пути, исключения и числа повторно сверяются с официальной
[документацией WB Content API](https://dev.wildberries.ru/docs/openapi/work-with-products)
и release notes; любое изменение требует новой версии snapshot и catalog tests.
Регистрация всех семи policies атомарна: клиент либо стартует с полным валидным
набором, либо constructor возвращает ошибку.

Одинаковые числовые параметры не объединяют buckets. Общую квоту совместно
расходуют только операции `BucketCommon`; upload, upload-add, error-list,
update, delete-trash и recover остаются независимыми.

### 16.3. Partition key

```
(sid, BucketID)
```

Cabinet ID, display name и token не являются limiter key. Все Personal tokens
одного `sid` разделяют quota, даже если они перечислены под разными cabinet ID.
Токены с разными `sid` получают независимые realms для одного BucketID.

### 16.4. Attempt flow

RateLimit middleware:

1. создаёт отдельный admission context с deadline не позже Overall,
   `RateLimitWaitTimeout` и Personal-token effective expiry;
2. вызывает `Acquire`;
3. сразу вызывает `waitCancel`, освобождая admission timer;
4. необратимо списывает один локальный token при успешном `Acquire`;
5. записывает wait duration;
6. регистрирует headers observer;
7. вызывает `next` один раз с исходным outer context, а не admission context.

`Acquire` не возвращает reservation и не имеет `Commit`/`Cancel`. После успеха
token не возвращается ни при cancellation, ни при local boundary error, ни при
transport error. Это исключает ошибочный refund, если конкурентный
`ObserveHeaders` уже уменьшил Remaining или заблокировал bucket после `429`.

Headers observer сразу передаёт limiter:

- status;
- `X-Ratelimit-Remaining`;
- `X-Ratelimit-Limit`;
- `X-Ratelimit-Reset`;
- `X-Ratelimit-Retry`;

Это полный allowlist первой версии. По официальному WB contract значения
`Retry` и `Reset` — decimal non-negative integer seconds; `Limit` и
`Remaining` — decimal non-negative integers. Parser удаляет только HTTP OWS по
краям, затем требует ASCII digits; sign, fraction, internal whitespace,
overflow и duplicate conflicting values отклоняются без записи raw value.
`Retry` ограничен `MaxServerRetryDelay`, а `Reset` — отдельным
`MaxServerResetDelay`: полное восстановление burst может занимать
`Interval * Burst` и быть заметно длиннее ожидания до одного retry. Generic
`Retry-After` намеренно не используется: он не является частью выбранного WB
rate-limit contract.

При `429` валидный `X-Ratelimit-Retry` задаёт `blockedUntil`. Более короткое
последующее наблюдение не сокращает block. `X-Ratelimit-Reset` и
`X-Ratelimit-Limit` уточняют восстановление burst, а
`X-Ratelimit-Remaining` на non-`429` может только консервативно clamp-ить
локальную доступность вниз.

### 16.5. Ошибки

Admission timeout:

- не вызывает transport;
- не увеличивает `http_calls`, но учитывается в `attempts_started`;
- не повторяется;
- попадает в finalizer с `client_do_called=false`.

Observation error:

- сохраняется как diagnostic;
- не отменяет чтение и Close body;
- не скрывает HTTP status;
- internal observer/state error может консервативно запретить auto-retry, если
  limiter state нельзя безопасно определить; обычная malformed header ветка
  использует определённый ниже fallback.

Exact fallback для `429`:

1. bucket немедленно clamp-ится до zero available tokens;
2. valid positive `X-Ratelimit-Retry <= MaxServerRetryDelay` задаёт точный
   `blockedUntil`;
3. missing, zero, malformed или conflicting Retry задаёт fallback
   `blockedUntil = max(existing, now + BucketPolicy.Interval)`;
4. Retry с overflow/значением выше bound задаёт
   `blockedUntil = max(existing, now + MaxServerRetryDelay)`;
5. raw header не логируется, пишется только typed diagnostic reason;
6. safe/evidence-backed `429` остаётся retry-eligible, а следующий `Acquire`
   выдерживает block; mutation без evidence остаётся terminal;
7. malformed `Limit`/`Reset` не отменяет уже применённый Retry/fallback block.

Fallback interval — локальная защитная эвристика, не заявленная гарантия WB.
Он гарантирует, что concurrent callers не расходуют оставшийся local burst
сразу после server `429`.

### 16.6. Сохраняемые свойства limiter

- token bucket;
- initial burst;
- дробный refill;
- FIFO waiters;
- bounded queue;
- bounded key cardinality;
- context cancellation;
- server feedback может уменьшать локальную оценку;
- `429` может продлить block;
- более короткое наблюдение не сокращает действующий block;
- transport error не возвращает уже списанный limiter permit;
- Client-local semantics и single-production-client contract явно
  документированы.

## 17. AttemptTimeout middleware

Timeout budgets независимы:

```
OverallTimeout
RateLimitWaitTimeout
AttemptTimeout
```

Порядок:

```
Overall context
  -> CredentialsPreflight
  -> RateLimit Acquire context
  -> Attempt context
       -> Auth
       -> Trace
       -> request
       -> HTTP
       -> body read
       -> Close
       -> Decode
       -> Classify
```

Attempt timeout не расходуется в limiter queue. Backoff также не входит в
AttemptTimeout, но входит в OverallTimeout.

`attemptCancel()` вызывается после body Close, Decode и Classify. Это не даёт
cancel-ошибке заменить реальную ошибку чтения.

Deadline создаётся через injected `DeadlineFactory`, а не прямым разбросанным
вызовом `context.WithTimeout`. Production adapter использует
`context.WithDeadline`; tests управляют clock/deadline/timer одним virtual-time
seam.

JSON decoder не умеет прерывать произвольный пользовательский
`UnmarshalJSON`. Поэтому AttemptTimeout имеет cooperative decode semantics:

1. context проверяется непосредственно до Decode;
2. decode выполняется только в attempt-local target;
3. context повторно проверяется сразу после Decode;
4. если deadline наступил во время успешного Decode, value не commit-ится и
   возвращается timeout/cancellation;
5. Overall/caller cause имеет приоритет над AttemptTimeout;
6. собственный `attemptCancel`, вызванный после classification, никогда не
   классифицируется как request failure.

Это ограничение документируется в GoDoc. Встроенные Content response types не
должны выполнять blocking I/O в `UnmarshalJSON`.

## 18. Trace middleware

Trace middleware создаёт новый `httptrace.ClientTrace` на attempt и измеряет:

- ожидание connection;
- DNS;
- connect;
- TLS;
- получение connection;
- момент записи request headers;
- first response byte;
- время до response headers.

Trace нужен также для `DeliveryState` mutation:

```
not_dispatched
provably_pre_wire
possibly_applied
response_received
```

Семантика:

- `not_dispatched` — terminal не вошёл в `http.Client.Do`;
- `provably_pre_wire` — owned standard transport вернул узко распознанную
  DNS/dial/TLS-before-request ошибку;
- `possibly_applied` — request был dispatch-нут, response headers нет и
  отсутствие применения доказать нельзя;
- `response_received` — response headers получены.

Отсутствие callback `WroteRequest` само по себе ничего не доказывает. Custom
RoundTripper может не вызывать `httptrace` hooks, поэтому его transport error
по умолчанию получает `possibly_applied`. `provably_pre_wire` разрешается
только для allowlisted typed phase errors owned standard transport; unsafe raw
URL/error при этом не публикуется.

Header values, URL query и body в trace не сохраняются.

## 19. Terminal handler

Terminal handler:

1. проверяет attempt/overall context непосредственно перед dispatch;
2. вызывает `PreparedOperation.NewRequest`;
3. получает новый `http.Request`, reader и ReadCloser над immutable bytes;
4. передаёт request именно fresh `AttemptState.Headers`, уже собранные Auth;
5. отмечает `ClientDoCalled` и вызывает owned `http.Client.Do`;
6. вызывает все headers observers;
7. ограниченно читает body;
8. выполняет bounded drain по правилам;
9. закрывает body;
10. возвращает materialized result.

Terminal не клонирует headers второй раз и не накладывает policy поверх Auth.
Если context отменён до шага 5, `http.Client.Do` не вызывается. Если отмена
происходит внутри `net/http`, `ClientDoCalled=true`, но injected RoundTripper
может не быть вызван.

Он не:

- принимает retry decision;
- ждёт backoff;
- пишет terminal attempt log;
- получает следующий attempt;
- возвращает raw response наружу.

Defensive contract обрабатывает:

- `response == nil && err == nil`;
- `response != nil && err != nil`;
- `response.Body == nil`;
- panic-free close/read paths;
- redirects;
- oversized body;
- truncated body.

## 20. Decode middleware

### 20.1. Почему decode находится внутри attempt

Для `200 OK` возможны:

- malformed JSON;
- тип поля не соответствует target;
- неожиданное окончание JSON;
- trailing JSON value;
- пустой body там, где operation требует JSON.

Retry middleware обязан видеть decode result, но это не означает автоматический
повтор каждой decode error. Pure syntax error, type mismatch, trailing value и
required-empty response считаются deterministic/non-retryable по умолчанию.
Safe retry разрешён для предшествующей transport/body-read truncation, а не для
любого JSON contract mismatch. Mutation не может автоматически повторяться
после полученного `2xx` с invalid body.

### 20.2. Attempt-local target

Caller target не декодируется напрямую. Prepare создаёт `ResponsePlan`:

```go
type ResponsePlan interface {
	NewAttemptTarget() any
	Decode([]byte, any) error
	Commit(any)
}
```

На каждой попытке:

```
new temporary target
-> decode
-> classify
-> либо discard
-> либо final Commit в caller target
```

Failed decode не оставляет частично заполненные map/slice/struct caller-а.

Для `JSONRequired`/`JSONOptional` Prepare заранее проверяет, что caller target —
non-nil pointer с settable root, и строит закрытый plan. Каждая попытка
декодируется в новый zero-value `*T`. Финальный `Commit` выполняет ровно один
`reflect.Set` корневого значения и не имеет error path. Это replacement
semantics: прежнее содержимое caller map, slice, struct или scalar заменяется
целиком, а не merge-ится.

Если root type реализует `json.Unmarshaler`, метод вызывается на временном
target, не на caller target. Любая validation, способная завершиться ошибкой,
происходит до единственного `reflect.Set`; поэтому partial commit невозможен.
Reflection metadata кешируется в `ResponsePlan`, сама decode остаётся обычным
`json.Decoder`.

### 20.3. JSON strictness

Decoder:

- принимает ровно одно JSON value;
- проверяет EOF после value;
- не использует `DisallowUnknownFields` глобально без operation-specific
  решения;
- сохраняет семантику чисел target-типа;
- не логирует JSON.

Operation metadata задаёт response mode:

```
JSONRequired
JSONOptional
NoContent
```

Exact semantics:

- non-`2xx`: Decode и Commit всегда пропускаются, caller target не меняется;
- `JSONRequired`: target обязателен; empty body или invalid JSON — contract
  error; final success делает один Commit;
- `JSONOptional`: target обязателен; empty body означает zero-value `T` и всё
  равно делает один replacement Commit; whitespace-only body не считается
  empty и должен быть valid JSON;
- `NoContent`: target обязан быть `nil`; empty body успешен без Commit;
  неожиданный non-empty body — contract error.

Для mutation неожиданный/invalid `2xx` body классифицируется по правилам
uncertain outcome, даже если HTTP status успешный.

## 21. Classification middleware

Classification выполняется после terminal и Decode, но до attempt cancel.

Приоритет:

1. известный HTTP status;
2. delivery state;
3. body read/size result;
4. JSON decode result;
5. close diagnostic;
6. observation diagnostic;
7. context cause.

Этот список задаёт классификацию evidence, а не разрешение продолжать работу.
После неё применяется cancellation gate:

- caller/Overall cancellation всегда запрещает новый attempt и final commit;
- AttemptTimeout не скрывает уже известный HTTP status;
- `429` с body timeout всё ещё классифицируется по status, если Overall жив;
- late timeout после успешного `2xx` Decode запрещает commit и возвращает
  `AttemptTimeoutError` без автоматического retry;
- attempt timeout до response headers может повторяться только для
  `RetrySafe`; mutation без доказательства `provably_pre_wire` uncertain.

Примеры:

- оборванный body `429` не запрещает разрешённый rate-limit retry;
- оборванный body `400` не делает response retryable;
- оборванный `200` safe operation может повторяться;
- invalid JSON/type mismatch `200` safe operation по умолчанию не повторяется;
- invalid JSON `200` mutation становится uncertain outcome;
- oversized error preview не скрывает известный `503`;
- caller/overall cancellation останавливает любые новые attempts.

## 22. Retry middleware

### 22.1. Основной алгоритм

```text
for attempt = 1..MaxAttempts
    create fresh AttemptState
    result = attemptChain(attempt)
    decision = retryPolicy(result, operation)
    result.RetryDecision = decision
    finalize(result)

    if terminal
        return result

    wait context-aware backoff
end
```

### 22.2. Базовая матрица

| Outcome | `RetrySafe` | `RetryRateLimitOnly` | `RetryDisabled` |
|---|---:|---:|---:|
| `429` | да | да | нет |
| `408` | да | нет | нет |
| `500/502/503/504` | да | нет | нет |
| temporary transport до/во время send | да | нет | нет |
| attempt timeout до response headers | да | нет | нет |
| late timeout после успешного Decode | нет | нет | нет |
| truncated `2xx` из-за body-read error | да | нет | нет |
| pure JSON syntax/type/contract error | нет | нет | нет |
| `400/401/403/404` | нет | нет | нет |
| Content `409` | нет без operation policy | нет | нет |
| response too large | нет по умолчанию | нет | нет |
| admission error | нет | нет | нет |
| caller/overall cancellation | нет | нет | нет |
| unknown failure | нет | нет | нет |

Retry matrix является pure table-driven policy. Unknown enum fail-closed.

### 22.3. Mutation

Mutation автоматически повторяется только для outcome, доказывающего отсутствие
применения. Сам факт получения `429` не считается таким доказательством без
versioned WB policy evidence для конкретной operation.

`RetryRateLimitOnly` активируется только если Stage 0 фиксирует официальное
правило WB «этот `429` означает rejection до side effect и request можно
повторить» для выбранного endpoint. Если такой гарантии нет или текст
неоднозначен, effective mode становится `RetryDisabled`, а mutation outcome —
`UncertainOutcomeError`. Generic рекомендация «повторить позже» не расширяет
mutation safety автоматически.

| Mutation outcome | Public result |
|---|---|
| valid expected `2xx` | success |
| `400/401/403/404/413/422` | terminal `APIError` как явный rejection |
| `409` | operation-specific; без policy `UncertainOutcomeError` |
| `429` с versioned rejection evidence | разрешённый rate-limit retry |
| `429` без такого evidence | `UncertainOutcomeError` |
| `408` или `5xx` | `UncertainOutcomeError` |
| transport `provably_pre_wire` | terminal safe transport error без auto-retry |
| transport `possibly_applied` | `UncertainOutcomeError` |
| truncated/invalid/oversized expected `2xx` | `UncertainOutcomeError` |

Известный status по-прежнему имеет приоритет над secondary body error; это не
означает, что любой status доказывает отсутствие side effect.

Если request мог быть передан WB, но достоверного ответа нет:

```
UncertainOutcomeError
```

Такая ошибка:

- сохраняет safe classified cause;
- содержит operation и attempt metadata;
- не содержит query/body/token;
- не запускает обычный safe retry;
- требует business reconciliation выше transport.

### 22.4. Backoff

- exponential cap;
- equal jitter;
- WB `X-Ratelimit-Retry` принимается только в пределах safety bound;
- overflow-safe arithmetic;
- context-aware sleeper;
- backoff входит в OverallTimeout;
- attempt log записывается до ожидания с `retry_scheduled=true`;
- cancellation во время ожидания возвращает `RetryInterruptedError`.

Observer сразу записывает server `blockedUntil` в limiter. Retry middleware
ждёт только local `BackoffDelay`; следующий `Acquire` ждёт оставшуюся часть
server block. Поэтому суммарная пауза близка к:

```text
max(local backoff, server block)
```

а не к их сумме. Если local backoff уже переждал server block, следующий
`Acquire` проходит без дополнительной задержки.

### 22.5. MaxAttempts

`MaxAttempts` включает первый запуск attempt chain:

```
MaxAttempts=1 -> без повторов
MaxAttempts=3 -> максимум три pipeline attempts и не более трёх HTTP calls
```

Redirects запрещены, nested application retry отсутствует. Стандартный
`http.Transport` сохраняет только оговорённое в разделе 10.1 внутреннее
поведение на уровне reused connection; оно не является WB attempt loop.

### 22.6. Exhausted и interrupted

`RetryExhaustedError` создаётся только если последний outcome был retry-eligible,
но следующий attempt запрещён глобальным `MaxAttempts`. Он сохраняет последний
safe cause/status через `Unwrap`. При `MaxAttempts=1` wrapper появляется только
если первая ошибка действительно была eligible; non-retryable outcome
возвращается напрямую.

`RetryInterruptedError` создаётся, когда retry уже был запланирован, но
продолжение оборвалось до следующего `http.Client.Do`:

- cancellation/timeout во время backoff;
- CredentialsPreflight следующей попытки;
- следующий RateLimit admission;
- cancellation или expiry boundary после `Acquire`.

Ошибка хранит terminal interruption и предыдущий retryable safe cause через
`Unwrap() []error`. Если следующая попытка дошла до HTTP и получила новый
terminal outcome, возвращается этот outcome, а не `RetryInterruptedError`.
Raw URL, token и response body никогда не попадают в unwrap graph.

## 23. Logging middleware

### 23.1. Attempt log

Finalize logger создаёт одну terminal запись на каждую начатую попытку.

Allowlist полей:

- `component=wb`;
- `cabinet_id`;
- `operation`;
- `bucket_id`;
- `method`;
- `host`;
- `path`;
- `request_bytes`;
- `attempt`;
- `max_attempts`;
- `client_do_called`;
- `response_received`;
- `delivery_state`;
- `rate_limit_wait_duration`;
- `network_headers_duration`;
- `body_read_duration`;
- `decode_duration`;
- `attempt_duration`;
- optional `status_code`;
- `response_bytes`;
- `response_body_complete`;
- `response_oversized`;
- `body_close_error`;
- optional safe enum `rate_limit_diagnostic`;
- `failure_class`;
- `retry_scheduled`;
- `retry_reason`;
- `retry_backoff_duration`;
- `rate_limit_block_duration`.

### 23.2. Operation summary

Одна запись после завершения `DoJSON`:

- cabinet ID;
- operation;
- total duration;
- attempts started;
- HTTP calls;
- responses received;
- retries scheduled;
- retry interrupted;
- final status;
- final failure class;
- success.

`OperationLogging` является самым внешним слоем и начинает diagnostic lifecycle
до проверки caller context. Поэтому даже pre-canceled call и local Prepare
error дают ровно одну summary с `attempts_started=0` и `http_calls=0`.
Для невалидной operation используется фиксированный safe placeholder, а не
непроверенное caller value. Request cancellation не отменяет саму синхронную
запись logger-а.

### 23.3. Запрещённые данные

Никогда не логируются:

- raw token;
- encoded token;
- `Authorization`;
- display cabinet name;
- `sid`;
- полный URL;
- query values;
- request body;
- response body или preview;
- полный набор headers;
- raw `*url.Error`, если он содержит URL/query.

Wire dump через `httputil.DumpRequestOut` и `DumpResponse` не используется в
production middleware.

### 23.4. Уровни

- successful attempt без retry: Debug;
- retryable attempt: Warn;
- final API/transport failure: Error;
- admission cancellation caller-ом: Debug;
- admission timeout/queue rejection не от caller-а: Warn;
- malformed rate headers: Warn diagnostic без raw values.

Уровни фиксируются тестами, но не влияют на retry result.

## 24. Error model

Минимальный набор typed errors:

```
ConfigError
CabinetConfigError
UnknownCabinetError
CabinetUnavailableError
UnsupportedTokenTypeError
ExpiredCredentialsError
CredentialsRotationDueError
ReadOnlyCredentialsError
MiddlewareContractError
UnsupportedDefaultTransportError
OperationValidationError
RequestValidationError
RequestEncodingError
RequestTooLargeError
QueryTooLargeError
URLTooLongError
ResponseTargetError
AdmissionError
AttemptTimeoutError
OverallTimeoutError
SafeTransportError
ResponseTooLargeError
ResponseReadError
ResponseDecodeError
APIError
UncertainOutcomeError
RetryExhaustedError
RetryInterruptedError
```

Каждый `Error()` строится из allowlisted metadata. Unsafe nested errors
проходят redaction boundary до публикации.

Known safe causes сохраняют `errors.Is/As`. Raw `*url.Error` с query не должен
быть доступен через public unwrap chain.

## 25. Конфигурация

### 25.1. Runtime ENV

| Environment | Default | Назначение |
|---|---:|---|
| `WB_API_BASE_URL` | `https://content-api.wildberries.ru` | разрешённый Content origin |
| `WB_API_TIMEOUT` | `60s` | OverallTimeout |
| `WB_API_RATE_LIMIT_WAIT_TIMEOUT` | `30s` | одно admission ожидание |
| `WB_API_ATTEMPT_TIMEOUT` | `20s` | одна полная попытка |
| `WB_API_RETRY_MAX_ATTEMPTS` | `3` | глобальное число attempts |
| `WB_API_RETRY_BASE_DELAY` | `250ms` | base backoff |
| `WB_API_RETRY_MAX_DELAY` | `5s` | backoff cap |
| `WB_API_MAX_SERVER_RETRY_DELAY` | `1m` | safety bound server delay |
| `WB_API_MAX_SERVER_RESET_DELAY` | `2m` | safety bound полного burst reset |
| `WB_API_MAX_QUERY_SIZE` | `16KiB` | encoded query bound |
| `WB_API_MAX_URL_SIZE` | `32KiB` | полный request URL bound |
| `WB_API_MAX_REQUEST_BODY_SIZE` | `32MiB` | request bound |
| `WB_API_MAX_RESPONSE_BODY_SIZE` | `32MiB` | success response bound |
| `WB_API_MAX_ERROR_BODY_SIZE` | `4KiB` | private error preview |
| `WB_API_MAX_LIMITER_KEYS` | `1024` | cardinality одного Client |
| `WB_API_MAX_WAITERS_PER_LIMITER` | `256` | bounded queue |
| `WB_API_CREDENTIAL_EXPIRY_WARNING` | `168h` | startup warning до effective expiry |

Независимо от ENV действуют compile-time ceilings:

```text
hardMaxRequestBodySize  = 64 MiB
hardMaxResponseBodySize = 64 MiB
hardMaxErrorBodySize    = 1 MiB
hardMaxQuerySize        = 64 KiB
hardMaxURLSize          = 128 KiB
hardMaxLimiterKeys      = 2048
```

Фиксированная Personal auth policy первой версии:

```text
personalTokenSafetyMargin = 120h
```

Margin намеренно не изменяется через ENV: клиент прекращает использовать token
за пять суток до указанного WB `exp`.

Большие payload требуют отдельного streaming contract. Увеличение hard ceiling
проходит отдельное security/performance review.

### 25.2. Credentials ENV

| Environment | Обязательность | Назначение |
|---|---|---|
| `WB_API_CABINETS` | да | ordered cabinet IDs |
| `WB_API_CABINET_<ID>_NAME` | да | display name |
| `WB_API_CABINET_<ID>_TOKEN` | да | raw Personal JWT |

Других credential variables нет. Неизвестный/orphan credential key
игнорируется с safe startup diagnostic; value не выводится. Если это опечатка
обязательного ключа, соответствующий listed cabinet будет отдельно rejected за
missing NAME/TOKEN.

### 25.3. Config validation

- origin должен быть exact HTTPS WB Content origin;
- userinfo, port, query и fragment в BaseURL запрещены;
- duration положительные и согласованы;
- `AttemptTimeout <= OverallTimeout`;
- `RateLimitWaitTimeout <= OverallTimeout`;
- MaxAttempts ограничен `1..5`;
- при `MaxAttempts > 1` выполняется `RetryBaseDelay > 0`;
- `RetryMaxDelay >= RetryBaseDelay`;
- `RetryMaxDelay <= OverallTimeout`;
- `MaxServerRetryDelay > 0` и не переполняет `time.Duration`;
- `MaxServerRetryDelay >= max(registered BucketPolicy.Interval)`;
- `MaxServerResetDelay > 0`, не переполняется и не меньше
  `max(Interval * Burst)` зарегистрированных policies;
- request/response/error body limits положительны, помещаются в `int64`, а
  `limit+1` не переполняется;
- body limits не превышают compile-time ceilings;
- `MaxErrorBodySize <= MaxResponseBodySize`;
- query/URL limits положительны, overflow-safe, не выше hard ceilings и
  `MaxQuerySize < MaxURLSize`;
- limiter key/waiter limits положительны и не превышают compile-time ceilings;
- `CredentialExpiryWarning` находится в диапазоне `1h..720h`;
- при сборке client overflow-safe проверяется
  `MaxLimiterKeys >= distinctPersonalSIDCount * len(registeredBucketPolicies)`,
  поэтому все valid cabinets могут использовать все зарегистрированные Content
  buckets; для текущего snapshot это максимум `128 * 7 = 896` keys;
- неизвестные/orphan credential variables превращаются в safe load issues, но
  не останавливают загрузку valid cabinets;
- manual invalid Config в `NewClient` проверяется повторно.

Body-size ENV разбирается отдельным strict `ByteSize` decoder с поддержкой
только документированных `KiB`/`MiB`; произвольная строка не передаётся напрямую
в integer config field. Нулевой production backoff запрещён; tests используют
virtual time seam.

## 26. Безопасность ENV

Текущий root `Makefile` выполняет `include .env` и глобальный `export`. Поэтому
WB credentials нельзя добавлять в общий `.env`: они попадут во все дочерние
make recipes и контейнерные команды.

Для local development вводится отдельный untracked файл `.env.wb`, который
загружается только в environment дочернего процесса `wb-service-run`.
Application по-прежнему получает обычные environment variables; меняется только
способ их безопасной передачи процессу.

`.env.wb` является данными, а не shell script. Его запрещено загружать через
`source`, `.`, `eval`, Make `include` или конструкцию `export $(...)`.
Local launcher использует strict dotenv parser без command substitution,
variable expansion и shell evaluation, проверяет grammar keys и передаёт
готовый `[]string{"KEY=value", ...}` только дочернему `wb-service` process.
Production binary по-прежнему читает только настоящий process environment и
не открывает `.env.wb` сам.

Разрешённый file grammar:

```text
blank line
# full-line comment
[A-Z][A-Z0-9_]*=<literal bytes до LF>
```

Parser делит строку по первому `=`, отклоняет BOM, CR, NUL, duplicate key,
`export`, quotes, escapes, whitespace вокруг key/`=` и inline comments. Значение
не trim-ится и не интерполируется. Более сложное display name должно передаваться
настоящим deployment environment, а не расширением mini-language файла.

До parse launcher fail-closed проверяет `.env.wb`:

- `Lstat`/no-follow open: это regular file, не symlink/device/directory;
- после open `fstat` подтверждает тот же file, закрывая TOCTOU race;
- owner равен effective UID процесса;
- `(mode & 0o077) == 0`;
- размер не больше compile-time `hardMaxWBEnvFileSize = 1 MiB`;
- разрешены только keys с prefix `WB_API_`.

Launcher строит deduplicated child environment map. Все обычные parent variables
наследуются один раз; любое совпадение `WB_API_*` key между parent environment
и `.env.wb` является startup error, а не неявным override. Это гарантирует, что
у дочернего процесса существует ровно одно значение каждого credential key.
Diagnostics называют только safe key/path/reason и не содержат value.

Перед размещением real credentials:

```bash
chmod 600 .env.wb
```

Обязательные правила:

- `.gitignore` явно содержит `.env.wb`;
- реальные tokens никогда не добавляются в `.env.example` или
  `.env.wb.example`;
- examples содержат только явно фиктивные placeholders;
- Makefile не include-ит и не global-export-ит `.env.wb`;
- local dotenv parser никогда не исполняет содержимое файла;
- local run target загружает `.env.wb` только для процесса приложения и не
  печатает environment;
- CI не печатает `env`;
- panic/error не содержит исходную ENV value;
- config structs с tokens не передаются в logger;
- process dumps и доступ к environment считаются privileged;
- при компрометации token отзывается в WB, ENV обновляется, процесс
  перезапускается;
- production deployment должен inject-ить variables только в WB service process
  через secret mechanism платформы, даже если application contract остаётся
  ENV.

Официальные требования WB:

- [система авторизации](https://dev.wildberries.ru/docs/openapi/api-information);
- [декодирование токенов](https://dev.wildberries.ru/knowledge-base/articles/019d49a1-1540-76f6-befe-726633dc11be);
- [лимиты запросов WB API](https://dev.wildberries.ru/knowledge-base/articles/019d49a1-28ca-7735-bf2f-98210695abc7).

Deployment precondition: Personal token допустим только для собственной или
on-premise интеграции продавца. Если эти 50–60 токенов принадлежат сторонним
продавцам и обрабатываются облачным партнёрским сервисом, выбранная схема не
соответствует правилам WB и этот план нельзя применять без отдельного изменения
типа авторизации. Код не пытается обойти это ограничение.

## 27. Dependency injection

Production dependencies:

- logger;
- base `http.RoundTripper`;
- clock;
- timer factory;
- deadline factory;
- random source;
- rate-limit state;
- cabinet registry.

Public API не должен позволять заменить safety-critical middleware. Internal
test constructor принимает fakes:

```go
type Dependencies struct {
	Transport  http.RoundTripper
	Clock      Clock
	Timers     TimerFactory
	Deadlines DeadlineFactory
	Random     Random
	Logger     Logger
}
```

```go
type DeadlineFactory interface {
	WithDeadline(
		context.Context,
		time.Time,
	) (context.Context, context.CancelFunc)
}
```

Nil dependencies возвращают constructor error, а не panic.

## 28. Concurrency и lifecycle

- `Client` immutable после construction;
- `CabinetRegistry` immutable;
- `CabinetClient` безопасен для concurrent use;
- registry из 50–60 кабинетов не создаёт goroutine/ticker на кабинет; limiter
  использует shared clock/timer machinery только при фактическом ожидании;
- credentials копируются как opaque value;
- request/attempt state никогда не разделяется между goroutines;
- caller не изменяет request DTO, query или response target конкурентно с
  `DoJSON`; Prepare сериализует/copy-ит inputs один раз и дальше не хранит
  mutable caller references, кроме закрытого final response target;
- shared limiter имеет собственную синхронизацию;
- production composition root создаёт ровно один `Client` на process и раздаёт
  его всем features/CabinetClient;
- два независимых вызова `NewClient` создают независимые limiter realms и могут
  умножить burst; это явно запрещено production wiring contract, а не скрыто
  process-global singleton-ом;
- logger thread-safe;
- один client владеет transport connection pool;
- `CloseIdleConnections` делегируется owned client/transport;
- закрытие idle connections не отменяет in-flight operations;
- token hot reload отсутствует, поэтому data race на credentials невозможен.

## 29. Целевая структура файлов

```
internal/core/transport/wb/
├── client.go
├── build_info.go
├── config.go
├── cabinet_config.go
├── cabinet_registry.go
├── dependencies.go
├── executor.go
├── terminal.go
├── request.go
├── response.go
├── errors.go
├── ratelimit_registry.go
├── internal/
│   ├── auth/
│   │   ├── credentials.go
│   │   └── token_claims.go
│   └── pipeline/
│       ├── operation.go
│       ├── attempt.go
│       ├── result.go
│       ├── report.go
│       ├── headers.go
│       ├── query_plan.go
│       ├── request_plan.go
│       └── response_plan.go
├── middleware/
│   ├── operation_chain.go
│   ├── attempt_chain.go
│   ├── finalize_chain.go
│   ├── operation_logger.go
│   ├── prepare.go
│   ├── retry.go
│   ├── credentials_preflight.go
│   ├── ratelimit.go
│   ├── attempt_timeout.go
│   ├── auth.go
│   ├── trace.go
│   ├── decode.go
│   ├── classify.go
│   ├── attempt_logger.go
│   └── metrics.go
├── policy/
│   ├── bucket_id.go
│   ├── bucket_policy.go
│   ├── retry_mode.go
│   └── content/
├── ratelimit/
│   ├── registry.go
│   ├── limiter.go
│   ├── acquire.go
│   ├── headers.go
│   ├── response.go
│   └── errors.go
└── retry/
    ├── classifier.go
    ├── backoff.go
    └── errors.go
```

Старые файлы `middleware/middleware.go` и `middleware/logger.go` не сохраняются
в текущем виде: package остаётся, но его контракт полностью заменяется.

`requestmeta` удаляется после переключения на typed pipeline.

## 30. Направление зависимостей

```
policy/content -> policy
ratelimit      -> policy
internal/auth  -> standard library only
internal/pipeline -> internal/auth + policy
retry          -> policy + internal/pipeline safe enums
middleware     -> internal/auth + internal/pipeline + policy + ratelimit + retry
wb root        -> internal/auth + middleware + internal/pipeline + policy/content
```

Middleware не импортирует root `wb`. Neutral pipeline types находятся в
`wb/internal/pipeline`, чтобы не возникал import cycle.

Opaque credentials и structural token parser находятся в `wb/internal/auth`.
Root registry, pipeline и Auth middleware импортируют этот leaf package; leaf
package никогда не импортирует root или middleware.

Logger получает только `AttemptReport`/`OperationReport`, а не
`AttemptState`.

## 31. Последовательность реализации

### Этап 0. Зафиксировать baseline

Действия:

1. Сохранить результаты текущих tests.
2. Найти все consumers `NewClient`, `ForCredentials`, `DoJSON`,
   `middleware.Chain` и `requestmeta`.
3. Зафиксировать текущие изменения dirty worktree и не перезаписывать
   unrelated user changes.
4. Добавить compile-time tests текущих public contracts.
5. Зафиксировать официальный Content operation/policy snapshot.
6. Для каждой candidate mutation retry policy сохранить official evidence;
   неоднозначные операции fail-closed перевести в `RetryDisabled`.

Gate:

- известен полный список consumers;
- новый path не включён частично;
- baseline tests воспроизводимы.

### Этап 1. Расширить transport config

Действия:

1. Добавить три timeout.
2. Добавить retry/body/limiter bounds.
3. Добавить query/full-URL bounds.
4. Добавить strict origin validation.
5. Создать `ByteSize` parser.
6. Установить compile-time ceilings.
7. Добавить exact env mapping tests.

Gate:

- invalid config отклоняется до client publication;
- manual Config и ENV проходят одну validation;
- config errors не содержат ENV values.

### Этап 2. Реализовать cabinet ENV loader

Действия:

1. Добавить `CabinetID`.
2. Разобрать `WB_API_CABINETS`.
3. Динамически загрузить NAME/TOKEN.
4. Зафиксировать Personal-only contract без mode variable.
5. Проверить объём 50–60 и hard limit 128 кабинетов.
6. Проверить bounds, duplicate ID/name/raw token, legacy и orphan variables.
7. Собрать immutable registry из valid entries и safe load report из rejected.
8. Fail startup только при fatal global config или zero valid entries.
9. Добавить safe config errors и per-cabinet reason codes.

Gate:

- несколько кабинетов загружаются детерминированно;
- 60 валидных Personal entries загружаются одним registry;
- `59 valid + 1 invalid` публикует 59 cabinets и один safe issue;
- zero valid cabinets останавливает startup;
- ни одна ошибка не содержит raw/encoded tokens;
- tests используют fake lookup без изменения process ENV.

### Этап 3. Реализовать token claims и credentials

Действия:

1. Добавить bounded JWT structural parser.
2. Разобрать allowlisted claims.
3. Проверить exact Personal contract `acc=3/for=self/t=false`.
4. Проверить Content permission/read-only.
5. Проверить WB `exp` и effective expiry с safety margin `120h`.
6. Проверить Personal `sid`.
7. Добавить безопасное startup-предупреждение о скором expiry.
8. Fail-closed отклонять любое отклонение от Personal contract.
9. Сделать fields credentials закрытыми.
10. Реализовать redacted `String/GoString`.

Gate:

- Personal contract покрыт;
- любое отклонение от Personal contract rejected;
- invalid/rotation-due token отклоняет только свой cabinet;
- malformed JWT не panic-ует;
- token prefix/whitespace/CRLF rejected;
- signature verification не заявлена и не имитируется.

### Этап 4. Добавить Cabinet Client API

Действия:

1. Добавить параллельные internal registry/client types без переключения
   текущего public constructor.
2. Добавить новый `ForCabinet` path за недостижимым из production composition
   seam.
3. Добавить safe `Cabinets`.
4. Сохранить legacy `ForCredentials`, constructor и `http.Client.Timeout` до
   атомарного этапа 11.
5. Создать shared limiter для всех новых cabinet clients.
6. Добавить `CloseIdleConnections`.

Gate:

- новый internal path выбирает только ID;
- unknown ID typed;
- token не имеет getter;
- concurrent lookup проходит race detector.

### Этап 5. Создать pipeline primitives

Действия:

1. Создать operation/attempt/finalize contracts.
2. Создать immutable prepared operation.
3. Создать fresh attempt state.
4. Создать safe reports.
5. Создать headers observer.
6. Реализовать chain builders.
7. Проверять nil и duplicate middleware IDs.
8. Зафиксировать fixed order snapshot test.

Gate:

- package компилируется без import cycle;
- ordinary middleware не может запустить второй send через public contract;
- safe report не содержит credentials/body/query.

### Этап 6. Harden Content catalog и limiter

Действия:

1. Сделать Content operations opaque.
2. Зарегистрировать все bucket policies атомарно.
3. Добавить limiter key `(sid, BucketID)`.
4. Реализовать bounded FIFO.
5. Ограничить key cardinality.
6. Реализовать необратимый `Acquire` перед каждым наблюдаемым
   `http.Client.Do`.
7. Harden exact WB rate headers.
8. Добавить conservative `429` fallback.
9. Добавить clock/timer seams.

Gate:

- exact catalog snapshot;
- одинаковый `sid` делит quota, разные `sid` изолированы;
- no double Acquire для одного send;
- cancellation churn без leaks;
- race tests проходят.

### Этап 7. Prepare, request и ResponsePlan

Действия:

1. Валидировать operation и target до Acquire.
2. Реализовать catalog-owned RequestPlan и доказать upper bound до marshal.
3. Marshal request один раз и проверить actual size.
4. Оценить query до encoding и проверить query/full-URL limits.
5. Создать immutable URL/template.
6. Реализовать attempt-local response target.
7. Реализовать commit без частичной мутации.
8. Добавить JSON response modes.

Gate:

- local errors не расходуют limiter token;
- request bytes идентичны между attempts;
- oversized request graph отклоняется до `json.Marshal`;
- encoded query/full URL exact boundaries покрыты;
- failed decode не меняет caller target;
- exact size boundaries покрыты.

### Этап 8. Реализовать attempt middleware

Действия:

1. CredentialsPreflight middleware.
2. RateLimit Acquire middleware.
3. Headers observer.
4. AttemptTimeout middleware.
5. Auth middleware.
6. Trace middleware.
7. Decode middleware.
8. Classification middleware.
9. Finalize metrics/logger middleware.

Gate:

- fixed order соответствует разделу 6;
- ровно один успешный Acquire перед каждым наблюдаемым `http.Client.Do`;
- Observe до Read;
- auth exact на каждой попытке;
- logger получает retry decision;
- tokens отсутствуют в reports/logs.

### Этап 9. Реализовать terminal handler

Действия:

1. Создавать fresh request/body/headers.
2. Использовать owned `http.Client.Timeout=0`.
3. Запретить redirects.
4. Вызвать base RoundTripper только через client.
5. Читать response bounded.
6. Реализовать drain/Close ownership.
7. Обработать nil/dual response cases.
8. Материализовать result.

Gate:

- один client Do вызывает injected RoundTripper не больше одного раза;
- body закрыт на всех ветках;
- response не выходит наружу;
- truncated/oversized cases deterministic;
- transport fake видит byte-exact requests.

### Этап 10. Реализовать Retry middleware

Действия:

1. Добавить pure classifier matrix.
2. Добавить delivery state.
3. Добавить mutation uncertainty.
4. Добавить global MaxAttempts.
5. Добавить jitter/backoff.
6. Добавить bounds для `X-Ratelimit-Retry`.
7. Вызывать finalizer после decision.
8. Обрабатывать cancellation during backoff.
9. Вызывать commit только после final success.

Gate:

- `429 -> 200` exact event order;
- body/decode safe retries работают;
- mutation не дублируется;
- retry interrupted корректно отражён;
- nested attempts отсутствуют.

### Этап 11. Атомарно переключить DoJSON

Действия:

1. Собрать production stack в `NewClient`.
2. Одним change set переключить public constructor на registry/logger/build и
   owned `http.Client.Timeout=0`.
3. Переключить `CabinetClient.DoJSON` тем же change set.
4. Не оставлять feature flag между legacy/new path.
5. Удалить `ForCredentials` consumers и сам legacy API.
6. Удалить старый single-send code.
7. Удалить `requestmeta`.
8. Заменить старый RoundTripper middleware package новым typed package.

Gate:

- в runtime достижим только новый stack;
- поиск не находит старый middleware type;
- public code не может вызвать transport в обход executor;
- integration suite проходит.

### Этап 12. Composition root и ENV documentation

Действия:

1. Добавить fake-only `.env.wb.example` entries.
2. Добавить `.env.wb` в `.gitignore`.
3. Документировать `chmod 600 .env.wb`.
4. Реализовать bounded no-follow strict local dotenv launcher.
5. Не включать WB tokens в global Make export.
6. Загрузить cabinet registry один раз при старте.
7. Создать WB Client после logger.
8. Передать Client/registry будущему WB feature через constructor.
9. Добавить startup error без tokens.
10. Документировать ротацию Personal tokens.

Если WB feature ещё отсутствует, production wiring не создаёт неиспользуемый
client. Loader и registry остаются полностью протестированными и готовы к
подключению первым consumer-ом.

Gate:

- real token не находится в tracked files;
- fatal global config и zero valid registry fail-closed;
- per-cabinet credential failure не блокирует valid cabinets;
- production process создаёт ровно один shared WB Client;
- example содержит только placeholders;
- lifecycle client закрывается корректно.

### Этап 13. Обновить документацию

Действия:

1. Обновить `wb-transport-architecture.md` по фактическому коду.
2. Проверить отсутствие ссылок на уже удалённые superseded-планы.
3. Добавить package GoDoc.
4. Зафиксировать middleware order.
5. Зафиксировать ENV contract и rotation runbook.
6. Добавить troubleshooting 401/403/429 без вывода credentials.

Gate:

- code и docs описывают один flow;
- старый `RoundTripper` middleware нигде не объявлен целевым;
- все acceptance criteria выполнены.

## 32. Обязательная тестовая матрица

### 32.1. Cabinet ENV

- missing/empty cabinet list;
- unknown credential variable даёт safe issue и игнорируется;
- duplicate ID;
- invalid ID;
- exact 50 и 60 entries;
- exact 128 entries и 129 rejected;
- missing NAME/TOKEN;
- empty/oversized name;
- invalid UTF-8/control characters;
- duplicate display name;
- duplicate raw token отклоняет все конфликтующие entries без disclosure;
- orphan cabinet variable;
- total credential bytes exact `512 KiB` и `limit+1`;
- `59 valid + 1 invalid` публикует 59 entries и один reason code;
- zero valid entries возвращает fatal startup error;
- deterministic `Cabinets()` order;
- fake lookup isolation;
- EnvSource key enumeration без value disclosure;
- strict `.env.wb` parser: no source/expansion/quotes/duplicates/CRLF;
- `.env.wb` symlink/owner/mode/type/size checks;
- parent/file `WB_API_*` collision rejected;
- errors не содержат input values.

### 32.2. Token validation

- exact valid Personal claims;
- любое сочетание claims кроме exact Personal contract rejected;
- wrong `acc/for/t`;
- missing/invalid `sid`;
- expired token;
- effective expiry равен `exp - 120h`;
- `now == effective_expiry` rejected;
- token с `exp` в пределах safety margin rejected только для своего cabinet;
- crossing effective expiry в running process блокирует только этот cabinet до
  `Acquire`/HTTP и возвращает `CredentialsRotationDueError`;
- startup warning внутри `CredentialExpiryWarning` содержит только safe fields;
- missing/float/overflow `exp`;
- exact WB boundary `now == exp` rejected;
- missing Content bit;
- read-only capability;
- exact `1<<1` Content и `1<<30` read-only masks;
- unknown permission bits не дают права;
- malformed segments/base64/JSON;
- duplicate/trailing JSON values;
- token exact `8 KiB` и `limit+1`;
- leading/trailing whitespace;
- raw `Bearer `;
- CR/LF/NUL;
- raw/encoded token отсутствует в errors/logs.

### 32.3. Client registry

- valid multiple cabinets;
- unknown cabinet;
- declared rejected cabinet возвращает `CabinetUnavailableError` с safe code;
- concurrent `ForCabinet`;
- одинаковый `sid` делит Personal limiter;
- разные raw tokens с одинаковым `sid` разрешены и делят limiter;
- разные `sid` получают независимые limiter realms;
- 60 и 128 distinct `sid` регистрируют все 7 buckets без capacity error;
- display name не влияет на key;
- token rotation не меняет feature contract;
- invalid rotated token не блокирует другие cabinets;
- token getter отсутствует;
- public arbitrary transport/middleware option отсутствует;
- non-`*http.Transport` global default даёт error, а не panic;
- safe info не содержит claims.

### 32.4. Middleware stack

- exact operation order;
- exact attempt order;
- exact unwind order;
- duplicate ID;
- nil middleware;
- operation/attempt second `next` возвращает contract error без второго send;
- finalize second `next` подавляется с diagnostic;
- RateLimit вызывает next максимум один раз;
- credentials preflight происходит до Acquire;
- admission deadline ограничен effective credentials expiry;
- expiry race после Acquire не вызывает HTTP и не возвращает token;
- cancellation после Acquire не возвращает token;
- transport error после Acquire не возвращает token;
- admission failure останавливает inner chain;
- Retry единственный вызывает attempt повторно;
- finalizer один раз на attempt;
- operation logger один раз на call;
- response observer до first body read;
- canceled attempt context не пропускает headers observer;
- ошибка одного observer не пропускает остальные;
- finalizer после retry decision;
- finalizer и operation summary выполняются после canceled context;
- pre-canceled `DoJSON` даёт summary и zero attempts;
- `waitCancel` выполняется сразу после `Acquire`;
- safe report allowlist.

### 32.5. Request/Auth

- URL/query encoding;
- operation QueryPlan rejects unknown key/multiplicity/value;
- query/full URL exact limit и `limit+1`;
- path не меняет origin;
- GET body rejected;
- JSON marshal один раз;
- exact request limit;
- RequestPlan estimate property и oversized generation до marshal;
- custom request `MarshalJSON`/`TextMarshaler` rejected;
- fresh request instances;
- fresh header maps;
- fresh body readers;
- exact ContentLength и `GetBody=nil`;
- byte-identical body;
- PreparedOperation не выдаёт URL/header template/body bytes;
- one `Bearer` prefix;
- Personal exact headers;
- pre-existing `Authorization` и unknown auth headers rejected;
- conditional Content-Type;
- valid/invalid BuildInfo и deterministic `dev` User-Agent;
- no credentials in context.

### 32.6. Response lifecycle

- success;
- `204`;
- empty optional body;
- required empty body;
- exact response limit;
- limit+1;
- exact error preview;
- truncated body;
- nil body;
- dual response/error;
- Close на каждой ветке;
- Close до явного owned `attemptCancel()` при unwind;
- Close до next Acquire;
- bounded drain не больше `64 KiB`;
- drain отсутствует после read error;
- connection reuse при полном EOF.

### 32.7. Decode

- valid JSON;
- non-`2xx` никогда не decode/commit-ит target;
- `JSONRequired` empty body rejected;
- `JSONOptional` empty body заменяет caller на zero value;
- `NoContent` требует nil target и empty body;
- malformed JSON;
- type mismatch;
- trailing value;
- target nil/non-pointer;
- map/slice/struct target;
- failed attempt не мутирует caller;
- successful JSON/optional commit ровно один раз, `NoContent` ноль;
- non-zero map/slice/struct replacement одним root set;
- root `json.Unmarshaler` вызывается только на temporary target;
- deadline во время Decode запрещает commit;
- pure decode errors safe operation не повторяет;
- mutation invalid `2xx` uncertain;
- decode duration в report;
- JSON отсутствует в logs.

### 32.8. Rate limiter

- registration atomicity;
- known/unknown bucket;
- initial burst/refill;
- FIFO;
- bounded waiters;
- bounded keys;
- cancellation head/middle/tail;
- no double Acquire для одного send;
- no refund after send;
- Remaining clamp down;
- `429` block;
- malformed/overflow headers;
- `429` missing/zero/conflicting/over-bound Retry exact fallback;
- exact allowlist четырёх `X-Ratelimit-*` headers;
- generic `Retry-After` игнорируется;
- fallback;
- Observe до Read;
- concurrent Acquire/Observe/cancellation под race.

### 32.9. Retry

- all matrix outcomes/modes;
- mutation `RetryRateLimitOnly` невозможен без versioned evidence;
- exact MaxAttempts;
- `429 -> 200`;
- `503 -> 200` safe;
- `503` mutation terminal;
- temporary transport safe retry;
- pre-wire mutation error;
- possibly-sent mutation error;
- truncated `200`;
- invalid JSON `200`;
- status priority over body error;
- response too large;
- admission failure;
- attempt timeout;
- overall timeout;
- simultaneous timeout;
- cancellation during backoff;
- interruption на следующем CredentialsPreflight/Acquire;
- exhaustion semantics при `MaxAttempts=1`;
- multi-unwrap содержит только safe causes;
- `X-Ratelimit-Retry` cap;
- `X-Ratelimit-Reset` имеет отдельный cap и покрывает `Interval * Burst`;
- local backoff и server block не суммируются дважды;
- custom RoundTripper error по умолчанию `possibly_applied`;
- `provably_pre_wire` только для allowlisted standard transport phases;
- jitter overflow;
- finalizer decision/delay;
- retry exhausted/interrupted errors.

### 32.10. Logging

- одна startup `Error` на rejected cabinet содержит только ID/reason code;
- startup summary различает `ready` и `degraded`, counts согласованы с registry;
- `59 valid + 1 invalid` не логирует token/name/claims/parser error;
- one attempt entry;
- one operation summary;
- admission has `client_do_called=false`;
- durations present;
- status optional correctly;
- `retry_scheduled` semantics;
- `retry_interrupted` summary;
- cabinet ID present;
- cabinet name absent;
- token/sid absent;
- query/body/headers absent;
- raw transport URL absent;
- logger failure does not change request outcome.

### 32.11. Concurrency/fuzz

- one Client across goroutines;
- 60 CabinetClient используются конкурентно через один Client;
- idle registry из 60 cabinets не создаёт 60 background goroutines/tickers;
- все CabinetClient одного Client делят limiter, два Client независимы;
- one CabinetClient across goroutines;
- limiter race;
- cancellation churn;
- token JWT parser fuzz;
- cabinet env parser fuzz;
- URL/path fuzz;
- rate headers fuzz;
- backoff fuzz;
- response reader fuzz;
- no goroutine/timer leaks.

## 33. Verification

После каждого этапа:

```bash
gofmt -w <changed-go-files>
go test ./internal/core/transport/wb/...
go test -race ./internal/core/transport/wb/...
go vet ./internal/core/transport/wb/...
git diff --check
```

Финальный gate:

```bash
go test ./cmd/... ./internal/...
go test -race ./cmd/... ./internal/...
go vet ./cmd/... ./internal/...
```

Проверка отсутствия старого `RoundTripper` middleware:

```bash
rg 'type Middleware func\(next http\.RoundTripper\)' \
  internal/core/transport/wb

rg 'requestmeta|ForCredentials\(' \
  cmd internal \
  --glob '*.go'
```

Первый `rg` должен ничего не найти. При этом package
`internal/core/transport/wb/middleware` обязан существовать с новыми typed
contracts.

## 34. Критерии завершения

План считается выполненным, когда одновременно:

1. существует один WB execution path;
2. middleware stack типизирован и имеет фиксированный порядок;
3. Retry middleware владеет единым attempt loop;
4. RateLimit выполняется перед каждой отправкой;
5. AttemptTimeout начинается после admission;
6. terminal handler является единственным владельцем response body;
7. RoundTripper используется только под owned HTTP client;
8. новый request/body/header создаётся на каждую попытку;
9. safe Retry учитывает body-read и decode failures;
10. mutation uncertainty не приводит к слепому повтору;
11. attempt finalizer получает retry decision;
12. один лог соответствует одной полной попытке;
13. ENV штатно поддерживает 50–60 Personal cabinets и hard limit 128;
14. ошибка credentials одного cabinet не блокирует остальные valid cabinets;
15. token перестаёт использоваться за `120h` до WB `exp`;
16. feature-код выбирает cabinet ID и не получает token;
17. Auth формирует только `Authorization: Bearer <Personal token>`;
18. limiter partition использует `(sid, BucketID)`, а не token/name;
19. credentials immutable и redacted;
20. ENV errors/logs не раскрывают tokens;
21. `.env.wb` не находится в Git, не global-export-ится и имеет режим `0600`;
22. unit/integration/race/fuzz/lifecycle tests проходят;
23. production composition root создаёт один Client и не дробит local limiter.

## 35. Итоговый контракт

После реализации поток выглядит так:

```
declared ENV cabinets
  -> per-entry validation
  -> safe rejected-cabinet report
  -> immutable CabinetRegistry with valid credentials
  -> Client.ForCabinet(id)
  -> typed WB middleware stack
  -> Retry orchestrator
  -> RateLimit per observable SDK attempt
  -> Auth per fresh request
  -> terminal http.Client
  -> base RoundTripper
  -> Observe headers
  -> bounded Read/Drain/Close
  -> attempt-local Decode
  -> Classification
  -> Retry decision
  -> terminal attempt log
  -> conditional final Commit
```

Middleware остаётся центральным механизмом композиции. Executor остаётся
явным владельцем жизненного цикла. `RoundTripper` остаётся стандартным сетевым
адаптером. Ни один из этих компонентов не дублирует ответственность другого.
