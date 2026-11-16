// country-service talks to the REST Countries v5 API and returns trimmed country data.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

// Only ask upstream for what we return.
const responseFields = "names.common,names.official,codes.alpha_2,capitals,region,subregion," +
	"population,area.kilometers,languages,currencies,flag.emoji,flag.url_png"

var (
	baseURL = getenv("RESTCOUNTRIES_URL", "https://api.restcountries.com/countries/v5")
	apiKey  = os.Getenv("RESTCOUNTRIES_API_KEY")
	client  = &http.Client{Timeout: 10 * time.Second}
)

// ---- Upstream (REST Countries v5) shapes ----

type apiCountry struct {
	Names struct {
		Common   string `json:"common"`
		Official string `json:"official"`
	} `json:"names"`
	Codes struct {
		Alpha2 string `json:"alpha_2"`
	} `json:"codes"`
	Capitals []struct {
		Name string `json:"name"`
	} `json:"capitals"`
	Region     string  `json:"region"`
	Subregion  string  `json:"subregion"`
	Population float64 `json:"population"`
	Area       struct {
		Kilometers float64 `json:"kilometers"`
	} `json:"area"`
	// Passed through untouched so we don't depend on their exact inner shape.
	Languages  any `json:"languages"`
	Currencies any `json:"currencies"`
	Flag       struct {
		Emoji  string `json:"emoji"`
		URLPNG string `json:"url_png"`
	} `json:"flag"`
}

type apiResponse struct {
	Data struct {
		Objects []apiCountry `json:"objects"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"errors"`
}

// ---- What we send to the gateway ----

type Country struct {
	Name       string   `json:"name"`
	Official   string   `json:"official_name"`
	Code       string   `json:"code"`
	Capitals   []string `json:"capitals"`
	Region     string   `json:"region"`
	Subregion  string   `json:"subregion"`
	Population int64    `json:"population"`
	AreaKm2    float64  `json:"area_km2"`
	Languages  any      `json:"languages"`
	Currencies any      `json:"currencies"`
	FlagEmoji  string   `json:"flag_emoji"`
	FlagPNG    string   `json:"flag_png"`
}

func main() {
	if apiKey == "" {
		log.Fatal("RESTCOUNTRIES_API_KEY is not set (tip: export RESTCOUNTRIES_API_KEY=rc_live_demo to test)")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /countries/{name}", getCountry)

	addr := ":" + getenv("PORT", "8081")
	log.Printf("country-service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func getCountry(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	// 1) Exact (case-insensitive) match on the common name, e.g. "Niger" only.
	status, res, err := fetch("/names.common/"+url.PathEscape(name), nil)
	if err != nil {
		upstreamDown(w, err)
		return
	}
	if status == http.StatusOK && len(res.Data.Objects) > 0 {
		writeJSON(w, http.StatusOK, convert(res.Data.Objects))
		return
	}
	if status != http.StatusOK && status != http.StatusNotFound {
		upstreamError(w, status, res)
		return
	}

	// 2) Fallback: partial match across common/official/alternate/native names.
	status, res, err = fetch("/name", url.Values{"q": {name}})
	if err != nil {
		upstreamDown(w, err)
		return
	}
	if status != http.StatusOK && status != http.StatusNotFound {
		upstreamError(w, status, res)
		return
	}
	if status == http.StatusNotFound || len(res.Data.Objects) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "country not found: " + name})
		return
	}
	writeJSON(w, http.StatusOK, convert(res.Data.Objects))
}

// fetch calls the upstream API with the bearer token and decodes the JSON body.
func fetch(path string, params url.Values) (int, apiResponse, error) {
	if params == nil {
		params = url.Values{}
	}
	params.Set("response_fields", responseFields)
	params.Set("limit", "10")

	req, err := http.NewRequest(http.MethodGet, baseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return 0, apiResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, apiResponse{}, err
	}
	defer resp.Body.Close()

	var out apiResponse
	// Body may not be JSON on some failures; ignore decode errors and rely on status.
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, nil
}

func convert(in []apiCountry) []Country {
	out := make([]Country, 0, len(in))
	for _, c := range in {
		caps := make([]string, 0, len(c.Capitals))
		for _, cp := range c.Capitals {
			caps = append(caps, cp.Name)
		}
		out = append(out, Country{
			Name: c.Names.Common, Official: c.Names.Official, Code: c.Codes.Alpha2,
			Capitals: caps, Region: c.Region, Subregion: c.Subregion,
			Population: int64(c.Population), AreaKm2: c.Area.Kilometers,
			Languages: c.Languages, Currencies: c.Currencies,
			FlagEmoji: c.Flag.Emoji, FlagPNG: c.Flag.URLPNG,
		})
	}
	return out
}

func upstreamDown(w http.ResponseWriter, err error) {
	log.Printf("upstream request failed: %v", err)
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not reach REST Countries"})
}

// upstreamError maps upstream failures to safe messages for the gateway.
// Auth problems are our config's fault, so we log details but don't expose them.
func upstreamError(w http.ResponseWriter, status int, res apiResponse) {
	msg, code := "", ""
	if len(res.Errors) > 0 {
		msg, code = res.Errors[0].Message, res.Errors[0].Code
	}
	log.Printf("upstream returned %d (code=%q): %s", status, code, msg)

	switch status {
	case http.StatusTooManyRequests:
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limited by REST Countries, try again shortly"})
	case http.StatusUnauthorized, http.StatusForbidden:
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "country-service is not authorized with REST Countries (check API key / plan limits)"})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("REST Countries returned %d", status)})
	}
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
