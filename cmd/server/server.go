package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

var (
	dbServiceURL string
	teamName     string
)

// DbValueResponse - структура для десеріалізації відповіді від сервісу БД
type DbValueResponse struct {
	Key   string      `json:"key,omitempty"`
	Value interface{} `json:"value,omitempty"`
	Error string      `json:"error,omitempty"`
}

func init() {
	dbServiceURL = os.Getenv("DB_SERVICE_URL")
	if dbServiceURL == "" {
		log.Println("SERVER_MAIN: Warning: DB_SERVICE_URL environment variable not set. Using default http://localhost:8081/db")
		dbServiceURL = "http://localhost:8081/db"
	}

	teamName = os.Getenv("TEAM_NAME")
	if teamName == "" {
		log.Println("SERVER_MAIN: Warning: TEAM_NAME environment variable not set. Using default 'duo'")
		teamName = "duo"
	}

	currentDate := time.Now().Format("2006-01-02")
	postURL := fmt.Sprintf("%s/%s", dbServiceURL, teamName)
	requestBody, err := json.Marshal(map[string]string{"value": currentDate})
	if err != nil {
		log.Printf("SERVER_MAIN_INIT: Failed to marshal date for DB: %v", err)
		return
	}

	log.Printf("SERVER_MAIN_INIT: Attempting to POST initial date '%s' for team '%s' to DB at %s", currentDate, teamName, postURL)

	maxRetries := 5
	var resp *http.Response
	for i := 0; i < maxRetries; i++ {
		resp, err = http.Post(postURL, "application/json", bytes.NewBuffer(requestBody))
		if err == nil {
			break
		}
		log.Printf("SERVER_MAIN_INIT: Failed to POST initial date (attempt %d/%d): %v. Retrying in 2 seconds...", i+1, maxRetries, err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		log.Printf("SERVER_MAIN_INIT: Failed to POST initial date to DB service after %d retries: %v", maxRetries, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("SERVER_MAIN_INIT: DB service returned non-OK status for initial POST: %s, Body: %s", resp.Status, string(bodyBytes))
	} else {
		log.Printf("SERVER_MAIN_INIT: Successfully saved current date for team '%s' to DB.", teamName)
	}
}

func someDataHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	queryKey := r.URL.Query().Get("key")
	if queryKey == "" {
		http.Error(w, "Query parameter 'key' is required", http.StatusBadRequest)
		return
	}
	log.Printf("SERVER_HANDLER: GET /api/v1/some-data for key: %s", queryKey)

	targetURL := fmt.Sprintf("%s/%s", dbServiceURL, queryKey)

	log.Printf("SERVER_HANDLER: Forwarding GET request to DB service: %s", targetURL)
	dbResp, err := http.Get(targetURL)
	if err != nil {
		log.Printf("SERVER_HANDLER: Error requesting data from DB service for key '%s': %v", queryKey, err)
		http.Error(w, "Internal server error (DB unreachable)", http.StatusInternalServerError)
		return
	}
	defer dbResp.Body.Close()

	if dbResp.StatusCode == http.StatusNotFound {
		log.Printf("SERVER_HANDLER: Key '%s' not found in DB service.", queryKey)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if dbResp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(dbResp.Body)
		log.Printf("SERVER_HANDLER: DB service returned non-OK status for key '%s': %s, Body: %s", queryKey, dbResp.Status, string(bodyBytes))
		http.Error(w, fmt.Sprintf("Error retrieving data from DB: status %s", dbResp.Status), http.StatusInternalServerError)
		return
	}

	var dataFromDb DbValueResponse
	if err := json.NewDecoder(dbResp.Body).Decode(&dataFromDb); err != nil {
		log.Printf("SERVER_HANDLER: Error decoding response from DB service for key '%s': %v", queryKey, err)
		http.Error(w, "Internal server error (bad DB response format)", http.StatusInternalServerError)
		return
	}

	if dataFromDb.Error != "" {
		log.Printf("SERVER_HANDLER: DB service returned an error for key '%s': %s", queryKey, dataFromDb.Error)
		if dbResp.StatusCode == http.StatusBadRequest {
			http.Error(w, dataFromDb.Error, http.StatusBadRequest)
		} else {
			http.Error(w, dataFromDb.Error, http.StatusInternalServerError)
		}
		return
	}

	log.Printf("SERVER_HANDLER: Successfully retrieved value for key '%s' from DB: %v", queryKey, dataFromDb.Value)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dataFromDb)
}

// healthHandler обробляє запити /health
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	// Можна додати тіло відповіді, якщо балансувальник його очікує, наприклад:
	// w.Header().Set("Content-Type", "application/json")
	// json.NewEncoder(w).Encode(map[string]string{"status": "OK"})
	log.Printf("SERVER_HANDLER: GET /health -> 200 OK")
}

func main() {
	http.HandleFunc("/api/v1/some-data", someDataHandler)
	http.HandleFunc("/health", healthHandler) // <--- ДОДАНО МАРШРУТ ДЛЯ HEALTH CHECK

	serverPort := os.Getenv("SERVER_PORT")
	if serverPort == "" {
		serverPort = "8080"
	}
	log.Printf("SERVER_MAIN: Main server starting on port %s...", serverPort)
	if err := http.ListenAndServe(":"+serverPort, nil); err != nil {
		log.Fatalf("SERVER_MAIN: Failed to start main server: %v", err)
	}
}
