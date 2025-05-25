// websocket/websocket.go

package websocket

import (
	"context"
	"encoding/json"
	"errors"

	// "io/ioutil" // Rimosso perché non usato direttamente in questa versione
	// Assicurati che "io" sia importato se usi io.ReadAll altrove
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
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
		// Allow all origins for now. In production, you might want to restrict this.
		return true
	},
}

// Client represents a single WebSocket/Long Polling client.
type Client struct {
	conn           *websocket.Conn
	send           chan Message
	mu             sync.Mutex
	isWS           bool
	lastActivity   time.Time
	claims         *auth.UserClaims
	ctx            context.Context
	cancel         context.CancelFunc
	userIdentifier string // Added to uniquely identify the user/client for cleanup
}

// UploadSessionState tracks the state of an ongoing file upload.
type UploadSessionState struct {
	Claims       *auth.UserClaims // Claims of the user who initiated the upload
	StorageName  string           // Name of the storage provider
	ItemPath     string           // Path of the item being uploaded
	LastActivity time.Time        // Last time a chunk was received for this upload
	ProviderType string           // Type of the storage provider (e.g., "local", "azure-blob")
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
	broadcast          chan Message // Non usato attivamente nel codice fornito, ma mantenuto per struttura
	config             *config.Config
	ctx                context.Context
	cancel             context.CancelFunc
	OngoingFileUploads map[string]*UploadSessionState
	FileUploadsMutex   sync.Mutex
}

// NewHub creates a new Hub.
func NewHub(ctx context.Context, cfg *config.Config) *Hub {
	hubCtx, hubCancel := context.WithCancel(ctx) // Crea un contesto figlio per l'hub
	return &Hub{
		clients:            make(map[*Client]bool),
		register:           make(chan *Client),
		unregister:         make(chan *Client),
		broadcast:          make(chan Message),
		config:             cfg,
		ctx:                hubCtx, // Usa il contesto figlio
		cancel:             hubCancel,
		OngoingFileUploads: make(map[string]*UploadSessionState),
		FileUploadsMutex:   sync.Mutex{},
	}
}

// Run starts the Hub, managing client registration/deregistration.
func (h *Hub) Run() {
	go h.cleanupLongPollingClients()
	go h.cleanupOrphanedUploads()

	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Printf("Client registered. Total clients: %d. User: %s (WS: %t)", len(h.clients), client.userIdentifier, client.isWS)
			}
			initialConfigMsg := Message{
				Type: "config_update",
				Payload: map[string]interface{}{
					"client_ping_interval_ms": h.config.ClientPingIntervalMs,
				},
			}
			// Send initial config in a non-blocking way
			go func() {
				select {
				case client.send <- initialConfigMsg:
					if config.IsLogLevel(config.LogLevelDebug) {
						log.Printf("Sent initial config to client %s (WS: %t)", client.userIdentifier, client.isWS)
					}
				case <-time.After(5 * time.Second): // Timeout for sending
					log.Printf("Timeout sending initial config to client %s (WS: %t)", client.userIdentifier, client.isWS)
				case <-client.ctx.Done(): // Client context might be cancelled if unregistered quickly
					if config.IsLogLevel(config.LogLevelDebug) {
						log.Printf("Client context for %s cancelled while sending initial config (WS: %t)", client.userIdentifier, client.isWS)
					}
				}
			}()

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.cancel() // Cancel client-specific context
				close(client.send)
				if client.conn != nil {
					client.conn.Close()
				}
				if config.IsLogLevel(config.LogLevelInfo) {
					log.Printf("Client unregistered: %s (WS: %t). Total clients: %d", client.userIdentifier, client.isWS, len(h.clients))
				}
				// Cleanup ongoing uploads for this client
				h.cleanupClientUploads(client)
			}

		case <-h.ctx.Done(): // Hub's main context is cancelled
			log.Println("Hub context cancelled, shutting down all client connections.")
			for client := range h.clients {
				// Unregister will also cancel client's context and close connection
				h.unregister <- client // This might block if unregister channel is full, consider non-blocking send or select
			}
			return // Exit Run loop
		}
	}
}

func (h *Hub) cleanupClientUploads(client *Client) {
    uploadsToCancel := []struct{ UploadKey string; SessionState *UploadSessionState }{}
    h.FileUploadsMutex.Lock()
    for uploadKey, sessionState := range h.OngoingFileUploads {
        clientEmail := "anonymous_or_unidentified"
        if client.claims != nil {
            clientEmail = client.claims.Email
        }
        sessionEmail := "anonymous_or_unidentified_session"
        if sessionState.Claims != nil {
            sessionEmail = sessionState.Claims.Email
        }

        shouldCancel := false
        if client.claims != nil && sessionState.Claims != nil {
            if client.claims.Email == sessionState.Claims.Email {
                shouldCancel = true
            }
        } else if client.claims == nil && sessionState.Claims == nil && client.userIdentifier != "" {
			// This is an attempt to match anonymous clients based on userIdentifier.
			// For greater robustness, userIdentifier should be stored in UploadSessionState.
			// If sessionState.Claims is nil, we compare client.userIdentifier with sessionState.Claims.Subject
			// (assuming Subject might hold a unique ID for anonymous sessions, otherwise this match is weak).
			// For now, we consider a match if both are anonymous AND userIdentifier is the same (even though we don't have userIdentifier in sessionState).
			// This logic for anonymous clients needs a more robust strategy if anonymous uploads are critical.
			// If UploadSessionState had a UserIdentifier field, the comparison would be:
			// client.userIdentifier == sessionState.UserIdentifier
			// For now, if both don't have claims and the client's userIdentifier is set,
			// it's an indication, but not a certainty.
			// Let's keep the original log for now, but this part is delicate.
			 if sessionState.Claims == nil { // Both anonymous
                 // log.Printf("DEBUG: Anonymous client %s disconnecting. Anonymous session %s for key %s. Matching logic needed.", client.userIdentifier, sessionEmail, uploadKey)
                 // For now, we don't automatically cancel anonymous uploads based solely on an anonymous client disconnecting
                 // unless there's a stronger link.
             }
		}


        if shouldCancel {
            log.Printf("Identified upload %s (by %s) for cancellation due to client %s disconnect.", uploadKey, sessionEmail, clientEmail)
            uploadsToCancel = append(uploadsToCancel, struct{ UploadKey string; SessionState *UploadSessionState }{uploadKey, sessionState})
        }
    }
    // Remove them from the map immediately after identifying
    for _, upload := range uploadsToCancel {
        delete(h.OngoingFileUploads, upload.UploadKey)
    }
    h.FileUploadsMutex.Unlock()

    if len(uploadsToCancel) > 0 {
        log.Printf("Initiating cleanup for %d uploads from disconnected client '%s'", len(uploadsToCancel), client.userIdentifier)
        go func(uploads []struct{ UploadKey string; SessionState *UploadSessionState }) {
            for _, upload := range uploads {
                claimsForCleanup := upload.SessionState.Claims
                provider, ok := storage.GetProvider(upload.SessionState.StorageName)
                if !ok {
                    log.Printf("Warning: Storage provider '%s' not found during disconnected client cleanup for '%s'", upload.SessionState.StorageName, upload.SessionState.ItemPath)
                    continue
                }
                var cancelErr error
                cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
                
                func() { // Anonymous function to manage defer for cleanupCancel
                    defer cleanupCancel()
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
                        log.Printf("Error during cleanup of upload '%s' (storage: %s, path: %s) for disconnected client '%s': %v", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath, client.userIdentifier, cancelErr)
                    } else {
                        log.Printf("Successfully cleaned up upload '%s' (storage: %s, path: %s) for disconnected client '%s'", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath, client.userIdentifier)
                    }
                }()
            }
        }(uploadsToCancel)
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
			clientsToUnregister := []*Client{}
			
			// For safety, briefly lock access to h.clients although it's manipulated only in Run()
			// If Run() could add/remove clients while we iterate, it would be necessary.
			// But since unregister is a channel, Run() processes it serially.
			// This iteration should be safe.
			for client := range h.clients { 
				if !client.isWS && now.Sub(client.lastActivity) > 60*time.Second { 
					clientsToUnregister = append(clientsToUnregister, client)
				}
			}
			for _, client := range clientsToUnregister {
				if config.IsLogLevel(config.LogLevelInfo) {
					log.Printf("Removing inactive Long Polling client: %s", client.userIdentifier)
				}
				h.unregister <- client 
			}
		case <-h.ctx.Done(): 
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Println("Long Polling client cleanup goroutine stopping due to hub context cancellation.")
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

	for {
		select {
		case <-ticker.C:
			now := time.Now()
			uploadsToCancel := []struct{ UploadKey string; SessionState *UploadSessionState }{}

			h.FileUploadsMutex.Lock()
			for uploadKey, sessionState := range h.OngoingFileUploads {
				if now.Sub(sessionState.LastActivity) > uploadCleanupTimeout {
					log.Printf("Detected orphaned upload: %s (last activity: %s, timeout: %s)", uploadKey, sessionState.LastActivity.Format(time.RFC3339), uploadCleanupTimeout.String())
					uploadsToCancel = append(uploadsToCancel, struct{ UploadKey string; SessionState *UploadSessionState }{uploadKey, sessionState})
				}
			}
			// Remove identified orphaned uploads from the map
			for _, upload := range uploadsToCancel {
				delete(h.OngoingFileUploads, upload.UploadKey)
			}
			h.FileUploadsMutex.Unlock()

			if len(uploadsToCancel) > 0 {
				log.Printf("Initiating cleanup for %d orphaned uploads.", len(uploadsToCancel))
				go func(uploads []struct{ UploadKey string; SessionState *UploadSessionState }) {
					for _, upload := range uploads {
						claimsForCleanup := upload.SessionState.Claims
						provider, ok := storage.GetProvider(upload.SessionState.StorageName)
						if !ok {
							log.Printf("Warning: Storage provider '%s' not found during orphaned upload cleanup for '%s'", upload.SessionState.StorageName, upload.SessionState.ItemPath)
							continue
						}
						var cancelErr error
						cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
						func() {
							defer cleanupCancel()
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
								log.Printf("Successfully cleaned up orphaned upload '%s' (storage: %s, path: %s)", upload.UploadKey, upload.SessionState.StorageName, upload.SessionState.ItemPath)
							}
						}()
					}
				}(uploadsToCancel)
			}

		case <-h.ctx.Done():
			if config.IsLogLevel(config.LogLevelInfo) {
				log.Println("Orphaned uploads cleanup goroutine context cancelled, stopping.")
			}
			return
		}
	}
}

// ServeWs handles WebSocket connection requests.
func (h *Hub) ServeWs(w http.ResponseWriter, r *http.Request, claims *auth.UserClaims) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Error upgrading to WebSocket: %v", err)
		http.Error(w, "Unable to establish WebSocket connection", http.StatusInternalServerError)
		return
	}

	clientCtx, clientCancel := context.WithCancel(h.ctx) 
	userIdent := "unauthenticated_ws_client"
	if claims != nil {
		userIdent = claims.Email
	} else {
		userIdent = fmt.Sprintf("anon-ws-%d", time.Now().UnixNano())
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
	}
	h.register <- client

	go client.writePump(h) // Pass h to writePump
	go client.readPump(h)
}

// ServeLongPolling handles Long Polling requests.
func (h *Hub) ServeLongPolling(w http.ResponseWriter, r *http.Request, claims *auth.UserClaims) {
	userIdent := "unauthenticated_lp_client"
	if claims != nil {
		userIdent = claims.Email
	} else {
		userIdent = fmt.Sprintf("anon-lp-%d", time.Now().UnixNano())
	}

	if r.Method == http.MethodPost {
		var msg Message
		r.Body = http.MaxBytesReader(w, r.Body, 1024*1024) 
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&msg); err != nil {
			log.Printf("Error parsing Long Polling message: %v", err)
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("LP Incoming Message (User: %s): Type=%s, RequestID=%s", userIdent, msg.Type, msg.RequestID)
		}

		reqCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second) 
		defer cancel()

		response, processErr := h.handleClientMessage(reqCtx, &msg, claims)
		if processErr != nil {
			log.Printf("Error processing Long Polling message for user %s: %v", userIdent, processErr)
			if response.Type == "" { 
				response.Type = "error"
				response.RequestID = msg.RequestID
			}
			if _, ok := response.Payload.(map[string]string); !ok && response.Type == "error" {
				// Ensure payload is a string-string map if it's a simple error
				if errStr, okStr := processErr.(error); okStr {
					response.Payload = map[string]string{"error": errStr.Error()}
				} else {
					response.Payload = map[string]string{"error": "Unknown processing error"}
				}
			}
		}
		
		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("LP Outgoing Response (User: %s): Type=%s, RequestID=%s", userIdent, response.Type, response.RequestID)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("Error sending Long Polling response for user %s: %v", userIdent, err)
		}

	} else if r.Method == http.MethodGet { 
		w.Header().Set("Content-Type", "application/json")
		initialConfigMsg := Message{
			Type: "config_update",
			Payload: map[string]interface{}{
				"client_ping_interval_ms": h.config.ClientPingIntervalMs,
			},
		}
		json.NewEncoder(w).Encode(initialConfigMsg)
	} else {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// readPump pumps messages from the WebSocket connection to the hub.
func (c *Client) readPump(h *Hub) {
	defer func() {
		h.unregister <- c 
	}()
	
	pongWait := time.Duration(h.config.ClientPingIntervalMs)*2 + 10*time.Second 
	if pongWait < 30*time.Second { 
		pongWait = 30 * time.Second
	}
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		c.lastActivity = time.Now() 
		return nil
	})

	for {
		select {
		case <-c.ctx.Done(): 
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Client readPump for %s stopping due to context cancellation: %v", c.userIdentifier, c.ctx.Err())
			}
			return
		default:
		}

		var msg Message
		err := c.conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNoStatusReceived) {
				log.Printf("WebSocket read error for client %s: %v", c.userIdentifier, err)
			} else if errors.Is(err, net.ErrClosed) {
                 log.Printf("WebSocket connection closed for client %s (net.ErrClosed).", c.userIdentifier)
            } else {
				log.Printf("WebSocket read error (potentially expected close) for client %s: %v", c.userIdentifier, err)
			}
			return 
		}
		c.lastActivity = time.Now() 

		if config.IsLogLevel(config.LogLevelDebug) {
			log.Printf("WS Incoming Message (User: %s): Type=%s, RequestID=%s", c.userIdentifier, msg.Type, msg.RequestID)
		}

		msgCtx, cancelMsgProcessing := context.WithTimeout(c.ctx, 60*time.Second) 

		go func(ctx context.Context, message Message) {
			defer cancelMsgProcessing() 

			response, processErr := h.handleClientMessage(ctx, &message, c.claims)
			if processErr != nil {
				log.Printf("Error processing message for user %s: %v", c.userIdentifier, processErr)
				if response.Type == "" {
					response.Type = "error"
					response.RequestID = message.RequestID
				}
				if _, ok := response.Payload.(map[string]string); !ok && response.Type == "error" {
					if errStr, okStr := processErr.(error); okStr {
						response.Payload = map[string]string{"error": errStr.Error()}
					} else {
						response.Payload = map[string]string{"error": "Unknown processing error"}
					}
				}
			}

			select {
			case c.send <- response:
			case <-ctx.Done(): 
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Context cancelled for user %s while sending response for msg %s: %v", c.userIdentifier, message.RequestID, ctx.Err())
				}
			}
		}(msgCtx, msg)
	}
}

// writePump pumps messages from the hub to the WebSocket connection.
func (c *Client) writePump(h *Hub) {
	pingPeriod := time.Duration(h.config.ClientPingIntervalMs) * time.Millisecond
	if h.config.ClientPingIntervalMs <= 0 { 
		log.Printf("Warning: ClientPingIntervalMs non valido o non positivo (%dms) in config. Uso fallback per pingPeriod (10s).", h.config.ClientPingIntervalMs)
		pingPeriod = 10 * time.Second 
	} else if pingPeriod <= 0 { 
		log.Printf("Warning: pingPeriod calcolato è 0 o negativo. Uso fallback (10s). Original Ms: %d", h.config.ClientPingIntervalMs)
		pingPeriod = 10 * time.Second
	}

	if config.IsLogLevel(config.LogLevelDebug) { // Modificato per usare IsLogLevel
		log.Printf("DEBUG: Client %s - Ping period established at %v (from config value %d ms)", c.userIdentifier, pingPeriod, h.config.ClientPingIntervalMs)
	}

	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				if c.conn != nil { 
					c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				}
				if config.IsLogLevel(config.LogLevelDebug) {
					log.Printf("Client send channel closed for %s. Closing WebSocket.", c.userIdentifier)
				}
				return
			}

			if c.conn == nil { 
				log.Printf("Error: client %s connection is nil in writePump before writing message.", c.userIdentifier)
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)) 
			c.mu.Lock()
			err := c.conn.WriteJSON(message)
			c.mu.Unlock()
			if err != nil {
				log.Printf("Error writing to WebSocket for client %s: %v", c.userIdentifier, err)
				return 
			}
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("WS Outgoing Message (User: %s): Type=%s, RequestID=%s", c.userIdentifier, message.Type, message.RequestID)
			}

		case <-ticker.C:
			if c.conn == nil { 
				log.Printf("Error: client %s connection is nil in writePump before sending ping.", c.userIdentifier)
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			c.mu.Lock()
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("Error sending Ping to WebSocket for client %s: %v", c.userIdentifier, err)
				c.mu.Unlock()
				return 
			}
			c.mu.Unlock()
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Sent Ping to client %s", c.userIdentifier)
			}

		case <-c.ctx.Done(): 
			if config.IsLogLevel(config.LogLevelDebug) {
				log.Printf("Client writePump for %s stopping due to context cancellation: %v", c.userIdentifier, c.ctx.Err())
			}
			return
		}
	}
}


// handleClientMessage processes messages received from clients (WS or LP).
func (h *Hub) handleClientMessage(ctx context.Context, msg *Message, claims *auth.UserClaims) (Message, error) {
	var response Message
	response.Type = msg.Type + "_response" 
	response.RequestID = msg.RequestID

	select {
	case <-ctx.Done():
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Context cancelled before processing message type '%s' (ID: %s): %v", msg.Type, msg.RequestID, ctx.Err())
		}
		response.Type = "error"
		response.Payload = map[string]string{"error": "request cancelled or timed out"}
		return response, ctx.Err()
	default:
	}

	switch msg.Type {
	case "get_filesystems":
		accessibleProviders := authz.GetAccessibleStorages(ctx, claims, h.config)
		response.Payload = accessibleProviders

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

		var tsFilter *time.Time
		if payload.TimestampFilter != "" {
			t, parseErr := time.Parse(time.RFC3339, payload.TimestampFilter)
			if parseErr == nil {
				tsFilter = &t
			} else {
				log.Printf("Warning: Invalid timestamp filter format '%s': %v", payload.TimestampFilter, parseErr)
			}
		}

		listResult, err := provider.ListItems(ctx, claims, payload.DirPath, page, itemsPerPage, payload.NameFilter, tsFilter)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				response.Type = "error"
				response.Payload = map[string]interface{}{
					"error":        "Directory not found",
					"storage_name": payload.StorageName,
					"dir_path":     payload.DirPath,
				}
				return response, nil
			}
			return response, fmt.Errorf("error listing items from storage '%s': %w", payload.StorageName, err)
		}
		response.Payload = map[string]interface{}{
			"items":          listResult.Items,
			"total_items":    listResult.TotalItems,
			"page":           listResult.Page,
			"items_per_page": listResult.ItemsPerPage,
			"storage_name":   payload.StorageName, 
			"dir_path":       payload.DirPath,     
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
			} else {
				response.Type = "error"
				response.Payload = map[string]string{"error": fmt.Sprintf("Error creating directory: %v", err)}
			}
			return response, nil 
		}
		response.Payload = map[string]interface{}{ 
			"status":   "success",
			"storage_name": payload.StorageName, 
			"dir_path": payload.DirPath, 
			"name":     filepath.Base(payload.DirPath), 
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

		err = provider.DeleteItem(ctx, claims, payload.ItemPath)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Item not found"}
			} else if errors.Is(err, storage.ErrPermissionDenied) {
				response.Type = "error"
				response.Payload = map[string]string{"error": "Access denied: write permission required"}
			} else {
				response.Type = "error"
				response.Payload = map[string]string{"error": fmt.Sprintf("Error deleting item: %v", err)}
			}
			return response, nil
		}
		response.Payload = map[string]interface{}{ 
			"status":    "success",
			"storage_name": payload.StorageName, 
			"item_path": payload.ItemPath,
			"name":      filepath.Base(payload.ItemPath),
		}

	case "check_directory_contents_request": 
		response.Type = "check_directory_contents_request_response" 
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
				response.Payload = map[string]string{"error": "Access denied: read permission required"}
				return response, nil
			}
			return response, fmt.Errorf("error checking storage access: %w", err)
		}

		provider, ok := storage.GetProvider(payload.StorageName)
		if !ok {
			return response, fmt.Errorf("storage provider '%s' not found", payload.StorageName)
		}

		listResult, err := provider.ListItems(ctx, claims, payload.DirPath, 1, 1, "", nil)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) { 
				response.Payload = map[string]interface{}{
					"has_contents": false, 
					"storage_name": payload.StorageName, 
					"dir_path":     payload.DirPath,
				}
				return response, nil
			}
			return response, fmt.Errorf("error listing items to check directory contents: %w", err)
		}
		response.Payload = map[string]interface{}{
			"has_contents": listResult.TotalItems > 0,
			"storage_name": payload.StorageName, 
			"dir_path":     payload.DirPath,     
		}


	case "ping":
		response.Type = "pong"
		response.Payload = msg.Payload 
		return response, nil

	case "config_update": 
		log.Printf("Received unexpected config_update message from client: %+v", msg)
		response.Type = "error"
		response.Payload = map[string]string{"error": "unexpected message type from client"}
		return response, errors.New("unexpected message type from client: config_update")

	default:
		response.Type = "error"
		response.Payload = map[string]string{"error": fmt.Sprintf("unsupported message type: %s", msg.Type)}
		return response, fmt.Errorf("unsupported message type: %s", msg.Type)
	}

	select {
	case <-ctx.Done():
		if config.IsLogLevel(config.LogLevelInfo) {
			log.Printf("Context cancelled after processing message type '%s' (ID: %s): %v", msg.Type, msg.RequestID, ctx.Err())
		}
		return response, ctx.Err()
	default:
	}

	return response, nil
}

