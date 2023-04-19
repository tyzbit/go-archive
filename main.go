package archiveorg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"github.com/avast/retry-go"
)

const (
	archiveApi  string = "https://wwwb-api.archive.org"
	archiveRoot string = "https://web.archive.org/web"
)

type ArchiveOrgWaybackAvailableResponse struct {
	URL               string `json:"url"`
	ArchivedSnapshots struct {
		Closest struct {
			Status    string `json:"status"`
			Available bool   `json:"available"`
			URL       string `json:"url"`
			Timestamp string `json:"timestamp"`
		} `json:"closest"`
	} `json:"archived_snapshots"`
}

type ArchiveOrgWaybackSaveResponse struct {
	URL     string `json:"url"`
	JobID   string `json:"job_id"`
	Message string `json:"message"`
}

type ArchiveOrgWaybackStatusResponse struct {
	Counters struct {
		Embeds   int `json:"embeds"`
		Outlinks int `json:"outlinks"`
	} `json:"counters"`
	DurationSec  float32  `json:"duration_sec"`
	FirstArchive bool     `json:"first_archive"`
	HttpStatus   int      `json:"http_status"`
	JobID        string   `json:"job_id"`
	OriginalURL  string   `json:"original_url"`
	Outlinks     []string `json:"outlinks"`
	Resources    []string `json:"resources"`
	Status       string   `json:"status"`
	Timestamp    string   `json:"timestamp"`
}

type ArchiveOrgWaybackSparklineResponse struct {
	Years   map[string][]int  `json:"years"`
	FirstTs string            `json:"first_ts"`
	LastTs  string            `json:"last_ts"`
	Status  map[string]string `json:"status"`
}

// RetriableError is a custom error that contains a positive duration for the next retry
type RetriableError struct {
	Err        error
	RetryAfter time.Duration
}

// Error returns error message and a Retry-After duration
func (e *RetriableError) Error() string {
	return fmt.Sprintf("%s (retry after %v)", e.Err.Error(), e.RetryAfter)
}

func GetLatestURL(url string, retryAttempts uint) (latestUrl string, err error) {
	r, err := CheckURLWaybackAvailable(url, retryAttempts)
	if err != nil {
		return "", fmt.Errorf("error checking if url is available in wayback: %w", err)
	}
	return r.ArchivedSnapshots.Closest.URL, nil
}

// Checks if a page is available in the Wayback Machine.
// r.ArchivedSnapshots will be populated if it is.
func CheckURLWaybackAvailable(url string, retryAttempts uint) (r ArchiveOrgWaybackAvailableResponse, err error) {
	resp := http.Response{}
	if err := retry.Do(func() error {
		client := http.Client{}
		respTry, err := client.Get(archiveApi + "/wayback/available?url=" + url)
		if err != nil {
			return &RetriableError{
				Err:        fmt.Errorf("error calling wayback api: %w", err),
				RetryAfter: 1 * time.Second,
			}
		}
		if resp.StatusCode == 429 {
			return fmt.Errorf("rate limited by wayback api")
		}
		resp = *respTry
		return nil
	},
		retry.Attempts(retryAttempts),
		retry.Delay(1*time.Second),
		retry.DelayType(retry.FixedDelay),
	); err != nil {
		return r, fmt.Errorf("all %d attempts failed: %w", retryAttempts, err)
	} else {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				panic(err)
			}
		}()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return r, fmt.Errorf("error reading body from wayback api: %w", err)
		}

		err = json.Unmarshal(body, &r)
		if err != nil {
			return r, fmt.Errorf("error unmarshalling json: %w, body: %v", err, string(body))
		}

		return r, nil
	}
}

// Takes a slice of strings and a boolean whether or not to archive the page if not found
// and returns a slice of strings of archive.org URLs and any errors.
func GetLatestURLs(urls []string, retryAttempts uint, archiveIfNotFound bool, cookie string) (archiveUrls []string, errs []error) {
	for _, url := range urls {
		var err error
		archiveUrl, err := GetLatestURL(url, retryAttempts)
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to get latest archive URL for %v, we got: %v, err: %w", url, archiveUrl, err))
			continue
		}
		if archiveUrl == "" {
			archiveUrl, err = ArchiveURL(url, retryAttempts, cookie)
			if err != nil {
				errs = append(errs, fmt.Errorf("unable to archive URL %v, we got: %v, err: %w", url, archiveUrl, err))
			}
		}
		archiveUrls = append(archiveUrls, archiveUrl)
	}

	return archiveUrls, errs
}

// Archives a given URL with archive.org. Returns an empty string and an error
// if the URL wasn't archived.
// Needs authentication (cookie).
func ArchiveURL(archiveURL string, retryAttempts uint, cookie string) (archivedURL string, err error) {
	client := &http.Client{}
	urlParams := "capture_all=1&url=" + url.QueryEscape(archiveURL)
	r, err := http.NewRequest(http.MethodPost, archiveApi+"/save/?"+urlParams, bytes.NewBuffer([]byte(urlParams)))
	if err != nil {
		return "", fmt.Errorf("Could not build http request")
	}
	r.Header = http.Header{
		"Accept":       {"application/json"},
		"Content-Type": {"application/x-www-form-urlencoded"},
		"Cookie":       {cookie},
	}
	resp, err := client.Do(r)
	if err != nil {
		return "", fmt.Errorf("error calling archive.org: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			panic(err)
		}
	}()

	switch resp.StatusCode {
	// May not be necessary anymore now that we're calling a real API
	case 301, 302:
		// Case insensitive
		location := resp.Header.Get("location")
		if location == "" {
			err = fmt.Errorf("archive.org did not reply with a location header")
		}
		return location, err
	// May not be necessary anymore now that we're calling a real API
	case 523, 520:
		return "", fmt.Errorf("archive.org declined to archive that page")
	default:
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("unable to read response body, err: %v", err)
		}

		s := ArchiveOrgWaybackSaveResponse{}
		_ = json.Unmarshal(body, &s)
		if s.JobID == "" {
			return "", fmt.Errorf("archive.org did not respond with a job_id: %v", string(body))
		}
		rs := ArchiveOrgWaybackStatusResponse{}
		if err := retry.Do(func() error {
			rsAttempt, err := CheckArchiveRequestStatus(s.JobID)
			if err != nil {
				return fmt.Errorf("error checking archive request status: %v", string(body))
			}
			if rsAttempt.Status == "pending" {
				return &RetriableError{
					Err:        fmt.Errorf("job is still pending"),
					RetryAfter: 3 * time.Second,
				}
			}
			if rsAttempt.Status == "success" {
				rs = rsAttempt
				return nil
			}
			return &RetriableError{
				Err: fmt.Errorf("archive.org request had unexpected status: %v", rsAttempt.Status),
			}
		},
			retry.Attempts(retryAttempts),
			retry.Delay(1*time.Second),
			retry.DelayType(retry.BackOffDelay),
		); err != nil {
			return "", fmt.Errorf("all %d attempts at archiving the page failed: %w", retryAttempts, err)
		} else {
			if rs.Timestamp != "" {
				// We could call the archive.org API again
				// but URLs are predictable
				return archiveRoot + rs.Timestamp + archiveURL, nil
			}
		}
		return "", fmt.Errorf("archive.org had an unexpected response: %v", string(body))
	}
}

// Checks the status of an archive request job.
func CheckArchiveRequestStatus(jobID string) (r ArchiveOrgWaybackStatusResponse, err error) {
	client := http.Client{}
	resp, err := client.Get(archiveApi + "/save/status/" + jobID)
	if err != nil {
		return r, fmt.Errorf("error calling wayback save status api: %w", err)
	}
	if resp.StatusCode == 429 {
		return r, fmt.Errorf("rate limited by wayback api")
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return r, fmt.Errorf("error reading body: %w", err)
	}
	err = json.Unmarshal(body, &r)
	if err != nil {
		return r, fmt.Errorf("error unmarshalling json: %w, body: %v", err, string(body))
	}
	return r, nil
}

// Checks the sparkline (history of archived copies) for a given URL
// Does not need to be authenticated.
func CheckArchiveSparkline(url string) (r ArchiveOrgWaybackSparklineResponse, err error) {
	client := http.Client{}
	resp, err := client.Get(archiveApi + "/__wb/sparkline/?collection=web&output=json&url=" + url)
	if err != nil {
		return r, fmt.Errorf("error calling wayback save status api: %w", err)
	}
	if resp.StatusCode == 429 {
		return r, fmt.Errorf("rate limited by wayback api")
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return r, fmt.Errorf("error reading body: %w", err)
	}
	err = json.Unmarshal(body, &r)
	if err != nil {
		return r, fmt.Errorf("error unmarshalling json: %w, body: %v", err, string(body))
	}
	return r, nil
}
