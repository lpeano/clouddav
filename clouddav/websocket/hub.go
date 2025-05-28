package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http" // Necessario per upgrader e CheckOrigin
	"sync"
	"time"

	"clouddav/auth"    // Assicurati che il percorso sia corretto
	"clouddav/config"  // Assicurati che il percorso sia corretto
	"clouddav/storage" // Assicurati che il percorso sia corretto

	// Rimuovi import specifici di provider se non usati direttamente qui
	// "clouddav/storage/azureblob"
	// "clouddav/storage/local"

	"github.com/gorilla/websocket" // Necessario per upgrader
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Permetti tutte le origini per semplicità. Da rivedere in produzione.
	},
}

// Hub gestisce i client WebSocket e Long Polling.
type Hub struct {
	clients            map[*Client]bool
	register           chan *Client
	unregister         chan *Client
	broadcast          chan Message // Attualmente non usato per broadcast generico
	config             *config.Config
	ctx                context.Context
	cancel             context.CancelFunc
	OngoingFileUploads map[string]*UploadSessionState
	FileUPloadsMutex   sync.Mutex
}

// NewHub crea un nuovo Hub.
func NewHub(appCtx context.Context, cfg *config.Config) *Hub {
	hubCtx, hubCancel := context.WithCancel(appCtx)
	return &Hub{
		clients:            make(map[*Client]bool),
		register:           make(chan *Client),
		unregister:         make(chan *Client),
		broadcast:          make(chan Message),
		config:             cfg,
		ctx:                hubCtx,
		cancel:             hubCancel,
		OngoingFileUploads: make(map[string]*UploadSessionState),
	}
}

// Run avvia l'Hub, gestendo la registrazione e de-registrazione dei client.
func (h *Hub) Run() {
	go h.cleanupLongPollingClients()
	go h.cleanupOrphanedUploads()

	// Corretta la chiamata a IsLogLevel
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Println("Hub in esecuzione...")
	}

	for {
		select {
		case <-h.ctx.Done():
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Println("Contesto dell'Hub cancellato, arresto in corso...")
			}
			for client := range h.clients {
				h.unregisterClient(client, "Server shutdown")
			}
			return
		case client := <-h.register:
			h.clients[client] = true
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Client registrato (User: %s, WS: %t). Client totali: %d", client.userIdentifier, client.isWS, len(h.clients))
			}
			initialConfigMsg := Message{
				Type: "config_update",
				Payload: map[string]interface{}{
					"client_ping_interval_ms": h.config.ClientPingIntervalMs,
				},
			}
			go func(c *Client, msg Message) {
				select {
				case c.send <- msg:
					// Corretta la chiamata a IsLogLevel
					if config.IsLogLevel(config.LogLevelDebug) {
						log.Printf("Inviata configurazione iniziale al client (User: %s, WS: %t)", c.userIdentifier, c.isWS)
					}
				case <-time.After(5 * time.Second):
					log.Printf("Timeout invio configurazione iniziale al client (User: %s, WS: %t)", c.userIdentifier, c.isWS)
				case <-c.ctx.Done():
					// Corretta la chiamata a IsLogLevel
					if config.IsLogLevel(config.LogLevelDebug) {
						log.Printf("Contesto client cancellato durante invio configurazione iniziale (User: %s, WS: %t)", c.userIdentifier, c.isWS)
					}
				}
			}(client, initialConfigMsg)

		case client := <-h.unregister:
			h.unregisterClient(client, "Client disconnected")

		case message := <-h.broadcast:
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Messaggio ricevuto su canale broadcast (non implementato per invio a tutti): %s", message.Type)
			}
		}
	}
}

func (h *Hub) unregisterClient(client *Client, reason string) {
	if _, ok := h.clients[client]; ok {
		delete(h.clients, client)
		close(client.send)
		if client.conn != nil {
			client.conn.Close()
		}
		client.cancel()

		// Corretta la chiamata a IsLogLevel
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Client de-registrato (User: %s, WS: %t, Reason: %s). Client totali: %d", client.userIdentifier, client.isWS, reason, len(h.clients))
		}

		var uploadsToCancelForProvider []struct {
			UploadKey    string
			SessionState *UploadSessionState
		}
		h.FileUPloadsMutex.Lock()
		// Corretta la chiamata a IsLogLevel
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("UnregisterClient: Acquisito FileUPloadsMutex per client %s", client.userIdentifier)
		}
		tempKeysToDelete := []string{}
		for uploadKey, sessionState := range h.OngoingFileUploads {
			clientMatch := false
			if client.claims != nil && sessionState.Claims != nil && client.claims.Email == sessionState.Claims.Email {
				clientMatch = true
			} else if client.claims == nil && sessionState.Claims != nil && client.userIdentifier != "" && client.userIdentifier == sessionState.Claims.Subject {
				clientMatch = true
			}

			if clientMatch {
				uploadsToCancelForProvider = append(uploadsToCancelForProvider, struct {
					UploadKey    string
					SessionState *UploadSessionState
				}{uploadKey, sessionState})
				tempKeysToDelete = append(tempKeysToDelete, uploadKey)
				// Corretta la chiamata a IsLogLevel
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("UnregisterClient: Identificato upload %s per cancellazione per client %s", uploadKey, client.userIdentifier)
				}
			}
		}

		for _, key := range tempKeysToDelete {
			delete(h.OngoingFileUploads, key)
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("UnregisterClient: Rimosso upload %s da OngoingFileUploads per client %s", key, client.userIdentifier)
			}
		}
		h.FileUPloadsMutex.Unlock()
		// Corretta la chiamata a IsLogLevel
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("UnregisterClient: Rilasciato FileUPloadsMutex per client %s", client.userIdentifier)
		}

		if len(uploadsToCancelForProvider) > 0 {
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Avvio cleanup per %d uploads per client disconnesso '%s'", len(uploadsToCancelForProvider), client.userIdentifier)
			}
			go func(uploads []struct {
				UploadKey    string
				SessionState *UploadSessionState
			}, disconnectedClientIdentifier string) {
				for _, upload := range uploads {
					claimsForCleanup := upload.SessionState.Claims
					provider, ok := storage.GetProvider(upload.SessionState.StorageName)
					if !ok {
						log.Printf("Warning: Storage provider '%s' non trovato durante cleanup upload per client disconnesso '%s'", upload.SessionState.StorageName, disconnectedClientIdentifier)
						continue
					}

					var cancelErr error
					cleanupCtx, cleanupCancelFunc := context.WithTimeout(context.Background(), 30*time.Second)

					switch p := provider.(type) {
					case interface{ CancelUpload(context.Context, *auth.UserClaims, string) error }:
						cancelErr = p.CancelUpload(cleanupCtx, claimsForCleanup, upload.SessionState.ItemPath)
					case interface{ CancelUpload(*auth.UserClaims, string) error }:
						cancelErr = p.CancelUpload(claimsForCleanup, upload.SessionState.ItemPath)
					default:
						log.Printf("Warning: CancelUpload non implementato o firma non corrispondente per storage provider tipo '%s' durante cleanup per client disconnesso.", provider.Type())
						cleanupCancelFunc()
						continue
					}
					cleanupCancelFunc()

					if cancelErr != nil {
						log.Printf("Errore durante cleanup upload '%s' (storage: %s, path: %s) per client disconnesso '%s': %v", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath, disconnectedClientIdentifier, cancelErr)
					} else {
						// Corretta la chiamata a IsLogLevel
						if config.IsLogLevel(config.LogLevelInfo) {
							log.Printf("Upload '%s' (storage: %s, path: %s) ripulito con successo per client disconnesso '%s'", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath, disconnectedClientIdentifier)
						}
					}
				}
			}(uploadsToCancelForProvider, client.userIdentifier)
		}
	}
}

// cleanupLongPollingClients rimuove i client Long Polling inattivi.
func (h *Hub) cleanupLongPollingClients() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Println("Goroutine di cleanup client Long Polling: contesto cancellato, arresto.")
			}
			return
		case <-ticker.C:
			now := time.Now()
			for client := range h.clients {
				client.connMu.Lock()
				isWSClient := client.isWS
				lastActivityTime := client.lastActivity
				client.connMu.Unlock()

				if !isWSClient && now.Sub(lastActivityTime) > (2*time.Minute) {
					// Corretta la chiamata a IsLogLevel
					if config.IsLogLevel(config.LogLevelInfo) {
						log.Printf("Rimozione client Long Polling inattivo (User: %s)", client.userIdentifier)
					}
					h.unregister <- client
				}
			}
		}
	}
}

// cleanupOrphanedUploads controlla periodicamente e annulla gli upload di file abbandonati.
func (h *Hub) cleanupOrphanedUploads() {
	cleanupInterval := 1 * time.Minute
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	uploadCleanupTimeout, err := h.config.GetUploadCleanupTimeout()
	if err != nil {
		log.Printf("Errore nel leggere upload_cleanup_timeout dalla config, usando default 10 minuti: %v", err)
		uploadCleanupTimeout = 10 * time.Minute
	}

	// Corretta la chiamata a IsLogLevel
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Cleanup upload orfani avviato. Timeout: %s, Intervallo: %s", uploadCleanupTimeout, cleanupInterval)
	}

	for {
		select {
		case <-h.ctx.Done():
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Println("Goroutine di cleanup upload orfani: contesto cancellato, arresto.")
			}
			return
		case <-ticker.C:
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Println("Esecuzione controllo cleanup upload orfani...")
			}
			now := time.Now()
			var uploadsToCancelForProvider []struct {
				UploadKey    string
				SessionState *UploadSessionState
			}

			h.FileUPloadsMutex.Lock()
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Println("CleanupOrphaned: Acquisito FileUPloadsMutex")
			}
			tempKeysToDelete := []string{}
			for uploadKey, sessionState := range h.OngoingFileUploads {
				if now.Sub(sessionState.LastActivity) > uploadCleanupTimeout {
					userEmail := "anonimo"
					if sessionState.Claims != nil {
						userEmail = sessionState.Claims.Email
					}
					// Corretta la chiamata a IsLogLevel
					if config.IsLogLevel(config.LogLevelInfo) {
						log.Printf("Trovato upload orfano: %s (User: %s, Storage: %s, Path: %s, LastActivity: %s, Timeout: %s)",
							uploadKey, userEmail, sessionState.StorageName, sessionState.ItemPath, sessionState.LastActivity.Format(time.RFC3339), uploadCleanupTimeout.String())
					}
					uploadsToCancelForProvider = append(uploadsToCancelForProvider, struct {
						UploadKey    string
						SessionState *UploadSessionState
					}{uploadKey, sessionState})
					tempKeysToDelete = append(tempKeysToDelete, uploadKey)
				}
			}
			for _, key := range tempKeysToDelete {
				delete(h.OngoingFileUploads, key)
				// Corretta la chiamata a IsLogLevel
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("CleanupOrphaned: Rimosso upload orfano %s da OngoingFileUploads", key)
				}
			}
			h.FileUPloadsMutex.Unlock()
			// Corretta la chiamata a IsLogLevel
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Println("CleanupOrphaned: Rilasciato FileUPloadsMutex")
			}

			if len(uploadsToCancelForProvider) > 0 {
				// Corretta la chiamata a IsLogLevel
				if config.IsLogLevel(config.LogLevelInfo) {
					log.Printf("Avvio cleanup a livello provider per %d upload orfani.", len(uploadsToCancelForProvider))
				}
				go func(uploads []struct{ UploadKey string; SessionState *UploadSessionState }) {
					for _, upload := range uploads {
						claimsForCleanup := upload.SessionState.Claims
						provider, ok := storage.GetProvider(upload.SessionState.StorageName)
						if !ok {
							log.Printf("Warning: Storage provider '%s' non trovato durante cleanup upload orfano '%s'", upload.SessionState.StorageName, upload.SessionState.ItemPath)
							continue
						}

						var cancelErr error
						cleanupCtx, cleanupCancelFunc := context.WithTimeout(context.Background(), 30*time.Second)

						switch p := provider.(type) {
						case interface{ CancelUpload(context.Context, *auth.UserClaims, string) error }:
							cancelErr = p.CancelUpload(cleanupCtx, claimsForCleanup, upload.SessionState.ItemPath)
						case interface{ CancelUpload(*auth.UserClaims, string) error }:
							cancelErr = p.CancelUpload(claimsForCleanup, upload.SessionState.ItemPath)
						default:
							log.Printf("Warning: CancelUpload non implementato o firma non corrispondente per storage provider tipo '%s' durante cleanup upload orfano.", provider.Type())
							cleanupCancelFunc()
							continue
						}
						cleanupCancelFunc()

						if cancelErr != nil {
							log.Printf("Errore durante cleanup upload orfano '%s' (storage: %s, path: %s): %v", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath, cancelErr)
						} else {
							// Corretta la chiamata a IsLogLevel
							if config.IsLogLevel(config.LogLevelInfo) {
								log.Printf("Upload orfano '%s' (storage: %s, path: %s) ripulito con successo.", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath)
							}
						}
					}
				}(uploadsToCancelForProvider)
			}
		}
	}
}

// ServeWs gestisce le richieste di connessione WebSocket.
func (h *Hub) ServeWs(w http.ResponseWriter, r *http.Request, userClaims *auth.UserClaims) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Errore durante l'upgrade a WebSocket: %v", err)
		http.Error(w, "Impossibile stabilire connessione WebSocket", http.StatusInternalServerError)
		return
	}

	clientCtx, clientCancel := context.WithCancel(h.ctx)

	userIdentifier := "anon-ws-" + fmt.Sprintf("%d", time.Now().UnixNano())
	if userClaims != nil && userClaims.Email != "" {
		userIdentifier = userClaims.Email
	} else if userClaims != nil && userClaims.Subject != "" {
		userIdentifier = userClaims.Subject
	}

	client := &Client{
		hub:            h,
		conn:           conn,
		send:           make(chan Message, 256),
		isWS:           true,
		claims:         userClaims,
		ctx:            clientCtx,
		cancel:         clientCancel,
		userIdentifier: userIdentifier,
		lastActivity:   time.Now(),
	}
	h.register <- client

	go client.writePump()
	go client.readPump()
}

// ServeLongPolling gestisce le richieste di Long Polling.
func (h *Hub) ServeLongPolling(w http.ResponseWriter, r *http.Request, userClaims *auth.UserClaims) {
	userIdentifier := "anon-lp-" + fmt.Sprintf("%d", time.Now().UnixNano())
	if userClaims != nil && userClaims.Email != "" {
		userIdentifier = userClaims.Email
	} else if userClaims != nil && userClaims.Subject != "" {
		userIdentifier = userClaims.Subject
	}

	if r.Method == http.MethodPost {
		var msg Message
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&msg); err != nil {
			log.Printf("Errore parsing messaggio Long Polling (User: %s): %v", userIdentifier, err)
			http.Error(w, "Richiesta non valida", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// Corretta la chiamata a IsLogLevel
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("LP Incoming Message (User: %s, Server): Type=%s, ReqID=%s, Payload=%+v", userIdentifier, msg.Type, msg.RequestID, msg.Payload)
		}

		msgProcessingCtx, cancelMsgProcessingCtx := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancelMsgProcessingCtx()

		// Qui chiamiamo HandleWebSocketMessage, che dovrebbe essere definito in message_handlers.go
		// Creiamo un client "fittizio" per LP.
		lpClient := &Client{
			hub:            h,
			conn:           nil,
			send:           make(chan Message), // Non usato attivamente per LP response qui
			isWS:           false,
			claims:         userClaims,
			ctx:            msgProcessingCtx,
			cancel:         func() {}, // No-op
			userIdentifier: userIdentifier,
			lastActivity:   time.Now(),
		}

		response, procErr := HandleWebSocketMessage(msgProcessingCtx, h, lpClient, &msg)
		if procErr != nil {
			log.Printf("Errore elaborazione messaggio Long Polling (User: %s, Type: %s, ReqID: %s): %v", userIdentifier, msg.Type, msg.RequestID, procErr)
			response = Message{
				Type:      "error",
				Payload:   map[string]string{"error_type": "processing_error", "message": procErr.Error()},
				RequestID: msg.RequestID,
			}
		}

		// Corretta la chiamata a IsLogLevel
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("LP Outgoing Response (User: %s, Server): Type=%s, ReqID=%s, Payload=%+v", userIdentifier, response.Type, response.RequestID, response.Payload)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("Errore invio risposta Long Polling (User: %s): %v", userIdentifier, err)
		}

	} else if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		initialConfigMsg := Message{
			Type: "config_update",
			Payload: map[string]interface{}{
				"client_ping_interval_ms": h.config.ClientPingIntervalMs,
			},
		}
		// Corretta la chiamata a IsLogLevel
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("LP GET request (User: %s), sending initial config.", userIdentifier)
		}
		json.NewEncoder(w).Encode(initialConfigMsg)

	} else {
		http.Error(w, "Metodo non consentito per Long Polling", http.StatusMethodNotAllowed)
	}
}

// Rimuovi la definizione duplicata di handleClientMessage da qui se HandleWebSocketMessage
// in message_handlers.go è la funzione principale per la gestione dei messaggi.
// func (h *Hub) handleClientMessage(opCtx context.Context, msg *Message, claims *auth.UserClaims) (Message, error) { ... }

