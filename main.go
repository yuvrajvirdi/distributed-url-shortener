package main

import (
	"fmt"
	"io/ioutil"
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
	numNodes       = 3
)

var (
	redisClient      *redis.Client
	mu               sync.Mutex
	randSource       rand.Source
	currentNodeIndex int32
	currentNodeMutex sync.Mutex
	serverMutex      sync.Mutex
	backendClient    *http.Client
)

func init() {
	randSource = rand.NewSource(time.Now().UnixNano())
	backendClient = &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost: numNodes,
		},
		Timeout: time.Second * 5,
	}
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
	frontendRouter.HandleFunc("/shorten", shortenUrlHandler).Methods("GET", "POST") // Use frontend handler directly
	frontendRouter.HandleFunc("/{shortCode}", redirectHandler).Methods("GET")

	// serve message for shortened URLs on the web browser
	go func() {
		fmt.Println("Frontend server is running on http://localhost:8080")
		http.ListenAndServe(":8080", frontendRouter) // Use frontend router directly
	}()

	// start node cluster (3 servers)
	go runNode(0, r1)
	go runNode(1, r2)
	go runNode(2, r3)

	// Keep the main goroutine running
	select {}
}

func runNode(nodeIndex int, router *mux.Router) {
	port := 8081 + nodeIndex
	fmt.Printf("Server %d is running on http://localhost:%d\n", nodeIndex+1, port)
	http.ListenAndServe(fmt.Sprintf(":%d", port), router)
}

func shortenUrlHandler(w http.ResponseWriter, r *http.Request) {
	url := r.FormValue("url")
	if url == "" {
		http.Error(w, "Missing 'url' parameter", http.StatusBadRequest)
		return
	}

	// generate the shortened URL using the shortenUrl function.
	shortUrl := shortenUrl(url)

	// get the next backend node in the round-robin sequence
	currentNodeMutex.Lock()
	nodePort := 8081 + currentNodeIndex
	currentNodeIndex = (currentNodeIndex + 1) % numNodes
	currentNodeMutex.Unlock()

	// Prepare the URL for forwarding the request to the selected backend node
	targetURL := fmt.Sprintf("http://localhost:%d/shorten", nodePort)

	// Create a new POST request with the "url" parameter
	req, err := http.NewRequest("POST", targetURL, strings.NewReader("url="+url))
	if err != nil {
		http.Error(w, "Failed to create request, posting", http.StatusInternalServerError)
		return
	}

	// Set the appropriate Content-Type header
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Perform the request to the backend node
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, "Failed to process request, request to backend node", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// read the response from the backend node
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to process request, load response", http.StatusInternalServerError)
		return
	}

	shortCode := string(body)
	if shortCode == "" {
		http.Error(w, "Failed to process request, short code", http.StatusInternalServerError)
		return
	}

	// extract the short code from the full shortened URL
	shortCode = extractShortCode(shortCode)

	// use the extracted short code to store the URL mapping in Redis.
	if err := storeUrlMapping(shortCode, url); err != nil {
		http.Error(w, "Failed to store URL mapping", http.StatusInternalServerError)
		return
	}

	// write the shortened URL back to the client.
	w.Write([]byte(shortUrl))
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
