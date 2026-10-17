package cardpublication_wb_transport

import (
	"context"
	"errors"
	"fmt"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type MediaDownloader struct{ downloader *http.Client }

func NewMediaDownloader() *MediaDownloader {
	return &MediaDownloader{
		downloader: &http.Client{
			Timeout: 45 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("too many media download redirects")
				}
				return validateMediaURL(request.URL)
			},
		},
	}
}

func (transport *MediaDownloader) DownloadMediaFile(
	ctx context.Context,
	link string,
) (contentapi.DownloadedMediaFile, error) {
	parsed, err := url.Parse(link)
	if err != nil || validateMediaURL(parsed) != nil {
		return contentapi.DownloadedMediaFile{}, errors.New("media URL is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return contentapi.DownloadedMediaFile{}, fmt.Errorf("build media download request: %w", err)
	}
	response, err := transport.downloader.Do(request)
	if err != nil {
		return contentapi.DownloadedMediaFile{}, fmt.Errorf("download media file: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return contentapi.DownloadedMediaFile{}, mediaDownloadError{
			status: response.StatusCode, delay: mediaDownloadRetryAfter(response.Header.Get("Retry-After")),
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, contentapi.MaxDownloadedMediaFileBytes+1))
	if err != nil {
		return contentapi.DownloadedMediaFile{}, fmt.Errorf("read media file: %w", err)
	}
	if len(data) == 0 || int64(len(data)) > contentapi.MaxDownloadedMediaFileBytes {
		return contentapi.DownloadedMediaFile{}, errors.New("media file is empty or exceeds 50 MiB")
	}
	mediaType := canonicalMediaType(response.Header.Get("Content-Type"), data)
	if mediaType == "" {
		return contentapi.DownloadedMediaFile{}, errors.New("media file has an unsupported type")
	}
	if strings.HasPrefix(mediaType, "image/") && int64(len(data)) > contentapi.MaxMediaImageFileBytes {
		return contentapi.DownloadedMediaFile{}, errors.New("image exceeds 32 MiB")
	}
	return contentapi.DownloadedMediaFile{
		FileName:  mediaFileName(parsed, mediaType),
		MediaType: mediaType,
		Data:      data,
	}, nil
}

func canonicalMediaType(header string, data []byte) string {
	mediaType, _, _ := mime.ParseMediaType(header)
	mediaType = strings.ToLower(mediaType)
	if !supportedMediaType(mediaType) {
		mediaType = strings.ToLower(http.DetectContentType(data))
	}
	if supportedMediaType(mediaType) {
		return mediaType
	}
	return ""
}

func validateMediaURL(parsed *url.URL) error {
	if parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("media URL is invalid")
	}
	return nil
}

func supportedMediaType(mediaType string) bool {
	switch strings.ToLower(mediaType) {
	case "image/jpeg", "image/png", "image/bmp", "image/gif", "image/webp",
		"video/mp4", "video/quicktime":
		return true
	default:
		return false
	}
}

func mediaFileName(source *url.URL, mediaType string) string {
	name := path.Base(source.Path)
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	name = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(name, "\r", ""), "\n", ""))
	if name == "" || name == "." || name == "/" {
		name = "media"
	}
	expectedExtension := map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png", "image/bmp": ".bmp",
		"image/gif": ".gif", "image/webp": ".webp", "video/mp4": ".mp4",
		"video/quicktime": ".mov",
	}[mediaType]
	extension := strings.ToLower(path.Ext(name))
	validExtension := extension == expectedExtension ||
		(mediaType == "image/jpeg" && extension == ".jpeg")
	if !validExtension {
		name = strings.TrimSuffix(name, path.Ext(name)) + expectedExtension
	}
	return name
}
