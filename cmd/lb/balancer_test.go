package main // Пакет має бути `main`, оскільки balancer.go знаходиться в пакеті main

import (
	"fmt"
	"net/url"
	"testing"
	// "sync" // Не потрібен для цих тестів, якщо не тестуємо паралельні зміни
)

// newTestServer створює екземпляр Server для тестів.
// ReverseProxy тут nil, бо він не потрібен для тестування логіки вибору.
func newTestServer(rawURL string, isHealthy bool, connections int64) *Server {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		// У тестах краще панікувати, якщо базові налаштування неправильні,
		// це вказує на помилку в самому тесті.
		panic(fmt.Sprintf("Failed to parse URL for test server %s: %v", rawURL, err))
	}
	return &Server{
		URL:          parsedURL,
		ActiveConns:  connections,
		IsHealthy:    isHealthy,
		ReverseProxy: nil, // Не потрібен для тестування логіки вибору selectLeastLoadedServer
		// mutex не потрібно ініціалізувати явно, нульове значення sync.RWMutex готове до використання
	}
}

func TestSelectLeastLoadedServer(t *testing.T) {
	// Зберігаємо оригінальний стан глобальної змінної `servers`
	// і відновлюємо його після завершення всіх тестів у цій функції.
	originalServers := servers
	defer func() { servers = originalServers }()

	testCases := []struct {
		name              string
		setupServers      func() []*Server // Функція для налаштування `servers` для конкретного тесту
		expectedServerURL string           // Порожній рядок, якщо очікується nil (немає здорових серверів)
	}{
		{
			name: "single healthy server with zero connections",
			setupServers: func() []*Server {
				return []*Server{
					newTestServer("http://server1:8080", true, 0),
				}
			},
			expectedServerURL: "http://server1:8080",
		},
		{
			name: "multiple healthy servers, select one with least connections",
			setupServers: func() []*Server {
				return []*Server{
					newTestServer("http://server1:8080", true, 5),
					newTestServer("http://server2:8080", true, 2), // Очікується цей
					newTestServer("http://server3:8080", true, 3),
				}
			},
			expectedServerURL: "http://server2:8080",
		},
		{
			name: "all servers unhealthy",
			setupServers: func() []*Server {
				return []*Server{
					newTestServer("http://server1:8080", false, 0),
					newTestServer("http://server2:8080", false, 0),
				}
			},
			expectedServerURL: "", // Очікуємо nil
		},
		{
			name: "one healthy server among unhealthy ones",
			setupServers: func() []*Server {
				return []*Server{
					newTestServer("http://server1:8080", false, 10),
					newTestServer("http://server2:8080", true, 5), // Очікується цей
					newTestServer("http://server3:8080", false, 0),
				}
			},
			expectedServerURL: "http://server2:8080",
		},
		{
			name: "tie in connections, should pick the first one encountered in the list",
			setupServers: func() []*Server {
				return []*Server{
					newTestServer("http://server1:8080", true, 2), // Очікується цей (перший з найменшими)
					newTestServer("http://server2:8080", true, 5),
					newTestServer("http://server3:8080", true, 2),
				}
			},
			expectedServerURL: "http://server1:8080",
		},
		{
			name: "no servers configured (empty list)",
			setupServers: func() []*Server {
				return []*Server{}
			},
			expectedServerURL: "", // Очікуємо nil
		},
		{
			name: "all healthy, all zero connections, pick first",
			setupServers: func() []*Server {
				return []*Server{
					newTestServer("http://server1:8080", true, 0), // Очікується цей
					newTestServer("http://server2:8080", true, 0),
					newTestServer("http://server3:8080", true, 0),
				}
			},
			expectedServerURL: "http://server1:8080",
		},
		{
			name: "last server has least connections",
			setupServers: func() []*Server {
				return []*Server{
					newTestServer("http://server1:8080", true, 3),
					newTestServer("http://server2:8080", true, 4),
					newTestServer("http://server3:8080", true, 1), // Очікується цей
				}
			},
			expectedServerURL: "http://server3:8080",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Встановлюємо глобальну змінну `servers` для цього конкретного тестового випадку.
			// Це необхідно, оскільки `selectLeastLoadedServer` використовує глобальну змінну.
			servers = tc.setupServers()

			selected := selectLeastLoadedServer()

			if tc.expectedServerURL == "" {
				if selected != nil {
					t.Errorf("test case '%s': expected no server (nil), but got %s", tc.name, selected.URL.String())
				}
			} else {
				if selected == nil {
					t.Errorf("test case '%s': expected server %s, but got nil", tc.name, tc.expectedServerURL)
				} else if selected.URL.String() != tc.expectedServerURL {
					// Надаємо більше інформації при помилці
					var actualConns int64
					var actualHealth bool
					// Оскільки selected може бути nil, перевіряємо це перед доступом до полів
					if selected != nil {
						actualConns = selected.GetActiveConns() // Використовуємо геттер для безпеки
						actualHealth = selected.GetHealth()     // Використовуємо геттер
					}
					t.Errorf("test case '%s': expected server %s, but got %s (ActiveConns: %d, IsHealthy: %t)",
						tc.name, tc.expectedServerURL, selected.URL.String(), actualConns, actualHealth)
				}
			}
		})
	}
}
