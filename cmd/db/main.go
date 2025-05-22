package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/Wandestes/software-architecture_4/datastore"
)

var db *datastore.Db

type DbResponse struct {
	Key   string      `json:"key,omitempty"`
	Value interface{} `json:"value,omitempty"`
	Error string      `json:"error,omitempty"`
}

func dbHandler(w http.ResponseWriter, r *http.Request) {

	key := strings.TrimPrefix(r.URL.Path, "/db/")
	if key == "" && r.Method != http.MethodPost {
		http.Error(w, "Key is missing in URL path", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		if key == "" {
			http.Error(w, "Key is missing in URL path for GET request", http.StatusBadRequest)
			return
		}
		dataType := r.URL.Query().Get("type")
		if dataType == "" {
			dataType = "string"
		}

		var value interface{}
		var err error

		log.Printf("DB_SERVER: GET request for key='%s', type='%s'", key, dataType)

		if dataType == "string" {
			value, err = db.Get(key)
		} else if dataType == "int64" {
			value, err = db.GetInt64(key)
		} else {
			log.Printf("DB_SERVER: Invalid type parameter: %s", dataType)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(DbResponse{Key: key, Error: "Invalid type parameter. Supported types: string, int64"})
			return
		}

		if err != nil {
			if errors.Is(err, datastore.ErrNotFound) {
				log.Printf("DB_SERVER: Key not found: %s", key)
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(DbResponse{Key: key, Error: "not found"})
			} else if errors.Is(err, datastore.ErrWrongType) {
				log.Printf("DB_SERVER: Wrong type for key: %s, requested type: %s", key, dataType)
				w.WriteHeader(http.StatusBadRequest) // Або інший відповідний код
				json.NewEncoder(w).Encode(DbResponse{Key: key, Error: err.Error()})
			} else {
				log.Printf("DB_SERVER: Failed to get value for key %s: %v", key, err)
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(DbResponse{Key: key, Error: err.Error()})
			}
			return
		}
		log.Printf("DB_SERVER: Successfully retrieved key '%s', value: %v", key, value)
		json.NewEncoder(w).Encode(DbResponse{Key: key, Value: value})

	case http.MethodPost:
		if key == "" {
			http.Error(w, "Key is missing in URL path for POST request", http.StatusBadRequest)
			return
		}
		var requestBody struct {
			Value interface{} `json:"value"`
		}

		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			log.Printf("DB_SERVER: Failed to decode POST request body for key %s: %v", key, err)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(DbResponse{Key: key, Error: "Failed to decode request body: " + err.Error()})
			return
		}
		log.Printf("DB_SERVER: POST request for key='%s', value: %v (type: %T)", key, requestBody.Value, requestBody.Value)

		var putErr error
		switch v := requestBody.Value.(type) {
		case string:
			putErr = db.Put(key, v)
		case float64:
			putErr = db.PutInt64(key, int64(v))
		case int:
			putErr = db.PutInt64(key, int64(v))
		case int64:
			putErr = db.PutInt64(key, v)
		default:
			log.Printf("DB_SERVER: Invalid value type in POST request body for key %s: %T", key, requestBody.Value)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(DbResponse{Key: key, Error: fmt.Sprintf("Invalid value type in request body: %T. Supported: string, number (for int64)", requestBody.Value)})
			return
		}

		if putErr != nil {
			log.Printf("DB_SERVER: Failed to put value for key %s: %v", key, putErr)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(DbResponse{Key: key, Error: putErr.Error()})
			return
		}
		log.Printf("DB_SERVER: Successfully stored key '%s', value: %v", key, requestBody.Value)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(DbResponse{Key: key, Value: requestBody.Value})

	default:
		log.Printf("DB_SERVER: Method not allowed: %s", r.Method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(DbResponse{Error: "Method not allowed"})
	}
}

func main() {
	dbDir := os.Getenv("DB_DIR")
	if dbDir == "" {
		dbDir = "./database_data"
	}
	log.Printf("DB_SERVER: Initializing database in directory: %s", dbDir)

	var err error
	db, err = datastore.NewDb(dbDir)
	if err != nil {
		log.Fatalf("DB_SERVER: Failed to initialize database: %v", err)
	}
	defer func() {
		log.Println("DB_SERVER: Closing database...")
		if errClose := db.Close(); errClose != nil {
			log.Printf("DB_SERVER: Error closing database: %v", errClose)
		}
		log.Println("DB_SERVER: Database closed.")
	}()

	http.HandleFunc("/db/", dbHandler)

	port := os.Getenv("DB_PORT")
	if port == "" {
		port = "8081"
	}
	log.Printf("DB_SERVER: Starting database server on port %s...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("DB_SERVER: Failed to start DB server: %v", err)
	}
}
