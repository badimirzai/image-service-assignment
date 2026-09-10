package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/badimirzai/image-service/internal/httpapi"
	"github.com/badimirzai/image-service/internal/service"
	"github.com/badimirzai/image-service/internal/store"
)

func main() {
	st := store.NewMemoryStore()
	svc := service.NewService(st)
	api := httpapi.NewServer(svc)

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	addr := ":" + port
	fmt.Printf("image service listening on %s\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "server failed: %v\n", err)
		os.Exit(1)
	}
}
