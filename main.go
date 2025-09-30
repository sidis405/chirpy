package main

import (
	"database/sql"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
	"github.com/sidis405/chirpy/internal/auth"
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
	secret         string
	polkaApiKey    string
}

type User struct {
	ID           uuid.UUID `json:"id"`
	IsChirpyRed  bool      `json:"is_chirpy_red"`
	Email        string    `json:"email"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Token        string    `json:"token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
}

type Chirp struct {
	ID        uuid.UUID `json:"id"`
	Body      string    `json:"body"`
	UserID    uuid.UUID `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
		secret:         os.Getenv("SECRET"),
		polkaApiKey:    os.Getenv("POLKA_KEY"),
	}

	mux := http.NewServeMux()
	filePathRoot := http.Dir(".")
	fs := http.FileServer(filePathRoot)
	mux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app/", fs)))

	mux.HandleFunc("GET /api/healthz", handleHealthz)
	mux.HandleFunc("POST /api/validate_chirp", handleValidateChirp)

	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		type parameters struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		params := parameters{}
		decoder := json.NewDecoder(r.Body)
		err := decoder.Decode(&params)

		if err != nil {
			respondWithError(w, 500, "cannot unmarshal data")
			return
		}

		user, err := apiCfg.db.GetUserByEmail(r.Context(), params.Email)
		if err != nil {
			respondWithError(w, 500, "error fetching user")
			return
		}
		matchesPwd, err := auth.CheckPasswordHash(params.Password, user.HashedPassword)
		if err != nil {
			respondWithError(w, 500, "cannot check pwd")
			return
		}

		if !matchesPwd {
			respondWithError(w, 401, "unauthorized")
			return
		}

		token, err := auth.MakeJWT(user.ID, apiCfg.secret)

		if err != nil {
			respondWithError(w, 401, fmt.Sprintf("%q", err))
		}

		refreshTokenString, err := auth.MakeRefreshToken()

		if err != nil {
			respondWithError(w, 401, fmt.Sprintf("%q", err))
		}

		refreshToken, err := apiCfg.db.CreateRefreshToken(r.Context(), database.CreateRefreshTokenParams{
			Token:     refreshTokenString,
			UserID:    user.ID,
			ExpiresAt: time.Now().Add(time.Duration(24*60) * time.Hour),
		})

		respondWithJson(w, 200, User{
			ID:           user.ID,
			Email:        user.Email,
			CreatedAt:    user.CreatedAt,
			UpdatedAt:    user.UpdatedAt,
			Token:        token,
			RefreshToken: refreshToken.Token,
			IsChirpyRed:  user.IsChirpyRed,
		})
		return
	})
	mux.HandleFunc("POST /api/refresh", func(w http.ResponseWriter, r *http.Request) {
		token, err := auth.GetBearerToken(r.Header)
		if err != nil {
			respondWithError(w, 400, "no token found in header")
			return
		}
		refreshToken, err := apiCfg.db.GetRefreshToken(r.Context(), token)

		if err != nil {
			respondWithError(w, 401, "unauthorized")
			return
		}

		type tokenResponse struct {
			Token string `json:"token"`
		}

		accessToken, err := auth.MakeJWT(refreshToken.UserID, apiCfg.secret)

		if err != nil {
			respondWithError(w, 500, "cannot generate new access token")
		}

		respondWithJson(w, 200, tokenResponse{Token: accessToken})
		return
	})
	mux.HandleFunc("POST /api/revoke", func(w http.ResponseWriter, r *http.Request) {
		token, err := auth.GetBearerToken(r.Header)
		if err != nil {
			respondWithError(w, 400, "no token found in header")
			return
		}
		refreshToken, err := apiCfg.db.GetRefreshToken(r.Context(), token)

		if err != nil {
			respondWithError(w, 401, "unauthorized")
			return
		}

		err = apiCfg.db.RevokeRefreshToken(r.Context(), refreshToken.Token)

		if err != nil {
			respondWithError(w, 500, "cannot revoke token")
			return
		}

		respondWithJson(w, 204, nil)
		return
	})
	mux.HandleFunc("POST /api/users", func(w http.ResponseWriter, r *http.Request) {
		type parameters struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		params := parameters{}
		decoder := json.NewDecoder(r.Body)
		err := decoder.Decode(&params)
		if err != nil {
			respondWithError(w, 500, "cannot unmarshal data")
			return
		}

		hashedPassword, err := auth.HashPassword(params.Password)
		if err != nil {
			respondWithError(w, 500, "hashing error")
			return
		}

		user, err := apiCfg.db.CreateUser(r.Context(), database.CreateUserParams{
			Email:          params.Email,
			HashedPassword: hashedPassword,
		})
		if err != nil {
			respondWithError(w, 400, fmt.Sprintf("%q", err))
		}

		respondWithJson(w, 201, User{
			ID:          user.ID,
			CreatedAt:   user.CreatedAt,
			UpdatedAt:   user.UpdatedAt,
			Email:       user.Email,
			IsChirpyRed: user.IsChirpyRed,
		})
		return
	})
	mux.HandleFunc("PUT /api/users", func(w http.ResponseWriter, r *http.Request) {
		token, err := auth.GetBearerToken(r.Header)
		if err != nil {
			respondWithError(w, 401, "missing token")
			return
		}

		userID, err := auth.ValidateJWT(token, apiCfg.secret)
		if err != nil {
			respondWithError(w, 401, "invalid token")
			return
		}

		type parameters struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		params := parameters{}
		decoder := json.NewDecoder(r.Body)
		err = decoder.Decode(&params)
		if err != nil {
			respondWithError(w, 500, "cannot unmarshal data")
			return
		}

		hashedPassword, err := auth.HashPassword(params.Password)
		if err != nil {
			respondWithError(w, 500, "hashing error")
			return
		}

		user, err := apiCfg.db.UpdateUser(r.Context(), database.UpdateUserParams{
			ID:             userID,
			Email:          params.Email,
			HashedPassword: hashedPassword,
		})

		respondWithJson(w, 200, User{
			ID:          user.ID,
			CreatedAt:   user.CreatedAt,
			UpdatedAt:   user.UpdatedAt,
			Email:       user.Email,
			IsChirpyRed: user.IsChirpyRed,
		})
		return
	})
	mux.HandleFunc("POST /api/chirps", func(w http.ResponseWriter, r *http.Request) {

		token, err := auth.GetBearerToken(r.Header)
		if err != nil {
			respondWithError(w, 401, "missing token")
			return
		}

		userID, err := auth.ValidateJWT(token, apiCfg.secret)
		if err != nil {
			respondWithError(w, 401, "invalid token")
			return
		}

		type parameters struct {
			Body string `json:"body"`
		}
		params := parameters{}
		decoder := json.NewDecoder(r.Body)
		err = decoder.Decode(&params)
		if err != nil {
			respondWithError(w, 500, "cannot unmarshal data")
			return
		}
		chirp, err := apiCfg.db.CreateChirp(r.Context(), database.CreateChirpParams{
			Body:   params.Body,
			UserID: userID,
		})

		respondWithJson(w, 201, dbChirpToChirpStruct(chirp))

		return
	})
	mux.HandleFunc("GET /api/chirps", func(w http.ResponseWriter, r *http.Request) {
		rawChirps, err := apiCfg.db.GetAllChirps(r.Context())
		if err != nil {
			respondWithError(w, 500, "cannot fetch chirps")
			return
		}
		var chirps []Chirp

		for _, chirp := range rawChirps {
			chirps = append(chirps, dbChirpToChirpStruct(chirp))
		}

		respondWithJson(w, 200, chirps)
	})
	mux.HandleFunc("GET /api/chirps/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		chirpId, err := uuid.Parse(id)
		if err != nil {
			respondWithError(w, 401, "invalid chirp uuid")
		}

		chirp, err := apiCfg.db.GetChirp(r.Context(), chirpId)
		if err != nil {
			respondWithError(w, 404, "not found")
			return
		}

		respondWithJson(w, 200, dbChirpToChirpStruct(chirp))
		return
	})
	mux.HandleFunc("DELETE /api/chirps/{id}", func(w http.ResponseWriter, r *http.Request) {
		token, err := auth.GetBearerToken(r.Header)
		if err != nil {
			respondWithError(w, 401, "missing token")
			return
		}

		userID, err := auth.ValidateJWT(token, apiCfg.secret)
		if err != nil {
			respondWithError(w, 401, "invalid token")
			return
		}

		id := r.PathValue("id")

		chirpId, err := uuid.Parse(id)
		if err != nil {
			respondWithError(w, 401, "invalid chirp uuid")
		}

		chirp, err := apiCfg.db.GetChirp(r.Context(), chirpId)
		if err != nil {
			respondWithError(w, 404, "not found")
			return
		}

		if chirp.UserID != userID {
			respondWithError(w, 403, "unauthorized")
			return
		}

		err = apiCfg.db.DeleteChirp(r.Context(), chirpId)

		if err != nil {
			respondWithError(w, 500, "cannot delete chirp")
		}

		respondWithJson(w, 204, nil)

		return
	})

	mux.HandleFunc("POST /api/polka/webhooks", func(w http.ResponseWriter, r *http.Request) {
		apiKey, err := auth.GetAPIKey(r.Header)
		if err != nil {
			respondWithError(w, 401, "invalid apikey")
			return
		}

		if apiKey != apiCfg.polkaApiKey {
			respondWithError(w, 401, "invalid apikey")
			return
		}

		type parameters struct {
			Event string `json:"event"`
			Data  struct {
				UserId string `json:"user_id"`
			} `json:"data"`
		}

		params := parameters{}
		decoder := json.NewDecoder(r.Body)
		err = decoder.Decode(&params)

		if err != nil {
			respondWithError(w, 500, "cannot unmarshal data")
			return
		}

		if params.Event != "user.upgraded" {
			respondWithJson(w, 204, nil)
			return
		}

		userUuid, err := uuid.Parse(params.Data.UserId)
		if err != nil {
			respondWithError(w, 400, "invalid uuid")
			return
		}

		_, err = apiCfg.db.UpgradeUser(r.Context(), userUuid)
		if err != nil {
			respondWithError(w, 404, "not found")
			return
		}

		respondWithJson(w, 204, nil)
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

func dbChirpToChirpStruct(chirp database.Chirp) Chirp {
	return Chirp{
		ID:        chirp.ID,
		Body:      chirp.Body,
		UserID:    chirp.UserID,
		CreatedAt: chirp.CreatedAt,
		UpdatedAt: chirp.UpdatedAt,
	}
}
