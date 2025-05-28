package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Assicurati che i percorsi di importazione siano corretti per la tua struttura di progetto
	"clouddav/auth"
	"clouddav/config"
	"clouddav/handlers"
	"clouddav/storage"
	"clouddav/storage/azureblob"
	"clouddav/storage/local"
	"clouddav/websocket"
)

func main() {
	// Carica la configurazione
	// Il percorso del file di configurazione può essere passato tramite una variabile d'ambiente
	// o un flag da riga di comando. Qui usiamo una variabile d'ambiente.
	configPath := os.Getenv("CONFIG_FILE_PATH")
	if configPath == "" {
		// Se non specificato, usa un percorso di default o esci se è mandatorio
		configPath = "config.yaml" // Assicurati che questo file esista o sia il tuo default
		log.Printf("CONFIG_FILE_PATH non impostata, utilizzo default: %s", configPath)
	}

	if err := config.LoadConfig(configPath); err != nil {
		log.Fatalf("Errore nel caricamento della configurazione da '%s': %v", configPath, err)
	}

	// Inizializza l'autenticazione Azure AD se abilitata
	if config.AppConfig.EnableAuth {
		if err := auth.InitAzureAD(&config.AppConfig); err != nil { // Passa il puntatore alla configurazione
			log.Fatalf("Errore nell'inizializzazione dell'autenticazione Azure AD: %v", err)
		}
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Println("Autenticazione Azure AD inizializzata.")
		}
	} else {
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Println("Autenticazione Azure AD disabilitata.")
		}
	}

	// Inizializza i provider di storage
	storage.ClearRegistry() // Pulisce il registro prima di inizializzare
	for _, sc := range config.AppConfig.Storages {
		var provider storage.StorageProvider
		var err error
		switch sc.Type {
		case "local":
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Inizializzazione provider locale: %+v", sc)
			}
			provider, err = local.NewProvider(&sc)
		case "azure-blob":
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Inizializzazione provider Azure Blob: %+v", sc)
			}
			provider, err = azureblob.NewProvider(&sc)
		default:
			log.Fatalf("Tipo di storage non riconosciuto configurato: %s", sc.Type)
		}

		if err != nil {
			log.Fatalf("Errore nell'inizializzazione del provider di storage %s (%s): %v", sc.Name, sc.Type, err)
		}
		if err := storage.RegisterProvider(provider); err != nil {
			log.Fatalf("Errore nella registrazione del provider di storage %s: %v", provider.Name(), err)
		}
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Provider di storage registrato con successo: Tipo='%s', Nome='%s'", provider.Type(), provider.Name())
		}
	}

	// Crea il contesto principale per l'applicazione
	appCtx, appCancel := context.WithCancel(context.Background())
	defer appCancel()

	// Inizializza l'Hub WebSocket
	wsHub := websocket.NewHub(appCtx, &config.AppConfig) // Passa il puntatore alla configurazione
	go wsHub.Run()                                     // Avvia l'Hub in una goroutine

	// Crea un nuovo multiplexer HTTP
	mainMux := http.NewServeMux()

	// Inizializza gli handler HTTP, passando l'Hub e il multiplexer
	// Assicurati che config.AppConfig sia un puntatore se InitRoutes lo richiede come tale
	handlers.InitRoutes(&config.AppConfig, wsHub, mainMux) // Passa il puntatore alla configurazione e mainMux

	// Configura il server HTTP
	readTimeout, writeTimeout, idleTimeout, err := config.AppConfig.GetTimeouts()
	if err != nil {
		log.Fatalf("Errore nel parsing dei timeout del server: %v", err)
	}

	// Recupera la porta dalla configurazione o usa un default
	// Questa parte potrebbe essere migliorata leggendo da config.AppConfig se hai un campo per la porta
	serverPort := os.Getenv("PORT")
	if serverPort == "" {
		serverPort = "8180" // Default port
	}
	serverAddr := ":" + serverPort


	server := &http.Server{
		Addr:         serverAddr,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
		Handler:      mainMux, // Usa il multiplexer configurato
	}

	// Avvia il server in una goroutine
	go func() {
		log.Printf("Server avviato sulla porta %s", serverAddr)
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

	// Annulla il contesto dell'applicazione per fermare le goroutine dell'Hub
	appCancel()

	log.Println("Server spento.")
}
