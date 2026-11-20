// gateway is the "Go-countries" frontend-facing API.
// It validates requests and forwards them to country-service.
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	// Base URL of the country-service only, e.g. http://localhost:8081
	// The gateway adds the route itself (see countriesPath).
	countryServiceURL = strings.TrimRight(os.Getenv("COUNTRY_SERVICE_URL"), "/")
	client            = &http.Client{Timeout: 15 * time.Second}
)

// Route on country-service that looks up a country by name.
const countriesPath = "/countries/"

func main() {
	if countryServiceURL == "" {
		log.Fatal("COUNTRY_SERVICE_URL is not set (e.g. export COUNTRY_SERVICE_URL=http://localhost:8081)")
	}
	if u, err := url.Parse(countryServiceURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		log.Fatalf("COUNTRY_SERVICE_URL is not a valid http(s) URL: %q", countryServiceURL)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", welcome)
	mux.HandleFunc("/country", country) // GET and POST handled inside
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	addr := ":" + getenv("PORT", "8080")
	log.Printf("go-countries gateway listening on %s (country-service: %s)", addr, countryServiceURL)
	log.Fatal(http.ListenAndServe(addr, cors(mux)))
}

func welcome(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "Welcome to Go-countries 🌍",
		"what_i_can_do": []map[string]string{
			{
				"endpoint":    "GET /country?name=malawi",
				"description": "Get details about a country (capital, region, population, languages, currencies, flag)",
			},
			{
				"endpoint":    `POST /country  body: {"name": "malawi"}`,
				"description": "Same as above, using a JSON body",
			},
		},
	})
}

func country(w http.ResponseWriter, r *http.Request) {
	var name string

	switch r.Method {
	case http.MethodGet:
		name = r.URL.Query().Get("name")
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": `invalid JSON, expected {"name": "..."}`})
			return
		}
		name = body.Name
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET or POST"})
		return
	}

	name = strings.TrimSpace(name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "country name is required"})
		return
	}

	// Forward to the second API.
	resp, err := client.Get(countryServiceURL + countriesPath + url.PathEscape(name))
	if err != nil {
		log.Printf("country-service error: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "country-service unavailable"})
		return
	}
	defer resp.Body.Close()

	// Pass the downstream status and body straight through.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// cors lets a browser frontend on another origin call this API.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
