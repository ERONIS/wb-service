package core_transport_telegram

import (
	"strings"
	"time"
)

const defaultTimeFormat = "02.01.2006 15:04"

// FormatProgressBar формирует текстовый прогресс-бар вида [#####.....].
func FormatProgressBar(done, total int64, width int) string {
	if total <= 0 || width <= 0 {
		return ""
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	filled := int(done * int64(width) / total)
	return "[" + strings.Repeat("#", filled) + strings.Repeat(".", width-filled) + "]"
}

// CalcPercent вычисляет процент выполнения с округлением до ближайшего целого.
func CalcPercent(done, total int64) int64 {
	if total <= 0 {
		return 0
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	return (done*100 + total/2) / total
}

// FormatDateTime форматирует время в стандартный интерфейсный формат "02.01.2006 15:04".
func FormatDateTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.Local().Format(defaultTimeFormat)
}

// FormatDateTimeUTC форматирует время в формате UTC с суффиксом "UTC".
func FormatDateTimeUTC(value time.Time) string {
	if value.IsZero() {
		return "не указано"
	}
	return value.UTC().Format(defaultTimeFormat) + " UTC"
}
