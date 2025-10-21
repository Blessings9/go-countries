package main

import (
	"log"
	"net/http"
)

func home(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("The Economy is doing badd"))

}
func main() {

	mux := http.NewServeMux()
	mux.HandleFunc("/", home)

	log.Print("Starting Server on: 8080")
	err := http.ListenAndServe(":8080", mux)
	log.Fatal(err)
}
