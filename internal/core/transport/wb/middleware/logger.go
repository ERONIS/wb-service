package core_transport_wb_middleware

import (
	"net/http"
	"time"

	core_transport_wb_requestmeta "github.com/ERONIS/wb-service/internal/core/transport/wb/requestmeta"
	"go.uber.org/zap"
)

// Мы тут объявлем я хочу получить функцию которая
// получает RoundTripper и возвращает RoundTripper
func Logger(logger *zap.Logger) Middleware {
	if logger == nil {
		panic("WB middleware logger is nil")
	}
	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(
			func(request *http.Request) (*http.Response, error) {
				startedAt := time.Now()

				response, err := next.RoundTrip(request)

				fields := []zap.Field{
					zap.String("component", "wb"),
					zap.String("method", request.Method),
					zap.String("host", request.URL.Host),
					zap.String("path", request.URL.Path),
					zap.Duration("duration", time.Since(startedAt)),
				}
				metadata, metadataExists :=
					core_transport_wb_requestmeta.FromContext(
						request.Context(),
					)

				if metadataExists {
					fields = append(
						fields,
						zap.String(
							"operation",
							metadata.OperationName,
						),
						zap.Int(
							"attempt",
							metadata.Attempt,
						),
					)
				}

				if err != nil {
					logger.Error("WB API request failed",
						append(fields, zap.Error(err))...)
					return response, err
				}
				fields = append(
					fields,
					zap.Int(
						"status_code",
						response.StatusCode,
					),
				)

				if response.StatusCode >= http.StatusBadRequest {
					logger.Warn(
						"WB API returned error response",
						fields...,
					)
				} else {
					logger.Debug(
						"WB API request completed",
						fields...,
					)
				}

				return response, nil
			},
		)

	}
}
