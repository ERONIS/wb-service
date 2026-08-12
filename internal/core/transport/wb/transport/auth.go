package transport

import "net/http"

type authorizationRoundTripper struct {
	next  http.RoundTripper
	token string
}

// Authorization добавляет токен одного кабинета в HTTP-запрос.
func Authorization(token string) WrapperFunc {
	return func(next http.RoundTripper) http.RoundTripper {
		return &authorizationRoundTripper{
			next:  next,
			token: token,
		}
	}
}

func (transport *authorizationRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	requestCopy := request.Clone(request.Context())
	if requestCopy.Header == nil {
		requestCopy.Header = make(http.Header)
	}

	requestCopy.Header.Set(
		"Authorization",
		transport.token,
	)

	return transport.next.RoundTrip(requestCopy)
}
