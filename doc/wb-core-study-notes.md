# Конспект изучения WB Core

## Этап 1. Назначение WB Core

`internal/core/transport/wb` — общий защищённый шлюз между feature-модулями
приложения и API Wildberries.

Разделение ответственности:

- feature-модуль решает, **что** нужно запросить у Wildberries;
- WB Core отвечает за то, **как** безопасно выполнить запрос.

WB Core хранит и проверяет токены кабинетов, связывает кабинет с продавцом,
формирует и отправляет HTTP-запросы, применяет rate limit и retry, разбирает
ответы и классифицирует ошибки.

Feature-код не получает сырой токен, прямой доступ к `http.Client` или
возможность отправлять запросы на произвольные URL. Он выбирает только заранее
описанную и разрешённую WB-операцию.

Упрощённый поток:

```text
Feature
  → разрешённая WB-операция
  → WB Core
  → Wildberries API
```

## Этап 2. Основные части WB Core

- `config` загружает и проверяет конфигурацию кабинетов.
- `identity` проверяет идентичность продавца и управляет безопасными bindings.
- `api` содержит закрытый каталог разрешённых операций Wildberries.
- `policy` описывает правила операций: метод, путь, retry, лимиты и статусы.
- `client` координирует полное выполнение логического запроса.
- `flowcontrol` управляет частотой запросов и ожиданием.
- `transport` выполняет отдельную HTTP-попытку.
- `Clientset` владеет кабинетами и общей инфраструктурой.
- `CabinetClient` является executor, привязанным к одному кабинету.

Упрощённая композиция:

```text
Clientset
  └── CabinetClient
        └── client
              ├── api + policy
              ├── flowcontrol
              └── transport
```

### Credentials и identity

`identity` содержит правила и абстракции идентификации. Корневой
`credentials.go` реализует их через конкретные возможности `Clientset`, Content
API `/ping` и General API `/seller-info`.

`credentials.go`:

- удалённо проверяет токен;
- получает подтверждённого продавца;
- предоставляет безопасный immutable snapshot без raw token и seller ID;
- проверяет `ClientGeneration`, не позволяя использовать executor от старого
  поколения credentials.

Интеграционный verifier находится в корневом `wb`, а не в `identity`, чтобы
`identity` не зависел от `Clientset`, WB API и HTTP client и не возникал цикл
импортов.

### Публичные ошибки

Корневой `errors.go` предоставляет `ClassifiedError` и `DeliveryState`:

- `NotDispatched` — запрос точно не отправлен;
- `ResponseReceived` — ответ WB получен;
- `UnknownDelivery` — неизвестно, был ли запрос принят WB.

`cardpublication` использует эти состояния для бизнес-решения: можно ли считать
операцию отклонённой, требуется ли сверка и безопасен ли повтор. WB Core сообщает
технический факт доставки, а feature интерпретирует его в своём процессе.

`ErrCabinetNotFound` и отдельные aliases кодов ошибок сейчас напрямую в feature
не используются.

### Небольшой рефакторинг

`CabinetInfo` перенесён из отдельного `types.go` в `clientset.go`, поскольку тип
используется публичным API `Clientset`. Принадлежность `wb.CabinetInfo` от этого
не изменилась: в Go её определяет пакет, а не файл.

## Этап 3. Назначение config

`internal/core/transport/wb/config` получает и локально проверяет настройки WB
Core до создания `Clientset`.

Состав пакета:

- `types.go` — модели конфигурации;
- `env.go` — чтение environment;
- `validation.go` — проверка значений;
- `format.go` — безопасное строковое представление.

Поток данных:

```text
Environment
  → env.go
  → Config и CabinetConfig
  → validation.go
  → Clientset
```

Проверяются наличие кабинетов и токенов, уникальность ID и имён, timeout и
безопасность base URL. `config` не обращается к WB и не подтверждает владельца
токена: это локальная структурная валидация.

Граница ответственности:

```text
config             → значение структурно корректно
credentialVerifier → WB принимает токен
identity           → токен связан с ожидаемым продавцом
```

## Этап 4. Модели config

`CabinetID` — отдельный тип поверх `string` и стабильный внутренний ключ
кабинета. Он не является seller ID Wildberries.

`CabinetConfig` содержит:

- `ID` — технический стабильный идентификатор;
- `Name` — изменяемое отображаемое имя;
- `Token` — секрет; поле исключено из JSON через `json:"-"`.

`Config` содержит общий `BaseURL`, `Timeout` и список `CabinetConfig`. Общие
настройки не дублируются для каждого кабинета.

Сырой token существует в `Config` только на этапе создания `Clientset`. Feature
не должен получать конфигурацию или token.

### Принятые решения

- Пока `CabinetID` задаётся явно и не зависит от имени, token или seller ID.
- В будущем для добавления кабинетов через Telegram лучше генерировать UUID,
  хранить кабинет и token в БД и отключать кабинет мягко вместо
  немедленного физического удаления.
- Текущий immutable `Clientset` для этого потребуется заменить или дополнить
  динамическим registry, но сейчас эта переработка отложена.
- `.env.wb` остаётся в корне проекта. Это файл запуска с секретами, а
  `internal/core/transport/wb/config` — пакет исходного Go-кода; смешивать их не
  следует.

## Этап 5. Clientset

`Clientset` — верхнеуровневый объект и внутренний composition root WB Core. Он
создаётся при запуске, собирает инфраструктуру и предоставляет доступ к
настроенным кабинетам.

Он владеет:

- реестром `CabinetClient`;
- безопасным списком `CabinetInfo`;
- credentials кабинетов и General API executors;
- identity registry;
- общим HTTP connection pool.

Основные точки доступа:

- `Cabinets()` возвращает копию безопасного списка ID и имён;
- `ForCabinet(id)` возвращает `CabinetClient` или `ErrCabinetNotFound`;
- `ExecutorForCabinet(id)` выдаёт узкий интерфейс `client.Executor`;
- pinned executor дополнительно проверяет поколение credentials.

Все кабинеты разделяют connection pool, но авторизация и rate limiting остаются
привязанными к конкретному кабинету. `Clientset` связывает `config`, `identity`,
`client`, `flowcontrol`, `transport` и `api`, однако делегирует им фактическую
подготовку и выполнение запросов.

## Этап 6. CabinetClient

`CabinetClient` — тонкая оболочка над generic executor, привязанная к одному
кабинету. Он хранит безопасные `ID` и `Name` и делегирует `Execute` внутреннему
executor. Сырого token в нём нет: Authorization встроен глубже в транспортную
цепочку при сборке клиента.

`Credential-bound` означает, что клиент всегда использует credentials выбранного
кабинета. `Generic` означает отсутствие endpoint-методов вроде `GetCards` или
`UploadCards`: клиент принимает только разрешённый `Operation`, query, body и
target результата.

Разделение ответственности:

```text
Clientset                   → выбирает кабинет
CabinetClient               → фиксирует кабинет и его credentials
feature/*/transport/wb      → выбирает конкретную операцию и DTO
внутренний executor         → выполняет операцию
```

Сначала изучается архитектурная карта всех компонентов. После неё выполняется
отдельный проход по реализации: поля, конструкторы, методы и реальные цепочки
вызовов. Внутренний код `Clientset` будет разобран первым во втором проходе.

## Этап 7. API catalog

`api/content/v1` и `api/general/v1` описывают поддерживаемые контракты двух
сервисов Wildberries. Суффикс `v1` фиксирует версию контракта.

Пакет содержит:

- query, request и response DTO внешнего API;
- функции закрытого каталога вроде `CardsListOperation`,
  `UploadCardsOperation` и `SellerInfoOperation`.

DTO отражают формат Wildberries, а не внутренние бизнес-модели. Общие DTO
централизованы в Core, чтобы feature-модули не дублировали JSON-контракты. При
необходимости feature transport преобразует внешний DTO в собственную модель.

Закрытый каталог не позволяет feature передавать произвольные method, URL и
правила выполнения. Feature выбирает заранее описанный `policy.Operation`, что
защищает Authorization и обеспечивает единые retry и rate-limit правила.

Сам `api` запросов не выполняет и не знает о token, `http.Client`, текущем
limiter или `Clientset`. Он только описывает разрешённые операции и данные.
