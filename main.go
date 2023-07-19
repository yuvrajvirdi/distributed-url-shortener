package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/mux"
	"golang.org/x/net/context"
)

const (
	shortUrlLength = 5
	letters        = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

var (
	redisClient *redis.Client
	mu          sync.Mutex
	randSource  rand.Source
)

func init() {
	randSource = rand.NewSource(time.Now().UnixNano())
}

func main() {
	// init redis client
	redisClient = redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   0,
	})

	// test Redis connection
	ctx := context.Background()
	err := redisClient.Set(ctx, "test_key", "test_value", 0).Err()
	if err != nil {
		fmt.Println("Failed to connect to Redis:", err)
	} else {
		fmt.Println("Successfully connected to Redis.")
	}

	// create three new routers for each server
	r1 := mux.NewRouter()
	r2 := mux.NewRouter()
	r3 := mux.NewRouter()

	// assign handlers for each router
	r1.HandleFunc("/shorten", shortenUrlHandler).Methods("GET", "POST")
	r2.HandleFunc("/shorten", shortenUrlHandler).Methods("GET", "POST")
	r3.HandleFunc("/shorten", shortenUrlHandler).Methods("GET", "POST")

	// create new router for the frontend (8080 port)
	frontendRouter := mux.NewRouter()
	frontendRouter.HandleFunc("/{shortCode}", redirectHandler).Methods("GET")

	// serve message for shortened URLs on web broswer
	go func() {
		http.Handle("/", frontendRouter)
		fmt.Println("Frontend server is running on http://localhost:8080")
		http.ListenAndServe(":8080", nil)
	}()

	// start first http server on port 8081
	go func() {
		fmt.Println("Server 1 is running on http://localhost:8081")
		http.ListenAndServe(":8081", r1)
	}()

	// start the second http server on port 8082
	go func() {
		fmt.Println("Server 2 is running on http://localhost:8082")
		http.ListenAndServe(":8082", r2)
	}()

	// start the third http server on port 8082
	go func() {
		fmt.Println("Server 3 is running on http://localhost:8083")
		http.ListenAndServe(":8083", r3)
	}()

	// Keep the main goroutine running
	select {}
}

func shortenUrlHandler(w http.ResponseWriter, r *http.Request) {
	url := r.FormValue("url")
	if url == "" {
		http.Error(w, "Missing 'url' parameter", http.StatusBadRequest)
		return
	}

	shortUrl := shortenUrl(url)
	if err := storeUrlMapping(shortUrl, url); err != nil {
		http.Error(w, "Failed to store URL mapping", http.StatusInternalServerError)
		return
	}

	// respond with shortened url
	fmt.Fprintf(w, "Shortened URL: %s", shortUrl)
}

func shortenUrl(url string) string {
	hashCode := rollingHash(url)

	code := ""
	for i := 0; i < shortUrlLength; i++ {
		idx := hashCode % int64(len(letters))
		code += string(letters[idx])
		hashCode /= int64(len(letters))
	}

	return fmt.Sprintf("http://localhost:8080/%s", code)
}

func rollingHash(s string) int64 {
	const base = 31
	var hash int64 = 0

	for _, ch := range s {
		hash = (hash*base + int64(ch)) % (1 << 31)
	}

	return hash
}

func storeUrlMapping(shortUrl, originalUrl string) error {
	ctx := context.Background()

	// extract hashed short code from the full url
	shortCode := extractShortCode(shortUrl)

	// use goroutine to execute the Redis set operation concurrently
	go func() {
		if err := redisClient.Set(ctx, shortCode, originalUrl, 0).Err(); err != nil {
			fmt.Printf("Error storing URL mapping: %v\n", err)
		}
	}()
	return nil
}

func extractShortCode(shortUrl string) string {
	// short code is in last part of url
	parts := strings.Split(shortUrl, "/")
	shortCode := parts[len(parts)-1]

	// remove any trailing slashes or spaces
	shortCode = strings.TrimRight(shortCode, "/ ")

	return shortCode
}

func redirectHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	shortCode := vars["shortCode"]

	ctx := context.Background()
	originalUrl, err := redisClient.Get(ctx, shortCode).Result()
	if err != nil {
		http.Error(w, "Shortened URL not found", http.StatusNotFound)
		return
	}

	if !strings.HasPrefix(originalUrl, "http://") && !strings.HasPrefix(originalUrl, "https://") {
		originalUrl = "http://" + originalUrl
	}

	http.Redirect(w, r, originalUrl, http.StatusFound)
}
