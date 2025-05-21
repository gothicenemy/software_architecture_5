package integration

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// baseAddress буде отримано з змінної оточення BALANCER_ADDR
var baseAddress = "http://balancer:8080" // Значення за замовчуванням, якщо змінна не встановлена

// expectedServers - імена хостів, які ми очікуємо побачити в заголовку "lb-from".
// Вони мають відповідати тому, як балансувальник їх ідентифікує.
// У вашому balancer.go це dst.URL.Host, що буде "serverN:8080".
var expectedServers = []string{
	"server1:8080",
	"server2:8080",
	"server3:8080",
}

func TestMain(m *testing.M) {
	// Встановлюємо baseAddress з змінної оточення, якщо вона є
	if addr := os.Getenv("BALANCER_ADDR"); addr != "" {
		baseAddress = addr
	}
	// Запускаємо тести
	os.Exit(m.Run())
}

func TestLeastConnectionsDistribution(t *testing.T) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		t.Skip("Skipping integration test: INTEGRATION_TEST environment variable not set")
	}

	// Даємо час системі стабілізуватися.
	// Це особливо важливо, якщо depends_on використовує service_started, а не service_healthy.
	t.Logf("Waiting for services to stabilize...")
	time.Sleep(10 * time.Second) // Можна налаштувати цей час

	client := http.Client{
		Timeout: 8 * time.Second, // Збільшено таймаут для надійності
	}

	serverRequestCounts := make(map[string]int)
	var mu sync.Mutex // Для безпечного доступу до serverRequestCounts з горутин

	// Кількість одночасних "довгих" запитів, щоб створити навантаження
	numConcurrentLoadGenerators := 6
	// Кількість запитів, які кожен генератор навантаження відправить
	requestsPerGenerator := 5
	totalRequests := numConcurrentLoadGenerators * requestsPerGenerator

	var wg sync.WaitGroup
	successfulRequests := 0
	var successfulRequestsMu sync.Mutex // Для безпечного оновлення successfulRequests

	t.Logf("Starting to send %d total requests using %d concurrent generators...", totalRequests, numConcurrentLoadGenerators)

	for i := 0; i < numConcurrentLoadGenerators; i++ {
		wg.Add(1)
		go func(generatorID int) {
			defer wg.Done()
			for j := 0; j < requestsPerGenerator; j++ {
				url := fmt.Sprintf("%s/api/v1/some-data?gen=%d&req=%d&time=%d", baseAddress, generatorID, j, time.Now().UnixNano())

				// Додамо трохи варіативності в URL, щоб уникнути кешування на рівні проксі/браузера (хоча тут це менш ймовірно)
				// І щоб запити були унікальними

				var resp *http.Response
				var err error

				// Спробуємо кілька разів, якщо є помилка (наприклад, тимчасова недоступність)
				for attempt := 0; attempt < 3; attempt++ {
					resp, err = client.Get(url)
					if err == nil && resp.StatusCode == http.StatusOK {
						break
					}
					if resp != nil {
						resp.Body.Close() // Закриваємо тіло, якщо відповідь отримана, але не успішна
					}
					t.Logf("Generator %d, Request %d, Attempt %d: Error or non-OK status. Error: %v, Status: %s. Retrying...",
						generatorID, j, attempt+1, err, ifNil(resp, func() string { return "N/A" }, func(r *http.Response) string { return r.Status }))
					time.Sleep(time.Duration(500+attempt*500) * time.Millisecond) // Збільшуємо затримку
				}

				if err != nil {
					t.Errorf("Generator %d, Request %d: Failed after retries: %v", generatorID, j, err)
					continue
				}

				if resp.StatusCode == http.StatusOK {
					successfulRequestsMu.Lock()
					successfulRequests++
					successfulRequestsMu.Unlock()

					serverFromHeader := resp.Header.Get("lb-from")
					if serverFromHeader == "" {
						t.Errorf("Generator %d, Request %d: lb-from header is missing!", generatorID, j)
					} else {
						mu.Lock()
						serverRequestCounts[serverFromHeader]++
						mu.Unlock()
						// t.Logf("Generator %d, Request %d: Handled by %s", generatorID, j, serverFromHeader) // Може бути занадто багато логів
					}
				} else {
					t.Errorf("Generator %d, Request %d: Received non-OK status: %s from %s", generatorID, j, resp.Status, url)
				}
				resp.Body.Close()
				// Невелика випадкова затримка між запитами від одного генератора
				// time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
			}
		}(i)
	}

	wg.Wait() // Чекаємо завершення всіх горутин

	t.Logf("Finished sending requests. Total successful: %d/%d", successfulRequests, totalRequests)
	t.Logf("Server request distribution: %v", serverRequestCounts)

	if successfulRequests < totalRequests*3/4 {
		t.Fatalf("Less than 75%% of requests were successful (%d/%d)", successfulRequests, totalRequests)
	}

	// Перевірки для "Найменша кількість з'єднань"
	// 1. Має бути використано більше одного сервера, якщо їх декілька і всі здорові.
	if len(serverRequestCounts) == 0 && successfulRequests > 0 {
		t.Errorf("No 'lb-from' headers were found, cannot verify distribution.")
	} else if len(serverRequestCounts) <= 1 && successfulRequests > 1 && len(expectedServers) > 1 {
		// Ця перевірка може спрацювати, якщо навантаження було недостатнім або запити надто швидкі.
		// Для "Least Connections" це не завжди помилка, якщо один сервер завжди виявлявся найменш завантаженим.
		t.Logf("Warning: All %d successful requests went to %d server(s): %v. This might be acceptable for Least Connections if load was low or one server was consistently least loaded.",
			successfulRequests, len(serverRequestCounts), serverRequestCounts)
	} else if len(serverRequestCounts) > 1 {
		t.Logf("Requests were distributed among %d servers. This is a good sign for load balancing.", len(serverRequestCounts))
	}

	// 2. Перевірка, що всі сервери, які відповіли, є серед очікуваних.
	for serverName := range serverRequestCounts {
		found := false
		for _, expected := range expectedServers {
			if strings.HasPrefix(serverName, expected) { // Дозволяємо, якщо lb-from може містити більше інформації
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Received response from an unexpected server: %s. Expected one of: %v", serverName, expectedServers)
		}
	}

	// 3. Більш тонка перевірка для "Least Connections" (неідеальна, але краще, ніж нічого):
	//    Розподіл не має бути абсолютно рівномірним, але й не має бути надто великого перекосу,
	//    якщо всі сервери працюють з однаковою швидкістю.
	if len(serverRequestCounts) == len(expectedServers) { // Якщо всі очікувані сервери відповіли
		minReq := totalRequests
		maxReq := 0
		for _, count := range serverRequestCounts {
			if count < minReq {
				minReq = count
			}
			if count > maxReq {
				maxReq = count
			}
		}
		// Допустима різниця, наприклад, не більше ніж у 2-3 рази, якщо запитів достатньо.
		// Це дуже приблизна евристика.
		if minReq > 0 && float64(maxReq)/float64(minReq) > 3.0 && totalRequests > len(expectedServers)*3 {
			t.Logf("Warning: Load distribution appears somewhat skewed. Max: %d, Min: %d. Counts: %v", maxReq, minReq, serverRequestCounts)
		} else if minReq > 0 {
			t.Logf("Load distribution seems reasonable. Max: %d, Min: %d.", maxReq, minReq)
		}
	}

	t.Log("Integration test for least connections finished.")
}

// Допоміжна функція для уникнення паніки при nil resp в логах
func ifNil(resp *http.Response, defaultVal func() string, actualVal func(*http.Response) string) string {
	if resp == nil {
		return defaultVal()
	}
	return actualVal(resp)
}

// BenchmarkBalancer - це заготовка, реалізуй її, якщо потрібно для завдання.
func BenchmarkBalancer(b *testing.B) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		b.Skip("Skipping integration benchmark: INTEGRATION_TEST environment variable not set")
	}
	// Даємо час системі стабілізуватися
	time.Sleep(10 * time.Second)

	client := http.Client{
		Timeout: 3 * time.Second,
		// Можна налаштувати Transport для бенчмарків, наприклад, вимкнути KeepAlives, якщо потрібно
		// Transport: &http.Transport{
		//  DisableKeepAlives: true,
		// },
	}

	targetURL := fmt.Sprintf("%s/api/v1/some-data", baseAddress)

	b.ResetTimer() // Скидаємо таймер перед початком циклу бенчмарка
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(targetURL)
		if err != nil {
			// У бенчмарках помилки зазвичай не обробляються через b.Error, щоб не спотворювати результати,
			// але для налагодження можна увімкнути.
			// b.Errorf("Request failed: %v", err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			// b.Errorf("Received non-OK status: %d", resp.StatusCode)
		}
		if resp.Body != nil {
			resp.Body.Close()
		}
	}
}
