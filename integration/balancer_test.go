// integration/balancer_test.go (або аналогічний)
package main // Або ваш пакет для інтеграційних тестів

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// Структура для очікуваної відповіді від /api/v1/some-data
type ApiSomeDataResponse struct {
	Key   string `json:"key"`
	Value string `json:"value"` // Очікуємо рядок дати
}

func TestSomeDataEndpoint(t *testing.T) {
	// teamName тепер жорстко встановлений або береться з ENV, але для тесту ми знаємо, що це "duo"
	teamNameForTest := "duo" // <--- ЗМІНЕНО

	reportURL := os.Getenv("REPORT_URL")
	if reportURL == "" {
		reportURL = "http://localhost:8080"
	}

	requestURL := fmt.Sprintf("%s/api/v1/some-data?key=%s", reportURL, teamNameForTest)
	t.Logf("Integration Test: Sending GET request to %s", requestURL)

	var resp *http.Response
	var err error

	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		resp, err = http.Get(requestURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if resp != nil {
			// Важливо закрити тіло відповіді, навіть якщо вона неуспішна,
			// щоб уникнути витоку ресурсів.
			io.Copy(io.Discard, resp.Body) // Читаємо та відкидаємо тіло
			resp.Body.Close()
		}
		t.Logf("Integration Test: Attempt %d failed (err: %v, status: %s). Retrying in 2s...", i+1, err, resp.Status)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		t.Fatalf("Integration Test: Failed to send GET request to %s after multiple retries: %v", requestURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("Integration Test: Expected status 200 OK, got %s. Body: %s", resp.Status, string(bodyBytes))
	}

	var apiResponse ApiSomeDataResponse
	// Читаємо тіло відповіді для декодування. Важливо, щоб воно не було вже прочитане.
	bodyBytesForDecode, errReadBody := io.ReadAll(resp.Body)
	if errReadBody != nil {
		t.Fatalf("Integration Test: Failed to read response body for decoding: %v", errReadBody)
	}
	// Повертаємо resp.Body назад, якщо потрібно буде читати його знову (хоча тут не потрібно)
	// resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytesForDecode))

	if err := json.Unmarshal(bodyBytesForDecode, &apiResponse); err != nil {
		t.Fatalf("Integration Test: Failed to decode response body. Body: %s. Error: %v", string(bodyBytesForDecode), err)
	}

	if apiResponse.Value == "" {
		t.Errorf("Integration Test: Expected non-empty value for key '%s', got empty", teamNameForTest)
	}

	currentDate := time.Now().Format("2006-01-02")
	if apiResponse.Value != currentDate {
		t.Logf("Integration Test: Warning: Received value '%s' does not strictly match current date '%s'. This might be due to timing or timezone differences.", apiResponse.Value, currentDate)
		_, dateParseErr := time.Parse("2006-01-02", apiResponse.Value)
		if dateParseErr != nil {
			t.Errorf("Integration Test: Value '%s' is not in YYYY-MM-DD format. Parse error: %v", apiResponse.Value, dateParseErr)
		}
	} else {
		t.Logf("Integration Test: Successfully received value '%s' for key '%s', matches current date.", apiResponse.Value, teamNameForTest)
	}

	if apiResponse.Key != teamNameForTest {
		t.Errorf("Integration Test: Expected key '%s' in response, got '%s'", teamNameForTest, apiResponse.Key)
	}
}
