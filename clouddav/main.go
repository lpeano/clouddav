package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"clouddav/auth"
	"clouddav/config"
	"clouddav/handlers"
	"clouddav/storage"
	"clouddav/storage/azureblob"
	"clouddav/storage/local"
	"clouddav/websocket" // Importa il package websocket
)

func main() {
	// Carica la configurazione
	config_path:=os.Getenv("CONFIG")
	if err := config.LoadConfig(config_path); err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Inizializza l'autenticazione Azure AD se abilitata
	if config.AppConfig.EnableAuth {
		if err := auth.InitAzureAD(&config.AppConfig); err != nil {
			log.Fatalf("Failed to initialize Azure AD authentication: %v", err)
		}
		log.Println("Azure AD authentication initialized.")
	} else {
		log.Println("Azure AD authentication is disabled.")
	}

	// Inizializza i provider di storage
	storage.ClearRegistry() // Pulisce il registro degli storage prima di inizializzare
	for _, sc := range config.AppConfig.Storages {
		var provider storage.StorageProvider
		var err error
		switch sc.Type {
		case "local":
			log.Printf("Inizializzazione provider locale: %+v", sc)
			provider, err = local.NewProvider(&sc)
		case "azure-blob":
			log.Printf("Inizializzazione provider Azure Blob: %+v", sc)
			provider, err = azureblob.NewProvider(&sc)
		default:
			log.Fatalf("Unknown storage type configured: %s", sc.Type)
		}

		if err != nil {
			log.Fatalf("Failed to initialize storage provider %s (%s): %v", sc.Name, sc.Type, err)
		}
		storage.RegisterProvider(provider)
		log.Printf("Storage provider registrato con successo: Type='%s', Name='%s'", provider.Type(), provider.Name())
	}

	// Crea il contesto principale per l'applicazione
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	// Inizializza il WebSocket Hub
	wsHub := websocket.NewHub(appCtx, &config.AppConfig)
	go wsHub.Run() // Avvia il Hub in una goroutine

	// Crea un nuovo multiplexer HTTP
	mainMux := http.NewServeMux()

	// Inizializza gli handler HTTP, passando il Hub e il multiplexer
	handlers.InitHandlers(&config.AppConfig, wsHub, mainMux) // Passa mainMux

	// Configura il server HTTP
	readTimeout, writeTimeout, idleTimeout, err := config.AppConfig.GetTimeouts()
	if err != nil {
		log.Fatalf("Failed to parse server timeouts: %v", err)
	}

	server := &http.Server{
		Addr:         ":8180", // Porta su cui il server ascolterà
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
		Handler:      mainMux, // Usa il multiplexer configurato
	}

	// Avvia il server in una goroutine
	go func() {
		log.Printf("Server avviato sulla porta %s", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server non avviato: %v", err)
		}
	}()

	// Gestione dello shutdown controllato
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM) // Cattura segnali di interruzione
	<-sigChan                                               // Blocca finché non riceve un segnale

	log.Println("Segnale di shutdown ricevuto. Spegnimento del server...")

	// Crea un contesto con timeout per lo shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Tenta lo shutdown controllato del server HTTP
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Shutdown del server forzato: %v", err)
	}

	// Annulla il contesto dell'applicazione per fermare le goroutine del Hub
	appCancel()

	log.Println("Server spento.")
}

