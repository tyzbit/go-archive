package archiveorg

import (
	"strings"
	"testing"
)

func TestGetLatestURLs(t *testing.T) {
	validUrls := []string{"https://golang.org", "https://go.dev"}
	archiveUrls, errs := GetLatestURLs(validUrls, 1, false)
	for _, err := range errs {
		if err != nil {
			t.Errorf("error getting latest URLs: %v", err)
		}
	}

	for _, archiveUrl := range archiveUrls {
		if !strings.HasPrefix(archiveUrl, "http://web.archive.org") {
			t.Errorf("unexpected response from archive.org: %v", archiveUrl)
		}
	}

	unarchivedUrls := []string{"https://10qpwo3imdeufnenfuyfgbgbdssd.com"}
	archiveUrls, _ = GetLatestURLs(unarchivedUrls, 1, true)
	for _, archiveUrl := range archiveUrls {
		if strings.HasPrefix(archiveUrl, "http://web.archive.org") {
			t.Errorf("archive.org unexpectedly has a response for %v: %v", archiveUrl, unarchivedUrls[0])
		}
	}

}
