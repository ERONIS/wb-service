package cardpublication_config_transport

import "testing"

func TestMediaUploadMethodFromEnvironment(t *testing.T) {
	t.Setenv("CARDPUBLICATION_MEDIA_UPLOAD_METHOD", " FILE ")
	config, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if config.MediaUploadMethod != MediaUploadByFile {
		t.Fatalf("media upload method = %q", config.MediaUploadMethod)
	}
}

func TestMediaUploadMethodRejectsUnknownValue(t *testing.T) {
	t.Setenv("CARDPUBLICATION_MEDIA_UPLOAD_METHOD", "other")
	if _, err := New(); err == nil {
		t.Fatal("expected invalid media upload method error")
	}
}
