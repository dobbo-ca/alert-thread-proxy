package main

import (
	"log"
	"net/http"
	"time"

	"github.com/dobbo-ca/alert-thread-proxy/internal/config"
	"github.com/dobbo-ca/alert-thread-proxy/internal/engine"
	"github.com/dobbo-ca/alert-thread-proxy/internal/server"
	"github.com/dobbo-ca/alert-thread-proxy/internal/slack"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	sc := slack.WithRetry(slack.New(cfg.SlackBotToken, cfg.SlackChannelID), 3, 200*time.Millisecond, time.Sleep)
	eng := engine.New(cfg, sc, time.Now)

	// periodic sweep for stale incidents + storm expiry
	go func() {
		t := time.NewTicker(time.Minute)
		for range t.C {
			eng.Sweep()
		}
	}()

	srv := server.New(eng)
	log.Printf("listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, srv.Mux()); err != nil {
		log.Fatal(err)
	}
}
