package statistics_postgres_query

import (
	"fmt"
	"strings"
	"time"
)

type WhereBuilder struct {
	conditions []string
	Arguments  []any
}

func (builder *WhereBuilder) Add(expression string, value any) {
	builder.Arguments = append(builder.Arguments, value)
	builder.conditions = append(
		builder.conditions,
		fmt.Sprintf(expression, len(builder.Arguments)),
	)
}

func (builder *WhereBuilder) AddIf(condition bool, expression string, value any) {
	if condition {
		builder.Add(expression, value)
	}
}

func (builder *WhereBuilder) AddCreatedRange(column string, from, to *time.Time) {
	if from != nil {
		builder.Add(column+" >= $%d", *from)
	}
	if to != nil {
		builder.Add(column+" < $%d", *to)
	}
}

func (builder *WhereBuilder) AddRaw(expression string) {
	builder.conditions = append(builder.conditions, expression)
}

func (builder *WhereBuilder) Clause() string {
	if len(builder.conditions) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(builder.conditions, " AND ")
}
