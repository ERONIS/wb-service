package transport

import "net/http"

// WrapperFunc оборачивает HTTP transport дополнительным поведением.
type WrapperFunc func(http.RoundTripper) http.RoundTripper

// Chain собирает wrappers в указанном порядке.
// Первый wrapper становится внешним и выполняется первым.
func Chain(
	base http.RoundTripper,
	wrappers ...WrapperFunc,
) http.RoundTripper {
	for index := len(wrappers) - 1; index >= 0; index-- {
		base = wrappers[index](base)
	}

	return base
}
