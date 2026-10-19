# Конспект: core/logger (`internal/core/logger`)

Пакет `core_logger` обеспечивает централизованную инициализацию, форматирование и управление жизненным циклом структурированного логгера приложения на базе библиотеки [`go.uber.org/zap`](https://github.com/uber-go/zap).

---

## 1. Структура пакета

| Файл | Назначение |
| :--- | :--- |
| `config.go` | Структура конфигурации `Config` и её чтение из переменных окружения через `kelseyhightower/envconfig`. |
| `logger.go` | Структура `Logger`, конструктор `NewLogger`, метод закрытия ресурсов `Close` и утилита `Fallback`. |

---

## 2. Конфигурация (`config.go`)

### Параметры (`Config`)
Переменные считываются с префиксом `LOGGER_`:

```go
type Config struct {
    Level  string `envconfig:"LEVEL" default:"info"`     // Уровень: debug, info, warn, error
    Folder string `envconfig:"FOLDER" default:"out/logs"` // Путь к каталогу для файлов логов
}
```

### Конструкторы
- `NewConfig() (Config, error)` — парсит переменные окружения, возвращает ошибку в случае некорректных значений.
- `NewConfigMust() Config` — вызывает `NewConfig()`, паникует при ошибке. Используется в точке входа `cmd/wb-service/main.go`, так как без логгера запуск невозможен.

---

## 3. Структура `Logger` (`logger.go`)

```go
type Logger struct {
    *zap.Logger
    file *os.File
}
```

- **Встраивание `*zap.Logger`**: позволяет напрямую вызывать методы логгера (`logger.Info(...)`, `logger.Error(...)`, `logger.With(...)`).
- **Поле `file *os.File`**: хранит открытый дескриптор лог-файла на диске для гарантированного закрытия при остановке приложения.

---

## 4. Как работает `NewLogger(config Config)`

Конструктор выполняет 6 последовательных шагов:

### 1. Парсинг уровня логирования
```go
zapLvl := zap.NewAtomicLevel()
if err := zapLvl.UnmarshalText([]byte(config.Level)); err != nil {
    return nil, fmt.Errorf("unmarshal log level: %w", err)
}
```
- Использует `zap.AtomicLevel` — потокобезопасный уровень, поддерживающий смену на лету без перезапуска сервиса.
- Преобразует строку (`"info"`, `"debug"` и др.) в числовой уровень `zapcore.Level`.

### 2. Подготовка каталога логов
```go
if err := os.MkdirAll(config.Folder, 0755); err != nil {
    return nil, fmt.Errorf("mkdir log folder: %w ", err)
}
```
- Рекурсивно создаёт каталог (по умолчанию `out/logs`).
- Идемпотентен (не падает, если папка уже существует). Права `0755` дают полные права владельцу и чтение остальным.

### 3. Формирование имени и открытие файла
```go
timestamp := time.Now().UTC().Format("2006-01-02T15-04-05.0000")
logFilePath := filepath.Join(config.Folder, fmt.Sprintf("%s.log", timestamp))
logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY, 0644)
```
- Время берётся в **UTC** для независимости от локальной зоны сервера.
- Формат `2006-01-02T15-04-05.0000` использует дефисы вместо двоеточий (кроссплатформенность, совместимость с Windows/NTFS) и микросекунды для исключения коллизий имён при частых перезапусках.
- Флаги `O_CREATE|O_WRONLY` и права `0644` открывают файл только на запись.

### 4. Настройка форматирования (Encoder)
```go
zapConfig := zap.NewDevelopmentEncoderConfig()
zapConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02T15:04:05.0000")
zapEncoder := zapcore.NewConsoleEncoder(zapConfig)
```
- Берётся база для разработки (`NewDevelopmentEncoderConfig`): цветные уровни, читаемые имена.
- Переопределяется формат времени на понятный человеку строковый `YYYY-MM-DDTHH:MM:SS.0000`.
- Используется консольный текстовый кодировщик (`NewConsoleEncoder`).

### 5. Мультиплексирование потоков (Tee Core)
```go
core := zapcore.NewTee(
    zapcore.NewCore(zapEncoder, zapcore.AddSync(os.Stdout), zapLvl),
    zapcore.NewCore(zapEncoder, zapcore.AddSync(logFile), zapLvl),
)
```
- Паттерн **Tee** дублирует каждую запись одновременно в два приёмника:
  1. `os.Stdout` — терминал / стандартный вывод контейнера (Docker, Kubernetes).
  2. `logFile` — файл на диске для сохранения локальной истории работы сервиса.
- `zapcore.AddSync` оборачивает вывод с поддержкой `Sync()`.

### 6. Сборка логгера и caller
```go
zapLogger := zap.New(core, zap.AddCaller())
return &Logger{
    Logger: zapLogger,
    file:   logFile,
}, nil
```
- `zap.AddCaller()` автоматически вычисляет и выводит имя файла и номер строки (`main.go:45`), откуда вызвано логирование.

---

## 5. Завершение работы (`Close`)

```go
func (l *Logger) Close() {
    if l == nil {
        return
    }

    _ = l.Sync()
    if l.file == nil {
        return
    }

    if err := l.file.Close(); err != nil {
        fmt.Println("failed to close application logger", err)
    }
}
```

- Вызывается через `defer logger.Close()` в `main()`.
- Сбрасывает незаписанные буферы (`Sync()`).
- Безопасно закрывает дескриптор файла `file.Close()`.

---

## 6. Защитная утилита `Fallback`

```go
func Fallback(loggers ...*zap.Logger) *zap.Logger {
    for _, l := range loggers {
        if l != nil {
            return l
        }
    }
    return zap.NewNop()
}
```

- Принимает переменное число логгеров и возвращает первый не-`nil`.
- Если все переданные логгеры равны `nil`, возвращает безопасную заглушку `zap.NewNop()`, предотвращая паники разыменования указателя (`nil pointer dereference`) в компонентах с опциональным логгером.

---

## 7. Схема потока данных

```
  Вызов логирования: logger.Info("событие", zap.String("key", "val"))
                             │
                             ▼
                    zap.Logger (+Caller)
                             │
                             ▼
                     zapcore.NewTee
                     ┌───────┴───────┐
                     ▼               ▼
                zapcore.Core    zapcore.Core
                     │               │
                     ▼               ▼
                 os.Stdout        logFile (*os.File)
                 (Консоль)       (out/logs/*.log)
```

---

## 8. Логирование в Telegram Middleware (`telegram/middleware/logger.go`)

Для мониторинга входящих событий Telegram Bot API реализован специализированный middleware:
`internal/core/transport/telegram/middleware/logger.go`.

### Ключевые возможности
1. **Замер длительности обработки (`duration`)**: фиксирует время выполнения хендлера с точностью до микросекунд.
2. **Структурированный контекст апдейта (`updateLogFields`)**:
   - `update_id`: уникальный идентификатор события Telegram;
   - `update_type`: тип действия (`message`, `callback_query`, `edited_message`, `chosen_inline_result`);
   - `sender_id`: Telegram ID пользователя;
   - `message_id`: идентификатор сообщения;
   - `callback`: уникальный ID нажатой inline-кнопки.
3. **Безопасность и маскирование токенов (`redactTelegramBotTokens`)**:
   - В логах ошибок библиотеки Telebot могут содержаться URL вида `https://api.telegram.org/bot<TOKEN>/...`.
   - Функция маскирует токен бота (`/bot<redacted>/`), исключая утечку секретов в stdout и файлы логов.
4. **Разделение уровней событий**:
   - `context.Canceled` — логируется как `Debug` (штатная отмена при завершении сервиса или таймауте).
   - Ошибки выполнения — логируются как `Error` с маскированным сообщением об ошибке.
   - Успешная обработка — логируется как `Debug` для минимизации шума в проде.

---

## 9. Паттерны использования в коде сервиса

### А. Принцип внедрения зависимостей
Конструкторы сервисов принимают `*zap.Logger` напрямую, а не через глобальные переменные:
```go
type Service struct {
    logger *zap.Logger
}

func NewService(logger *zap.Logger) *Service {
    return &Service{
        logger: core_logger.Fallback(logger).Named("my_service"),
    }
}
```

### Б. Именование подсистем (`Named`)
Метод `.Named(...)` формирует древовидное имя источника логов (например, `main.wb_client.content`):
```go
subLogger := logger.Named("card_dispatcher")
subLogger.Info("dispatcher started")
// В выводе лога: "caller": "...", "logger": "main.card_dispatcher"
```

### В. Контекстные поля (`With`)
Для фиксации сквозных идентификаторов бизнес-операции:
```go
taskLogger := logger.With(
    zap.Int64("transfer_id", transferID),
    zap.String("seller_id", string(sellerID)),
)
taskLogger.Info("card publication started")
```

### Г. Типобезопасные поля (Zero Allocation)
Для сохранения производительности `zap` предпочтительно использовать типизированные конструкторы полей:
- `zap.String("key", str)`
- `zap.Int64("id", id)`
- `zap.Duration("elapsed", dur)`
- `zap.Error(err)` (автоматически формирует ключ `"error"`)
- Избегать `zap.Any(...)` на критических путях из-за накладных расходов на рефлексию.

---

## 10. Таблица переменных окружения

| Переменная | По умолчанию | Описание |
| :--- | :--- | :--- |
| `LOGGER_LEVEL` | `info` | Минимальный уровень логирования: `debug`, `info`, `warn`, `error`, `dpanic`, `panic`, `fatal`. |
| `LOGGER_FOLDER` | `out/logs` | Путь к каталогу на диске, куда сохраняются файлы логов сессий. |
