// integration/balancer_test.go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

type ApiSomeDataResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func TestSomeDataEndpoint(t *testing.T) {
	teamNameForTest := "duo"

	// Отримуємо адресу балансувальника зі змінної середовища BALANCER_ADDR,
	// яка встановлюється в docker-compose.test.yaml
	reportURL := os.Getenv("BALANCER_ADDR")
	if reportURL == "" {
		// Якщо запускаємо тест локально (не в Docker), можемо використовувати localhost:8090
		// Але для CI, де все в Docker, BALANCER_ADDR має бути встановлено.
		t.Logf("Warning: BALANCER_ADDR environment variable not set. Defaulting to http://localhost:8090 for local testing.")
		reportURL = "http://localhost:8090"
	}

	requestURL := fmt.Sprintf("%s/api/v1/some-data?key=%s", reportURL, teamNameForTest)
	t.Logf("Integration Test: Sending GET request to %s", requestURL)

	var resp *http.Response
	var err error

	maxRetries := 10
	retryDelay := 3 * time.Second

	for i := 0; i < maxRetries; i++ {
		resp, err = http.Get(requestURL)
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				break
			}
			statusText := "unknown (response was nil)"
			if resp != nil {
				statusText = resp.Status
			}
			t.Logf("Integration Test: Attempt %d received status: %s. Retrying in %v...", i+1, statusText, retryDelay)
			if resp != nil && resp.Body != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		} else {
			t.Logf("Integration Test: Attempt %d http.Get failed (err: %v). Retrying in %v...", i+1, err, retryDelay)
		}

		if i == maxRetries-1 {
			break
		}
		time.Sleep(retryDelay)
	}

	if err != nil {
		t.Fatalf("Integration Test: Failed to send GET request to %s after %d retries: %v", requestURL, maxRetries, err)
	}
	if resp == nil {
		t.Fatalf("Integration Test: HTTP response is nil after %d retries, though no error was reported by http.Get for the last attempt.", maxRetries)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("Integration Test: Expected status 200 OK after retries, got %s. Body: %s", resp.Status, string(bodyBytes))
	}

	var apiResponse ApiSomeDataResponse
	bodyBytesForDecode, errReadBody := io.ReadAll(resp.Body)
	if errReadBody != nil {
		t.Fatalf("Integration Test: Failed to read response body for decoding: %v", errReadBody)
	}

	if err := json.Unmarshal(bodyBytesForDecode, &apiResponse); err != nil {
		t.Fatalf("Integration Test: Failed to decode response body. Body: %s. Error: %v", string(bodyBytesForDecode), err)
	}

	if apiResponse.Value == "" {
		t.Errorf("Integration Test: Expected non-empty value for key '%s', got empty", teamNameForTest)
	}

	_, dateParseErr := time.Parse("2006-01-02", apiResponse.Value)
	if dateParseErr != nil {
		t.Errorf("Integration Test: Value '%s' is not in YYYY-MM-DD format. Parse error: %v", apiResponse.Value, dateParseErr)
	} else {
		t.Logf("Integration Test: Successfully received value '%s' for key '%s', and it is in correct date format.", apiResponse.Value, teamNameForTest)
	}

	if apiResponse.Key != teamNameForTest {
		t.Errorf("Integration Test: Expected key '%s' in response, got '%s'", teamNameForTest, apiResponse.Key)
	}
}
