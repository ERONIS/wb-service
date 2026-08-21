# Учебный конспект по WB Core

Изучаемый пакет:

```text
internal/core/transport/wb
```

Этот файл — накопительный конспект. После каждого разобранного этапа сюда
добавляется краткая выжимка, ссылки на ключевые файлы и вопросы для
самопроверки.

## Формат обучения

1. Разбираем назначение механизма.
2. Проходим реализующий его Go-код: структуры, функции, ветвления и важные
   языковые приёмы.
3. Проверяем понимание на нескольких вопросах.
4. Переходим дальше только после сообщения «понял».
5. В конце собираем весь путь одного реального запроса и итоговую шпаргалку.

## План

- [x] Этап 1. Назначение, границы и карта пакетов.
- [ ] Этап 2. Конфигурация, создание `Clientset`, кабинеты и credentials.
- [ ] Этап 3. Каталог Content API: `Operation`, bucket policies и wire DTO.
- [ ] Этап 4. Контракт `Executor`, подготовка query/body и создание HTTP request.
- [ ] Этап 5. HTTP transport: общий connection pool, Authorization и attempt trace.
- [ ] Этап 6. Чтение response, декодирование, классификация ошибок и delivery state.
- [ ] Этап 7. Rate limiting, наблюдение заголовков WB, retry и backoff.
- [ ] Этап 8. Логирование, безопасность данных и полный lifecycle запроса.
- [ ] Этап 9. Реальное использование из `feature`, практический разбор и итоговая
  модель.

---

## Этап 1. Назначение, границы и карта пакетов

### Главная мысль

WB Core — это общая транспортная инфраструктура для безопасного выполнения
заранее описанных запросов к Wildberries Content API от имени одного из
настроенных кабинетов.

Удобная формула одного вызова:

```text
кабинет + операция + query/body + тип результата
                         ↓
              управляемый HTTP lifecycle
```

Пакет решает общие технические задачи: конфигурацию кабинетов, авторизацию,
HTTP, лимиты, повторные попытки, чтение ответа, классификацию ошибок и
логирование. Он не решает бизнес-задачи вроде подготовки карточки, выбора
целевого кабинета или управления переносом.

### Где находится граница

```text
feature/service
    решает, зачем и когда обращаться к WB
            ↓
feature/*/transport/wb
    выбирает разрешённую операцию и wire DTO
            ↓
internal/core/transport/wb
    безопасно выполняет технический lifecycle запроса
            ↓
Wildberries Content API
```

Следовательно:

- `feature` владеет сценарием и интерпретацией ответа;
- feature-адаптер выбирает конкретную операцию;
- WB Core знает, как надёжно доставить запрос, но не знает бизнес-смысла
  сценария;
- токен и произвольный `*http.Client` не передаются в feature-код.

### Карта пакета

В текущем состоянии WB Core состоит из 44 Go-файлов и примерно 5,5 тысяч строк.

```text
internal/core/transport/wb/
├── clientset.go, cabinet.go   публичный фасад и выбор кабинета
├── credentials.go            безопасная идентичность credential
├── types.go, errors.go        публичные типы и error-контракт
├── config/                    env-конфигурация и её валидация
├── api/content/v1/            каталог операций и JSON/query DTO
├── policy/                    неизменяемые правила операций и bucket-ов
├── client/                    оркестратор полного lifecycle запроса
│   ├── request/               подготовка URL, query и JSON body
│   └── response/              bounded read, close и JSON decode
├── flowcontrol/               limiter, server hints, retry backoff
└── transport/                 RoundTripper, Authorization, HTTP trace
```

### Роли основных слоёв

| Слой | На какой вопрос отвечает |
|---|---|
| корневой `wb` | Как получить клиент нужного кабинета? |
| `api/content/v1` | Как выглядит конкретная операция WB и её wire DTO? |
| `policy` | Какие неизменяемые правила есть у операции? |
| `client` | В каком порядке выполнить весь запрос? |
| `client/request` | Как один раз безопасно подготовить запрос? |
| `client/response` | Как ограниченно прочитать и декодировать ответ? |
| `flowcontrol` | Когда можно отправлять и можно ли повторить? |
| `transport` | Как физически отправить запрос с credential кабинета? |

### Направление зависимостей

Главный оркестратор — `client`. Он собирает узкие механизмы в один lifecycle:

```text
root wb
  ├── config
  ├── api/content/v1 ──→ policy
  ├── client
  │     ├── request ──→ policy
  │     ├── response
  │     ├── flowcontrol ──→ config + policy
  │     ├── policy
  │     └── transport
  └── transport
```

`request`, `response`, `flowcontrol` и `transport` не управляют всем запросом
самостоятельно. Каждый из них предоставляет ограниченный механизм, а порядок
их вызовов задаёт `client.APIClient.Execute`.

### Две точки входа в реальном приложении

1. `cmd/wb-service/main.go` один раз создаёт process-wide `Clientset` и передаёт
   его feature-адаптерам.
2. `feature/*/transport/wb` получает executor кабинета, выбирает операцию из
   `api/content/v1` и вызывает generic helper `client.ExecuteResponse[T]`.

Пример общего пути:

```text
feature transport
→ Clientset.ExecutorForCabinet(...)
→ contentapi.SubjectsOperation()
→ client.ExecuteResponse[SubjectsResponse](...)
→ APIClient.Execute(...)
→ request / limiter / transport / response
```

### Что важно не перепутать

- WB Core — не весь модуль Wildberries и не бизнес-сервис переноса карточек.
- `api/content/v1` хранит описание HTTP-контракта, но сам запрос не отправляет.
- `transport` — самый нижний HTTP-слой; полный алгоритм находится в `client`.
- Один logical request может включать несколько physical attempts при
  разрешённом retry.
- «Какую операцию выполнить» и «от имени какого кабинета» — две независимые
  части вызова.

### Файлы для повторения

- `cmd/wb-service/main.go`
- `internal/core/transport/wb/clientset.go`
- `internal/core/transport/wb/cabinet.go`
- `internal/core/transport/wb/client/api_client.go`
- `internal/core/transport/wb/client/execute_response.go`
- `internal/feature/cardprepare/transport/wb/catalog.go`

### Самопроверка

1. Почему выбор бизнес-сценария не находится в WB Core?
2. Чем `client` отличается от `transport`?
3. Где выбираются кабинет и конкретная операция?
4. Почему `api/content/v1` не является готовым HTTP-клиентом сам по себе?

---

## Этап 2. Config, Clientset, кабинеты и credentials

### Общая цепочка startup

```text
main
→ config.NewConfigMust()
  → config.NewConfig()
    → envconfig.Process(...)
    → loadCabinetsFromEnv()
    → Config.Validate()
→ wb.NewForConfig(ctx, ...)
  → prepareClientsetConfig(...)
  → transport.NewSharedTransport()
  → buildClientset(...)
    → один CabinetClient на каждый кабинет
  → verifyCredentialsAtStartup(...)
    → CredentialSnapshot(...)
    → POST Cards List с limit=1 для каждого кабинета
→ только проверенный Clientset возвращается вызывающему коду
```

### 1. Типы конфигурации

`config/types.go` содержит только данные:

```go
type CabinetID string

type CabinetConfig struct {
	ID    CabinetID
	Name  string
	Token string `json:"-"`
}

type Config struct {
	BaseURL  string
	Timeout  time.Duration
	Cabinets []CabinetConfig
}
```

`CabinetID` — отдельный named type поверх `string`. Поэтому ID сложнее случайно
перепутать с обычной строкой, но при необходимости доступно явное преобразование
`config.CabinetID(value)`.

Тег `json:"-"` запрещает стандартному JSON encoder сериализовать token. Это
один защитный слой, но он не защищает `%v`/`%#v`, поэтому форматирование также
переопределено отдельно.

### 2. Загрузка environment

`NewConfig` загружает общие поля через `envconfig` с префиксом `WB_API`:

```go
type environmentConfig struct {
	BaseURL string        `envconfig:"BASE_URL" default:"https://content-api.wildberries.ru"`
	Timeout time.Duration `envconfig:"TIMEOUT" default:"20s"`
}
```

Отсюда получаются `WB_API_BASE_URL` и `WB_API_TIMEOUT`.

Список кабинетов динамический, поэтому он читается вручную:

```text
WB_API_CABINETS=main,backup
        ↓ strings.Split
main   → WB_API_CABINET_MAIN_NAME / _TOKEN
backup → WB_API_CABINET_BACKUP_NAME / _TOKEN
```

`os.LookupEnv` выбран вместо `os.Getenv`, чтобы различать отсутствующую
переменную и присутствующую переменную с пустым значением.

ID и name очищаются через `strings.TrimSpace`. Token намеренно не очищается:
невидимый пробел в credential должен вызвать ошибку, а не молча изменить
секрет.

`NewConfigMust` — startup helper:

```go
func NewConfigMust() Config {
	config, err := NewConfig()
	if err != nil {
		panic(...)
	}
	return config
}
```

Паника здесь означает fail-fast: без корректных WB credentials приложение не
должно продолжать запуск в частично рабочем состоянии.

### 3. Валидация и защита token

`Config.Validate` последовательно проверяет:

- непустой `BaseURL` без внешних пробелов;
- положительный timeout;
- хотя бы один кабинет;
- непустые и уникальные ID;
- уникальные имена без учёта регистра и внешних пробелов;
- корректный UTF-8 и не более 128 символов в имени;
- непустой token без внешних пробелов и не более 16 KiB.

Для множества уже встреченных значений используется идиоматический Go set:

```go
seenIDs := make(map[CabinetID]struct{}, len(config.Cabinets))

if _, exists := seenIDs[cabinet.ID]; exists {
	return fmt.Errorf("cabinet ID %q is duplicated", cabinet.ID)
}
seenIDs[cabinet.ID] = struct{}{}
```

Пустая `struct{}` не хранит полезных данных — map используется только для
проверки присутствия ключа.

`CabinetConfig.String()` и `GoString()` заменяют непустой token на
`--- REDACTED ---`. Поэтому обычное и Go-syntax форматирование структуры не
выводит secret. Вместе с `json:"-"` это снижает риск утечки токена в лог.

### 4. Копирование и нормализация Config

`wb.NewForConfig` начинает с `prepareClientsetConfig`:

```go
configCopy := *configuration
configCopy.Cabinets = append(
	[]config.CabinetConfig(nil),
	configuration.Cabinets...,
)
```

Первая строка копирует struct, но slice в Go — это header с указателем на общий
backing array. Поэтому второй `append` создаёт отдельный массив кабинетов.
Иначе последующая сортировка могла бы изменить slice вызывающего кода.

После копирования код:

1. валидирует config;
2. нормализует display names;
3. сортирует кабинеты по ID;
4. строго разбирает production base URL.

`parseProductionBaseURL` разрешает только:

```text
https://content-api.wildberries.ru
```

Запрещены другой host, HTTP, port, userinfo, произвольный path, query и
fragment. Это одновременно фиксирует правильный endpoint и не позволяет
случайно отправить Authorization на чужой сервер.

### 5. Сборка Clientset

`Clientset` хранит четыре части состояния:

```go
type Clientset struct {
	cabinets         map[config.CabinetID]*CabinetClient
	cabinetInfos     []CabinetInfo
	credentialTokens map[config.CabinetID]string
	sharedTransport  *transport.SharedTransport
}
```

- `cabinets` нужен для быстрого поиска executor по ID;
- `cabinetInfos` — безопасный отсортированный публичный snapshot;
- `credentialTokens` остаётся внутри root package и нужен credential API;
- `sharedTransport` владеет общим connection pool.

`buildClientset` сначала создаёт один registry rate limiters, затем в цикле для
каждого кабинета строит:

```text
shared transport
→ cabinet Authorization wrapper
→ attempt trace wrapper
→ cabinet http.Client
→ APIClient
→ CabinetClient
```

Ключевой фрагмент:

```go
roundTripper := sharedTransport.RoundTripper(
	transport.Authorization(cabinetConfig.Token),
	transport.AttemptTrace(),
)

apiClient, err := client.NewAPIClient(
	cabinetConfig.ID,
	cabinetConfig.Name,
	baseURL,
	cabinetHTTPClient,
	rateLimiters,
	500*time.Millisecond,
	3,
	logger,
)
```

В реальном коде retry values передаются через constants. У каждого кабинета
свой credential-bound wrapper и `APIClient`, но network connection pool и
limiter registry общие для `Clientset`.

Сигнатура `buildClientset` использует named return `err`:

```go
func buildClientset(...) (_ *Clientset, err error) {
	defer func() {
		if err != nil {
			sharedTransport.CloseIdleConnections()
		}
	}()
```

Это гарантирует очистку уже созданных ресурсов, если ошибка возникла посередине
цикла. Результат строится по правилу all-or-error: частичный `Clientset`
наружу не возвращается.

После сборки `verifyClientsetCredentials` выполняет startup verification. Если
она завершается ошибкой, уже собранный `Clientset` также закрывает idle
connections и наружу возвращается `nil`.

### 6. Публичный доступ к кабинетам

`Cabinets()` возвращает копию slice:

```go
result := make([]CabinetInfo, len(clientset.cabinetInfos))
copy(result, clientset.cabinetInfos)
return result
```

Вызывающий код может менять полученный slice, не повреждая registry.

`ForCabinet(id)` делает lookup в map и оборачивает sentinel error через `%w`,
поэтому снаружи доступен `errors.Is(err, wb.ErrCabinetNotFound)`.

`ExecutorForCabinet` возвращает более узкий интерфейс `client.Executor`, хотя
внутри лежит `*CabinetClient`. Это уменьшает доступную feature-коду поверхность.

`CabinetClient.Execute` только делегирует вызов внутреннему executor:

```go
return cabinet.executor.Execute(ctx, operation, query, body, target)
```

Проверка на этапе компиляции:

```go
var _ client.Executor = (*CabinetClient)(nil)
```

Она ничего не выполняет runtime, но заставляет компилятор подтвердить, что
`*CabinetClient` реализует интерфейс `client.Executor`.

### 7. CredentialSnapshot

`CredentialSnapshot(now)` не возвращает raw token или raw seller ID. Он
декодирует только payload compact JWT и формирует:

```go
type CredentialIdentity struct {
	CabinetID           config.CabinetID
	SellerKey           SellerKey
	ContentRead         bool
	ContentWrite        bool
	CredentialExpiresAt time.Time
	ClientGeneration    ClientGeneration
}
```

Алгоритм `decodeCredentialToken`:

1. требует ровно три непустые JWT-части;
2. декодирует payload через base64url без padding;
3. ограничивает payload 16 KiB;
4. декодирует JSON с `UseNumber`, чтобы числа не превращались во `float64`;
5. запрещает второй/trailing JSON value;
6. проверяет UUID claims `id` и `sid`;
7. читает unsigned integer claims `s` и `exp`;
8. отклоняет истёкший token.

Подпись JWT локально не проверяется. Поэтому сразу после snapshot конструктор
выполняет удалённую проверку каждого credential:

```text
wb.NewForConfig(ctx, ...)
→ buildClientset(...)
→ verifyCredentialsAtStartup(ctx, now)
  → CredentialSnapshot(now)
  → PinnedExecutor(cabinetID, generation)
  → client.ExecuteResponse[CardsListResponse](CardsListOperation)
  → POST /content/v2/get/cards/list с limit=1
```

До сети WB Core требует установленный Content capability bit. Удалённый Cards
List probe подтверждает, что WB принимает сам token и разрешает ему безопасную
read-операцию Content API. Дополнительно проверяется базовая целостность raw
response: неотрицательный `cursor.total` и не более одной карточки при `limit=1`.

Специальный `/ping` для этого не используется: официальная документация
ограничивает его тремя запросами за 30 секунд и предупреждает, что
автоматизированное использование будет временно блокироваться.

Следовательно, ни один из публичных конструкторов `Clientset` не возвращает
непроверенный credential-bound client. Оба конструктора теперь принимают
`context.Context`, чтобы startup probe можно было отменить.

Это проверка на конкретный момент времени, а не вечная гарантия: после startup
token может истечь или быть отозван. Возможность mutation выводится из
аутентифицированного claim `s`, но специально выполнять mutation ради проверки
write-доступа нельзя.

### Что отдельно проверяет transfer

Проверка Core отвечает на вопрос «credential принимается Content API».
`transfer` решает другой вопрос: «безопасно ли использовать этот кабинет как
цель мутации».

`CredentialSnapshot` передаёт без raw secrets:

- `CabinetID`;
- `SellerKey` — digest seller ID;
- `ContentRead` и `ContentWrite`;
- время истечения;
- `ClientGeneration` — digest token.

`validateTargetCredential` требует корректный ID, непустые digests,
неистёкший credential и одновременно `ContentRead == true` и
`ContentWrite == true`. Поэтому валидный read-only token принимается Core для
read-сценариев, но отклоняется `transfer`, которому нужны mutation-права.

Registry запрещает дубликаты `CabinetID` и `SellerKey`. Второе условие не
позволяет дважды включить одного продавца под разными локальными именами
кабинетов.

Binding — сохранённая в PostgreSQL связь:

```text
CabinetID → SellerKey
```

При первом запуске она создаётся. При следующих startup строка блокируется
через `FOR UPDATE` и сравнивается с новым snapshot. Если прежний `CabinetID`
внезапно указывает на другого продавца, binding получает статус
`identity_mismatch`, а startup `transfer` завершается ошибкой. Это защищает от
случайной подмены token между кабинетами.

`binding_revision` имеет начальное значение `1` и представляет версию identity
binding; текущий код не разрешает автоматическую смену seller, поэтому сам его
не увеличивает. `capability_revision` также начинается с `1`, но увеличивается,
если изменились `ContentRead` или `ContentWrite`.

Из cohort name, порядка targets, seller keys, обеих revisions и capability
flags вычисляется SHA-256 snapshot revision. Она фиксирует точный набор целей,
с которым создавался transfer, чтобы незаметное изменение target cohort не
продолжило старый workflow с другими получателями.

Capabilities извлекаются bit masks:

```go
contentRead := claims.Properties&contentCapabilityBit != 0
contentWrite := contentRead && claims.Properties&readOnlyBit == 0
```

Оператор `&` оставляет интересующий бит. Запись разрешена, только если есть
Content capability и не установлен read-only bit.

### 8. SellerKey, ClientGeneration и pinning

Вместо чувствительных исходных значений наружу выходят SHA-256 digests:

```text
SellerKey        = hash("wb-seller-key:v1", sellerID)
ClientGeneration = hash("wb-client-generation:v1", rawToken)
```

Разные domain strings не позволяют одинаковому исходному значению дать
одинаковый digest в двух разных смыслах. Перед каждой частью в hash записывается
её длина, поэтому границы частей однозначны.

`PinnedExecutor(id, generation)` снова вычисляет generation текущего token и
сравнивает её с сохранённой workflow generation. Если token был заменён между
подготовкой операции и выполнением, executor не выдаётся. Это защищает долгий
workflow от незаметной смены credential.

### Инварианты этапа

- Конфигурация либо полностью корректна, либо приложение не стартует.
- Caller-owned config и `http.Client` не изменяются.
- Порядок кабинетов deterministic благодаря сортировке по ID.
- Частично собранный `Clientset` не публикуется.
- `Clientset` публикуется только после локальной проверки claims и успешного
  Content API Cards List probe каждого кабинета.
- Feature не получает raw token и raw seller ID.
- Все клиенты кабинетов используют один shared connection pool.

### Файлы для повторения

- `internal/core/transport/wb/config/types.go`
- `internal/core/transport/wb/config/env.go`
- `internal/core/transport/wb/config/validation.go`
- `internal/core/transport/wb/config/format.go`
- `internal/core/transport/wb/clientset.go`
- `internal/core/transport/wb/cabinet.go`
- `internal/core/transport/wb/credentials.go`

### Самопроверка

1. Почему после `configCopy := *configuration` отдельно копируется slice?
2. Почему token не обрабатывается через `strings.TrimSpace`?
3. Что общего и что отдельного у клиентов разных кабинетов?
4. Что гарантирует `var _ client.Executor = (*CabinetClient)(nil)`?
5. Почему декодирование JWT payload не доказывает валидность token?
6. От какой смены защищает `PinnedExecutor`?

---

## Этап 3. Operation, API catalog, bucket policies и wire DTO

### Главная модель

Конкретный WB endpoint представлен не методом клиента, а значением
`policy.Operation`:

```text
Operation =
    identity
  + HTTP contract
  + retry safety
  + rate-limit bucket
  + body modes
  + byte bounds
```

DTO отвечает за форму данных, `Operation` — за правила доставки этих данных.
Например, `SubjectsQuery` не содержит URL, а `SubjectsOperation()` не содержит
Go-тип результата. Вместе они образуют полный wire contract.

### 1. Почему существуют OperationSpec и Operation

В `policy/operation.go` есть две структуры:

```go
type OperationSpec struct {
	ID               OperationID
	Method           string
	Path             string
	BucketID         BucketID
	Kind             OperationKind
	RetryMode        RetryMode
	SuccessStatuses  []int
	RequestMode      BodyMode
	ResponseMode     BodyMode
	MaxRequestBytes  int64
	MaxResponseBytes int64
}

type Operation struct {
	id               OperationID
	method           string
	path             string
	// остальные поля также unexported
}
```

`OperationSpec` — mutable вход для конструктора. `Operation` — проверенное
значение с закрытыми полями, которое executor может безопасно использовать.

Цепочка создания:

```go
operation, err := policy.NewOperation(spec)
```

```text
OperationSpec
→ validateOperationSpec
→ buildOperation
→ immutable-by-API Operation
```

Поля `Operation` нельзя менять снаружи пакета `policy`. Доступ идёт через
методы `Method()`, `Path()`, `Kind()` и остальные getters.

Slice success statuses требует отдельной защиты:

```go
successStatuses: cloneSuccessStatuses(spec.SuccessStatuses)
```

и при чтении:

```go
func (operation Operation) SuccessStatuses() []int {
	return cloneSuccessStatuses(operation.successStatuses)
}
```

Без обеих копий вызывающий код мог бы изменить внутренний slice операции через
общий backing array.

### 2. Что валидирует NewOperation

`validateOperationSpec` объединяет четыре группы инвариантов.

Identity и modes:

- `ID` и `BucketID` непустые;
- kind только `read` или `mutation`;
- retry только `never` или `read_safe`;
- retry запрещён для mutation;
- request/response mode только `none` или `json`.

HTTP contract:

- разрешены только GET и POST;
- path начинается ровно с одного `/`;
- path не может быть absolute URL;
- в path запрещены host, query и fragment.

Success statuses:

- список не пуст;
- только диапазон 2xx;
- нет дубликатов.

Byte bounds:

- при `BodyModeNone` соответствующий max bytes обязан быть `0`;
- при `BodyModeJSON` соответствующий max bytes обязан быть положительным.

Эти правила проверяют согласованность manifest, но не бизнес-валидность DTO.
Например, `MaxCardsListPageSize == 100` не проверяется автоматически внутри
`Operation`; это обязанность feature/service, формирующего request.

### 3. Read/mutation не равны GET/POST

`OperationKind` описывает семантику, а HTTP method — wire protocol.

`Cards List` является POST, потому что принимает JSON body, но логически только
читает данные:

```go
Method:    http.MethodPost,
Kind:      policy.OperationKindRead,
RetryMode: policy.RetryModeReadSafe,
```

`Upload Cards` также является POST, но меняет состояние:

```go
Method:    http.MethodPost,
Kind:      policy.OperationKindMutation,
RetryMode: policy.RetryModeNever,
```

Именно kind и retry mode, а не HTTP method, разрешают executor повторить
операцию.

### 4. Статический API catalog

`api/content/v1/operations.go` содержит 17 операций:

| Группа | Операции |
|---|---|
| categories | parent categories, subjects, characteristics, brands |
| directories | colors, kinds, countries, seasons, VAT, TNVED |
| cards | limits, list, trash list, error list, upload, upload-add |
| media | save by links |

Большинство операций создаётся во время package initialization:

```go
subjectsOperation = mustOperation(policy.OperationSpec{
	ID:               operationIDSubjects,
	Method:           http.MethodGet,
	Path:             "/content/v2/object/all",
	BucketID:         bucketIDContentCommon,
	Kind:             policy.OperationKindRead,
	RetryMode:        policy.RetryModeReadSafe,
	SuccessStatuses:  []int{http.StatusOK},
	RequestMode:      policy.BodyModeNone,
	ResponseMode:     policy.BodyModeJSON,
	MaxRequestBytes:  0,
	MaxResponseBytes: maxSubjectsResponseBytes,
})
```

`mustOperation` вызывает `policy.NewOperation` и паникует при ошибке. Неверный
manifest — ошибка разработчика в статическом коде, поэтому приложение не должно
запускаться с ним.

Наружу возвращается готовое значение:

```go
func SubjectsOperation() policy.Operation {
	return subjectsOperation
}
```

Особый случай — endpoint с параметром в path:

```go
func SubjectCharacteristicsOperation(
	subjectID int64,
) (policy.Operation, error)
```

Он сначала требует положительный `subjectID`, затем безопасно строит path через
`strconv.FormatInt` и создаёт новую проверенную operation.

Каталог «закрытый» архитектурно: feature должен брать operations только из
`api/content/v1`. Но это правило не полностью обеспечено компилятором, потому
что `policy.NewOperation` экспортирован и технически доступен feature-коду.
Запрет зафиксирован контрактом и комментарием конструктора.

### 5. Rate-limit buckets

Операция содержит `BucketID`, а параметры bucket хранятся отдельно:

```go
type BucketSpec struct {
	ID         BucketID
	Interval   time.Duration
	Burst      int
	MaxWaiters int
}
```

В Content API catalog определено восемь buckets. Например:

```go
policy.BucketSpec{
	ID:         bucketIDCardsUpload,
	Interval:   6 * time.Second,
	Burst:      5,
	MaxWaiters: 256,
}
```

Операции с одним `BucketID` делят одну квоту внутри одного кабинета. При этом
одинаковый bucket разных кабинетов позже получит отдельный limiter.

`mustBucketSpec` проверяет статическую конфигурацию при package initialization.
`BucketSpecs()` возвращает копию slice, чтобы вызывающий код не изменил catalog:

```go
specs := make([]policy.BucketSpec, len(contentBucketSpecs))
copy(specs, contentBucketSpecs)
return specs
```

Точная механика `Interval`, `Burst` и `MaxWaiters` будет разобрана в этапе 7.

### 6. Transport byte bounds и semantic bounds

`bounds.go` содержит две категории ограничений.

Unexported byte bounds используются executor-ом:

```go
maxCardsListRequestBytes  = 64 * kibibyte
maxCardsListResponseBytes = 32 * mebibyte
maxUploadCardsRequestBytes = 10_000_000
```

Они защищают память и не позволяют бесконечно читать или отправлять body.

Exported semantic bounds использует feature-код:

```go
MaxCardsListPageSize       = 100
MaxUploadGroups            = 100
MaxVariantsPerGroup        = 30
MaxProductTitleRunes       = 60
MaxProductDescriptionRunes = 5000
```

Byte limit отвечает «сколько памяти допустимо», semantic limit — «принимает ли
такой request WB API».

### 7. Wire DTO и struct tags

DTO — прямое отображение JSON/query контракта WB без бизнес-логики.

Query DTO использует custom tag `url`:

```go
type SubjectsQuery struct {
	Locale   Locale `url:"locale,omitempty"`
	Name     string `url:"name,omitempty"`
	Limit    int    `url:"limit,omitempty"`
	Offset   int    `url:"offset,omitempty"`
	ParentID int64  `url:"parentID,omitempty"`
}
```

Этот tag будет обработан собственным encoder из `client/request`, а не
стандартной библиотекой.

JSON DTO использует стандартные `json` tags:

```go
type CardsListRequest struct {
	Settings CardsListSettings `json:"settings"`
}
```

`omitempty` означает «не сериализовать zero value». Поэтому pointer часто
нужен, чтобы различать «поле отсутствует» и «поле явно равно нулю»:

```go
WithPhoto *int `json:"withPhoto,omitempty"`
Price     *int64 `json:"price,omitempty"`
```

Anonymous embedding `ResponseMeta` поднимает его JSON-поля на верхний уровень:

```go
type SubjectsResponse struct {
	Data []Subject `json:"data"`
	ResponseMeta
}
```

Wire JSON имеет форму:

```json
{
  "data": [],
  "error": false,
  "errorText": "",
  "additionalErrors": null
}
```

`any` используется там, где WB допускает данные нескольких JSON-типов, например
в значении характеристики. Цена за гибкость — необходимость type switch или
дополнительной проверки в feature.

Named slice request:

```go
type UploadCardsRequest []UploadCardsGroup
```

сериализуется как JSON array, а не object.

### 8. Чего DTO и catalog не делают

- DTO не выполняет HTTP request.
- Struct tags не валидируют значения.
- `omitempty` влияет на encoding, но не подтверждает корректность данных.
- `ResponseMeta.Error` интерпретируется feature/service, не executor-ом.
- `APIErrorResponse` описан как wire type, но текущий executor не публикует
  декодированный non-2xx body вызывающему коду.
- Catalog определяет транспортные правила, но не реализует pagination или
  бизнес-workflow.

### Файлы для повторения

- `internal/core/transport/wb/policy/operation.go`
- `internal/core/transport/wb/policy/status.go`
- `internal/core/transport/wb/policy/retry.go`
- `internal/core/transport/wb/policy/bucket.go`
- `internal/core/transport/wb/api/content/v1/operations.go`
- `internal/core/transport/wb/api/content/v1/bucket.go`
- `internal/core/transport/wb/api/content/v1/bounds.go`
- `internal/core/transport/wb/api/content/v1/categories.go`
- `internal/core/transport/wb/api/content/v1/cards.go`

### Самопроверка

1. Зачем разделены `OperationSpec` и `Operation`?
2. Почему `Cards List` можно retry, хотя его HTTP method — POST?
3. Что произойдёт при ошибке в статическом manifest операции?
4. Для чего `SuccessStatuses()` возвращает новый slice?
5. Чем transport byte bound отличается от semantic API bound?
6. Что именно означает pointer в поле с `omitempty`?
