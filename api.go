package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/cors"
)

type ApiError struct {
	Error string
}

type apiFunc func(http.ResponseWriter, *http.Request) error

func makeHttpHandleFunc(f apiFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := f(w, r); err != nil {
			// hanle the error, inhance to add correct error type based on the response error
			WriteJSON(w, http.StatusBadRequest, ApiError{Error: err.Error()})
		}
	}
}

type APIServer struct {
	addr          string
	searchService SearchService
}

func NewAPIServer(addr string, searchServ SearchService) *APIServer {
	return &APIServer{ // or New(APIServer)
		addr:          addr,
		searchService: searchServ,
	}
}

func (s *APIServer) Run() {
	router := http.NewServeMux()

	router.HandleFunc("GET /api/v1/retail/netsearch", makeHttpHandleFunc(s.handleGetHello))
	//router.HandleFunc("GET /account/{id}", makeHttpHandleFunc(s.handelGetAccountByID))
	router.HandleFunc("POST /api/v1/retail/netsearch", makeHttpHandleFunc(s.handleProdSearch))

	// middlewareChain: order is matter
	middlewareChain := MiddlewareChain(
		RequiredAuthMiddleware,
		RequestLoggerMiddleware,
	)

	// cors
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	})

	//
	server := http.Server{
		Addr:    s.addr,
		Handler: c.Handler(middlewareChain(router)),
	}

	go func() {
		// service connections
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	log.Printf("Server has started %s", s.addr)

	// Wait for interrupt signal to gracefully shutdown the server with
	// a timeout of 5 seconds.

	// The Context type provides a Done() method. This returns a channel that receives an empty struct{} type every time
	//  the context receives a cancellation signal

	quit := make(chan os.Signal, 1)

	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit // receive data from channel will be blocked until the data is available in the channel

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// shutdown server
	log.Println("Shutdown Server ...")
	if err := server.Shutdown(ctx); err != nil {
		log.Fatal("Server Shutdown:", err)
	}

	// catching ctx.Done(). timeout of 5 seconds
	<-ctx.Done()
	log.Println("timeout of 5 seconds.")
	//}
	log.Println("Server exiting")
}
