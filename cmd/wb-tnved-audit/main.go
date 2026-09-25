package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	wbtransport "github.com/ERONIS/wb-service/internal/core/transport/wb/transport"
)

const (
	defaultEndpoint  = "https://content-api.wildberries.ru/content/v2/get/cards/list?locale=ru"
	pageSize         = 100
	maxPageAttempts  = 5
	retryBaseDelay   = time.Second
	defaultUserAgent = "wb-service-tnved-audit/1.0"
)

type options struct {
	tokenEnv string
	envFile  string
	logFile  string
	output   string
	limit    int
	endpoint string
}

type cardsRequest struct {
	Settings cardsSettings `json:"settings"`
}

type cardsSettings struct {
	Sort   cardsSort   `json:"sort"`
	Cursor cardsCursor `json:"cursor"`
}

type cardsSort struct {
	Ascending bool `json:"ascending"`
}

type cardsCursor struct {
	UpdatedAt string `json:"updatedAt,omitempty"`
	NMID      int64  `json:"nmID,omitempty"`
	Limit     int    `json:"limit"`
}

type cardsResponse struct {
	Cards  []card      `json:"cards"`
	Cursor cardsCursor `json:"cursor"`
}

type card struct {
	NMID            int64            `json:"nmID"`
	SubjectID       int64            `json:"subjectID"`
	SubjectName     string           `json:"subjectName"`
	VendorCode      string           `json:"vendorCode"`
	Title           string           `json:"title"`
	Characteristics []characteristic `json:"characteristics"`
}

type characteristic struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type auditResult struct {
	Cards   []card
	Scanned int
}

type httpStatusError struct {
	statusCode int
	retryAfter time.Duration
	body       string
}

func (failure *httpStatusError) Error() string {
	return fmt.Sprintf("WB вернул HTTP %d: %s", failure.statusCode, failure.body)
}

func main() {
	opts := parseFlags()
	logger, closeLog, err := newAuditLogger(opts.logFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка создания лога:", err)
		os.Exit(1)
	}
	defer closeLog()

	logger.Printf(
		"event=start limit=%d output=%q token_env=%q env_file=%q endpoint=%q",
		opts.limit, opts.output, opts.tokenEnv, opts.envFile, opts.endpoint,
	)
	sharedTransport, err := wbtransport.NewSharedTransport()
	if err != nil {
		logger.Printf("event=failed error=%q", fmt.Errorf("создать HTTP transport: %w", err))
		os.Exit(1)
	}
	defer sharedTransport.CloseIdleConnections()
	client := &http.Client{
		Transport: sharedTransport.RoundTripper(),
		Timeout:   90 * time.Second,
	}
	if err := run(context.Background(), opts, client, logger); err != nil {
		logger.Printf("event=failed error=%q", err)
		os.Exit(1)
	}
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.tokenEnv, "token-env", "WB_API_CABINET_SHOP_000_TOKEN", "переменная окружения с токеном кабинета Илка")
	flag.StringVar(&opts.envFile, "env-file", ".env.wb", "env-файл, используемый, если token-env не задана")
	flag.StringVar(&opts.logFile, "log-file", "wb-tnved-audit.log", "диагностический лог без токенов")
	flag.StringVar(&opts.output, "output", "tnved-missing-ilka-all.csv", "путь к итоговому CSV")
	flag.IntVar(&opts.limit, "limit", 0, "максимальное количество карточек без ТН ВЭД; 0 — все")
	flag.StringVar(&opts.endpoint, "endpoint", defaultEndpoint, "адрес WB Content API")
	flag.Parse()
	return opts
}

func newAuditLogger(path string) (*log.Logger, func(), error) {
	if strings.TrimSpace(path) == "" {
		return log.New(os.Stderr, "", log.Ldate|log.Ltime|log.LUTC), func() {}, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	logger := log.New(io.MultiWriter(os.Stderr, file), "", log.Ldate|log.Ltime|log.LUTC)
	return logger, func() { _ = file.Close() }, nil
}

func run(ctx context.Context, opts options, client *http.Client, logger *log.Logger) error {
	if opts.limit < 0 {
		return errors.New("limit не может быть отрицательным")
	}
	if strings.TrimSpace(opts.output) == "" {
		return errors.New("output не должен быть пустым")
	}
	if client == nil {
		return errors.New("HTTP-клиент не настроен")
	}
	if logger == nil {
		return errors.New("логгер не настроен")
	}
	token := strings.TrimSpace(os.Getenv(opts.tokenEnv))
	if token == "" && strings.TrimSpace(opts.envFile) != "" {
		var err error
		token, err = readEnvValue(opts.envFile, opts.tokenEnv)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("прочитать %s: %w", opts.envFile, err)
		}
	}
	if token == "" {
		return fmt.Errorf("токен %s не найден в окружении или файле %s", opts.tokenEnv, opts.envFile)
	}
	userAgent := resolveUserAgent(opts.envFile)
	logger.Printf("event=credentials_loaded source=%q", credentialSource(opts))

	result, err := loadMissingTNVED(ctx, client, opts.endpoint, token, userAgent, opts.limit, logger)
	if err != nil {
		return err
	}
	if err := writeCSV(opts.output, result.Cards); err != nil {
		return err
	}
	logger.Printf(
		"event=completed scanned=%d missing_tnved=%d output=%q",
		result.Scanned, len(result.Cards), opts.output,
	)
	return nil
}

func resolveUserAgent(envFile string) string {
	if value := strings.TrimSpace(os.Getenv("WB_API_USER_AGENT")); value != "" {
		return value
	}
	if value, err := readEnvValue(envFile, "WB_API_USER_AGENT"); err == nil && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return defaultUserAgent
}

func credentialSource(opts options) string {
	if strings.TrimSpace(os.Getenv(opts.tokenEnv)) != "" {
		return "environment:" + opts.tokenEnv
	}
	return "file:" + opts.envFile + ":" + opts.tokenEnv
}

func readEnvValue(path, key string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(name) != key {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			return value[1 : len(value)-1], nil
		}
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			unquoted, unquoteErr := strconv.Unquote(value)
			if unquoteErr != nil {
				return "", fmt.Errorf("некорректное значение %s: %w", key, unquoteErr)
			}
			return unquoted, nil
		}
		return value, nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}

func loadMissingTNVED(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	token string,
	userAgent string,
	limit int,
	logger *log.Logger,
) (auditResult, error) {
	cursor := cardsCursor{Limit: pageSize}
	result := auditResult{}
	if limit > 0 {
		result.Cards = make([]card, 0, limit)
	}

	for page := 1; ; page++ {
		startedAt := time.Now()
		response, err := loadPageWithRetry(ctx, client, endpoint, token, userAgent, cursor, page, logger)
		if err != nil {
			logger.Printf(
				"event=page_failed page=%d duration=%s cursor_nm_id=%d cursor_updated_at=%q error=%q",
				page, time.Since(startedAt).Round(time.Millisecond), cursor.NMID, cursor.UpdatedAt, err,
			)
			return auditResult{}, fmt.Errorf("страница %d: %w", page, err)
		}
		result.Scanned += len(response.Cards)
		for _, item := range response.Cards {
			if !hasTNVED(item.Characteristics) {
				result.Cards = append(result.Cards, item)
				if limit > 0 && len(result.Cards) == limit {
					logger.Printf(
						"event=page page=%d duration=%s received=%d scanned=%d missing_tnved=%d limit_reached=true next_cursor_nm_id=%d next_cursor_updated_at=%q",
						page, time.Since(startedAt).Round(time.Millisecond), len(response.Cards), result.Scanned,
						len(result.Cards), response.Cursor.NMID, response.Cursor.UpdatedAt,
					)
					return result, nil
				}
			}
		}
		logger.Printf(
			"event=page page=%d duration=%s received=%d scanned=%d missing_tnved=%d limit_reached=false next_cursor_nm_id=%d next_cursor_updated_at=%q",
			page, time.Since(startedAt).Round(time.Millisecond), len(response.Cards), result.Scanned,
			len(result.Cards), response.Cursor.NMID, response.Cursor.UpdatedAt,
		)

		if len(response.Cards) < pageSize {
			return result, nil
		}
		next := cardsCursor{UpdatedAt: response.Cursor.UpdatedAt, NMID: response.Cursor.NMID, Limit: pageSize}
		if next.UpdatedAt == cursor.UpdatedAt && next.NMID == cursor.NMID {
			return auditResult{}, errors.New("курсор WB не изменился")
		}
		cursor = next

		select {
		case <-ctx.Done():
			return auditResult{}, ctx.Err()
		case <-time.After(650 * time.Millisecond):
		}
	}
}

func loadPageWithRetry(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	token string,
	userAgent string,
	cursor cardsCursor,
	page int,
	logger *log.Logger,
) (cardsResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= maxPageAttempts; attempt++ {
		startedAt := time.Now()
		response, err := loadPage(ctx, client, endpoint, token, userAgent, cursor)
		if err == nil {
			if attempt > 1 {
				logger.Printf(
					"event=request_recovered page=%d attempt=%d duration=%s",
					page, attempt, time.Since(startedAt).Round(time.Millisecond),
				)
			}
			return response, nil
		}
		lastErr = err
		if attempt == maxPageAttempts || !retryablePageError(ctx, err) {
			break
		}
		delay := retryDelay(err, attempt)
		logger.Printf(
			"event=request_retry page=%d attempt=%d duration=%s retry_in=%s error=%q",
			page, attempt, time.Since(startedAt).Round(time.Millisecond), delay, err,
		)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return cardsResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
	return cardsResponse{}, lastErr
}

func retryablePageError(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		return statusErr.statusCode == http.StatusRequestTimeout ||
			statusErr.statusCode == http.StatusTooManyRequests ||
			statusErr.statusCode >= http.StatusInternalServerError
	}
	return true
}

func retryDelay(err error, attempt int) time.Duration {
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) && statusErr.retryAfter > 0 {
		return statusErr.retryAfter
	}
	return retryBaseDelay * time.Duration(1<<(attempt-1))
}

func loadPage(ctx context.Context, client *http.Client, endpoint, token, userAgent string, cursor cardsCursor) (cardsResponse, error) {
	body, err := json.Marshal(cardsRequest{Settings: cardsSettings{
		Sort:   cardsSort{Ascending: true},
		Cursor: cursor,
	}})
	if err != nil {
		return cardsResponse{}, fmt.Errorf("сформировать запрос: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return cardsResponse{}, fmt.Errorf("создать запрос: %w", err)
	}
	request.Header.Set("Authorization", token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", userAgent)

	response, err := client.Do(request)
	if err != nil {
		return cardsResponse{}, fmt.Errorf("запросить карточки WB: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return cardsResponse{}, &httpStatusError{
			statusCode: response.StatusCode,
			retryAfter: parseRetryAfter(response.Header),
			body:       strings.TrimSpace(string(message)),
		}
	}

	var result cardsResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return cardsResponse{}, fmt.Errorf("прочитать ответ WB: %w", err)
	}
	return result, nil
}

func parseRetryAfter(header http.Header) time.Duration {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		value = strings.TrimSpace(header.Get("X-Ratelimit-Retry"))
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil {
		if delay := time.Until(deadline); delay > 0 {
			return delay
		}
	}
	return 0
}

func hasTNVED(characteristics []characteristic) bool {
	for _, item := range characteristics {
		if !isTNVEDName(item.Name) {
			continue
		}
		return hasValue(item.Value)
	}
	return false
}

func isTNVEDName(name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	key = strings.NewReplacer(" ", "", "\t", "", "-", "", "_", "").Replace(key)
	return key == "тнвэд" || key == "кодтнвэд"
}

func hasValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		for _, item := range typed {
			if hasValue(item) {
				return true
			}
		}
		return false
	default:
		return strings.TrimSpace(fmt.Sprint(typed)) != ""
	}
}

func writeCSV(path string, cards []card) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("создать CSV: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString("\ufeff"); err != nil {
		return fmt.Errorf("записать BOM: %w", err)
	}
	writer := csv.NewWriter(file)
	writer.Comma = ';'
	if err := writer.Write([]string{"№", "nmID", "Артикул продавца", "subjectID", "Предмет", "Название", "Ссылка WB"}); err != nil {
		return fmt.Errorf("записать заголовок CSV: %w", err)
	}
	for index, item := range cards {
		row := []string{
			strconv.Itoa(index + 1),
			strconv.FormatInt(item.NMID, 10),
			item.VendorCode,
			strconv.FormatInt(item.SubjectID, 10),
			item.SubjectName,
			item.Title,
			fmt.Sprintf("https://www.wildberries.ru/catalog/%d/detail.aspx", item.NMID),
		}
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("записать строку CSV: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("завершить CSV: %w", err)
	}
	return nil
}
