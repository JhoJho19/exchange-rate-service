package httpapi

import "net/http"

func NewRouter(handler *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /rates/updates", handler.CreateUpdate)
	mux.HandleFunc("GET /rates/updates/{id}", handler.GetUpdate)
	mux.HandleFunc("GET /rates/latest/{base}/{quote}", handler.GetLatest)
	return mux
}
