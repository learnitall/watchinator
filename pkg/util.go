package pkg

import (
	"strings"

	"github.com/shurcooL/githubv4"
)

func WriteSliceToBuilderIfNotNil[T any](builder *strings.Builder, prefix string, items *[]T, toString func(i T) string) {
	if items == nil {
		return
	}

	itemsDeref := *items

	builder.WriteString(prefix)

	n := len(itemsDeref) - 1
	for i, item := range itemsDeref {
		builder.WriteString(toString(item))

		if i != n {
			builder.WriteString(",")
		}
	}
}

func WriteStringToBuilderIfNotNil[T any](builder *strings.Builder, prefix string, field *T, toString func(i T) string) {
	if field == nil {
		return
	}

	builder.WriteString(prefix)
	builder.WriteString(toString(*field))
}

func GitHubv4StringToGoString(s githubv4.String) string {
	return string(s)
}
