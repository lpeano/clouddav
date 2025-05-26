package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil" // ioutil è deprecato da Go 1.16, considera "io" e "os"
	"log"
	"net/http"
	"path/filepath"
	"strings" // Aggiunto per strings.Contains in readPump error handling
	"sync"
	"time"

	"clouddav/auth"
	"clouddav/config"
	"clouddav/internal/authz"
	"clouddav/storage"
	"clouddav/storage/azureblob"
	"clouddav/storage/local"

	"github.com/gorilla/websocket"
)

// Upgrader for WebSocket
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Permetti tutte le origini per semplicità, da rivedere in produzione
	},
}

// Client represents a single WebSocket/Long Polling client.
type Client struct {
	conn           *websocket.Conn
	send           chan Message
	mu             sync.Mutex        // Protegge conn durante la scrittura
	isWS           bool              // True se è una connessione WebSocket
	lastActivity   time.Time         // Ultima attività per client Long Polling
	claims         *auth.UserClaims  // Claims dell'utente autenticato
	ctx            context.Context   // Contesto del client, derivato dal Hub
	cancel         context.CancelFunc// Funzione per cancellare il contesto del client
	userIdentifier string            // Identificatore univoco per il client (email o ID generato)
	hub            *Hub              // <<< MODIFICA: Riferimento all'Hub
}

// UploadSessionState tracks the state of an ongoing file upload.
type UploadSessionState struct {
	Claims       *auth.UserClaims
	StorageName  string
	ItemPath     string
	LastActivity time.Time
	ProviderType string
}

// Message represents a message sent or received via WebSocket/Long Polling.
type Message struct {
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload"`
	RequestID string      `json:"request_id,omitempty"`
}

// Hub manages WebSocket and Long Polling clients.
type Hub struct {
	clients            map[*Client]bool
	register           chan *Client
	unregister         chan *Client
	broadcast          chan Message
	config             *config.Config
	ctx                context.Context
	cancel             context.CancelFunc
	OngoingFileUploads map[string]*UploadSessionState
	FileUploadsMutex   sync.Mutex
}

// NewHub creates a new Hub.
func NewHub(ctx context.Context, cfg *config.Config) *Hub {
	hubCtx, hubCancel := context.WithCancel(ctx)
	return &Hub{
		clients:            make(map[*Client]bool),
		register:           make(chan *Client),
		unregister:         make(chan *Client),
		broadcast:          make(chan Message),
		config:             cfg,
		ctx:                hubCtx,
		cancel:             hubCancel,
		OngoingFileUploads: make(map[string]*UploadSessionState),
		FileUploadsMutex:   sync.Mutex{},
	}
}

// Run starts the Hub, managing client registration/deregistration.
func (h *Hub) Run() {
	go h.cleanupLongPollingClients()
	go h.cleanupOrphanedUploads()

	if config.IsLogLevel(config.LogLevelInfo) {
		log.Println("Hub running...")
	}

	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Client registered (User: %s, WS: %t). Total clients: %d", client.userIdentifier, client.isWS, len(h.clients))
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
					if config.IsLogLevel(config.LogLevelDebug) {
						log.Printf("Sent initial config to client (User: %s, WS: %t)", c.userIdentifier, c.isWS)
					}
				case <-time.After(5 * time.Second):
					log.Printf("Timeout sending initial config to client (User: %s, WS: %t)", c.userIdentifier, c.isWS)
				case <-c.ctx.Done():
					if config.IsLogLevel(config.LogLevelDebug) {
						log.Printf("Client context cancelled while sending initial config (User: %s, WS: %t)", c.userIdentifier, c.isWS)
					}
				}
			}(client, initialConfigMsg)

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
				if client.conn != nil {
					client.conn.Close()
				}
				client.cancel()

				if config.IsLogLevel(config.LogLevelInfo) {
					log.Printf("Client unregistered (User: %s, WS: %t). Total clients: %d", client.userIdentifier, client.isWS, len(h.clients))
				}

				uploadsToCancelForProvider := []struct{ UploadKey string; SessionState *UploadSessionState }{}
				h.FileUploadsMutex.Lock()
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Unregister: Locked FileUploadsMutex for client %s", client.userIdentifier)
				}
				tempKeysToDelete := []string{}
				for uploadKey, sessionState := range h.OngoingFileUploads {
					clientMatch := false
					if client.claims != nil && sessionState.Claims != nil && client.claims.Email == sessionState.Claims.Email {
						clientMatch = true
					} else if client.claims == nil && sessionState.Claims != nil && client.userIdentifier != "" && client.userIdentifier == sessionState.Claims.Subject {
						// Gestione per client anonimi se userIdentifier è stato usato in Claims.Subject
						clientMatch = true
					}
					// Aggiungere altre logiche di match se necessario per client anonimi

					if clientMatch {
						uploadsToCancelForProvider = append(uploadsToCancelForProvider, struct{ UploadKey string; SessionState *UploadSessionState }{uploadKey, sessionState})
						tempKeysToDelete = append(tempKeysToDelete, uploadKey)
						if config.IsLogLevel(config.LogLevelDebug) {
							log.Printf("Unregister: Identified upload %s for cancellation for client %s", uploadKey, client.userIdentifier)
						}
					}
				}

				for _, key := range tempKeysToDelete {
					delete(h.OngoingFileUploads, key)
					if config.IsLogLevel(config.LogLevelDebug) {
						log.Printf("Unregister: Removed upload %s from OngoingFileUploads for client %s", key, client.userIdentifier)
					}
				}
				h.FileUploadsMutex.Unlock()
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Unregister: Unlocked FileUploadsMutex for client %s", client.userIdentifier)
				}

				if len(uploadsToCancelForProvider) > 0 {
					if config.IsLogLevel(config.LogLevelInfo) {
						log.Printf("Initiating cleanup for %d uploads from disconnected client '%s'", len(uploadsToCancelForProvider), client.userIdentifier)
					}
					go func(uploads []struct{ UploadKey string; SessionState *UploadSessionState }, disconnectedClientIdentifier string) {
						for _, upload := range uploads {
							claimsForCleanup := upload.SessionState.Claims
							provider, ok := storage.GetProvider(upload.SessionState.StorageName)
							if !ok {
								log.Printf("Warning: Storage provider '%s' not found during disconnected client cleanup for '%s'", upload.SessionState.StorageName, upload.SessionState.ItemPath)
								continue
							}
							var cancelErr error
							cleanupCtx, cleanupCancelFunc := context.WithTimeout(context.Background(), 30*time.Second)
							func() {
								defer cleanupCancelFunc()
								switch p := provider.(type) {
								case *local.LocalFilesystemProvider:
									cancelErr = p.CancelUpload(claimsForCleanup, upload.SessionState.ItemPath)
								case *azureblob.AzureBlobStorageProvider:
									cancelErr = p.CancelUpload(cleanupCtx, claimsForCleanup, upload.SessionState.ItemPath)
								default:
									log.Printf("Warning: CancelUpload not implemented for storage type '%s' during disconnected client cleanup.", provider.Type())
									return
								}
								if cancelErr != nil {
									log.Printf("Error during cleanup of upload '%s' (storage: %s, path: %s) for disconnected client '%s': %v", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath, disconnectedClientIdentifier, cancelErr)
								} else {
									if config.IsLogLevel(config.LogLevelInfo) {
										log.Printf("Successfully cleaned up upload '%s' (storage: %s, path: %s) for disconnected client '%s'", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath, disconnectedClientIdentifier)
									}
								}
							}()
						}
					}(uploadsToCancelForProvider, client.userIdentifier)
				}
			}
		case message := <-h.broadcast:
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
					client.cancel()
				}
			}
		case <-h.ctx.Done():
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Println("Hub context cancelled, shutting down...")
			}
			for client := range h.clients {
				go func(c *Client) {
					select {
					case h.unregister <- c:
					case <-time.After(1 * time.Second):
						log.Printf("Timeout unregistering client %s during hub shutdown", c.userIdentifier)
						if _, ok := h.clients[c]; ok { // Ricontrolla perché potrebbe essere stato deregistrato nel frattempo
							delete(h.clients, c)
							close(c.send)
							if c.conn != nil {
								c.conn.Close()
							}
							c.cancel()
						}
					}
				}(client)
			}
			log.Println("Hub shutdown complete.")
			return
		}
	}
}

// cleanupLongPollingClients removes inactive Long Polling clients.
func (h *Hub) cleanupLongPollingClients() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			for client := range h.clients {
				client.mu.Lock()
				isWSClient := client.isWS
				lastActivityTime := client.lastActivity
				client.mu.Unlock()

				if !isWSClient && now.Sub(lastActivityTime) > 60*time.Second {
					if config.IsLogLevel(config.LogLevelInfo) {
						log.Printf("Removing inactive Long Polling client (User: %s)", client.userIdentifier)
					}
					h.unregister <- client
				}
			}
		case <-h.ctx.Done():
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Println("Long Polling client cleanup goroutine context cancelled, stopping.")
			}
			return
		}
	}
}

// cleanupOrphanedUploads periodically checks for and cancels orphaned uploads.
func (h *Hub) cleanupOrphanedUploads() {
	cleanupInterval := 1 * time.Minute
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	uploadCleanupTimeout, err := h.config.GetUploadCleanupTimeout()
	if err != nil {
		log.Printf("Error getting upload cleanup timeout from config, using default 10 minutes: %v", err)
		uploadCleanupTimeout = 10 * time.Minute
	}
	if config.IsLogLevel(config.LogLevelInfo) {
		log.Printf("Orphaned uploads cleanup started. Timeout: %s, Interval: %s", uploadCleanupTimeout, cleanupInterval)
	}

	for {
		select {
		case <-ticker.C:
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Println("Running orphaned uploads cleanup check...")
			}
			now := time.Now()
			uploadsToCancelForProvider := []struct{ UploadKey string; SessionState *UploadSessionState }{}

			h.FileUploadsMutex.Lock()
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Println("CleanupOrphaned: Locked FileUploadsMutex")
			}
			tempKeysToDelete := []string{}
			for uploadKey, sessionState := range h.OngoingFileUploads {
				if now.Sub(sessionState.LastActivity) > uploadCleanupTimeout {
					userEmail := "anonymous"
					if sessionState.Claims != nil {
						userEmail = sessionState.Claims.Email
					}
					if config.IsLogLevel(config.LogLevelInfo) {
						log.Printf("Detected orphaned upload: %s (User: %s, Storage: %s, Path: %s, LastActivity: %s, Timeout: %s)",
							uploadKey, userEmail, sessionState.StorageName, sessionState.ItemPath, sessionState.LastActivity.Format(time.RFC3339), uploadCleanupTimeout.String())
					}
					uploadsToCancelForProvider = append(uploadsToCancelForProvider, struct{ UploadKey string; SessionState *UploadSessionState }{uploadKey, sessionState})
					tempKeysToDelete = append(tempKeysToDelete, uploadKey)
				}
			}

			for _, key := range tempKeysToDelete {
				delete(h.OngoingFileUploads, key)
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("CleanupOrphaned: Removed orphaned upload %s from OngoingFileUploads map", key)
				}
			}
			h.FileUploadsMutex.Unlock()
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Println("CleanupOrphaned: Unlocked FileUploadsMutex")
			}

			if len(uploadsToCancelForProvider) > 0 {
				if config.IsLogLevel(config.LogLevelInfo) {
					log.Printf("Initiating provider-level cleanup for %d orphaned uploads.", len(uploadsToCancelForProvider))
				}
				go func(uploads []struct{ UploadKey string; SessionState *UploadSessionState }) {
					for _, upload := range uploads {
						claimsForCleanup := upload.SessionState.Claims
						provider, ok := storage.GetProvider(upload.SessionState.StorageName)
						if !ok {
							log.Printf("Warning: Storage provider '%s' not found during orphaned upload cleanup for '%s'", upload.SessionState.StorageName, upload.SessionState.ItemPath)
							continue
						}
						var cancelErr error
						cleanupCtx, cleanupCancelFunc := context.WithTimeout(context.Background(), 30*time.Second)
						func() {
							defer cleanupCancelFunc()
							switch p := provider.(type) {
							case *local.LocalFilesystemProvider:
								cancelErr = p.CancelUpload(claimsForCleanup, upload.SessionState.ItemPath)
							case *azureblob.AzureBlobStorageProvider:
								cancelErr = p.CancelUpload(cleanupCtx, claimsForCleanup, upload.SessionState.ItemPath)
							default:
								log.Printf("Warning: CancelUpload not implemented for storage type '%s' during orphaned upload cleanup.", provider.Type())
								return
							}
							if cancelErr != nil {
								log.Printf("Error during cleanup of orphaned upload '%s' (storage: %s, path: %s): %v", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath, cancelErr)
							} else {
								if config.IsLogLevel(config.LogLevelInfo) {
									log.Printf("Successfully cleaned up orphaned upload '%s' (storage: %s, path: %s)", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath)
								}
							}
						}()
					}
				}(uploadsToCancelForProvider)
			} else {
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Println("No orphaned uploads found in this check.")
				}
			}
		case <-h.ctx.Done():
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Println("Orphaned uploads cleanup goroutine context cancelled, stopping.")
			}
			return
		}
	}
}

// ServeWs handles WebSocket connection requests after user authentication checks.
func (h *Hub) ServeWs(w http.ResponseWriter, r *http.Request, claims *auth.UserClaims) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error upgrading to WebSocket: %v", err)
		http.Error(w, "Unable to establish WebSocket connection", http.StatusInternalServerError)
		return
	}

	clientCtx, clientCancel := context.WithCancel(h.ctx)

	userIdent := "anon-ws-" + fmt.Sprintf("%d", time.Now().UnixNano())
	if claims != nil && claims.Email != "" {
		userIdent = claims.Email
	}

	client := &Client{
		conn:           conn,
		send:           make(chan Message, 256),
		isWS:           true,
		claims:         claims,
		ctx:            clientCtx,
		cancel:         clientCancel,
		userIdentifier: userIdent,
		lastActivity:   time.Now(),
		hub:            h, // <<< MODIFICA: Passa il riferimento all'Hub
	}
	h.register <- client

	go client.writePump()
	go client.readPump() // <<< MODIFICA: readPump ora usa c.hub internamente
}

// ServeLongPolling handles Long Polling requests after user authentication checks.
func (h *Hub) ServeLongPolling(w http.ResponseWriter, r *http.Request, claims *auth.UserClaims) {
	userIdent := "anon-lp-" + fmt.Sprintf("%d", time.Now().UnixNano())
	if claims != nil && claims.Email != "" {
		userIdent = claims.Email
	}

	if r.Method == http.MethodPost {
		var msg Message
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&msg); err != nil {
			log.Printf("Error parsing Long Polling message: %v", err)
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("LP Incoming Message (User: %s, Server): Type=%s, RequestID=%s, Payload=%+v", userIdent, msg.Type, msg.RequestID, msg.Payload)
		}
		reqCtx := r.Context()
		response, processErr := h.handleClientMessage(reqCtx, &msg, claims)
		if processErr != nil {
			log.Printf("Error processing Long Polling message (User: %s): %v", userIdent, processErr)
			response = Message{
				Type:      "error",
				Payload:   map[string]string{"error": processErr.Error()},
				RequestID: msg.RequestID,
			}
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("LP Outgoing Response (User: %s, Server): Type=%s, RequestID=%s, Payload=%+v", userIdent, response.Type, response.RequestID, response.Payload)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("Error sending Long Polling response (User: %s): %v", userIdent, err)
		}
	} else if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		initialConfigMsg := Message{
			Type: "config_update",
			Payload: map[string]interface{}{
				"client_ping_interval_ms": h.config.ClientPingIntervalMs,
			},
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("LP GET request (User: %s), sending initial config.", userIdent)
		}
		json.NewEncoder(w).Encode(initialConfigMsg)
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// readPump reads messages from the WebSocket client and processes them.
func (c *Client) readPump() { // <<< MODIFICA: Rimosso h *Hub come parametro
	defer func() {
		c.hub.unregister <- c // <<< MODIFICA: Usa c.hub
	}()

	pongWait := time.Duration(c.hub.config.ClientPingIntervalMs*3) * time.Millisecond // <<< MODIFICA: Usa c.hub.config
	if pongWait <= 0 {
		pongWait = 60 * time.Second
	}

	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		c.mu.Lock()
		c.lastActivity = time.Now()
		c.mu.Unlock()
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Pong received from client (User: %s)", c.userIdentifier)
		}
		return nil
	})

	for {
		select {
		case <-c.ctx.Done():
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Client context cancelled in readPump (User: %s): %v", c.userIdentifier, c.ctx.Err())
			}
			return
		default:
		}

		var msg Message
		err := c.conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNormalClosure) {
				log.Printf("WebSocket read error (User: %s): %v", c.userIdentifier, err)
			} else if err == io.EOF || errors.Is(err, websocket.ErrCloseSent) || strings.Contains(err.Error(), "use of closed network connection") {
				if config.IsLogLevel(config.LogLevelInfo) {
					log.Printf("WebSocket connection closed normally by client (User: %s): %v", c.userIdentifier, err)
				}
			} else {
				log.Printf("Unexpected WebSocket read error (User: %s): %v", c.userIdentifier, err)
			}
			return
		}

		c.mu.Lock()
		c.lastActivity = time.Now()
		c.mu.Unlock()

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("WS Incoming Message (User: %s): Type=%s, RequestID=%s, Payload=%+v", c.userIdentifier, msg.Type, msg.RequestID, msg.Payload)
		}

		msgCtx, cancelMsgCtx := context.WithTimeout(c.ctx, 60*time.Second)

		go func(ctx context.Context, message Message) {
			defer cancelMsgCtx()
			response, processErr := c.hub.handleClientMessage(ctx, &message, c.claims) // <<< MODIFICA: Usa c.hub
			if processErr != nil {
				log.Printf("Error processing message (User: %s, Type: %s, ReqID: %s): %v", c.userIdentifier, message.Type, message.RequestID, processErr)
				response = Message{
					Type:      "error",
					Payload:   map[string]string{"error": processErr.Error()},
					RequestID: message.RequestID,
				}
			}
			select {
			case c.send <- response:
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("WS Outgoing Response (User: %s): Type=%s, RequestID=%s, Payload=%+v", c.userIdentifier, response.Type, response.RequestID, response.Payload)
				}
			case <-c.ctx.Done():
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Client context cancelled while queueing response (User: %s, Type: %s, ReqID: %s)", c.userIdentifier, response.Type, response.RequestID)
				}
			case <-ctx.Done():
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Message processing context done before sending response (User: %s, Type: %s, ReqID: %s): %v", c.userIdentifier, response.Type, response.RequestID, ctx.Err())
				}
			}
		}(msgCtx, msg)
	}
}

// writePump sends messages to the WebSocket client.
func (c *Client) writePump() {
	// Intervallo di ping inviato dal server al client WebSocket
	pingPeriod := time.Duration(c.hub.config.ClientPingIntervalMs) * time.Millisecond // <<< MODIFICA: Usa c.hub.config
	if pingPeriod <= 0 {
		pingPeriod = 30 * time.Second // Fallback
	}
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				if config.IsLogLevel(config.LogLevelInfo) {
					log.Printf("Send channel closed for client (User: %s), closing WebSocket.", c.userIdentifier)
				}
				return
			}

			c.mu.Lock()
			err := c.conn.WriteJSON(message)
			c.mu.Unlock()
			if err != nil {
				log.Printf("Error writing to WebSocket (User: %s): %v", c.userIdentifier, err)
				return
			}
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("WS Outgoing Message (User: %s): Type=%s, RequestID=%s, Payload=%+v", c.userIdentifier, message.Type, message.RequestID, message.Payload)
			}

		case <-c.ctx.Done():
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Client context cancelled in writePump (User: %s): %v", c.userIdentifier, c.ctx.Err())
			}
			c.conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
			c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"))
			return

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			c.mu.Lock()
			err := c.conn.WriteMessage(websocket.PingMessage, nil)
			c.mu.Unlock()
			if err != nil {
				log.Printf("Error sending Ping to WebSocket (User: %s): %v", c.userIdentifier, err)
				return
			}
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Sent Ping to client (User: %s)", c.userIdentifier)
			}
		}
	}
}

// handleClientMessage processes messages received from clients (WS or LP).
// (La funzione handleClientMessage rimane sostanzialmente invariata rispetto alla versione precedente,
// dato che il problema principale era nell'acquisizione del lock in handleUpload e nell'accesso
// alla configurazione da writePump. Si assume che la logica interna di handleClientMessage sia corretta
// per quanto riguarda l'elaborazione dei tipi di messaggio.)
func (h *Hub) handleClientMessage(ctx context.Context, msg *Message, claims *auth.UserClaims) (Message, error) {
	var response Message
	response.Type = msg.Type + "_response"
	response.RequestID = msg.RequestID

	select {
	case <-ctx.Done():
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Context cancelled before processing message (Type: %s, ReqID: %s): %v", msg.Type, msg.RequestID, ctx.Err())
		}
		response.Type = "error"
		response.Payload = map[string]string{"error": "request cancelled or timed out"}
		return response, ctx.Err()
	default:
	}

	var userIdentifier string
	if claims != nil && claims.Email != "" {
		userIdentifier = claims.Email
	} else {
		userIdentifier = "anonymous"
	}

	if config.IsLogLevel(config.LogLevelDebug) {
		log.Printf("Processing message (User: %s, Type: %s, ReqID: %s)", userIdentifier, msg.Type, msg.RequestID)
	}

	switch msg.Type {
	case "get_filesystems":
		accessibleStorages := authz.GetAccessibleStorages(ctx, claims, h.config)
		response.Payload = accessibleStorages
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("get_filesystems_response (User: %s, ReqID: %s): Found %d accessible storages", userIdentifier, msg.RequestID, len(accessibleStorages))
		}

	case "list_directory":
		var payload struct {
			StorageName     string `json:"storage_name"`
			DirPath         string `json:"dir_path"`
			Page            int    `json:"page"`
			ItemsPerPage    int    `json:"items_per_page"`
			NameFilter      string `json:"name_filter"`
			TimestampFilter string `json:"timestamp_filter"`
		}
		payloadBytes, err := json.Marshal(msg.Payload)
		if err != nil {
			return response, fmt.Errorf("failed to marshal payload for list_directory: %w", err)
		}
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			return response, fmt.Errorf("invalid list_directory payload: %w", err)
		}

		if err := authz.CheckStorageAccess(ctx, claims, payload.StorageName, payload.DirPath, "read", h.config); err != nil {
			if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: read permission required"}
				return response, nil
			}
			return response, fmt.Errorf("error checking storage access for list_directory: %w", err)
		}

		provider, ok := storage.GetProvider(payload.StorageName)
		if !ok {
			return response, fmt.Errorf("storage provider '%s' not found", payload.StorageName)
		}

		itemsPerPage := h.config.Pagination.ItemsPerPage
		if payload.ItemsPerPage > 0 {
			itemsPerPage = payload.ItemsPerPage
		}
		page := payload.Page
		if page <= 0 {
			page = 1
		}

		var tFilter *time.Time
		if payload.TimestampFilter != "" {
			parsedTime, parseErr := time.Parse(time.RFC3339, payload.TimestampFilter)
			if parseErr != nil {
				log.Printf("Warning: Invalid timestamp filter format for list_directory (User: %s, ReqID: %s): %v", userIdentifier, msg.RequestID, parseErr)
			} else {
				tFilter = &parsedTime
			}
		}

		listResponse, err := provider.ListItems(ctx, claims, payload.DirPath, page, itemsPerPage, payload.NameFilter, tFilter)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Directory not found"}
				return response, nil
			}
			return response, fmt.Errorf("error listing items from storage '%s' (User: %s, ReqID: %s): %w", payload.StorageName, userIdentifier, msg.RequestID, err)
		}
		response.Payload = struct {
			*storage.ListItemsResponse
			StorageName string `json:"storage_name"`
			DirPath     string `json:"dir_path"`
		}{
			ListItemsResponse: listResponse,
			StorageName:       payload.StorageName, // payload è la variabile che contiene i dati della richiesta
			DirPath:           payload.DirPath,     // payload è la variabile che contiene i dati della richiesta
		}
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("list_directory_response (User: %s, ReqID: %s): Listed %d items for %s/%s", userIdentifier, msg.RequestID, len(listResponse.Items), payload.StorageName, payload.DirPath)
		}

	case "read_file":
		var payload struct {
			StorageName string `json:"storage_name"`
			ItemPath    string `json:"item_path"`
		}
		payloadBytes, err := json.Marshal(msg.Payload)
		if err != nil {
			return response, fmt.Errorf("failed to marshal payload for read_file: %w", err)
		}
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			return response, fmt.Errorf("invalid read_file payload: %w", err)
		}

		if err := authz.CheckStorageAccess(ctx, claims, payload.StorageName, payload.ItemPath, "read", h.config); err != nil {
			if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: read permission required"}
				return response, nil
			}
			return response, fmt.Errorf("error checking storage access for read_file: %w", err)
		}

		provider, ok := storage.GetProvider(payload.StorageName)
		if !ok {
			return response, fmt.Errorf("storage provider '%s' not found", payload.StorageName)
		}

		reader, err := provider.OpenReader(ctx, claims, payload.ItemPath)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Item not found"}
			} else if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: read permission required"}
			} else {
				return response, fmt.Errorf("error opening item '%s/%s' (User: %s, ReqID: %s): %w", payload.StorageName, payload.ItemPath, userIdentifier, msg.RequestID, err)
			}
			return response, nil
		}
		defer reader.Close()

		content, err := ioutil.ReadAll(reader)
		if err != nil {
			select {
			case <-ctx.Done():
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Context cancelled during reading item '%s/%s' (User: %s, ReqID: %s): %v", payload.StorageName, payload.ItemPath, userIdentifier, msg.RequestID, ctx.Err())
				}
				return response, ctx.Err()
			default:
			}
			return response, fmt.Errorf("error reading item content '%s/%s' (User: %s, ReqID: %s): %w", payload.StorageName, payload.ItemPath, userIdentifier, msg.RequestID, err)
		}
		response.Payload = string(content)
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("read_file_response (User: %s, ReqID: %s): Read %d bytes from %s/%s", userIdentifier, msg.RequestID, len(content), payload.StorageName, payload.ItemPath)
		}

	case "create_directory":
		var payload struct {
			StorageName string `json:"storage_name"`
			DirPath     string `json:"dir_path"`
		}
		payloadBytes, err := json.Marshal(msg.Payload)
		if err != nil {
			return response, fmt.Errorf("failed to marshal payload for create_directory: %w", err)
		}
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			return response, fmt.Errorf("invalid create_directory payload: %w", err)
		}

		if err := authz.CheckStorageAccess(ctx, claims, payload.StorageName, payload.DirPath, "write", h.config); err != nil {
			if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: write permission required"}
				return response, nil
			}
			return response, fmt.Errorf("error checking storage access for create_directory: %w", err)
		}

		provider, ok := storage.GetProvider(payload.StorageName)
		if !ok {
			return response, fmt.Errorf("storage provider '%s' not found", payload.StorageName)
		}

		err = provider.CreateDirectory(ctx, claims, payload.DirPath)
		if err != nil {
			if errors.Is(err, storage.ErrAlreadyExists) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Directory already exists"}
			} else if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: write permission required"}
			} else if errors.Is(err, storage.ErrNotImplemented) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Create directory not supported for this storage type"}
			} else {
				return response, fmt.Errorf("error creating directory '%s/%s' (User: %s, ReqID: %s): %w", payload.StorageName, payload.DirPath, userIdentifier, msg.RequestID, err)
			}
			return response, nil
		}
		response.Payload = map[string]string{"status": "success", "dir_path": payload.DirPath, "name": filepath.Base(payload.DirPath)}
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("create_directory_response (User: %s, ReqID: %s): Successfully created directory %s/%s", userIdentifier, msg.RequestID, payload.StorageName, payload.DirPath)
		}

	case "delete_item":
		var payload struct {
			StorageName string `json:"storage_name"`
			ItemPath    string `json:"item_path"`
		}
		payloadBytes, err := json.Marshal(msg.Payload)
		if err != nil {
			return response, fmt.Errorf("failed to marshal payload for delete_item: %w", err)
		}
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			return response, fmt.Errorf("invalid delete_item payload: %w", err)
		}

		if err := authz.CheckStorageAccess(ctx, claims, payload.StorageName, payload.ItemPath, "write", h.config); err != nil {
			if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: write permission required"}
				return response, nil
			}
			return response, fmt.Errorf("error checking storage access for delete_item: %w", err)
		}

		provider, ok := storage.GetProvider(payload.StorageName)
		if !ok {
			return response, fmt.Errorf("storage provider '%s' not found", payload.StorageName)
		}
		itemName := filepath.Base(payload.ItemPath)
		err = provider.DeleteItem(ctx, claims, payload.ItemPath)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Item not found"}
			} else if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: write permission required"}
			} else if errors.Is(err, storage.ErrNotImplemented) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Delete not supported for this storage type"}
			} else {
				return response, fmt.Errorf("error deleting item '%s/%s' (User: %s, ReqID: %s): %w", payload.StorageName, payload.ItemPath, userIdentifier, msg.RequestID, err)
			}
			return response, nil
		}
		response.Payload = map[string]string{"status": "success", "item_path": payload.ItemPath, "name": itemName}
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("delete_item_response (User: %s, ReqID: %s): Successfully deleted item %s/%s", userIdentifier, msg.RequestID, payload.StorageName, payload.ItemPath)
		}

	case "check_directory_contents_request":
		var payload struct {
			StorageName string `json:"storage_name"`
			DirPath     string `json:"dir_path"`
		}
		payloadBytes, err := json.Marshal(msg.Payload)
		if err != nil {
			return response, fmt.Errorf("failed to marshal payload for check_directory_contents_request: %w", err)
		}
		if err := json.Unmarshal(payloadBytes, &payload); err != nil {
			return response, fmt.Errorf("invalid check_directory_contents_request payload: %w", err)
		}

		if err := authz.CheckStorageAccess(ctx, claims, payload.StorageName, payload.DirPath, "read", h.config); err != nil {
			if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: read permission required to check directory contents"}
				return response, nil
			}
			return response, fmt.Errorf("error checking storage access for check_directory_contents_request: %w", err)
		}

		provider, ok := storage.GetProvider(payload.StorageName)
		if !ok {
			return response, fmt.Errorf("storage provider '%s' not found", payload.StorageName)
		}

		listResponse, err := provider.ListItems(ctx, claims, payload.DirPath, 1, 1, "", nil)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				response.Payload = map[string]bool{"has_contents": false}
				return response, nil
			}
			return response, fmt.Errorf("error listing items to check directory contents: %w", err)
		}
		response.Payload = map[string]bool{"has_contents": listResponse.TotalItems > 0}
		return response, nil

	case "ping":
		response.Type = "pong"
		response.Payload = msg.Payload
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("Ping received (User: %s, ReqID: %s), sending Pong.", userIdentifier, msg.RequestID)
		}
		return response, nil

	case "config_update":
		log.Printf("Received unexpected config_update message from client (User: %s, ReqID: %s): %+v", userIdentifier, msg.RequestID, msg)
		response.Type = "error"
		response.Payload = map[string]string{"error": "unexpected message type: config_update from client"}
		return response, errors.New("unexpected message type: config_update from client")

	default:
		response.Type = "error"
		response.Payload = map[string]string{"error": fmt.Sprintf("unsupported message type: %s", msg.Type)}
		log.Printf("Unsupported message type received (User: %s, Type: %s, ReqID: %s)", userIdentifier, msg.Type, msg.RequestID)
		return response, fmt.Errorf("unsupported message type: %s", msg.Type)
	}

	select {
	case <-ctx.Done():
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Context cancelled after processing message (User: %s, Type: %s, ReqID: %s): %v", userIdentifier, msg.Type, msg.RequestID, ctx.Err())
		}
		response.Type = "error"
		response.Payload = map[string]string{"error": "request cancelled or timed out during processing"}
		return response, ctx.Err()
	default:
	}

	return response, nil
}
