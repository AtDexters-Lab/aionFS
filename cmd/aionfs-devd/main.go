package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AtDexters-Lab/aionFS/internal/auth"
	"github.com/AtDexters-Lab/aionFS/internal/httpapi"
	"github.com/AtDexters-Lab/aionFS/internal/store"
)

func main() {
	listenAddr := flag.String("listen", "0.0.0.0:7080", "HTTP listen address")
	dataDir := flag.String("data-dir", "./data", "Directory for persisted dev state")
	tlsCert := flag.String("tls-cert", "", "Path to PEM encoded TLS certificate")
	tlsKey := flag.String("tls-key", "", "Path to PEM encoded TLS private key")
	tlsClientCA := flag.String("tls-client-ca", "", "Optional PEM bundle of client CAs for mTLS")
	tokenFile := flag.String("token-file", "", "Optional JSON map of bearer tokens to principals")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("failed to create data directory: %v", err)
	}

	var tokenProvider auth.TokenProvider
	if *tokenFile != "" {
		provider, err := auth.NewStaticProvider(*tokenFile)
		if err != nil {
			log.Fatalf("failed to load token file: %v", err)
		}
		tokenProvider = provider
		log.Printf("token provider loaded with %d entries", provider.Size())
	}

	st, err := store.NewFileStore(*dataDir)
	if err != nil {
		log.Fatalf("failed to initialise state store: %v", err)
	}
	defer st.Close()

	tlsConfig := buildTLSConfig(*tlsCert, *tlsKey, *tlsClientCA)

	api := httpapi.NewServer(st, tokenProvider)
	srv := &http.Server{
		Addr:         *listenAddr,
		Handler:      api.Router(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
		TLSConfig:    tlsConfig,
	}

	go func() {
		if tlsConfig != nil {
			log.Printf("aionfs-devd serving with TLS on %s", *listenAddr)
			if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				log.Fatalf("https server failure: %v", err)
			}
			return
		}

		log.Printf("aionfs-devd serving HTTP on %s", *listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server failure: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Printf("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("error during shutdown: %v", err)
	}
}

func buildTLSConfig(certPath, keyPath, clientCAPath string) *tls.Config {
	if certPath == "" && keyPath == "" {
		return nil
	}

	if certPath == "" || keyPath == "" {
		log.Fatal("both --tls-cert and --tls-key must be provided for TLS")
	}

	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		log.Fatalf("failed to load TLS keypair: %v", err)
	}

	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}

	if clientCAPath != "" {
		caPEM, err := os.ReadFile(clientCAPath)
		if err != nil {
			log.Fatalf("failed to read client CA bundle: %v", err)
		}
		caPool := x509.NewCertPool()
		if ok := caPool.AppendCertsFromPEM(caPEM); !ok {
			log.Fatal("no client CAs found in provided bundle")
		}
		tlsConfig.ClientCAs = caPool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsConfig
}
