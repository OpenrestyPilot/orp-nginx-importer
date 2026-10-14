package main

import (
	"log"
	"net/http"
	"os"

	"github.com/ORP/orp-nginx-importer/internal/importer"
)

func main() {
	addr := os.Getenv("NGINX_IMPORTER_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	store := os.Getenv("NGINX_IMPORTER_STORAGE")
	if store == "" {
		store = "./data"
	}
	api, err := importer.New(store, os.Getenv("ORP_BACKEND_URL"))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("nginx importer listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, api.Handler()))
}
