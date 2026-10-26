package response

import (
	"encoding/json"
	"strings"
	"unicode"
)

const maxProblemFieldRunes = 160

// ErrorSummary извлекает из ограниченного тела только основные поля ошибки WB.
func ErrorSummary(body []byte) string {
	var problem map[string]json.RawMessage
	if json.Unmarshal(body, &problem) != nil {
		return ""
	}

	fields := []string{
		problemField("code", problemCode(problem["code"])),
		problemField("title", problemString(problem["title"])),
		problemField("error_text", problemString(problem["errorText"])),
		problemField("request_id", problemString(problem["requestId"])),
	}
	visible := fields[:0]
	for _, field := range fields {
		if field != "" {
			visible = append(visible, field)
		}
	}

	return strings.Join(visible, " ")
}

func problemString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func problemCode(raw json.RawMessage) string {
	if value := problemString(raw); value != "" {
		return value
	}
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return ""
	}
	return number.String()
}

func problemField(name string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	runes := make([]rune, 0, maxProblemFieldRunes)
	for _, char := range value {
		if unicode.IsControl(char) || unicode.IsSpace(char) {
			char = ' '
		}
		runes = append(runes, char)
		if len(runes) == maxProblemFieldRunes {
			break
		}
	}

	return name + "=" + strings.TrimSpace(string(runes))
}
