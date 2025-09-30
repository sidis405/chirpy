package main

import (
	"database/sql"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/sidis405/chirpy/internal/database"
)
import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
)

const port = "8080"

type apiConfig struct {
	fileserverHits atomic.Int32
	db             *database.Queries
}

type User struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email     string    `json:"email"`
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (cfg *apiConfig) hitsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	message := fmt.Sprintf("<html>\n  <body>\n    <h1>Welcome, Chirpy Admin</h1>\n    <p>Chirpy has been visited %d times!</p>\n  </body>\n</html>", cfg.fileserverHits.Load())
	_, _ = w.Write([]byte(message))
}

func (cfg *apiConfig) resetHandler() {
	cfg.fileserverHits = atomic.Int32{}
}

func main() {
	_ = godotenv.Load()

	dbURL := os.Getenv("DB_URL")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		panic(err)
	}

	apiCfg := apiConfig{
		fileserverHits: atomic.Int32{},
		db:             database.New(db),
	}

	mux := http.NewServeMux()
	filePathRoot := http.Dir(".")
	fs := http.FileServer(filePathRoot)
	mux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app/", fs)))

	mux.HandleFunc("GET /api/healthz", handleHealthz)
	mux.HandleFunc("POST /api/validate_chirp", handleValidateChirp)
	mux.HandleFunc("POST /api/users", func(w http.ResponseWriter, r *http.Request) {
		type parameters struct {
			Email string `json:"email"`
		}
		params := parameters{}
		decoder := json.NewDecoder(r.Body)
		err := decoder.Decode(&params)
		if err != nil {
			respondWithError(w, 500, "cannot unmarshal data")
			return
		}
		user, err := apiCfg.db.CreateUser(r.Context(), params.Email)
		if err != nil {
			respondWithError(w, 400, fmt.Sprintf("%q", err))
		}

		respondWithJson(w, 201, User{
			ID:        user.ID,
			CreatedAt: user.CreatedAt,
			UpdatedAt: user.UpdatedAt,
			Email:     user.Email,
		})
		return
	})

	mux.HandleFunc("GET /admin/metrics", apiCfg.hitsHandler)
	mux.HandleFunc("POST /admin/reset", func(w http.ResponseWriter, r *http.Request) {
		isDev := os.Getenv("PLATFORM") == "dev"

		if !isDev {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(403)
			_, _ = w.Write([]byte("forbidden"))
		}

		apiCfg.resetHandler()
		apiCfg.db.DeleteAllUsers(r.Context())

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(200)
		message := fmt.Sprintf("Hits: %d", apiCfg.fileserverHits.Load())
		_, _ = w.Write([]byte(message))
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	log.Printf("Serving files from %s on port: %s\n", filePathRoot, port)
	log.Fatal(srv.ListenAndServe())
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write([]byte("OK"))
}

func handleValidateChirp(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Body string `json:"body"`
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)
	if err != nil {
		respondWithError(w, 500, "Something went wrong")
		return
	}

	if len(params.Body) > 140 {
		respondWithError(w, 400, "Chirp is too long")
		return
	}

	type okResponse struct {
		CleanedBody string `json:"cleaned_body"`
	}

	// redacting stuff
	words := strings.Split(params.Body, " ")
	var out []string
	bad := []string{
		"kerfuffle", "sharbert", "fornax",
	}
	for _, word := range words {
		if slices.Contains(bad, strings.ToLower(word)) {
			out = append(out, "****")
		} else {
			out = append(out, word)
		}
	}

	respondWithJson(w, 200, okResponse{CleanedBody: strings.Join(out, " ")})
	return
}

func respondWithError(w http.ResponseWriter, code int, msg string) {
	type errResponse struct {
		Error string `json:"error"`
	}

	resBody := errResponse{Error: msg}
	data, err := json.Marshal(resBody)
	if err != nil {
		log.Printf("Error mashalling JSON: %s", err)
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(data)
	return
}

func respondWithJson(w http.ResponseWriter, code int, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error mashalling JSON: %s", err)
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(data)
}
