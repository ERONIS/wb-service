package cardpublication_wb_transport

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	core_wb "github.com/ERONIS/wb-service/internal/core/transport/wb"
)

// A download can be retried without sending anything to WB. Retain status and
// Retry-After so the workflow can distinguish a busy origin from a missing file.
type mediaDownloadError struct {
	status int
	delay  time.Duration
}

func (e mediaDownloadError) Error() string {
	return fmt.Sprintf("download media file: unexpected HTTP status %d", e.status)
}
func (e mediaDownloadError) Code() string                    { return "media_download_status" }
func (e mediaDownloadError) HTTPStatus() int                 { return e.status }
func (e mediaDownloadError) RetryAfter() time.Duration       { return e.delay }
func (e mediaDownloadError) Delivery() core_wb.DeliveryState { return core_wb.NotDispatched }

func mediaDownloadRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if seconds, err := strconv.ParseInt(header, 10, 32); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(header); err == nil {
		return max(time.Until(at), 0)
	}
	return 0
}
